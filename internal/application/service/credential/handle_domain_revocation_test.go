package credential

import (
	"context"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSavedHandleRechecksVaultDomainsWithoutWideningApproval(t *testing.T) {
	store, vault, _ := newTestHandles(t)
	ctx := context.Background()
	item, err := vault.Create(ctx, alice, LoginInput{
		Site: "example.com", Domains: []string{"old.example.com"}, Username: "alice", Password: "test-password",
	})
	require.NoError(t, err)
	handle, err := store.Create(ctx, NewHandle{
		Owner: alice, SessionID: "session", Domains: []string{"example.com", "old.example.com"}, VaultItemID: item.ID,
		Fields: []types.SecretField{{Name: types.SecureFormFieldPassword, Type: types.SecretFieldPassword}},
	})
	require.NoError(t, err)

	// The issued handle stays alive while the user removes an old destination
	// and adds a new one in credential settings.
	_, err = vault.Update(ctx, alice, item.ID, LoginPatch{Domains: []string{"new.example.com"}})
	require.NoError(t, err)
	secret, err := store.Resolve(ctx, alice.TenantID, alice.UserID, "session", handle)
	require.NoError(t, err)
	require.Equal(t, []string{"example.com"}, secret.Domains,
		"a live handle must neither retain a revoked domain nor acquire a destination absent from its original approval")
	require.Equal(t, "test-password", secret.Fields[0].Value)
}
