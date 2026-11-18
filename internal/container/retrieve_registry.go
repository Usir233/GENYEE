package container

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	esv7 "github.com/elastic/go-elasticsearch/v7"
	"github.com/elastic/go-elasticsearch/v8"
	_ "github.com/go-sql-driver/mysql" // Register MySQL wire protocol driver for Apache Doris
	"github.com/milvus-io/milvus/client/v2/milvusclient"
	"github.com/qdrant/go-client/qdrant"
	"github.com/tencent/vectordatabase-sdk-go/tcvectordb"
	"github.com/weaviate/weaviate-go-client/v5/weaviate"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/auth"
	wgrpc "github.com/weaviate/weaviate-go-client/v5/weaviate/grpc"
	"google.golang.org/grpc"
	"gorm.io/gorm"

	dorisRepo "github.com/Usir233/GENYEE/internal/application/repository/retriever/doris"
	elasticsearchRepoV7 "github.com/Usir233/GENYEE/internal/application/repository/retriever/elasticsearch/v7"
	elasticsearchRepoV8 "github.com/Usir233/GENYEE/internal/application/repository/retriever/elasticsearch/v8"
	milvusRepo "github.com/Usir233/GENYEE/internal/application/repository/retriever/milvus"
	openSearchRepo "github.com/Usir233/GENYEE/internal/application/repository/retriever/opensearch"
	postgresRepo "github.com/Usir233/GENYEE/internal/application/repository/retriever/postgres"
	qdrantRepo "github.com/Usir233/GENYEE/internal/application/repository/retriever/qdrant"
	sqliteRetrieverRepo "github.com/Usir233/GENYEE/internal/application/repository/retriever/sqlite"
	tencentVectorDBRepo "github.com/Usir233/GENYEE/internal/application/repository/retriever/tencentvectordb"
	weaviateRepo "github.com/Usir233/GENYEE/internal/application/repository/retriever/weaviate"
	"github.com/Usir233/GENYEE/internal/application/service/retriever"
	"github.com/Usir233/GENYEE/internal/config"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	secutils "github.com/Usir233/GENYEE/internal/utils"
)

// newEnvQdrantClient builds the env-configured qdrant client, refusing a host
// whitelist-only mode does not admit before the client exists.
func newEnvQdrantClient(host string, port int, apiKey string, useTLS bool) (*qdrant.Client, error) {
	if err := secutils.CheckSSRFWhitelistOnly(host); err != nil {
		return nil, err
	}
	return qdrant.NewClient(&qdrant.Config{Host: host, Port: port, APIKey: apiKey, UseTLS: useTLS})
}

// newEnvMilvusClient is newEnvQdrantClient's twin for the env-configured
// milvus client; its address carries the port, which the whitelist never
// matches on.
func newEnvMilvusClient(ctx context.Context, cfg *milvusclient.ClientConfig) (*milvusclient.Client, error) {
	host := cfg.Address
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if err := secutils.CheckSSRFWhitelistOnly(host); err != nil {
		return nil, err
	}
	return milvusclient.New(ctx, cfg)
}

func initRetrieveEngineRegistry(
	db *gorm.DB, cfg *config.Config, auditSvc interfaces.AuditLogService,
	storeRepo interfaces.VectorStoreRepository, engineFactory interfaces.EngineFactory,
) (interfaces.RetrieveEngineRegistry, error) {
	registry := retriever.NewRetrieveEngineRegistry(storeRepo, engineFactory)
	retrieveDriver := strings.Split(os.Getenv("RETRIEVE_DRIVER"), ",")
	log := logger.GetLogger(context.Background())
	auditSink := newAuditSinkAdapter(auditSvc)

	if slices.Contains(retrieveDriver, "postgres") {
		pgRepository := postgresRepo.NewPostgresRetrieveEngineRepository(db)
		if err := registry.Register(
			retriever.NewKVHybridRetrieveEngine(pgRepository, types.PostgresRetrieverEngineType),
		); err != nil {
			log.Errorf("Register postgres retrieve engine failed: %v", err)
		} else {
			log.Infof("Register postgres retrieve engine success")
		}
	}
	if slices.Contains(retrieveDriver, "sqlite") {
		sqliteRepo := sqliteRetrieverRepo.NewSQLiteRetrieveEngineRepository(db)
		if err := registry.Register(
			retriever.NewKVHybridRetrieveEngine(sqliteRepo, types.SQLiteRetrieverEngineType),
		); err != nil {
			log.Errorf("Register sqlite retrieve engine failed: %v", err)
		} else {
			log.Infof("Register sqlite retrieve engine success")
		}
	}
	if slices.Contains(retrieveDriver, "elasticsearch_v8") {
		client, err := elasticsearch.NewTypedClient(elasticsearch.Config{
			Addresses: []string{os.Getenv("ELASTICSEARCH_ADDR")},
			Username:  os.Getenv("ELASTICSEARCH_USERNAME"),
			Password:  os.Getenv("ELASTICSEARCH_PASSWORD"),
		})
		if err != nil {
			log.Errorf("Create elasticsearch_v8 client failed: %v", err)
		} else {
			elasticsearchRepo := elasticsearchRepoV8.NewElasticsearchEngineRepository(client, cfg, nil)
			if err := registry.Register(
				retriever.NewKVHybridRetrieveEngine(
					elasticsearchRepo, types.ElasticsearchRetrieverEngineType,
				),
			); err != nil {
				log.Errorf("Register elasticsearch_v8 retrieve engine failed: %v", err)
			} else {
				log.Infof("Register elasticsearch_v8 retrieve engine success")
			}
		}
	}

	if slices.Contains(retrieveDriver, "elasticsearch_v7") {
		client, err := esv7.NewClient(esv7.Config{
			Addresses: []string{os.Getenv("ELASTICSEARCH_ADDR")},
			Username:  os.Getenv("ELASTICSEARCH_USERNAME"),
			Password:  os.Getenv("ELASTICSEARCH_PASSWORD"),
		})
		if err != nil {
			log.Errorf("Create elasticsearch_v7 client failed: %v", err)
		} else {
			elasticsearchRepo := elasticsearchRepoV7.NewElasticsearchEngineRepository(client, cfg, nil)
			if err := registry.Register(
				retriever.NewKVHybridRetrieveEngine(
					elasticsearchRepo, types.ElasticsearchRetrieverEngineType,
				),
			); err != nil {
				log.Errorf("Register elasticsearch_v7 retrieve engine failed: %v", err)
			} else {
				log.Infof("Register elasticsearch_v7 retrieve engine success")
			}
		}
	}

	if slices.Contains(retrieveDriver, "opensearch") {
		cc := &types.ConnectionConfig{
			Addr:               os.Getenv("OPENSEARCH_ADDR"),
			Username:           os.Getenv("OPENSEARCH_USERNAME"),
			Password:           os.Getenv("OPENSEARCH_PASSWORD"),
			InsecureSkipVerify: strings.EqualFold(os.Getenv("OPENSEARCH_INSECURE_SKIP_VERIFY"), "true"),
		}
		client, err := openSearchRepo.NewOpenSearchClient(cc)
		if err != nil {
			log.Errorf("Create opensearch client failed: %v", err)
		} else if repo, err := openSearchRepo.NewRepository(
			context.Background(), client, "", nil, openSearchRepo.WithAuditSink(auditSink),
		); err != nil {
			log.Errorf("Create opensearch repository failed: %v", err)
		} else if err := registry.Register(
			retriever.NewKVHybridRetrieveEngine(repo, types.OpenSearchRetrieverEngineType),
		); err != nil {
			log.Errorf("Register opensearch retrieve engine failed: %v", err)
		} else {
			log.Infof("Register opensearch retrieve engine success")
		}
	}

	if slices.Contains(retrieveDriver, "qdrant") {
		store := types.FindEnvVectorStore("qdrant", os.Getenv, "__env_qdrant__")
		cc := store.ConnectionConfig

		log.Infof("Connecting to Qdrant at %s:%d (TLS: %v)", cc.Host, cc.Port, cc.UseTLS)

		client, err := newEnvQdrantClient(cc.Host, cc.Port, cc.APIKey, cc.UseTLS)
		if err != nil {
			log.Errorf("Create qdrant client failed: %v", err)
		} else {
			qdrantRepository := qdrantRepo.NewQdrantRetrieveEngineRepository(client, &store.IndexConfig)
			if err := registry.Register(
				retriever.NewKVHybridRetrieveEngine(
					qdrantRepository, types.QdrantRetrieverEngineType,
				),
			); err != nil {
				_ = client.Close()
				log.Errorf("Register qdrant retrieve engine failed: %v", err)
			} else {
				log.Infof("Register qdrant retrieve engine success")
			}
		}
	}
	if slices.Contains(retrieveDriver, "weaviate") {
		weaviateHost := os.Getenv("WEAVIATE_HOST")
		if weaviateHost == "" {
			weaviateHost = "weaviate:8080"
		}
		weaviateGrpcAddress := os.Getenv("WEAVIATE_GRPC_ADDRESS")
		if weaviateGrpcAddress == "" {
			weaviateGrpcAddress = "weaviate:50051"
		}
		weaviateScheme := os.Getenv("WEAVIATE_SCHEME")
		if weaviateScheme == "" {
			weaviateScheme = "http"
		}
		var authConfig auth.Config
		if strings.EqualFold(strings.TrimSpace(os.Getenv("WEAVIATE_AUTH_ENABLED")), "true") {
			if apiKey := strings.TrimSpace(os.Getenv("WEAVIATE_API_KEY")); apiKey != "" {
				authConfig = auth.ApiKey{Value: apiKey}
			}
		}
		weaviateClient, err := weaviate.NewClient(weaviate.Config{
			Host: weaviateHost,
			GrpcConfig: &wgrpc.Config{
				Host: weaviateGrpcAddress,
			},
			Scheme:     weaviateScheme,
			AuthConfig: authConfig,
		})
		if err != nil {
			log.Errorf("Create weaviate client failed: %v", err)
		} else {
			weaviateRepository := weaviateRepo.NewWeaviateRetrieveEngineRepository(weaviateClient, nil)
			if err := registry.Register(
				retriever.NewKVHybridRetrieveEngine(
					weaviateRepository, types.WeaviateRetrieverEngineType,
				),
			); err != nil {
				log.Errorf("Register weaviate retrieve engine failed: %v", err)
			} else {
				log.Infof("Register weaviate retrieve engine success")
			}
		}
	}
	if slices.Contains(retrieveDriver, "milvus") {
		milvusCfg := milvusclient.ClientConfig{
			DialOptions: []grpc.DialOption{
				grpc.WithConnectParams(grpc.ConnectParams{MinConnectTimeout: 5 * time.Second}),
			},
		}
		milvusAddress := os.Getenv("MILVUS_ADDRESS")
		if milvusAddress == "" {
			milvusAddress = "localhost:19530"
		}
		milvusCfg.Address = milvusAddress
		if milvusUsername := os.Getenv("MILVUS_USERNAME"); milvusUsername != "" {
			milvusCfg.Username = milvusUsername
		}
		if milvusPassword := os.Getenv("MILVUS_PASSWORD"); milvusPassword != "" {
			milvusCfg.Password = milvusPassword
		}
		if milvusDBName := os.Getenv("MILVUS_DB_NAME"); milvusDBName != "" {
			milvusCfg.DBName = milvusDBName
		}
		dialCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		milvusCli, err := newEnvMilvusClient(dialCtx, &milvusCfg)
		cancel()
		if err != nil {
			log.Errorf("Create milvus client failed: %v", err)
		} else {
			milvusRepository := milvusRepo.NewMilvusRetrieveEngineRepository(milvusCli, nil)
			if err := registry.Register(
				retriever.NewKVHybridRetrieveEngine(
					milvusRepository, types.MilvusRetrieverEngineType,
				),
			); err != nil {
				_ = milvusCli.Close(context.Background())
				log.Errorf("Register milvus retrieve engine failed: %v", err)
			} else {
				log.Infof("Register milvus retrieve engine success")
			}
		}
	}
	if slices.Contains(retrieveDriver, "doris") {
		dorisAddr := os.Getenv("DORIS_ADDR")
		if dorisAddr == "" {
			dorisAddr = "doris-fe:9030"
		}
		dorisDatabase := os.Getenv("DORIS_DATABASE")
		if dorisDatabase == "" {
			dorisDatabase = "genyee"
		}
		dorisUsername := os.Getenv("DORIS_USERNAME")
		if dorisUsername == "" {
			dorisUsername = "root"
		}
		dorisPassword := os.Getenv("DORIS_PASSWORD")
		dorisHTTPPort := 8030
		if portStr := os.Getenv("DORIS_HTTP_PORT"); portStr != "" {
			if port, err := strconv.Atoi(portStr); err == nil {
				dorisHTTPPort = port
			}
		}

		dsn := fmt.Sprintf("%s:%s@tcp(%s)/%s?charset=utf8mb4&parseTime=true&loc=Local&interpolateParams=true",
			dorisUsername, dorisPassword, dorisAddr, dorisDatabase)
		dorisDB, err := sql.Open("mysql", dsn)
		if err != nil {
			log.Errorf("Create doris client failed: %v", err)
		} else {
			dorisDB.SetMaxOpenConns(20)
			dorisDB.SetMaxIdleConns(5)
			dorisDB.SetConnMaxLifetime(time.Hour)

			httpBase := "http://" + hostFromAddr(dorisAddr) + ":" + strconv.Itoa(dorisHTTPPort)
			dorisRepository := dorisRepo.NewDorisRetrieveEngineRepository(
				dorisDB, httpBase, dorisUsername, dorisPassword, dorisDatabase, nil,
			)
			if err := registry.Register(
				retriever.NewKVHybridRetrieveEngine(
					dorisRepository, types.DorisRetrieverEngineType,
				),
			); err != nil {
				_ = dorisDB.Close()
				log.Errorf("Register doris retrieve engine failed: %v", err)
			} else {
				log.Infof("Register doris retrieve engine success: %s db=%s", dorisAddr, dorisDatabase)
			}
		}
	}
	if slices.Contains(retrieveDriver, "tencent_vectordb") {
		addr := os.Getenv("TENCENT_VECTORDB_ADDR")
		username := os.Getenv("TENCENT_VECTORDB_USERNAME")
		apiKey := os.Getenv("TENCENT_VECTORDB_API_KEY")
		if addr == "" || username == "" || apiKey == "" {
			log.Errorf("Missing Tencent VectorDB configuration")
		} else {
			client, err := tcvectordb.NewRpcClient(addr, username, apiKey, &tcvectordb.ClientOption{
				ReadConsistency: tcvectordb.EventualConsistency,
				Timeout:         10 * time.Second,
			})
			if err != nil {
				log.Errorf("Create tencent_vectordb client failed: %v", err)
			} else {
				tencentRepository := tencentVectorDBRepo.NewTencentVectorDBRetrieveEngineRepository(
					client,
					os.Getenv("TENCENT_VECTORDB_DATABASE"),
					nil,
				)
				if err := registry.Register(
					retriever.NewKVHybridRetrieveEngine(
						tencentRepository, types.TencentVectorDBRetrieverEngineType,
					),
				); err != nil {
					log.Errorf("Register tencent_vectordb retrieve engine failed: %v", err)
				} else {
					log.Infof("Register tencent_vectordb retrieve engine success")
				}
			}
		}
	}

	if storeReg, ok := registry.(*retriever.RetrieveEngineRegistry); ok {
		loadDBStoresIntoRegistry(storeReg, db, cfg, auditSink)
	}

	return registry, nil
}

// loadDBStoresIntoRegistry loads VectorStore records from DB and registers them
// in the registry's byStoreID map. Failures are logged and skipped (non-fatal).
func loadDBStoresIntoRegistry(
	storeRegistry interfaces.StoreRegistry,
	db *gorm.DB,
	cfg *config.Config,
	auditSink openSearchRepo.AuditSink,
) {
	ctx := context.Background()
	log := logger.GetLogger(ctx)

	var stores []types.VectorStore
	if err := db.Find(&stores).Error; err != nil {
		log.Warnf("Failed to load vector stores from DB: %v", err)
		return
	}

	if len(stores) == 0 {
		return
	}

	log.Infof("Loading %d vector store(s) from database", len(stores))
	for _, store := range stores {
		svc, err := createEngineServiceFromStore(ctx, store, db, cfg, auditSink)
		if err != nil {
			log.Errorf("Failed to create engine for store %s (%s): %v", store.ID, store.Name, err)
			continue
		}
		storeRegistry.RegisterWithStoreID(store.ID, svc)
		log.Infof("Registered DB vector store: id=%s, name=%s, engine=%s", store.ID, store.Name, store.EngineType)
	}
}
