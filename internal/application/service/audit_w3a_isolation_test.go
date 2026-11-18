package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/agent/collaboration"
	"github.com/Usir233/GENYEE/internal/application/access"
	"github.com/Usir233/GENYEE/internal/application/repository"
	"github.com/Usir233/GENYEE/internal/config"
	"github.com/Usir233/GENYEE/internal/datasource"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Finding 1: Suspended / Disabled / Inactive Workspace & DataSource Isolation
// ---------------------------------------------------------------------------

type w3aUserServiceStub struct {
	interfaces.UserService
	user *types.User
}

func (s *w3aUserServiceStub) GetUserByID(_ context.Context, _ string) (*types.User, error) {
	return s.user, nil
}

type w3aTenantMemberStub struct {
	interfaces.TenantMemberService
	member *types.TenantMember
}

func (s *w3aTenantMemberStub) GetMembership(_ context.Context, _ string, _ uint64) (*types.TenantMember, error) {
	return s.member, nil
}

type w3aTenantServiceStub struct {
	interfaces.TenantService
	tenant *types.Tenant
}

func (s *w3aTenantServiceStub) GetTenantByID(_ context.Context, _ uint64) (*types.Tenant, error) {
	return s.tenant, nil
}

type w3aTenantRepoStub struct {
	interfaces.TenantRepository
	tenants map[uint64]*types.Tenant
	err     error
}

func (r *w3aTenantRepoStub) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.tenants != nil {
		if t, ok := r.tenants[id]; ok {
			return t, nil
		}
	}
	return nil, repository.ErrTenantNotFound
}

func (r *w3aTenantRepoStub) AdjustStorageUsed(_ context.Context, _ uint64, _ int64) error {
	return nil
}

type w3aDSRepoStub struct {
	interfaces.DataSourceRepository
	byID map[string]*types.DataSource
}

func (r *w3aDSRepoStub) FindByID(_ context.Context, id string) (*types.DataSource, error) {
	if ds, ok := r.byID[id]; ok {
		cp := *ds
		return &cp, nil
	}
	return nil, datasource.ErrDataSourceNotFound
}

func (r *w3aDSRepoStub) FindByKnowledgeBase(_ context.Context, kbID string) ([]*types.DataSource, error) {
	var out []*types.DataSource
	for _, ds := range r.byID {
		if ds.KnowledgeBaseID == kbID {
			cp := *ds
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (r *w3aDSRepoStub) Delete(_ context.Context, id string) error {
	delete(r.byID, id)
	return nil
}

func (r *w3aDSRepoStub) Update(_ context.Context, ds *types.DataSource) error {
	cp := *ds
	r.byID[ds.ID] = &cp
	return nil
}

type w3aSyncLogRepoStub struct {
	interfaces.SyncLogRepository
	logs map[string]*types.SyncLog
}

func (r *w3aSyncLogRepoStub) FindByID(_ context.Context, id string) (*types.SyncLog, error) {
	if l, ok := r.logs[id]; ok {
		cp := *l
		return &cp, nil
	}
	return nil, datasource.ErrSyncLogNotFound
}

func (r *w3aSyncLogRepoStub) Update(_ context.Context, log *types.SyncLog) error {
	cp := *log
	r.logs[log.ID] = &cp
	return nil
}

func (r *w3aSyncLogRepoStub) FindLatest(_ context.Context, _ string) (*types.SyncLog, error) {
	return nil, nil
}

func (r *w3aSyncLogRepoStub) CancelPendingByDataSource(_ context.Context, _ string) error {
	return nil
}

type w3aCountingConnector struct {
	called int
}

func (c *w3aCountingConnector) Type() string { return "w3a-conn" }
func (c *w3aCountingConnector) Validate(_ context.Context, _ *types.DataSourceConfig) error {
	c.called++
	return nil
}

func (c *w3aCountingConnector) ListResources(
	_ context.Context,
	_ *types.DataSourceConfig,
	_ string,
) ([]types.Resource, error) {
	c.called++
	return nil, nil
}

func (c *w3aCountingConnector) ResolveResourceAncestors(
	_ context.Context,
	_ *types.DataSourceConfig,
	_ []string,
) ([]string, error) {
	c.called++
	return nil, nil
}

func (c *w3aCountingConnector) FetchAll(
	_ context.Context,
	_ *types.DataSourceConfig,
	_ []string,
) ([]types.FetchedItem, error) {
	c.called++
	return nil, nil
}

func (c *w3aCountingConnector) FetchIncremental(
	_ context.Context,
	_ *types.DataSourceConfig,
	_ *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	c.called++
	return nil, &types.SyncCursor{}, nil
}

func TestAuditW3A_InactiveWorkspaceBlockedInBackgroundPaths(t *testing.T) {
	activeUser := &types.User{ID: "u-1", IsActive: true}
	activeMember := &types.TenantMember{
		UserID:   "u-1",
		TenantID: 10,
		Role:     types.TenantRoleAdmin,
		Status:   types.TenantMemberStatusActive,
	}

	for _, status := range []string{"suspended", "disabled", "inactive"} {
		t.Run("automation_ownerContext_"+status, func(t *testing.T) {
			autoSvc := &AutomationService{
				users:   &w3aUserServiceStub{user: activeUser},
				members: &w3aTenantMemberStub{member: activeMember},
				tenants: &w3aTenantServiceStub{tenant: &types.Tenant{ID: 10, Status: status}},
			}
			_, err := autoSvc.ownerContext(context.Background(), &types.Automation{
				ID:          "auto-1",
				TenantID:    10,
				OwnerUserID: "u-1",
			})
			require.ErrorIs(t, err, errOwnerUnavailable, "inactive tenant status %q must pause automation", status)
		})

		t.Run("collaboration_workerContext_"+status, func(t *testing.T) {
			collab := &AgentCollaboration{
				users:   &w3aUserServiceStub{user: activeUser},
				members: &w3aTenantMemberStub{member: activeMember},
				tenants: &w3aTenantServiceStub{tenant: &types.Tenant{ID: 10, Status: status}},
			}
			_, err := collab.workerContext(context.Background(), collaboration.Scope{
				TenantID: 10,
				UserID:   "u-1",
			})
			require.Error(t, err, "inactive tenant status %q must block delegated worker run", status)
		})
	}

	t.Run("datasource_process_sync_cancels_suspended_tenant_and_rejects_mismatched_log", func(t *testing.T) {
		conn := &w3aCountingConnector{}
		reg := datasource.NewConnectorRegistry()
		require.NoError(t, reg.Register(conn))

		dsRepo := &w3aDSRepoStub{byID: map[string]*types.DataSource{
			"ds-10": {
				ID:              "ds-10",
				TenantID:        10,
				KnowledgeBaseID: "kb-10",
				Type:            "w3a-conn",
				Status:          types.DataSourceStatusActive,
				Config:          []byte(`{"type":"w3a-conn"}`),
			},
		}}
		syncRepo := &w3aSyncLogRepoStub{logs: map[string]*types.SyncLog{
			"log-10": {ID: "log-10", DataSourceID: "ds-10", TenantID: 10, Status: types.SyncLogStatusRunning},
			"log-foreign": {
				ID:           "log-foreign",
				DataSourceID: "ds-other",
				TenantID:     99,
				Status:       types.SyncLogStatusRunning,
			},
		}}
		kbLookup := &writeKBLookup{kb: &types.KnowledgeBase{ID: "kb-10", TenantID: 10}}
		tenants := &w3aTenantRepoStub{tenants: map[uint64]*types.Tenant{
			10: {ID: 10, Status: "suspended"},
		}}
		svc := NewDataSourceService(dsRepo, syncRepo, nil, kbLookup, nil, reg, nil, tenants, nil, nil)

		// 1) Mismatched syncLog.DataSourceID must fail with SkipRetry before touching connector.
		mismatchPayload, _ := json.Marshal(types.DataSourceSyncPayload{
			DataSourceID: "ds-10",
			TenantID:     10,
			SyncLogID:    "log-foreign",
		})
		err := svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, mismatchPayload))
		require.ErrorIs(t, err, asynq.SkipRetry)
		require.Zero(t, conn.called)

		// 2) Suspended tenant must cancel syncLog without running external connector.
		suspendedPayload, _ := json.Marshal(types.DataSourceSyncPayload{
			DataSourceID: "ds-10",
			TenantID:     10,
			SyncLogID:    "log-10",
		})
		err = svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, suspendedPayload))
		require.NoError(t, err)
		require.Zero(t, conn.called, "suspended tenant must not invoke external connector")
		require.Equal(t, types.SyncLogStatusCanceled, syncRepo.logs["log-10"].Status)

		// 3) Cross-tenant caller in context must be rejected on DeleteDataSource & ListDataSources.
		foreignCtx := types.WithCaller(
			context.WithValue(context.Background(), types.TenantIDContextKey, uint64(99)),
			types.Caller{TenantID: 99, UserID: "attacker", Role: types.TenantRoleAdmin},
		)
		require.Error(t, svc.DeleteDataSource(foreignCtx, "ds-10"))
		require.Contains(t, dsRepo.byID, "ds-10")
		_, err = svc.ListDataSources(foreignCtx, "kb-10")
		require.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// Finding 2: Cross-Tenant Share Leakage & Duplicate-Share Overwrite Auth
// ---------------------------------------------------------------------------

type w3aAgentRepoStub struct {
	interfaces.CustomAgentRepository
	agents map[string]*types.CustomAgent // key: fmt.Sprintf("%d:%s", tenantID, id)
}

func (r *w3aAgentRepoStub) GetAgentByID(_ context.Context, id string, tenantID uint64) (*types.CustomAgent, error) {
	if a, ok := r.agents[fmt.Sprintf("%d:%s", tenantID, id)]; ok {
		return a, nil
	}
	return nil, repository.ErrCustomAgentNotFound
}

type w3aAgentShareRepoStub struct {
	interfaces.AgentShareRepository
	shares  []*types.AgentShare
	updated []*types.AgentShare
}

func (r *w3aAgentShareRepoStub) ListByAgent(_ context.Context, agentID string) ([]*types.AgentShare, error) {
	var out []*types.AgentShare
	for _, s := range r.shares {
		if s.AgentID == agentID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (r *w3aAgentShareRepoStub) Create(_ context.Context, _ *types.AgentShare) error {
	return repository.ErrAgentShareAlreadyExists
}

func (r *w3aAgentShareRepoStub) GetByAgentAndOrg(_ context.Context, agentID, orgID string) (*types.AgentShare, error) {
	for _, s := range r.shares {
		if s.AgentID == agentID && s.OrganizationID == orgID {
			return s, nil
		}
	}
	return nil, repository.ErrAgentShareNotFound
}

func (r *w3aAgentShareRepoStub) Update(_ context.Context, s *types.AgentShare) error {
	r.updated = append(r.updated, s)
	return nil
}

func (r *w3aAgentShareRepoStub) GetShareByAgentIDForTenant(
	_ context.Context,
	_ uint64,
	agentID string,
	exclude uint64,
) (*types.AgentShare, error) {
	for _, s := range r.shares {
		if s.AgentID == agentID && s.SourceTenantID != exclude {
			return s, nil
		}
	}
	return nil, repository.ErrAgentShareNotFound
}

type w3aOrgRepoStub struct {
	interfaces.OrganizationRepository
	org     *types.Organization
	members map[string]*types.OrganizationTenantMember // key: fmt.Sprintf("%s:%d", orgID, tenantID)
}

func (r *w3aOrgRepoStub) GetByID(_ context.Context, id string) (*types.Organization, error) {
	if r.org != nil && r.org.ID == id {
		return r.org, nil
	}
	return nil, repository.ErrOrganizationNotFound
}

func (r *w3aOrgRepoStub) GetTenantMember(
	_ context.Context,
	orgID string,
	tenantID uint64,
) (*types.OrganizationTenantMember, error) {
	if m, ok := r.members[fmt.Sprintf("%s:%d", orgID, tenantID)]; ok {
		return m, nil
	}
	return nil, repository.ErrOrgMemberNotFound
}

type w3aKBShareRepoStub struct {
	interfaces.KBShareRepository
	existing *types.KnowledgeBaseShare
	updated  int
}

func (r *w3aKBShareRepoStub) Create(_ context.Context, _ *types.KnowledgeBaseShare) error {
	return repository.ErrKBShareAlreadyExists
}

func (r *w3aKBShareRepoStub) GetByKBAndOrg(_ context.Context, _, _ string) (*types.KnowledgeBaseShare, error) {
	cp := *r.existing
	return &cp, nil
}

func (r *w3aKBShareRepoStub) Update(_ context.Context, s *types.KnowledgeBaseShare) error {
	r.existing = s
	r.updated++
	return nil
}

func TestAuditW3A_AgentAndKBShareIsolation(t *testing.T) {
	t.Run("agent_share_filters_composite_pk_collisions_and_guards_nil_agent", func(t *testing.T) {
		agentRepo := &w3aAgentRepoStub{agents: map[string]*types.CustomAgent{
			"1:same-id": {
				ID:       "same-id",
				TenantID: 1,
				Config:   types.CustomAgentConfig{ModelID: "m-1", KBSelectionMode: "none"},
			},
			"2:nil-agent": nil,
		}}
		shareRepo := &w3aAgentShareRepoStub{shares: []*types.AgentShare{
			{ID: "sh-1", AgentID: "same-id", SourceTenantID: 1, OrganizationID: "org-a"},
			{ID: "sh-2", AgentID: "same-id", SourceTenantID: 2, OrganizationID: "org-shared"},
			{ID: "sh-nil", AgentID: "nil-agent", SourceTenantID: 2, OrganizationID: "org-shared"},
		}}
		orgRepo := &w3aOrgRepoStub{
			org: &types.Organization{ID: "org-shared", Name: "Org"},
			members: map[string]*types.OrganizationTenantMember{
				"org-shared:1": {OrganizationID: "org-shared", TenantID: 1, Role: types.OrgRoleEditor},
			},
		}
		kbRepo := newFakeKBRepo()
		svc := &agentShareService{
			shareRepo: shareRepo,
			agentRepo: agentRepo,
			orgRepo:   orgRepo,
			kbRepo:    kbRepo,
		}

		// ListSharesByAgent for tenant 1 must NOT leak tenant 2's share for "same-id".
		gotShares, err := svc.ListSharesByAgent(context.Background(), "same-id", 1)
		require.NoError(t, err)
		require.Len(t, gotShares, 1)
		require.Equal(t, uint64(1), gotShares[0].SourceTenantID)

		// ShareAgent on ErrAgentShareAlreadyExists must NOT overwrite tenant 2's share row in org-shared.
		_, err = svc.ShareAgent(context.Background(), "same-id", "org-shared", "u-1", 1, types.OrgRoleViewer)
		require.Error(t, err)
		require.Empty(t, shareRepo.updated)

		// sharedAgentInfo with nil share.Agent must not panic.
		require.NotPanics(t, func() {
			info := svc.sharedAgentInfo(context.Background(), &types.AgentShare{
				ID:             "sh-nil",
				SourceTenantID: 2,
				Agent:          nil,
			}, types.OrgRoleViewer, map[string]bool{})
			require.NotNil(t, info)
		})

		// GetSharedAgentForTenant with nil returned agent must return ErrAgentNotFoundForShare without panic.
		_, err = svc.GetSharedAgentForTenant(context.Background(), 1, types.TenantRoleViewer, "nil-agent")
		require.ErrorIs(t, err, ErrAgentNotFoundForShare)
	})

	t.Run("kb_share_duplicate_overwrite_requires_canManageShare", func(t *testing.T) {
		kbRepo := newFakeKBRepo()
		kbRepo.rows["kb-1"] = &types.KnowledgeBase{ID: "kb-1", TenantID: 1, EmbeddingModelID: "emb-1"}
		shareRepo := &w3aKBShareRepoStub{
			existing: &types.KnowledgeBaseShare{
				ID:              "kbs-1",
				KnowledgeBaseID: "kb-1",
				OrganizationID:  "org-1",
				SharedByUserID:  "owner-admin",
				SourceTenantID:  1,
				Permission:      types.OrgRoleAdmin,
			},
		}
		orgRepo := &w3aOrgRepoStub{
			org: &types.Organization{ID: "org-1", Name: "Org"},
			members: map[string]*types.OrganizationTenantMember{
				"org-1:1": {OrganizationID: "org-1", TenantID: 1, Role: types.OrgRoleEditor},
			},
		}
		svc := &kbShareService{
			shareRepo: shareRepo,
			orgRepo:   orgRepo,
			kbRepo:    kbRepo,
		}

		// other-contributor is neither the share creator ("owner-admin") nor a tenant/org admin,
		// so re-POSTing ShareKnowledgeBase must NOT downgrade Permission from Admin to Viewer!
		contribCtx := context.WithValue(context.Background(), types.TenantRoleContextKey, types.TenantRoleContributor)
		_, err := svc.ShareKnowledgeBase(contribCtx, "kb-1", "org-1", "other-contributor", 1, types.OrgRoleViewer)
		require.ErrorIs(t, err, ErrSharePermissionDenied)
		require.Zero(t, shareRepo.updated)
		require.Equal(t, types.OrgRoleAdmin, shareRepo.existing.Permission)
	})
}

// ---------------------------------------------------------------------------
// Finding 3: KnowledgeBase Delete / SetEmbeddingModel / Copy / Custom Storage
// ---------------------------------------------------------------------------

type w3aStorageResolverStub struct {
	interfaces.StorageBackendResolver
	svc   interfaces.FileService
	calls int
}

func (r *w3aStorageResolverStub) ResolveFileService(
	_ context.Context, _ *types.Tenant, _, _, _ string,
) (interfaces.FileService, string, error) {
	r.calls++
	return r.svc, "s3", nil
}

func (r *w3aStorageResolverStub) ResolveBackend(
	_ context.Context, _ *types.Tenant, backendID, _ string,
) (*types.StorageBackend, error) {
	return &types.StorageBackend{
		ID:       backendID,
		Provider: "s3",
		Config: types.StorageBackendConfig{
			BucketName:      "owner-bucket",
			Endpoint:        "https://s3.example.invalid",
			AccessKeyID:     "ak",
			SecretAccessKey: "sk",
		},
	}, nil
}

type w3aDeleteFileTracker struct {
	interfaces.FileService
	deleted []string
}

func (f *w3aDeleteFileTracker) DeleteFile(_ context.Context, filePath string) error {
	f.deleted = append(f.deleted, filePath)
	return nil
}

type w3aKGRepoListStub struct {
	interfaces.KnowledgeRepository
	items []*types.Knowledge
}

func (r *w3aKGRepoListStub) ListKnowledgeByKnowledgeBaseID(
	_ context.Context,
	_ uint64,
	_ string,
) ([]*types.Knowledge, error) {
	return r.items, nil
}

func (r *w3aKGRepoListStub) DeleteKnowledgeList(_ context.Context, _ uint64, _ []string) error {
	return nil
}

type w3aChunkDeleteNoopRepo struct {
	interfaces.ChunkRepository
}

func (w3aChunkDeleteNoopRepo) ListImageInfoByKnowledgeIDs(
	_ context.Context,
	_ uint64,
	_ []string,
) ([]interfaces.ChunkImageInfo, error) {
	return nil, nil
}

func (w3aChunkDeleteNoopRepo) DeleteChunksByKnowledgeID(_ context.Context, _ uint64, _ string) error {
	return nil
}

func TestAuditW3A_KnowledgeBaseDeleteAndCopyIsolation(t *testing.T) {
	t.Run("DeleteKnowledgeBase_guards_zero_tenant_cross_tenant_shared_editor_and_narrow_apikey", func(t *testing.T) {
		repo := newFakeKBRepo()
		repo.rows["kb-owner"] = &types.KnowledgeBase{ID: "kb-owner", Name: "Owner KB", TenantID: 7}
		svc := newPR3KBService(repo, &fakeRegistry{}, &fakeOwnership{})

		// 1) Missing tenant in context returns error instead of panicking.
		var err error
		require.NotPanics(t, func() {
			err = svc.DeleteKnowledgeBase(context.Background(), "kb-owner")
		})
		require.Error(t, err)

		// 2) Cross-tenant caller cannot delete kb-owner.
		crossCtx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(99))
		require.Error(t, svc.DeleteKnowledgeBase(crossCtx, "kb-owner"))
		require.Contains(t, repo.rows, "kb-owner")

		// 3) Org-shared Editor from workspace 1 (EffectiveTenantID == 7) cannot delete workspace 7's KB!
		baseCaller := types.WithCaller(
			context.Background(),
			types.Caller{TenantID: 1, UserID: "shared-editor", Role: types.TenantRoleAdmin},
		)
		grant := &access.KBAccess{
			KnowledgeBase:     repo.rows["kb-owner"],
			Caller:            types.CallerFromContext(baseCaller),
			EffectiveTenantID: 7,
			Permission:        types.OrgRoleEditor,
		}
		sharedEditorCtx := grant.Context(baseCaller)
		require.Error(t, svc.DeleteKnowledgeBase(sharedEditorCtx, "kb-owner"))
		require.Contains(t, repo.rows, "kb-owner")

		// 4) Narrowed API key scoped only to "kb-other" cannot delete "kb-owner".
		narrowCtx := types.WithTenantAPIKeyScope(
			context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7)),
			types.TenantAPIKeyScope{
				KeyID:            1,
				ScopeType:        types.APIKeyScopeTenant,
				KnowledgeBaseIDs: types.StringArray{"kb-other"},
				Capabilities:     types.StringArray{string(types.APIKeyCapabilityManageKnowledgeBases)},
			},
		)
		require.Error(t, svc.DeleteKnowledgeBase(narrowCtx, "kb-owner"))
		require.Contains(t, repo.rows, "kb-owner")

		// 5) SetEmbeddingModel rejects cross-tenant caller.
		require.Error(t, svc.SetEmbeddingModel(crossCtx, "kb-owner", "emb-hijack"))
		require.Empty(t, repo.rows["kb-owner"].EmbeddingModelID)
	})

	t.Run("CopyKnowledgeBase_preserves_WikiOnly_IndexingStrategy_and_ExtractConfig", func(t *testing.T) {
		repo := newFakeKBRepo()
		wikiOnly := &types.KnowledgeBase{
			ID:       "src-wiki",
			Name:     "Wiki Source",
			TenantID: 1,
			Type:     types.KnowledgeBaseTypeDocument,
			IndexingStrategy: types.IndexingStrategy{
				VectorEnabled:  false,
				KeywordEnabled: false,
				GraphEnabled:   false,
				WikiEnabled:    true,
			},
			WikiConfig: &types.WikiConfig{
				SynthesisModelID: "chat-wiki",
			},
			QuestionGenerationConfig: &types.QuestionGenerationConfig{
				Enabled:       true,
				QuestionCount: 4,
			},
		}
		repo.rows["src-wiki"] = wikiOnly
		svc := newPR3KBService(repo, &fakeRegistry{}, &fakeOwnership{})
		copyCtx, err := access.WithKBTransferTask(
			context.Background(),
			wikiOnly,
			&types.KnowledgeBase{ID: "reserved-copy", TenantID: 1},
			1,
			access.KBTransferClone,
			"clone-task",
			true,
		)
		require.NoError(t, err)

		_, tgt, err := svc.CopyKnowledgeBase(copyCtx, "src-wiki", "")
		require.NoError(t, err)
		assert.False(t, tgt.IndexingStrategy.VectorEnabled, "cloned Wiki-only KB must not re-enable VectorEnabled")
		assert.False(t, tgt.IndexingStrategy.KeywordEnabled, "cloned Wiki-only KB must not re-enable KeywordEnabled")
		assert.True(t, tgt.IndexingStrategy.WikiEnabled, "cloned Wiki-only KB must preserve WikiEnabled")
		require.NotNil(t, tgt.WikiConfig)
		assert.Equal(t, "chat-wiki", tgt.WikiConfig.SynthesisModelID)
		require.NotNil(t, tgt.QuestionGenerationConfig)
		assert.True(t, tgt.QuestionGenerationConfig.Enabled)
		assert.Equal(t, 4, tgt.QuestionGenerationConfig.QuestionCount)
	})

	t.Run("ProcessKBDelete_deletes_files_from_resolved_custom_storage_backend", func(t *testing.T) {
		globalTracker := &w3aDeleteFileTracker{}
		customTracker := &w3aDeleteFileTracker{}
		resolver := &w3aStorageResolverStub{svc: customTracker}
		backendPath := types.BuildStorageBackendPath("backend-custom", "s3://custom-bucket/doc.pdf")

		kbSvc := &knowledgeBaseService{
			repo: newFakeKBRepo(),
			kgRepo: &w3aKGRepoListStub{
				items: []*types.Knowledge{
					{ID: "k-1", TenantID: 7, KnowledgeBaseID: "kb-del", FilePath: backendPath, StorageSize: 128},
				},
			},
			chunkRepo:       w3aChunkDeleteNoopRepo{},
			tenantRepo:      &w3aTenantRepoStub{tenants: map[uint64]*types.Tenant{7: {ID: 7}}},
			fileSvc:         globalTracker,
			storageResolver: resolver,
		}
		payloadBytes, _ := json.Marshal(types.KBDeletePayload{
			TenantID:        7,
			KnowledgeBaseID: "kb-del",
		})
		require.NoError(t, kbSvc.ProcessKBDelete(context.Background(), asynq.NewTask(types.TypeKBDelete, payloadBytes)))
		assert.Equal(
			t,
			[]string{backendPath},
			customTracker.deleted,
			"custom backend file must be deleted via resolved backend service",
		)
		assert.Empty(t, globalTracker.deleted, "global fallback must not receive custom backend path")
	})
}

// ---------------------------------------------------------------------------
// Finding 6 & 7: Evaluation & Chunk Tenant / Pre-Auth / Non-Embedding Safety
// ---------------------------------------------------------------------------

type w3aChunkRepoRevisionSpy struct {
	interfaces.ChunkRepository
	chunk         *types.Chunk
	revisionReads int
	updated       *types.Chunk
}

func (r *w3aChunkRepoRevisionSpy) GetChunkByID(_ context.Context, tenantID uint64, id string) (*types.Chunk, error) {
	if r.chunk != nil && r.chunk.ID == id && r.chunk.TenantID == tenantID {
		cp := *r.chunk
		return &cp, nil
	}
	return nil, repository.ErrChunkNotFound
}

func (r *w3aChunkRepoRevisionSpy) GetChunkRevision(
	_ context.Context,
	_ uint64,
	_ string,
	_ int,
) (*types.ChunkRevision, error) {
	r.revisionReads++
	return &types.ChunkRevision{Revision: 1, Content: "old", IsEnabled: true}, nil
}

func (r *w3aChunkRepoRevisionSpy) UpdateChunk(_ context.Context, c *types.Chunk) error {
	cp := *c
	r.updated = &cp
	r.chunk = &cp
	return nil
}

func TestAuditW3A_EvaluationAndChunkTenantIsolation(t *testing.T) {
	t.Run("Evaluation_and_EvaluationResult_guard_zero_tenant_cross_tenant_KB_and_bound_memory", func(t *testing.T) {
		kbRepo := newFakeKBRepo()
		kbRepo.rows["kb-foreign"] = &types.KnowledgeBase{
			ID:               "kb-foreign",
			TenantID:         99,
			EmbeddingModelID: "emb",
			SummaryModelID:   "sum",
		}
		kbSvc := newPR3KBService(kbRepo, &fakeRegistry{}, &fakeOwnership{})
		evalSvc := &EvaluationService{
			config:                  &config.Config{},
			knowledgeBaseService:    kbSvc,
			evaluationMemoryStorage: newEvaluationMemoryStorageWithPath(""),
		}
		evalSvc.evaluationMemoryStorage.register(&types.EvaluationDetail{
			Task: &types.EvaluationTask{ID: "eval-1", TenantID: 1, Status: types.EvaluationStatueSuccess},
		})

		// 1) Missing tenant returns error instead of panicking.
		var err error
		require.NotPanics(t, func() {
			_, err = evalSvc.EvaluationResult(context.Background(), "eval-1")
		})
		require.Error(t, err)

		require.NotPanics(t, func() {
			_, err = evalSvc.Evaluation(context.Background(), "default", "kb-foreign", "chat", "")
		})
		require.Error(t, err)

		// 2) Cross-tenant knowledgeBaseID is rejected before creating temporary KB.
		ctxTenant1 := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
		_, err = evalSvc.Evaluation(ctxTenant1, "default", "kb-foreign", "chat", "")
		require.Error(t, err)
		require.Len(t, kbRepo.rows, 1, "foreign template KB must not spawn an evaluation KB")

		// 3) Narrowed API key cannot clone an out-of-scope KB into Evaluation.
		kbRepo.rows["kb-owned"] = &types.KnowledgeBase{
			ID:               "kb-owned",
			TenantID:         1,
			EmbeddingModelID: "emb",
			SummaryModelID:   "sum",
		}
		narrowCtx := types.WithTenantAPIKeyScope(ctxTenant1, types.TenantAPIKeyScope{
			KeyID:            1,
			ScopeType:        types.APIKeyScopeTenant,
			KnowledgeBaseIDs: types.StringArray{"kb-allowed-only"},
			Capabilities:     types.StringArray{string(types.APIKeyCapabilityRetrieve)},
		})
		_, err = evalSvc.Evaluation(narrowCtx, "default", "kb-owned", "chat", "")
		require.Error(t, err)
	})

	t.Run(
		"Chunk_RevertDocumentChunk_checks_write_access_before_reading_revision_and_DeleteGeneratedQuestion_handles_w"+
			"iki_kb",
		func(
			t *testing.T,
		) {
			kbRepo := newFakeKBRepo()
			kbRepo.rows["kb-wiki"] = &types.KnowledgeBase{
				ID:       "kb-wiki",
				TenantID: 7,
				Type:     types.KnowledgeBaseTypeDocument,
				IndexingStrategy: types.IndexingStrategy{
					VectorEnabled:  false,
					KeywordEnabled: false,
					WikiEnabled:    true,
				},
			}
			chunk := &types.Chunk{
				ID:              "c-1",
				TenantID:        7,
				KnowledgeBaseID: "kb-wiki",
				KnowledgeID:     "k-1",
				ChunkType:       types.ChunkTypeText,
				IsEnabled:       true,
			}
			require.NoError(t, chunk.SetDocumentMetadata(&types.DocumentChunkMetadata{
				GeneratedQuestions: []types.GeneratedQuestion{{ID: "q-1", Question: "What is GENYEE?"}},
			}))
			chunkRepo := &w3aChunkRepoRevisionSpy{chunk: chunk}
			svc := &chunkService{
				chunkRepository: chunkRepo,
				kbRepository:    kbRepo,
				knowledgeRepo: &w3aKnowledgeRepoMem{rows: map[string]*types.Knowledge{
					"k-1": {
						ID:              "k-1",
						TenantID:        7,
						KnowledgeBaseID: "kb-wiki",
						ParseStatus:     types.ParseStatusCompleted,
					},
				}},
			}

			// 1) Read-only shared viewer must be rejected BEFORE GetChunkRevision is queried!
			viewerBase := types.WithCaller(
				context.Background(),
				types.Caller{TenantID: 1, UserID: "viewer", Role: types.TenantRoleViewer},
			)
			viewerGrant := &access.KBAccess{
				KnowledgeBase:     kbRepo.rows["kb-wiki"],
				Caller:            types.CallerFromContext(viewerBase),
				EffectiveTenantID: 7,
				Permission:        types.OrgRoleViewer,
			}
			_, err := svc.RevertDocumentChunk(viewerGrant.Context(viewerBase), "c-1", 1, nil)
			require.Error(t, err)
			assert.Zero(t, chunkRepo.revisionReads, "GetChunkRevision must not be called before writableChunk succeeds")

			// 2) DeleteGeneratedQuestion on a shared Editor context (without TenantIDContextKey set directly on base)
			// and on a Wiki-only KB (NeedsEmbeddingModel() == false) must succeed without panic or nil modelService
			// error!
			editorBase := types.WithCaller(
				context.Background(),
				types.Caller{TenantID: 1, UserID: "editor", Role: types.TenantRoleAdmin},
			)
			editorGrant := &access.KBAccess{
				KnowledgeBase:     kbRepo.rows["kb-wiki"],
				Caller:            types.CallerFromContext(editorBase),
				EffectiveTenantID: 7,
				Permission:        types.OrgRoleEditor,
			}
			require.NoError(t, svc.DeleteGeneratedQuestion(editorGrant.Context(editorBase), "c-1", "q-1"))
			meta, err := chunkRepo.updated.DocumentMetadata()
			require.NoError(t, err)
			assert.Empty(t, meta.GeneratedQuestions)
		})
}

// ---------------------------------------------------------------------------
// Finding 5: FAQ Tag Scope, Read Auth, Task Progress Isolation & Upload Cleanup
// ---------------------------------------------------------------------------

type w3aHybridSearchSpy struct {
	interfaces.KnowledgeBaseService
	kb         *types.KnowledgeBase
	lastParams types.SearchParams
}

func (s *w3aHybridSearchSpy) GetKnowledgeBaseByID(_ context.Context, _ string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

func (s *w3aHybridSearchSpy) HybridSearch(
	_ context.Context,
	_ string,
	params types.SearchParams,
) ([]*types.SearchResult, error) {
	s.lastParams = params
	return nil, nil
}

type w3aFailingEnqueuer struct {
	interfaces.TaskEnqueuer
}

func (w3aFailingEnqueuer) Enqueue(_ *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	return nil, errors.New("redis queue down")
}

type w3aMemObjectFileSvc struct {
	interfaces.FileService
	saved   map[string][]byte
	deleted []string
}

func (f *w3aMemObjectFileSvc) SaveBytes(
	_ context.Context,
	data []byte,
	tenantID uint64,
	fileName string,
	_ bool,
) (string, error) {
	if f.saved == nil {
		f.saved = map[string][]byte{}
	}
	p := fmt.Sprintf("local://%d/exports/%s", tenantID, fileName)
	f.saved[p] = bytes.Clone(data)
	return p, nil
}

func (f *w3aMemObjectFileSvc) GetFile(_ context.Context, filePath string) (io.ReadCloser, error) {
	b, ok := f.saved[filePath]
	if !ok {
		return nil, errors.New("object not found")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (f *w3aMemObjectFileSvc) DeleteFile(_ context.Context, filePath string) error {
	f.deleted = append(f.deleted, filePath)
	delete(f.saved, filePath)
	return nil
}

func TestAuditW3A_FAQTagScopeAndTaskProgressIsolation(t *testing.T) {
	t.Run("FAQ_Update_List_Search_enforce_KB_tag_scope_and_read_auth", func(t *testing.T) {
		f := newFAQWriteFixture(t)
		// Foreign tag SeqID=22 belongs to KB "other" in the same workspace (TenantID=7).
		// 1) UpdateFAQEntry must reject binding SeqID=22 onto KB "kb"!
		_, err := f.svc.UpdateFAQEntry(f.ctx, "kb", 1, &types.FAQEntryPayload{
			StandardQuestion: "How do I log in?",
			Answers:          []string{"Use SSO."},
			TagID:            22,
		})
		require.Error(t, err, "UpdateFAQEntry must reject foreign-KB tag SeqID=22")

		// 2) ListFAQEntries must reject filtering by foreign-KB tag SeqID=22!
		_, err = f.svc.ListFAQEntries(f.ctx, "kb", &types.Pagination{Page: 1, PageSize: 10}, nil, 22, "", "", "", nil)
		require.Error(t, err, "ListFAQEntries must reject foreign-KB tag SeqID=22")

		// 3) SearchFAQEntries: nil request must not panic; foreign tag SeqID=22 must not leak into HybridSearch;
		// and OnlyRecommended=true must be forwarded in the non-priority branch!
		searchSpy := &w3aHybridSearchSpy{kb: f.kb}
		f.svc.kbService = searchSpy
		require.NotPanics(t, func() {
			_, err = f.svc.SearchFAQEntries(f.ctx, "kb", nil)
		})
		require.Error(t, err)

		_, err = f.svc.SearchFAQEntries(f.ctx, "kb", &types.FAQSearchRequest{
			QueryText:           "SSO",
			FirstPriorityTagIDs: []int64{22}, // foreign KB tag — must be ignored/rejected, never passed to HybridSearch
			OnlyRecommended:     true,
		})
		require.NoError(t, err)
		assert.Empty(t, searchSpy.lastParams.TagIDs, "foreign KB tag must not be forwarded to HybridSearch")
		assert.True(t, searchSpy.lastParams.OnlyRecommended, "OnlyRecommended must be forwarded in non-priority search")

		// 4) Unshared cross-tenant caller must be rejected on GetFAQEntry / ExportFAQEntries / ExportFAQEntriesJSON.
		unsharedCaller := types.WithCaller(
			context.WithValue(context.Background(), types.TenantIDContextKey, uint64(99)),
			types.Caller{TenantID: 99, UserID: "intruder", Role: types.TenantRoleAdmin},
		)
		_, err = f.svc.GetFAQEntry(unsharedCaller, "kb", 1)
		require.Error(t, err)
		_, err = f.svc.ExportFAQEntries(unsharedCaller, "kb")
		require.Error(t, err)
		_, err = f.svc.ExportFAQEntriesJSON(unsharedCaller, "kb")
		require.Error(t, err)
	})

	t.Run("FAQ_and_Clone_Move_progress_enforce_tenant_and_apikey_scope_and_cleanup_failed_enqueue_blob", func(
		t *testing.T,
	) {
		f := newFAQWriteFixture(t)
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		f.svc.redisClient = rdb
		f.svc.tenantRepo = faqAdmissionTenant{}
		objStore := &w3aMemObjectFileSvc{}
		f.svc.fileSvc = objStore
		f.svc.task = w3aFailingEnqueuer{}

		// 1) Caller-supplied task_id encoding foreign tenant 999 must be rejected by UpsertFAQEntries!
		foreignTaskID := secutils.GenerateTaskID("faq_import", 999, "kb")
		_, err := f.svc.UpsertFAQEntries(f.ctx, "kb", &types.FAQBatchUpsertPayload{
			TaskID:  foreignTaskID,
			Entries: []types.FAQEntryPayload{{StandardQuestion: "Q1", Answers: []string{"A1"}}},
		})
		require.Error(t, err, "UpsertFAQEntries must reject task_id minted for another tenant")

		// 2) Large batch (> 200 entries) whose Enqueue fails must delete the uploaded EntriesURL blob!
		largeEntries := make([]types.FAQEntryPayload, 205)
		for i := range largeEntries {
			largeEntries[i] = types.FAQEntryPayload{
				StandardQuestion: fmt.Sprintf("Q-%d", i),
				Answers:          []string{"A"},
			}
		}
		_, err = f.svc.UpsertFAQEntries(f.ctx, "kb", &types.FAQBatchUpsertPayload{
			Entries: largeEntries,
		})
		require.Error(t, err)
		assert.Len(t, objStore.deleted, 1, "failed Enqueue must clean up uploaded EntriesURL")
		assert.Empty(t, objStore.saved)

		// 3) GetFAQImportProgress must not panic when TenantIDContextKey is absent on completed progress,
		// and must reject narrowed API keys and cross-tenant callers!
		validTaskID := secutils.GenerateTaskID("faq_import", 7, "kb")
		require.NoError(t, f.svc.saveFAQImportProgress(context.Background(), &types.FAQImportProgress{
			TaskID:      validTaskID,
			KBID:        "kb",
			KnowledgeID: "faq",
			Status:      types.FAQImportStatusCompleted,
		}))
		require.NotPanics(t, func() {
			_, err = f.svc.GetFAQImportProgress(context.Background(), validTaskID)
		})
		require.NoError(t, err)

		crossTenantCtx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(99))
		_, err = f.svc.GetFAQImportProgress(crossTenantCtx, validTaskID)
		require.Error(t, err, "cross-tenant caller must not read FAQ import progress")

		narrowKeyCtx := types.WithTenantAPIKeyScope(
			context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7)),
			types.TenantAPIKeyScope{
				KeyID:            1,
				ScopeType:        types.APIKeyScopeTenant,
				KnowledgeBaseIDs: types.StringArray{"other-kb"},
				Capabilities:     types.StringArray{string(types.APIKeyCapabilityRetrieve)},
			},
		)
		_, err = f.svc.GetFAQImportProgress(narrowKeyCtx, validTaskID)
		require.Error(t, err, "narrowed API key must not read progress of out-of-scope KB")

		// 4) GetKBCloneProgress and GetKnowledgeMoveProgress also enforce tenant + API-key scope.
		cloneTaskID := secutils.GenerateTaskID("kb_clone", 7, "kb")
		require.NoError(t, f.svc.SaveKBCloneProgress(context.Background(), &types.KBCloneProgress{
			TaskID:    cloneTaskID,
			SourceID:  "kb",
			TargetID:  "kb-dst",
			Status:    types.KBCloneStatusProcessing,
			CreatedAt: time.Now().Unix(),
		}))
		_, err = f.svc.GetKBCloneProgress(crossTenantCtx, cloneTaskID)
		require.Error(t, err)
		_, err = f.svc.GetKBCloneProgress(narrowKeyCtx, cloneTaskID)
		require.Error(t, err)

		moveTaskID := secutils.GenerateTaskID("kg_move", 7, "kb")
		require.NoError(t, f.svc.SaveKnowledgeMoveProgress(context.Background(), &types.KnowledgeMoveProgress{
			TaskID:     moveTaskID,
			SourceKBID: "kb",
			TargetKBID: "kb-dst",
			Status:     types.KBCloneStatusProcessing,
			CreatedAt:  time.Now().Unix(),
		}))
		_, err = f.svc.GetKnowledgeMoveProgress(crossTenantCtx, moveTaskID)
		require.Error(t, err)
		_, err = f.svc.GetKnowledgeMoveProgress(narrowKeyCtx, moveTaskID)
		require.Error(t, err)
	})
}
