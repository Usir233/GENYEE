package repository

import (
	"context"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func resourceCleanupTestDB(t *testing.T, backend string) *gorm.DB {
	t.Helper()
	var db *gorm.DB
	var err error
	if backend == "postgres" {
		dsn := os.Getenv("GENYEE_STORAGE_AUDIT_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("isolated PostgreSQL audit DSN is not configured")
		}
		admin, openErr := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		require.NoError(t, openErr)
		adminSQL, openErr := admin.DB()
		require.NoError(t, openErr)
		var database string
		require.NoError(t, admin.Raw("SELECT current_database()").Scan(&database).Error)
		require.Equal(t, "storage_audit", database, "only the disposable storage_audit database may be used")
		schema := "audit_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
		t.Cleanup(func() {
			require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
			require.NoError(t, adminSQL.Close())
		})
		db, err = gorm.Open(postgres.Open(dsn+" search_path="+schema), &gorm.Config{})
	} else {
		db, err = gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "resources.db")+"?_busy_timeout=5000&_journal_mode=WAL"), &gorm.Config{})
	}
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.StoredResource{}, &types.ResourceBinding{}))
	return db
}

func TestResourceCleanupWaitsForConcurrentBinding(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			testResourceCleanupWaitsForConcurrentBinding(t, resourceCleanupTestDB(t, backend))
		})
	}
}

func testResourceCleanupWaitsForConcurrentBinding(t *testing.T, db *gorm.DB) {
	repo := NewResourceRepository(db).(*resourceRepository)
	resource := storedResource("r", "HandleAAAAAAAAAAAAAA01", 7, "hash", time.Now())
	require.NoError(t, repo.Create(context.Background(), resource))
	bindingStarted, resumeBinding := make(chan struct{}), make(chan struct{})
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("pause_binding", func(tx *gorm.DB) {
		if tx.Statement.Table == "resource_bindings" {
			close(bindingStarted)
			<-resumeBinding
		}
	}))
	cleanupStarted := make(chan struct{})
	type cleanupContextKey struct{}
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("observe_cleanup", func(tx *gorm.DB) {
		if tx.Statement.Context.Value(cleanupContextKey{}) != nil {
			select {
			case <-cleanupStarted:
			default:
				close(cleanupStarted)
			}
		}
	}))
	bindDone := make(chan error, 1)
	go func() {
		bindDone <- repo.CreateBinding(context.Background(), &types.ResourceBinding{ResourceID: resource.ID, TenantID: 7, OwnerType: types.ResourceOwnerKnowledge, OwnerID: "concurrent", Relation: types.ResourceRelationExtractedImage})
	}()
	<-bindingStarted
	type cleanupResult struct {
		resource *types.StoredResource
		err      error
	}
	cleanupDone := make(chan cleanupResult, 1)
	go func() {
		r, err := repo.ClaimUnboundDeletion(context.WithValue(context.Background(), cleanupContextKey{}, true), resource.Handle)
		cleanupDone <- cleanupResult{r, err}
	}()
	<-cleanupStarted
	select {
	case result := <-cleanupDone:
		close(resumeBinding)
		<-bindDone
		t.Fatalf("cleanup must wait for the in-flight binding transaction, got %+v", result)
	case <-time.After(50 * time.Millisecond):
	}
	close(resumeBinding)
	require.NoError(t, <-bindDone)
	result := <-cleanupDone
	require.NoError(t, result.err)
	require.Nil(t, result.resource, "the committed binding protects the file")
	live, err := repo.GetByID(context.Background(), resource.ID)
	require.NoError(t, err)
	require.NotNil(t, live)
}

func TestResourceCleanupRejectsStaleConcurrentBinding(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			db := resourceCleanupTestDB(t, backend)
			repo := NewResourceRepository(db).(*resourceRepository)
			resource := storedResource("r", "HandleAAAAAAAAAAAAAA01", 7, "hash", time.Now())
			require.NoError(t, repo.Create(context.Background(), resource))
			// This is the stale record held by a Bind that resolved before cleanup.
			stale, err := repo.GetByHandle(context.Background(), resource.Handle)
			require.NoError(t, err)
			cleaned, err := repo.ClaimUnboundDeletion(context.Background(), resource.Handle)
			require.NoError(t, err)
			require.NotNil(t, cleaned)
			require.Error(t, repo.CreateBinding(context.Background(), &types.ResourceBinding{ResourceID: stale.ID, TenantID: 7, OwnerType: types.ResourceOwnerKnowledge, OwnerID: "late", Relation: types.ResourceRelationExtractedImage}))
			count, err := repo.CountBindings(context.Background(), resource.ID)
			require.NoError(t, err)
			require.Zero(t, count)
		})
	}
}
