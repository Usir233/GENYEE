package access

import (
	"context"
	"errors"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
)

type shareLookup struct {
	permission types.OrgMemberRole
	err        error
	caller     uint64
	role       types.TenantRole
}

func (s *shareLookup) CheckTenantKBPermission(
	_ context.Context,
	_ string,
	caller uint64,
	role types.TenantRole,
) (types.OrgMemberRole, bool, error) {
	s.caller, s.role = caller, role
	return s.permission, s.permission != "", s.err
}

type agentLookup struct {
	agent    *types.CustomAgent
	any      bool
	err      error
	anyCalls int
	source   uint64
}

func (s *agentLookup) GetSharedAgentForTenant(
	_ context.Context,
	_ uint64,
	_ types.TenantRole,
	_ string,
	source ...uint64,
) (*types.CustomAgent, error) {
	s.source = source[0]
	return s.agent, s.err
}

func (s *agentLookup) TenantCanAccessKBViaSomeSharedAgent(
	context.Context,
	uint64,
	types.TenantRole,
	*types.KnowledgeBase,
) (bool, error) {
	s.anyCalls++
	return s.any, s.err
}

func TestResolveKBPermissionMatrix(t *testing.T) {
	for _, tt := range []struct {
		name                                            string
		own                                             bool
		permission                                      types.OrgMemberRole
		required                                        types.OrgMemberRole
		shareError                                      bool
		agentID, source, mode                           string
		selected                                        []string
		wrongTenant, missingAgent, agentError, anyAgent bool
		want                                            types.OrgMemberRole
		wantErr                                         error
	}{
		{name: "own KB", own: true, required: types.OrgRoleEditor, want: types.OrgRoleAdmin},
		{
			name:       "organization reader",
			permission: types.OrgRoleViewer,
			required:   types.OrgRoleViewer,
			want:       types.OrgRoleViewer,
		},

		{
			name:       "organization editor",
			permission: types.OrgRoleEditor,
			required:   types.OrgRoleEditor,
			want:       types.OrgRoleEditor,
		},

		{
			name:       "reader cannot write",
			permission: types.OrgRoleViewer,
			required:   types.OrgRoleEditor,
			wantErr:    ErrForbidden,
		},

		{
			name:       "share lookup fails closed",
			permission: types.OrgRoleEditor,
			shareError: true,
			required:   types.OrgRoleViewer,
			wantErr:    ErrForbidden,
		},

		{
			name:       "independent agent grant after share error",
			shareError: true,
			anyAgent:   true,
			required:   types.OrgRoleViewer,
			want:       types.OrgRoleViewer,
		},

		{name: "any shared agent read", anyAgent: true, required: types.OrgRoleViewer, want: types.OrgRoleViewer},
		{name: "any agent cannot write", anyAgent: true, required: types.OrgRoleEditor, wantErr: ErrForbidden},
		{
			name:       "agent lookup error",
			anyAgent:   true,
			agentError: true,
			required:   types.OrgRoleViewer,
			wantErr:    ErrForbidden,
		},

		{
			name:     "explicit all",
			agentID:  "agent",
			source:   "2",
			mode:     "all",
			required: types.OrgRoleViewer,
			want:     types.OrgRoleViewer,
		},

		{
			name:     "explicit selected",
			agentID:  "agent",
			mode:     "selected",
			selected: []string{"kb"},
			required: types.OrgRoleViewer,
			want:     types.OrgRoleViewer,
		},

		{
			name:     "selection miss does not fall back",
			agentID:  "agent",
			mode:     "selected",
			selected: []string{"other"},
			anyAgent: true,
			required: types.OrgRoleViewer,
			wantErr:  ErrForbidden,
		},

		{
			name:     "none does not fall back",
			agentID:  "agent",
			mode:     "none",
			anyAgent: true,
			required: types.OrgRoleViewer,
			wantErr:  ErrForbidden,
		},

		{
			name:     "unknown mode denied",
			agentID:  "agent",
			mode:     "invalid",
			required: types.OrgRoleViewer,
			wantErr:  ErrForbidden,
		},

		{
			name:        "tenant mismatch denied",
			agentID:     "agent",
			mode:        "all",
			wrongTenant: true,
			required:    types.OrgRoleViewer,
			wantErr:     ErrForbidden,
		},

		{
			name:         "missing explicit agent does not fall back",
			agentID:      "agent",
			missingAgent: true,
			anyAgent:     true,
			required:     types.OrgRoleViewer,
			wantErr:      ErrForbidden,
		},

		{
			name:     "invalid source rejected",
			agentID:  "agent",
			source:   "bad",
			mode:     "all",
			required: types.OrgRoleViewer,
			wantErr:  ErrInvalidAgentSource,
		},

		{
			name:     "own grant precedes agent parsing",
			own:      true,
			agentID:  "agent",
			source:   "bad",
			required: types.OrgRoleViewer,
			want:     types.OrgRoleAdmin,
		},

		{
			name:       "share grant precedes agent parsing",
			permission: types.OrgRoleViewer,
			agentID:    "agent",
			source:     "bad",
			required:   types.OrgRoleViewer,
			want:       types.OrgRoleViewer,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			kb := &types.KnowledgeBase{ID: "kb", TenantID: 2}
			if tt.own {
				kb.TenantID = 1
			}
			shares := &shareLookup{permission: tt.permission}
			if tt.shareError {
				shares.err = errors.New("unavailable")
			}
			agents := &agentLookup{
				any: tt.anyAgent,
				agent: &types.CustomAgent{
					TenantID: 2,
					Config:   types.CustomAgentConfig{KBSelectionMode: tt.mode, KnowledgeBases: tt.selected},
				},
			}
			if tt.wrongTenant {
				agents.agent.TenantID = 3
			}
			if tt.missingAgent {
				agents.agent = nil
			}
			if tt.agentError {
				agents.err = errors.New("unavailable")
			}
			request := KBRequest{
				Caller:              types.Caller{TenantID: 1, Role: types.TenantRoleViewer},
				AgentID:             tt.agentID,
				AgentSourceTenantID: tt.source,
			}
			grant, err := ResolveKB(context.Background(), request, kb, tt.required, shares, agents)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.Nil(t, grant)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, grant.Permission)
				require.Equal(t, uint64(1), grant.Caller.TenantID)
				require.Equal(t, kb.TenantID, grant.EffectiveTenantID)
			}
			if !tt.own {
				require.Equal(t, uint64(1), shares.caller)
				require.Equal(t, types.TenantRoleViewer, shares.role)
			}
			if tt.agentID != "" {
				require.Zero(t, agents.anyCalls)
			}
			if tt.source == "2" {
				require.Equal(t, uint64(2), agents.source)
			}
		})
	}
}

func TestResolveKBMissingIdentityResourceAndAPIKeyScope(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb", TenantID: 1}
	_, err := ResolveKB(context.Background(), KBRequest{}, kb, types.OrgRoleViewer, nil, nil)
	require.ErrorIs(t, err, ErrUnauthorized)
	_, err = ResolveKB(
		context.Background(),
		KBRequest{Caller: types.Caller{TenantID: 1}},
		nil,
		types.OrgRoleViewer,
		nil,
		nil,
	)
	require.ErrorIs(t, err, ErrNotFound)
	ctx := types.WithTenantAPIKeyScope(
		context.Background(),
		types.TenantAPIKeyScope{KnowledgeBaseIDs: types.StringArray{"other"}},
	)
	_, err = ResolveKB(ctx, KBRequest{Caller: types.Caller{TenantID: 1}}, kb, types.OrgRoleViewer, nil, nil)
	require.Error(t, err, "API key scope must apply even to owned KBs")
}

func TestResolveKBAndWithGrantRejectUnmaterializedCallerMismatchAndInvalidRole(t *testing.T) {
	ownKB := &types.KnowledgeBase{ID: "kb-own", TenantID: 1}
	foreignKB := &types.KnowledgeBase{ID: "kb-foreign", TenantID: 2}

	otherTenantCtx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(9))
	_, err := ResolveKB(
		otherTenantCtx,
		KBRequest{Caller: types.Caller{TenantID: 1, UserID: "alice", Role: types.TenantRoleAdmin}},
		ownKB,
		types.OrgRoleViewer,
		nil,
		nil,
	)
	require.ErrorIs(t, err, ErrForbidden, "ResolveKB must reject caller mismatch even before CallerContextKey is materialized")

	grant, err := ResolveKB(
		context.Background(),
		KBRequest{Caller: types.Caller{TenantID: 1, UserID: "alice", Role: types.TenantRoleAdmin}},
		ownKB,
		types.OrgRoleViewer,
		nil,
		nil,
	)
	require.NoError(t, err)

	untouched := grant.WithGrant(otherTenantCtx)
	require.Equal(t, uint64(9), types.CallerFromContext(untouched).TenantID, "WithGrant must not overwrite an authenticated tenant")
	require.False(t, HasKBGrant(untouched, ownKB.ID, ownKB.TenantID, types.OrgRoleViewer))

	scoped := grant.Context(otherTenantCtx)
	require.Equal(t, uint64(9), types.CallerFromContext(scoped).TenantID, "Context must not switch execution tenant on caller mismatch")

	_, err = ResolveKB(
		context.Background(),
		KBRequest{Caller: types.Caller{TenantID: 1}},
		foreignKB,
		types.OrgMemberRole(""),
		&shareLookup{permission: types.OrgRoleViewer},
		nil,
	)
	require.ErrorIs(t, err, ErrForbidden, "empty/invalid required role must fail closed")

	_, err = ResolveKB(
		context.Background(),
		KBRequest{Caller: types.Caller{TenantID: 1}},
		&types.KnowledgeBase{ID: "kb-zero", TenantID: 0},
		types.OrgRoleViewer,
		nil,
		nil,
	)
	require.ErrorIs(t, err, ErrNotFound, "KB with zero owner tenant must not resolve")
}

func TestResolveKBFileRejectsZeroCallerAndMismatchedKBTenant(t *testing.T) {
	const ref = "resource://AbCdEfGhIjKlMnOpQrStUv"
	catalog := fileCatalog{
		&types.StoredResource{TenantID: 2, PhysicalPath: "local://2/exports/a.png", OriginalName: "a.png"},
	}
	bindings := &fileBinding{allowed: true}

	zeroGrant := &KBAccess{
		Caller:            types.Caller{Role: types.TenantRoleViewer},
		KnowledgeBase:     &types.KnowledgeBase{ID: "kb", TenantID: 2},
		EffectiveTenantID: 2,
		Permission:        types.OrgRoleViewer,
	}
	_, err := ResolveKBFile(context.Background(), zeroGrant, "kb", ref, catalog, bindings)
	require.ErrorIs(t, err, ErrForbidden, "unauthenticated zero caller must not match zero context caller")

	base := callerContext()
	mismatchedGrant := &KBAccess{
		Caller:            types.CallerFromContext(base),
		KnowledgeBase:     &types.KnowledgeBase{ID: "kb", TenantID: 3},
		EffectiveTenantID: 2,
		Permission:        types.OrgRoleViewer,
	}
	_, err = ResolveKBFile(base, mismatchedGrant, "kb", ref, catalog, bindings)
	require.ErrorIs(t, err, ErrForbidden, "grant with mismatched KB tenant and effective tenant must fail closed")
}

func TestAuthorizeKBAccessRejectsNilKnowledgeBaseWithoutPanic(t *testing.T) {
	ctx := callerContext()
	err := AuthorizeKBAccess(ctx, nil, []*types.KnowledgeBase{nil})
	require.Error(t, err)
}
