package container

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	modelruntime "github.com/Usir233/GENYEE/internal/models/runtime"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/duckdb/duckdb-go/v2" // Register duckdb database/sql driver
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Usir233/GENYEE/internal/application/service/file"
	"github.com/Usir233/GENYEE/internal/config"
	"github.com/Usir233/GENYEE/internal/database"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/storageallowlist"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
)

const duckDBExtensionInstallTimeout = 2 * time.Minute

// initDatabase initializes the primary relational database connection (PostgreSQL or SQLite).
func initDatabase(_ *config.Config) (*gorm.DB, error) {
	var dialector gorm.Dialector
	var migrateDSN string
	var sqliteDBPath string
	switch os.Getenv("DB_DRIVER") {
	case "postgres":
		sslMode := strings.TrimSpace(os.Getenv("DB_SSLMODE"))
		if sslMode == "" {
			sslMode = "disable"
		}
		gormDSN := fmt.Sprintf(
			"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s TimeZone=UTC",
			os.Getenv("DB_HOST"),
			os.Getenv("DB_PORT"),
			os.Getenv("DB_USER"),
			os.Getenv("DB_PASSWORD"),
			os.Getenv("DB_NAME"),
			sslMode,
		)
		dialector = postgres.Open(gormDSN)

		// URL-encode password to handle special characters like !@#
		encodedPassword := url.QueryEscape(os.Getenv("DB_PASSWORD"))

		// Check if postgres is in RETRIEVE_DRIVER to determine skip_embedding
		retrieveDriver := strings.Split(os.Getenv("RETRIEVE_DRIVER"), ",")
		skipEmbedding := "true"
		if slices.Contains(retrieveDriver, "postgres") {
			skipEmbedding = "false"
		}
		logger.Infof(context.Background(), "Skip embedding: %s", skipEmbedding)

		migrateDSN = fmt.Sprintf(
			"postgres://%s:%s@%s:%s/%s?sslmode=%s&options=-c%%20app.skip_embedding=%s",
			os.Getenv("DB_USER"),
			encodedPassword,
			os.Getenv("DB_HOST"),
			os.Getenv("DB_PORT"),
			os.Getenv("DB_NAME"),
			url.QueryEscape(sslMode),
			skipEmbedding,
		)

		// Log non-secret DB connection metadata only
		logger.Infof(context.Background(), "DB Config: user=%s host=%s port=%s dbname=%s sslmode=%s",
			os.Getenv("DB_USER"),
			os.Getenv("DB_HOST"),
			os.Getenv("DB_PORT"),
			os.Getenv("DB_NAME"),
			sslMode,
		)
	case "sqlite":
		dbPath := os.Getenv("DB_PATH")
		if dbPath == "" {
			dbPath = "./data/genyee.db"
		}
		if dir := filepath.Dir(dbPath); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("failed to create SQLite data directory %s: %w", dir, err)
			}
		}
		sqlite_vec.Auto()
		dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on"
		dialector = sqlite.Open(dsn)
		sqliteDBPath = dbPath
		migrateDSN = "sqlite3://" + dbPath
		logger.Infof(context.Background(), "DB Config: driver=sqlite path=%s", dbPath)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", os.Getenv("DB_DRIVER"))
	}
	db, err := gorm.Open(dialector, &gorm.Config{
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
		Logger: newGormLogger(os.Stdout),
	})
	if err != nil {
		return nil, err
	}

	closeRawDB := func() {
		if rawDB, dbErr := db.DB(); dbErr == nil && rawDB != nil {
			_ = rawDB.Close()
		}
	}

	// Sanity check: dialect-specific code in services compares db.Name() to
	// "postgres" / "sqlite" string literals.
	if name := db.Name(); name != "postgres" && name != "sqlite" {
		closeRawDB()
		return nil, fmt.Errorf(
			"unsupported gorm dialector %q; expected postgres or sqlite "+
				"(see vectorStoreService.isPostgres for impact)", name)
	}

	if os.Getenv("DB_DRIVER") == "sqlite" {
		sqlDB, err := db.DB()
		if err != nil {
			return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
		}
		if err := sqlDB.Ping(); err != nil {
			_ = sqlDB.Close()
			return nil, fmt.Errorf("failed to ping SQLite database: %w", err)
		}
	}

	// Run database migrations automatically unless disabled via AUTO_MIGRATE=false
	if os.Getenv("AUTO_MIGRATE") != "false" {
		logger.Infof(context.Background(), "Running database migrations...")

		autoRecover := os.Getenv("AUTO_RECOVER_DIRTY") != "false"
		migrationOpts := database.MigrationOptions{
			AutoRecoverDirty: autoRecover,
			SQLiteDBPath:     sqliteDBPath,
		}

		if err := database.RunMigrationsWithOptions(migrateDSN, migrationOpts); err != nil {
			logger.Warnf(context.Background(), "Database migration failed: %v", err)
			logger.Warnf(
				context.Background(),
				"Continuing with application startup. Please run migrations manually if needed.",
			)
		}

		resolveStorageProviderPending(db)
		migrateLegacyStorageBackends(db)

		types.ValidateModelParameters = modelruntime.ValidateRow
		if err := types.LoadBuiltinModelsConfig(context.Background(), db, config.ConfigDir()); err != nil {
			logger.Warnf(context.Background(), "Load builtin models config failed: %v", err)
		}
	} else {
		logger.Infof(context.Background(), "Auto-migration is disabled (AUTO_MIGRATE=false)")
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	if os.Getenv("DB_DRIVER") == "sqlite" {
		sqlDB.SetMaxOpenConns(1)
	} else {
		sqlDB.SetMaxIdleConns(10)
		applyDBMaxOpenConns(context.Background(), sqlDB)
	}
	sqlDB.SetConnMaxLifetime(10 * time.Minute)

	return db, nil
}

func registerDatabaseCleanup(db *gorm.DB, cleaner interfaces.ResourceCleaner) {
	if db == nil || cleaner == nil {
		return
	}
	cleaner.RegisterWithName("Database", func() error {
		sqlDB, err := db.DB()
		if err != nil {
			return fmt.Errorf("resolve sql.DB for cleanup: %w", err)
		}
		return sqlDB.Close()
	})
}

// resolveStorageProviderPending replaces the "__pending_env__" sentinel in
// knowledge_bases.storage_provider_config with the actual STORAGE_TYPE from the environment.
func resolveStorageProviderPending(db *gorm.DB) {
	storageType := strings.TrimSpace(os.Getenv("STORAGE_TYPE"))
	if storageType == "" {
		storageType = "local"
	}
	storageType = strings.ToLower(storageType)
	encodedCfg, err := json.Marshal(map[string]string{"provider": storageType})
	if err != nil {
		logger.Warnf(context.Background(), "Failed to marshal storage provider config: %v", err)
		return
	}

	query := "UPDATE knowledge_bases SET storage_provider_config = ? WHERE " +
		"storage_provider_config IS NOT NULL AND storage_provider_config->>'provider' = '__pending_env__'"
	if db.Name() == "sqlite" {
		// SQLite TEXT columns may hold empty strings (""); guard with json_valid
		// before json_extract so one legacy row never aborts the whole UPDATE.
		query = "UPDATE knowledge_bases SET storage_provider_config = ? WHERE " +
			"storage_provider_config IS NOT NULL AND json_valid(storage_provider_config) = 1 " +
			"AND json_extract(storage_provider_config, '$.provider') = '__pending_env__'"
	}

	result := db.Exec(query, string(encodedCfg))
	if result.Error != nil {
		logger.Warnf(context.Background(), "Failed to resolve __pending_env__ storage providers: %v", result.Error)
	} else if result.RowsAffected > 0 {
		logger.Infof(
			context.Background(),
			"Resolved %d knowledge bases with __pending_env__ storage provider -> %s",
			result.RowsAffected,
			storageType,
		)
	}

	syncSequences(db)
	resetPendingTasks(db)
}

// migrateLegacyStorageBackends backfills the storage_backends table from each
// workspace's legacy StorageEngineConfig (or environment defaults) and binds
// existing knowledge bases to the resulting backend.
func migrateLegacyStorageBackends(db *gorm.DB) {
	var tenants []*types.Tenant
	if err := db.Find(&tenants).Error; err != nil {
		logger.Warnf(context.Background(), "Failed to load workspaces for storage backend migration: %v", err)
		return
	}
	if len(tenants) == 0 {
		return
	}

	var aliases []*types.StorageBackend
	if err := db.Where("legacy_alias = ?", true).Find(&aliases).Error; err != nil {
		logger.Warnf(context.Background(), "Failed to load legacy storage aliases: %v", err)
		return
	}
	existingAliases := make(map[uint64]map[string]*types.StorageBackend, len(aliases))
	for _, alias := range aliases {
		byProvider := existingAliases[alias.TenantID]
		if byProvider == nil {
			byProvider = make(map[string]*types.StorageBackend)
			existingAliases[alias.TenantID] = byProvider
		}
		byProvider[alias.Provider] = alias
	}

	for _, tenant := range tenants {
		if err := migrateTenantLegacyStorage(db, tenant, existingAliases[tenant.ID]); err != nil {
			logger.Warnf(
				context.Background(),
				"Failed to migrate storage backends for workspace %d: %v",
				tenant.ID,
				err,
			)
		}
	}
}

func migrateTenantLegacyStorage(
	db *gorm.DB,
	tenant *types.Tenant,
	existingByProvider map[string]*types.StorageBackend,
) error {
	return db.Transaction(func(tx *gorm.DB) error {
		legacy := tenant.StorageEngineConfig
		defaultProvider := ""
		if legacy != nil {
			defaultProvider = strings.ToLower(strings.TrimSpace(legacy.DefaultProvider))
		}
		if defaultProvider == "" {
			defaultProvider = strings.ToLower(strings.TrimSpace(os.Getenv("STORAGE_TYPE")))
		}
		if defaultProvider == "" {
			defaultProvider = "local"
		}

		backendIDs := make(map[string]string)
		for _, provider := range storageallowlist.Supported() {
			if existing := existingByProvider[provider]; existing != nil {
				if existing.Source == types.StorageBackendSourceEnv {
					desired := types.StorageBackendFromLegacy(tenant.ID, provider, legacy)
					if desired == nil && provider == defaultProvider {
						desired = types.StorageBackendFromEnvironment(tenant.ID)
					}
					if desired != nil && desired.Provider == provider {
						err := tx.Model(&types.StorageBackend{}).
							Where("id = ?", existing.ID).
							Updates(map[string]interface{}{
								"name":       desired.Name,
								"config":     desired.Config,
								"source":     desired.Source,
								"status":     desired.Status,
								"updated_at": time.Now(),
							}).
							Error
						if err != nil {
							return fmt.Errorf("refresh %s storage alias %s: %w", provider, existing.ID, err)
						}
					}
				}
				backendIDs[provider] = existing.ID
				continue
			}
			backend := types.StorageBackendFromLegacy(tenant.ID, provider, legacy)
			if backend == nil && provider == defaultProvider {
				backend = types.StorageBackendFromEnvironment(tenant.ID)
			}
			if backend == nil {
				continue
			}
			if err := tx.Create(backend).Error; err != nil {
				return fmt.Errorf("create %s storage backend: %w", provider, err)
			}
			backendIDs[provider] = backend.ID
		}

		if tenant.DefaultStorageBackendID == nil {
			if id := backendIDs[defaultProvider]; id != "" {
				err := tx.Model(&types.Tenant{}).
					Where("id = ?", tenant.ID).
					Update("default_storage_backend_id", id).
					Error
				if err != nil {
					return fmt.Errorf("set default_storage_backend_id: %w", err)
				}
			}
		}

		var kbs []*types.KnowledgeBase
		if err := tx.Where("tenant_id = ? AND storage_backend_id IS NULL", tenant.ID).Find(&kbs).Error; err != nil {
			return fmt.Errorf("list unbound knowledge bases: %w", err)
		}
		for _, kb := range kbs {
			provider := kb.GetStorageProvider()
			if provider == "" {
				provider = defaultProvider
			}
			if id := backendIDs[provider]; id != "" {
				err := tx.Model(&types.KnowledgeBase{}).
					Where("id = ? AND storage_backend_id IS NULL", kb.ID).
					Update("storage_backend_id", id).
					Error
				if err != nil {
					return fmt.Errorf("bind knowledge base %s storage backend: %w", kb.ID, err)
				}
			}
		}
		return nil
	})
}

// syncSequences ensures PostgreSQL sequences for auto-increment columns (seq_id)
// are at least as high as the current MAX value in each table.
func syncSequences(db *gorm.DB) {
	if db.Name() != "postgres" {
		return
	}
	pairs := [][2]string{
		{"chunks", "chunks_seq_id_seq"},
		{"knowledge_tags", "knowledge_tags_seq_id_seq"},
	}
	for _, p := range pairs {
		table, seq := p[0], p[1]
		stmt := fmt.Sprintf(
			`SELECT setval('%s', GREATEST(nextval('%s'), (SELECT COALESCE(MAX(seq_id), 0) FROM %s)))`,
			seq, seq, table,
		)
		if err := db.Exec(stmt).Error; err != nil {
			logger.Warnf(context.Background(), "Failed to sync sequence %s: %v", seq, err)
		} else {
			logger.Infof(context.Background(), "Synced sequence %s with table %s", seq, table)
		}
	}
}

// initFileService initializes the file storage service backed by the resource catalog.
func initFileService(cfg *config.Config, catalog interfaces.ResourceCatalog) (interfaces.FileService, error) {
	inner, err := initRawFileService(cfg)
	if err != nil {
		return nil, err
	}
	if _, dummy := inner.(*file.DummyFileService); dummy {
		return file.NewResourceCatalogFileService(inner, catalog), nil
	}
	return file.NewGlobalResourceCatalogFileService(inner, catalog), nil
}

func initRawFileService(_ *config.Config) (interfaces.FileService, error) {
	storageType := strings.TrimSpace(os.Getenv("STORAGE_TYPE"))
	if storageType == "" {
		storageType = "local"
	}
	switch storageType {
	case "minio":
		if os.Getenv("MINIO_ENDPOINT") == "" ||
			os.Getenv("MINIO_ACCESS_KEY_ID") == "" ||
			os.Getenv("MINIO_SECRET_ACCESS_KEY") == "" ||
			os.Getenv("MINIO_BUCKET_NAME") == "" {
			return nil, fmt.Errorf("missing MinIO configuration")
		}
		return file.NewMinioFileService(
			os.Getenv("MINIO_ENDPOINT"),
			os.Getenv("MINIO_ACCESS_KEY_ID"),
			os.Getenv("MINIO_SECRET_ACCESS_KEY"),
			os.Getenv("MINIO_BUCKET_NAME"),
			strings.EqualFold(os.Getenv("MINIO_USE_SSL"), "true"),
		)
	case "cos":
		if os.Getenv("COS_BUCKET_NAME") == "" ||
			os.Getenv("COS_REGION") == "" ||
			os.Getenv("COS_SECRET_ID") == "" ||
			os.Getenv("COS_SECRET_KEY") == "" ||
			os.Getenv("COS_PATH_PREFIX") == "" {
			return nil, fmt.Errorf("missing COS configuration")
		}
		return file.NewCosFileServiceWithTempBucket(
			os.Getenv("COS_BUCKET_NAME"),
			os.Getenv("COS_REGION"),
			os.Getenv("COS_SECRET_ID"),
			os.Getenv("COS_SECRET_KEY"),
			os.Getenv("COS_PATH_PREFIX"),
			os.Getenv("COS_TEMP_BUCKET_NAME"),
			os.Getenv("COS_TEMP_REGION"),
		)
	case "tos":
		if os.Getenv("TOS_ENDPOINT") == "" ||
			os.Getenv("TOS_REGION") == "" ||
			os.Getenv("TOS_ACCESS_KEY") == "" ||
			os.Getenv("TOS_SECRET_KEY") == "" ||
			os.Getenv("TOS_BUCKET_NAME") == "" {
			return nil, fmt.Errorf("missing TOS configuration")
		}
		return file.NewTosFileServiceWithTempBucket(
			os.Getenv("TOS_ENDPOINT"),
			os.Getenv("TOS_REGION"),
			os.Getenv("TOS_ACCESS_KEY"),
			os.Getenv("TOS_SECRET_KEY"),
			os.Getenv("TOS_BUCKET_NAME"),
			os.Getenv("TOS_PATH_PREFIX"),
			os.Getenv("TOS_TEMP_BUCKET_NAME"),
			os.Getenv("TOS_TEMP_REGION"),
		)
	case "s3":
		accessKey, secretKey := os.Getenv("S3_ACCESS_KEY"), os.Getenv("S3_SECRET_KEY")
		if os.Getenv("S3_REGION") == "" ||
			os.Getenv("S3_BUCKET_NAME") == "" ||
			(accessKey == "") != (secretKey == "") {
			return nil, fmt.Errorf("missing S3 configuration")
		}
		pathPrefix := os.Getenv("S3_PATH_PREFIX")
		if pathPrefix == "" {
			pathPrefix = "genyee/"
		}
		return file.NewS3FileService(
			os.Getenv("S3_ENDPOINT"),
			accessKey,
			secretKey,
			os.Getenv("S3_BUCKET_NAME"),
			os.Getenv("S3_REGION"),
			pathPrefix,
		)
	case "obs":
		if os.Getenv("OBS_ENDPOINT") == "" ||
			os.Getenv("OBS_ACCESS_KEY") == "" ||
			os.Getenv("OBS_SECRET_KEY") == "" ||
			os.Getenv("OBS_BUCKET_NAME") == "" {
			return nil, fmt.Errorf("missing OBS configuration")
		}
		obsRegion := os.Getenv("OBS_REGION")
		obsPathPrefix := os.Getenv("OBS_PATH_PREFIX")
		if obsPathPrefix == "" {
			obsPathPrefix = "genyee/"
		}
		return file.NewObsFileService(
			os.Getenv("OBS_ENDPOINT"),
			obsRegion,
			os.Getenv("OBS_ACCESS_KEY"),
			os.Getenv("OBS_SECRET_KEY"),
			os.Getenv("OBS_BUCKET_NAME"),
			obsPathPrefix,
		)
	case "oss":
		if os.Getenv("OSS_ENDPOINT") == "" ||
			os.Getenv("OSS_REGION") == "" ||
			os.Getenv("OSS_ACCESS_KEY") == "" ||
			os.Getenv("OSS_SECRET_KEY") == "" ||
			os.Getenv("OSS_BUCKET_NAME") == "" {
			return nil, fmt.Errorf("missing OSS configuration")
		}
		pathPrefix := os.Getenv("OSS_PATH_PREFIX")
		if pathPrefix == "" {
			pathPrefix = "genyee/"
		}
		return file.NewOssFileServiceWithTempBucket(
			os.Getenv("OSS_ENDPOINT"),
			os.Getenv("OSS_REGION"),
			os.Getenv("OSS_ACCESS_KEY"),
			os.Getenv("OSS_SECRET_KEY"),
			os.Getenv("OSS_BUCKET_NAME"),
			pathPrefix,
			os.Getenv("OSS_TEMP_BUCKET_NAME"),
			os.Getenv("OSS_TEMP_REGION"),
		)
	case "local":
		baseDir := os.Getenv("LOCAL_STORAGE_BASE_DIR")
		if baseDir == "" {
			baseDir = "/data/files"
		}
		externalURL := strings.TrimSpace(os.Getenv("APP_EXTERNAL_URL"))
		return file.NewLocalFileService(baseDir, externalURL), nil
	case "dummy":
		return file.NewDummyFileService(), nil
	default:
		return nil, fmt.Errorf("unsupported storage type: %s", storageType)
	}
}

// NewDuckDB opens an in-memory DuckDB instance and optionally loads spatial/excel extensions.
func NewDuckDB() (*sql.DB, error) {
	sqlDB, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("failed to open duckdb: %w", err)
	}

	if strings.EqualFold(os.Getenv("DUCKDB_SKIP_EXTENSION_LOAD"), "true") ||
		os.Getenv("DUCKDB_SKIP_EXTENSION_LOAD") == "1" {
		logger.Infof(context.Background(),
			"[DuckDB] Skipping spatial/excel extension install/load "+
				"(DUCKDB_SKIP_EXTENSION_LOAD is set; xlsx ingest may fail without them)")
	} else {
		extCtx, cancel := context.WithTimeout(context.Background(), duckDBExtensionInstallTimeout)
		defer cancel()
		for _, ext := range []string{"spatial", "excel"} {
			if _, err := sqlDB.ExecContext(extCtx, fmt.Sprintf("INSTALL %s;", ext)); err != nil {
				logger.Warnf(extCtx, "[DuckDB] Failed to install %s extension: %v", ext, err)
			}
			if _, err := sqlDB.ExecContext(extCtx, fmt.Sprintf("LOAD %s;", ext)); err != nil {
				logger.Warnf(extCtx, "[DuckDB] Failed to load %s extension: %v", ext, err)
			}
		}
	}

	return sqlDB, nil
}

func registerDuckDBCleanup(duckDB *sql.DB, cleaner interfaces.ResourceCleaner) {
	if duckDB == nil || cleaner == nil {
		return
	}
	cleaner.RegisterWithName("DuckDB", func() error {
		return duckDB.Close()
	})
}
