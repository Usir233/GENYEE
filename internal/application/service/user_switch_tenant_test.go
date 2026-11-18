package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Usir233/GENYEE/internal/application/access"
	"github.com/Usir233/GENYEE/internal/config"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
)

// switchTenantUserRepo stores users by value so UpdateUser behaves like
// a real persistence round-trip: a change written by the service is only
// visible to later reads once UpdateUser succeeds.
type switchTenantUserRepo struct {
	interfaces.UserRepository
	users       map[string]types.User
	failUpdates bool
	updateCalls int
}

func (r *switchTenantUserRepo) GetUserByID(_ context.Context, id string) (*types.User, error) {
	stored, ok := r.users[id]
	if !ok {
		return nil, errors.New("user not found")
	}
	user := stored
	return &user, nil
}

func (r *switchTenantUserRepo) UpdateUser(_ context.Context, user *types.User) error {
	r.updateCalls++
	if r.failUpdates {
		return errors.New("simulated update failure")
	}
	r.users[user.ID] = *user
	return nil
}

type countingAuthTokenRepo struct {
	stubAuthTokenRepo
	createCalls int
}

func (s *countingAuthTokenRepo) CreateToken(ctx context.Context, token *types.AuthToken) error {
	s.createCalls++
	return s.stubAuthTokenRepo.CreateToken(ctx, token)
}

func newSwitchTenantTestService(repo *switchTenantUserRepo, memberSvc interfaces.TenantMemberService) *userService {
	return &userService{
		userRepo:      repo,
		tokenRepo:     &stubAuthTokenRepo{tokens: map[string]*types.AuthToken{}},
		tenantService: &provisioningTenantService{},
		memberService: memberSvc,
	}
}

func withLiveSwitchSession(ctx context.Context, t *testing.T, svc *userService, user *types.User) context.Context {
	t.Helper()
	accessTok, _, err := svc.generateTokensForTenant(ctx, user, user.TenantID)
	if err != nil {
		t.Fatalf("seed switch session: %v", err)
	}
	return access.WithPresentedAccessToken(ctx, accessTok)
}

// TestSwitchTenantRecordsLastActiveTenantPreference pins the landing
// contract for non-SPA switch paths (server-to-server re-signs, API
// clients): a successful switch persists the target workspace as the
// user's "last active tenant" preference, the response agrees with it,
// and a fresh login resolves back to the switched workspace.
func TestSwitchTenantRecordsLastActiveTenantPreference(t *testing.T) {
	ctx := context.Background()
	repo := &switchTenantUserRepo{users: map[string]types.User{
		"alice": {ID: "alice", TenantID: 7, IsActive: true},
	}}
	memberSvc := &membershipLookupService{
		byTenant: map[uint64]*types.TenantMember{
			42: {TenantID: 42, Status: types.TenantMemberStatusActive},
		},
	}
	svc := newSwitchTenantTestService(repo, memberSvc)
	user, _ := repo.GetUserByID(ctx, "alice")
	ctx = withLiveSwitchSession(ctx, t, svc, user)

	resp, err := svc.SwitchTenant(ctx, user, 42, "")
	if err != nil {
		t.Fatalf("SwitchTenant: %v", err)
	}
	if resp.RefreshToken != "" {
		t.Fatalf("RefreshToken = %q, want empty when switching without refresh_token", resp.RefreshToken)
	}

	stored, _ := repo.GetUserByID(ctx, "alice")
	if stored.Preferences.LastActiveTenantID == nil || *stored.Preferences.LastActiveTenantID != 42 {
		t.Fatalf("persisted LastActiveTenantID = %v, want 42", stored.Preferences.LastActiveTenantID)
	}
	if resp.User.Preferences.LastActiveTenantID == nil || *resp.User.Preferences.LastActiveTenantID != 42 {
		t.Fatalf("response LastActiveTenantID = %v, want 42 (response must agree with the active workspace)",
			resp.User.Preferences.LastActiveTenantID)
	}
	if got := svc.resolveLoginTenantID(ctx, stored); got != 42 {
		t.Fatalf("fresh login after switch resolves to %d, want 42", got)
	}
}

// TestSwitchTenantBackToHomeWritesHomePreference pins the unconditional
// write: switching back to home records the home ID instead of clearing.
// resolveLoginTenantID treats *home == cleared, so landing semantics are
// identical without a server-side clear sentinel.
func TestSwitchTenantBackToHomeWritesHomePreference(t *testing.T) {
	ctx := context.Background()
	repo := &switchTenantUserRepo{users: map[string]types.User{
		"alice": {ID: "alice", TenantID: 7, IsActive: true},
	}}
	memberSvc := &membershipLookupService{
		byTenant: map[uint64]*types.TenantMember{
			7: {TenantID: 7, Status: types.TenantMemberStatusActive},
		},
	}
	svc := newSwitchTenantTestService(repo, memberSvc)
	user, _ := repo.GetUserByID(ctx, "alice")
	ctx = withLiveSwitchSession(ctx, t, svc, user)

	if _, err := svc.SwitchTenant(ctx, user, 7, ""); err != nil {
		t.Fatalf("SwitchTenant back to home: %v", err)
	}

	stored, _ := repo.GetUserByID(ctx, "alice")
	want := uint64(7)
	if stored.Preferences.LastActiveTenantID == nil || *stored.Preferences.LastActiveTenantID != want {
		t.Fatalf("persisted LastActiveTenantID = %v, want %d (unconditional write)",
			stored.Preferences.LastActiveTenantID, want)
	}
}

// TestSwitchTenantPreferenceWriteFailureAbortsSwitch pins the
// fail-closed contract: RefreshToken re-resolves from last-active, so
// a switch that cannot persist the preference must not issue tokens
// whose later refresh would bounce to a different workspace.
func TestSwitchTenantPreferenceWriteFailureAbortsSwitch(t *testing.T) {
	ctx := context.Background()
	repo := &switchTenantUserRepo{
		users:       map[string]types.User{"alice": {ID: "alice", TenantID: 7, IsActive: true}},
		failUpdates: true,
	}
	memberSvc := &membershipLookupService{
		byTenant: map[uint64]*types.TenantMember{
			7:  {TenantID: 7, Status: types.TenantMemberStatusActive},
			42: {TenantID: 42, Status: types.TenantMemberStatusActive},
		},
	}
	tokens := &countingAuthTokenRepo{stubAuthTokenRepo: stubAuthTokenRepo{tokens: map[string]*types.AuthToken{}}}
	svc := newSwitchTenantTestService(repo, memberSvc)
	svc.tokenRepo = tokens
	user, _ := repo.GetUserByID(ctx, "alice")
	ctx = withLiveSwitchSession(ctx, t, svc, user)
	tokens.createCalls = 0

	resp, err := svc.SwitchTenant(ctx, user, 42, "")
	if err == nil {
		t.Fatal("SwitchTenant must fail when the preference write fails")
	}
	if resp != nil {
		t.Fatal("failed switch must not return a login response")
	}
	if tokens.createCalls != 0 {
		t.Fatalf("CreateToken calls = %d, want 0 (no tokens before preference persists)", tokens.createCalls)
	}
	stored, _ := repo.GetUserByID(ctx, "alice")
	if stored.Preferences.LastActiveTenantID != nil {
		t.Fatalf("LastActiveTenantID = %v, want nil after failed write", stored.Preferences.LastActiveTenantID)
	}
	if got := svc.resolveLoginTenantID(ctx, stored); got != 7 {
		t.Fatalf("fresh login after failed switch resolves to %d, want home 7", got)
	}
}

// TestSwitchTenantSuperuserRecordsPreferenceWithoutMembership covers
// CanAccessAllTenants switching into a tenant with no membership row:
// the preference still has to persist so refresh/login follow the switch.
func TestSwitchTenantSuperuserRecordsPreferenceWithoutMembership(t *testing.T) {
	ctx := context.Background()
	repo := &switchTenantUserRepo{users: map[string]types.User{
		"alice": {ID: "alice", TenantID: 7, IsActive: true, CanAccessAllTenants: true},
	}}
	svc := newSwitchTenantTestService(repo, &membershipLookupService{byTenant: map[uint64]*types.TenantMember{}})
	svc.config = &config.Config{Tenant: &config.TenantConfig{EnableCrossTenantAccess: true}}
	user, _ := repo.GetUserByID(ctx, "alice")
	ctx = withLiveSwitchSession(ctx, t, svc, user)

	resp, err := svc.SwitchTenant(ctx, user, 42, "")
	if err != nil {
		t.Fatalf("SwitchTenant superuser: %v", err)
	}
	stored, _ := repo.GetUserByID(ctx, "alice")
	if stored.Preferences.LastActiveTenantID == nil || *stored.Preferences.LastActiveTenantID != 42 {
		t.Fatalf("persisted LastActiveTenantID = %v, want 42", stored.Preferences.LastActiveTenantID)
	}
	if resp.User.Preferences.LastActiveTenantID == nil || *resp.User.Preferences.LastActiveTenantID != 42 {
		t.Fatalf("response LastActiveTenantID = %v, want 42", resp.User.Preferences.LastActiveTenantID)
	}
}

// TestSwitchTenantPreservesOidcOnlyLogin proves the preference write
// goes through the PATCH merge, not a blob overwrite.
func TestSwitchTenantPreservesOidcOnlyLogin(t *testing.T) {
	ctx := context.Background()
	oidcOnly := true
	repo := &switchTenantUserRepo{users: map[string]types.User{
		"alice": {
			ID:       "alice",
			TenantID: 7,
			IsActive: true,
			Preferences: types.UserPreferences{
				OidcOnlyLogin: &oidcOnly,
			},
		},
	}}
	memberSvc := &membershipLookupService{
		byTenant: map[uint64]*types.TenantMember{
			42: {TenantID: 42, Status: types.TenantMemberStatusActive},
		},
	}
	svc := newSwitchTenantTestService(repo, memberSvc)
	user, _ := repo.GetUserByID(ctx, "alice")
	ctx = withLiveSwitchSession(ctx, t, svc, user)

	if _, err := svc.SwitchTenant(ctx, user, 42, ""); err != nil {
		t.Fatalf("SwitchTenant: %v", err)
	}
	stored, _ := repo.GetUserByID(ctx, "alice")
	if stored.Preferences.OidcOnlyLogin == nil || !*stored.Preferences.OidcOnlyLogin {
		t.Fatal("OidcOnlyLogin was dropped by the last-active write")
	}
	if stored.Preferences.LastActiveTenantID == nil || *stored.Preferences.LastActiveTenantID != 42 {
		t.Fatalf("LastActiveTenantID = %v, want 42", stored.Preferences.LastActiveTenantID)
	}
}

// TestSwitchTenantNonMemberWritesNoPreference guards the write
// placement: the preference is only recorded after membership
// validation passes, so a rejected switch leaves the user's state
// untouched.
func TestSwitchTenantNonMemberWritesNoPreference(t *testing.T) {
	ctx := context.Background()
	repo := &switchTenantUserRepo{users: map[string]types.User{
		"alice": {ID: "alice", TenantID: 7, IsActive: true},
	}}
	svc := newSwitchTenantTestService(repo, &membershipLookupService{byTenant: map[uint64]*types.TenantMember{}})
	user, _ := repo.GetUserByID(ctx, "alice")
	ctx = withLiveSwitchSession(ctx, t, svc, user)

	if _, err := svc.SwitchTenant(ctx, user, 42, ""); !errors.Is(err, ErrMembershipNotFound) {
		t.Fatalf("SwitchTenant for non-member: err = %v, want ErrMembershipNotFound", err)
	}
	if repo.updateCalls != 0 {
		t.Fatalf("UpdateUser calls = %d, want 0 (no preference write before validation)", repo.updateCalls)
	}
}
