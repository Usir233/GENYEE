package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type failingEntropyReader struct{}

func (failingEntropyReader) Read(_ []byte) (int, error) {
	return 0, errors.New("entropy pool unavailable")
}

type stubOrgRepo struct {
	interfaces.OrganizationRepository
	orgs                 []*types.Organization
	getMemberCalls       int
	batchMemberCalls     int
	countMemberCalls     int
	batchCountCalls      int
	createdOrg           *types.Organization
	updatedInviteCode    string
	membershipsByOrgID   map[string]*types.OrganizationTenantMember
	memberCountsByOrgID  map[string]int64
	adminForGenerateCode bool
}

func (r *stubOrgRepo) Create(_ context.Context, org *types.Organization) error {
	r.createdOrg = org
	return nil
}

func (r *stubOrgRepo) AddTenantMember(_ context.Context, _ *types.OrganizationTenantMember, _ int) error {
	return nil
}

func (r *stubOrgRepo) GetByID(_ context.Context, id string) (*types.Organization, error) {
	for _, o := range r.orgs {
		if o.ID == id {
			return o, nil
		}
	}
	return &types.Organization{ID: id, Name: "Org", InviteCodeValidityDays: 7}, nil
}

func (r *stubOrgRepo) GetTenantMember(
	_ context.Context,
	orgID string,
	tenantID uint64,
) (*types.OrganizationTenantMember, error) {
	r.getMemberCalls++
	if r.adminForGenerateCode {
		return &types.OrganizationTenantMember{OrganizationID: orgID, TenantID: tenantID, Role: types.OrgRoleAdmin}, nil
	}
	if m, ok := r.membershipsByOrgID[orgID]; ok {
		return m, nil
	}
	return nil, errors.New("not member")
}

func (r *stubOrgRepo) ListTenantMembersByTenantForOrgs(
	_ context.Context, _ uint64, _ []string,
) (map[string]*types.OrganizationTenantMember, error) {
	r.batchMemberCalls++
	if r.membershipsByOrgID == nil {
		return map[string]*types.OrganizationTenantMember{}, nil
	}
	return r.membershipsByOrgID, nil
}

func (r *stubOrgRepo) CountTenantMembers(_ context.Context, orgID string) (int64, error) {
	r.countMemberCalls++
	return r.memberCountsByOrgID[orgID], nil
}

func (r *stubOrgRepo) CountTenantMembersByOrganizations(_ context.Context, orgIDs []string) (map[string]int64, error) {
	r.batchCountCalls++
	out := make(map[string]int64, len(orgIDs))
	for _, id := range orgIDs {
		out[id] = r.memberCountsByOrgID[id]
	}
	return out, nil
}

func (r *stubOrgRepo) ListSearchable(_ context.Context, _ string, _ int) ([]*types.Organization, error) {
	return r.orgs, nil
}

func (r *stubOrgRepo) UpdateInviteCode(_ context.Context, _ string, code string, _ *time.Time) error {
	r.updatedInviteCode = code
	return nil
}

type stubKBShareRepo struct {
	interfaces.KBShareRepository
	listOrgCalls  int
	batchCntCalls int
	counts        map[string]int64
}

func (r *stubKBShareRepo) ListByOrganization(_ context.Context, orgID string) ([]*types.KnowledgeBaseShare, error) {
	r.listOrgCalls++
	n := int(r.counts[orgID])
	out := make([]*types.KnowledgeBaseShare, n)
	return out, nil
}

func (r *stubKBShareRepo) CountByOrganizations(_ context.Context, _ []string) (map[string]int64, error) {
	r.batchCntCalls++
	return r.counts, nil
}

type stubAgentShareRepo struct {
	interfaces.AgentShareRepository
	listOrgCalls  int
	batchCntCalls int
	counts        map[string]int64
	sharedForTen  []*types.AgentShare
}

func (r *stubAgentShareRepo) ListByOrganization(_ context.Context, orgID string) ([]*types.AgentShare, error) {
	r.listOrgCalls++
	n := int(r.counts[orgID])
	out := make([]*types.AgentShare, n)
	return out, nil
}

func (r *stubAgentShareRepo) CountByOrganizations(_ context.Context, _ []string) (map[string]int64, error) {
	r.batchCntCalls++
	return r.counts, nil
}

func (r *stubAgentShareRepo) ListSharedAgentsForTenant(_ context.Context, _ uint64) ([]*types.AgentShare, error) {
	return r.sharedForTen, nil
}

type stubDisabledSharedAgentRepo struct {
	interfaces.TenantDisabledSharedAgentRepository
}

func (stubDisabledSharedAgentRepo) ListByTenantID(
	_ context.Context,
	_ uint64,
) ([]*types.TenantDisabledSharedAgent, error) {
	return nil, nil
}

func TestGenerateInviteCodePropagatesEntropyFailure(t *testing.T) {
	origReader := rand.Reader
	rand.Reader = failingEntropyReader{}
	defer func() { rand.Reader = origReader }()

	orgRepo := &stubOrgRepo{adminForGenerateCode: true}
	svc := NewOrganizationService(orgRepo, nil, nil, nil)

	_, err := svc.CreateOrganization(context.Background(), "user-1", 10, &types.CreateOrganizationRequest{
		Name: "Secure Org",
	})
	require.Error(
		t,
		err,
		"CreateOrganization must fail closed when crypto/rand fails instead of emitting all-zero invite code",
	)

	_, err = svc.GenerateInviteCode(context.Background(), "org-1", "user-1", 10)
	require.Error(t, err, "GenerateInviteCode must fail closed when crypto/rand fails")
}

func TestSearchSearchableOrganizationsBatchesPerOrgCountsAndMemberships(t *testing.T) {
	orgs := make([]*types.Organization, 0, 5)
	memberCounts := make(map[string]int64, 5)
	kbCounts := make(map[string]int64, 5)
	agentCounts := make(map[string]int64, 5)
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("org-%d", i)
		orgs = append(orgs, &types.Organization{ID: id, Name: id, Searchable: true, MemberLimit: 50})
		memberCounts[id] = int64(i + 1)
		kbCounts[id] = int64(i * 2)
		agentCounts[id] = int64(i)
	}
	orgRepo := &stubOrgRepo{
		orgs:                orgs,
		memberCountsByOrgID: memberCounts,
		membershipsByOrgID: map[string]*types.OrganizationTenantMember{
			"org-2": {OrganizationID: "org-2", TenantID: 99, Role: types.OrgRoleViewer},
		},
	}
	kbRepo := &stubKBShareRepo{counts: kbCounts}
	agRepo := &stubAgentShareRepo{counts: agentCounts}

	svc := NewOrganizationService(orgRepo, nil, kbRepo, agRepo)
	resp, err := svc.SearchSearchableOrganizations(context.Background(), 99, "", 20)
	require.NoError(t, err)
	require.Len(t, resp.Organizations, 5)
	require.True(t, resp.Organizations[2].IsAlreadyMember)
	require.Equal(t, 3, resp.Organizations[2].MemberCount)
	require.Equal(t, 4, resp.Organizations[2].ShareCount)
	require.Equal(t, 2, resp.Organizations[2].AgentShareCount)

	require.Zero(t, orgRepo.getMemberCalls, "must not call per-org GetTenantMember in loop")
	require.Equal(t, 1, orgRepo.batchMemberCalls)
	require.Zero(t, kbRepo.listOrgCalls, "must not call per-org KBShareRepository.ListByOrganization in loop")
	require.Equal(t, 1, kbRepo.batchCntCalls)
	require.Zero(t, agRepo.listOrgCalls, "must not call per-org AgentShareRepository.ListByOrganization in loop")
	require.Equal(t, 1, agRepo.batchCntCalls)
	require.Zero(t, orgRepo.countMemberCalls, "must use batched member count when supported")
	require.Equal(t, 1, orgRepo.batchCountCalls)
}

func TestListSharedAgentsBatchesOrgMembershipLookup(t *testing.T) {
	shares := make([]*types.AgentShare, 0, 4)
	memberships := make(map[string]*types.OrganizationTenantMember, 4)
	for i := 0; i < 4; i++ {
		orgID := fmt.Sprintf("org-%d", i)
		agentID := fmt.Sprintf("custom-agent-%d", i)
		shares = append(shares, &types.AgentShare{
			ID:             fmt.Sprintf("share-%d", i),
			AgentID:        agentID,
			OrganizationID: orgID,
			SourceTenantID: uint64(100 + i),
			Permission:     types.OrgRoleViewer,
			Agent: &types.CustomAgent{
				ID:     agentID,
				Name:   agentID,
				Config: types.CustomAgentConfig{KBSelectionMode: "none"},
			},
		})
		memberships[orgID] = &types.OrganizationTenantMember{
			OrganizationID: orgID,
			TenantID:       7,
			Role:           types.OrgRoleViewer,
		}
	}
	orgRepo := &stubOrgRepo{membershipsByOrgID: memberships}
	shareRepo := &stubAgentShareRepo{sharedForTen: shares}
	svc := &agentShareService{
		shareRepo:    shareRepo,
		disabledRepo: stubDisabledSharedAgentRepo{},
		orgRepo:      orgRepo,
	}

	got, err := svc.ListSharedAgents(context.Background(), 7, types.TenantRoleAdmin)
	require.NoError(t, err)
	require.Len(t, got, 4)
	require.Zero(t, orgRepo.getMemberCalls, "ListSharedAgents must batch org membership lookup")
	require.Equal(t, 1, orgRepo.batchMemberCalls)
}

type stubBatchDSRepo struct {
	interfaces.DataSourceRepository
	items []*types.DataSource
}

func (r *stubBatchDSRepo) FindByKnowledgeBase(_ context.Context, _ string) ([]*types.DataSource, error) {
	return r.items, nil
}

type stubBatchSyncLogRepo struct {
	interfaces.SyncLogRepository
	singleCalls int
	batchCalls  int
	latestByDS  map[string]*types.SyncLog
}

func (r *stubBatchSyncLogRepo) FindLatest(_ context.Context, dsID string) (*types.SyncLog, error) {
	r.singleCalls++
	return r.latestByDS[dsID], nil
}

func (r *stubBatchSyncLogRepo) FindLatestByDataSourceIDs(
	_ context.Context, dsIDs []string,
) (map[string]*types.SyncLog, error) {
	r.batchCalls++
	out := make(map[string]*types.SyncLog, len(dsIDs))
	for _, id := range dsIDs {
		if l, ok := r.latestByDS[id]; ok {
			out[id] = l
		}
	}
	return out, nil
}

func TestDataSourceListBatchesLatestSyncLogs(t *testing.T) {
	dsList := []*types.DataSource{
		{ID: "ds-1", KnowledgeBaseID: "kb-1", TenantID: 1},
		{ID: "ds-2", KnowledgeBaseID: "kb-1", TenantID: 1},
		{ID: "ds-3", KnowledgeBaseID: "kb-1", TenantID: 1},
	}
	syncRepo := &stubBatchSyncLogRepo{
		latestByDS: map[string]*types.SyncLog{
			"ds-1": {ID: "log-1", DataSourceID: "ds-1"},
			"ds-3": {ID: "log-3", DataSourceID: "ds-3"},
		},
	}
	svc := &DataSourceService{
		dsRepo:      &stubBatchDSRepo{items: dsList},
		syncLogRepo: syncRepo,
	}

	got, err := svc.ListDataSources(context.Background(), "kb-1")
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.NotNil(t, got[0].LatestSyncLog)
	require.Equal(t, "log-1", got[0].LatestSyncLog.ID)
	require.Nil(t, got[1].LatestSyncLog)
	require.NotNil(t, got[2].LatestSyncLog)
	require.Equal(t, "log-3", got[2].LatestSyncLog.ID)
	require.Zero(t, syncRepo.singleCalls, "ListDataSources must not call FindLatest in an N+1 loop")
	require.Equal(t, 1, syncRepo.batchCalls)
}

type w2StubEmbedChannelRepo struct {
	interfaces.EmbedChannelRepository
	channels map[string]*types.EmbedChannel
}

func (r *w2StubEmbedChannelRepo) GetByID(_ context.Context, id string) (*types.EmbedChannel, error) {
	return r.channels[id], nil
}

func TestEmbedResolveSessionTokenEvictsStaleAndRotatedKeys(t *testing.T) {
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("SYSTEM_AES_KEY", "test-embed-signing-key-32-bytes!!!")

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	repo := &w2StubEmbedChannelRepo{
		channels: map[string]*types.EmbedChannel{
			"ch-live": {ID: "ch-live", Enabled: true, PublishToken: "em_current"},
		},
	}
	svc := &embedChannelService{repo: repo, redis: rdb}
	ctx := context.Background()

	token, _, err := svc.IssueSessionToken(ctx, "ch-live")
	require.NoError(t, err)

	gotCh, err := svc.ResolveSessionToken(ctx, token)
	require.NoError(t, err)
	require.Equal(t, "ch-live", gotCh)

	// Rotating the publish token must reject and evict the cached Redis entry.
	repo.channels["ch-live"].PublishToken = "em_rotated"
	_, err = svc.ResolveSessionToken(ctx, token)
	require.ErrorIs(t, err, ErrEmbedTokenInvalid)
	require.False(t, mr.Exists(embedSessionRedisPrefix+token))

	// Deleted channel must also reject and evict the cached Redis entry.
	token2, _, err := svc.IssueSessionToken(ctx, "ch-live")
	require.NoError(t, err)
	delete(repo.channels, "ch-live")
	_, err = svc.ResolveSessionToken(ctx, token2)
	require.ErrorIs(t, err, ErrEmbedTokenInvalid)
	require.False(t, mr.Exists(embedSessionRedisPrefix+token2))
}

type stubBatchSuggestionRepo struct {
	interfaces.MessageSuggestionRepository
	singleCalls int
	batchCalls  int
	deletedIDs  []string
}

func (r *stubBatchSuggestionRepo) DeleteBySessionID(_ context.Context, _ uint64, id string) error {
	r.singleCalls++
	r.deletedIDs = append(r.deletedIDs, id)
	return nil
}

func (r *stubBatchSuggestionRepo) DeleteBySessionIDs(_ context.Context, _ uint64, ids []string) error {
	r.batchCalls++
	r.deletedIDs = append(r.deletedIDs, ids...)
	return nil
}

func TestSessionDeleteSuggestionsBatchUsesSingleBatchQuery(t *testing.T) {
	sugRepo := &stubBatchSuggestionRepo{}
	svc := &sessionService{suggestionRepo: sugRepo}
	svc.deleteSessionSuggestionsBatch(context.Background(), 42, []string{"s-1", "s-2", "s-3"})

	require.Zero(t, sugRepo.singleCalls, "deleteSessionSuggestionsBatch must avoid per-session DeleteBySessionID loop")
	require.Equal(t, 1, sugRepo.batchCalls)
	require.Equal(t, []string{"s-1", "s-2", "s-3"}, sugRepo.deletedIDs)
}
