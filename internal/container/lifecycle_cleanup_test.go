package container

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
)

type closableDocReader struct {
	interfaces.DocumentReader
	closed atomic.Int32
}

func (c *closableDocReader) Close() error {
	c.closed.Add(1)
	return nil
}

type closableStreamManager struct {
	interfaces.StreamManager
	closed atomic.Int32
}

func (c *closableStreamManager) Close() error {
	c.closed.Add(1)
	return nil
}

func TestInfrastructureCleanups_CloseDBRedisDocReaderStreamAndDuckDB(t *testing.T) {
	cleaner := NewResourceCleaner()

	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	registerDatabaseCleanup(gormDB, cleaner)

	mini := miniredis.RunT(t)
	t.Setenv("REDIS_ADDR", mini.Addr())
	t.Setenv("REDIS_DB", "0")
	rdb, err := initRedisClient()
	require.NoError(t, err)
	require.NotNil(t, rdb)
	registerRedisCleanup(rdb, cleaner)

	docReader := &closableDocReader{}
	registerDocReaderCleanup(docReader, cleaner)

	streams := &closableStreamManager{}
	registerStreamManagerCleanup(streams, cleaner)

	duck, err := sql.Open("duckdb", ":memory:")
	require.NoError(t, err)
	registerDuckDBCleanup(duck, cleaner)

	errs := cleaner.Cleanup(context.Background())
	require.Empty(t, errs)
	require.Equal(t, int32(1), docReader.closed.Load())
	require.Equal(t, int32(1), streams.closed.Load())

	// Closed handles must reject subsequent operations.
	require.Error(t, rdb.Ping(context.Background()).Err())
	require.Error(t, duck.Ping())
	rawSQL, err := gormDB.DB()
	require.NoError(t, err)
	require.Error(t, rawSQL.Ping())
}

func TestMigrateLegacyStorageBackends_RollsBackTenantOnPartialFailure(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE tenants (
		id INTEGER PRIMARY KEY, name TEXT, storage_engine_config TEXT,
		default_storage_backend_id TEXT, updated_at DATETIME, deleted_at DATETIME
	)`).Error)
	// Deliberately omit knowledge_bases table so the final step inside
	// migrateTenantLegacyStorage fails, proving the earlier StorageBackend
	// insert and Tenant update roll back atomically.
	require.NoError(t, db.AutoMigrate(&types.StorageBackend{}))

	tenantConfig, err := (&types.StorageEngineConfig{
		DefaultProvider: "local",
		Local:           &types.LocalEngineConfig{PathPrefix: "workspace-tx"},
	}).Value()
	require.NoError(t, err)
	require.NoError(t, db.Exec(
		"INSERT INTO tenants(id, name, storage_engine_config) VALUES (?, ?, ?)",
		11, "workspace-tx", tenantConfig,
	).Error)

	migrateLegacyStorageBackends(db)

	var backendCount int64
	require.NoError(t, db.Model(&types.StorageBackend{}).Where("tenant_id = ?", 11).Count(&backendCount).Error)
	require.Zero(t, backendCount, "failed tenant storage migration must roll back created backend rows")

	var defaultBackendID sql.NullString
	require.NoError(
		t,
		db.Raw("SELECT default_storage_backend_id FROM tenants WHERE id = 11").Scan(&defaultBackendID).Error,
	)
	require.False(t, defaultBackendID.Valid)
}
