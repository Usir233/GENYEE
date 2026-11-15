package sandboxalloc

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/Usir233/GENYEE/internal/sandbox/allocation"
)

func TestPlaceRejectsCrossTenantAndInvalidSession(t *testing.T) {
	enableUserMode(t)
	id := uuid.NewString()
	e := newEnv(t, fakeConfigs{"cfg-a": "user"}, webSession(id))
	ctx := context.Background()

	ownerAssign := e.place(t, id, "cfg-a")
	require.Equal(t, allocation.ModeUser, ownerAssign.Mode())

	// Cross-tenant caller supplying Tenant 7's placed SessionID under Tenant 99
	// must NOT receive Tenant 7's assignment or shared UserMachine address.
	leaked, err := e.svc.Place(ctx, PlaceRequest{
		SessionTenantID: 99,
		SessionID:       id,
		ConfigTenantID:  99,
		ConfigID:        "cfg-a",
	})
	require.Error(t, err, "cross-tenant Place must be rejected even after session is placed")
	require.Empty(t, leaked.SessionID)

	// Zero tenant ID or blank session ID must fail closed immediately.
	_, err = e.svc.Place(ctx, PlaceRequest{
		SessionTenantID: 0,
		SessionID:       id,
		ConfigTenantID:  7,
		ConfigID:        "cfg-a",
	})
	require.Error(t, err, "zero SessionTenantID must be rejected")

	_, err = e.svc.Place(ctx, PlaceRequest{
		SessionTenantID: 7,
		SessionID:       "   ",
		ConfigTenantID:  7,
		ConfigID:        "cfg-a",
	})
	require.Error(t, err, "blank SessionID must be rejected")
}

func TestGCFencesCursorAndReleaseWhenLockIsLost(t *testing.T) {
	const replicaBCursor = `{"empty_since":"2026-01-02T03:04:05Z","digest":"replica-b-digest"}`

	t.Run("capped scan does not overwrite new holder cursor", func(t *testing.T) {
		mini := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
		t.Cleanup(func() { _ = client.Close() })

		thirty := 30
		e, _ := releasedMachine(t)
		ops := &reclaimRecorder{
			beforeCheck: func() {
				// Simulate lock lease expiry and takeover by Replica B mid-scan.
				require.NoError(t, mini.Set(UserMachineGCLockKey, "replica-b-token"))
				require.NoError(t, mini.Set(UserMachineGCCursorKey, replicaBCursor))
			},
		}
		gc := NewUserMachineGC(e.svc, NewWorkspaceCleaner(e.svc, fakeResolver{mgr: ops}),
			retentionConfigs{days: &thirty}, client)
		gc.batch, gc.maxRun = 1, 1
		gc.now = func() time.Time { return time.Now().UTC().Add(48 * time.Hour) }

		_, err := gc.RunOnce(context.Background())
		require.NoError(t, err)

		lockHolder, err := mini.Get(UserMachineGCLockKey)
		require.NoError(t, err)
		require.Equal(t, "replica-b-token", lockHolder, "stale replica must not release Replica B's lock")

		gotCursor, err := mini.Get(UserMachineGCCursorKey)
		require.NoError(t, err)
		require.Equal(t, replicaBCursor, gotCursor, "stale replica must not overwrite Replica B's cursor")
	})

	t.Run("completed scan does not delete new holder cursor", func(t *testing.T) {
		mini := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
		t.Cleanup(func() { _ = client.Close() })

		thirty := 30
		e, _ := releasedMachine(t)
		ops := &reclaimRecorder{
			beforeCheck: func() {
				require.NoError(t, mini.Set(UserMachineGCLockKey, "replica-b-token"))
				require.NoError(t, mini.Set(UserMachineGCCursorKey, replicaBCursor))
			},
		}
		gc := NewUserMachineGC(e.svc, NewWorkspaceCleaner(e.svc, fakeResolver{mgr: ops}),
			retentionConfigs{days: &thirty}, client)
		gc.batch, gc.maxRun = 1, 10
		gc.now = func() time.Time { return time.Now().UTC().Add(48 * time.Hour) }

		_, err := gc.RunOnce(context.Background())
		require.NoError(t, err)

		gotCursor, err := mini.Get(UserMachineGCCursorKey)
		require.NoError(t, err, "stale replica must not DEL Replica B's cursor key at scan end")
		require.Equal(t, replicaBCursor, gotCursor)
	})
}
