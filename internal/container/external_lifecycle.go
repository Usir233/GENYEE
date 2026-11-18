package container

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/panjf2000/ants/v2"
	"github.com/redis/go-redis/v9"
	"go.uber.org/dig"

	"github.com/Usir233/GENYEE/internal/application/service"
	"github.com/Usir233/GENYEE/internal/application/service/sandboxalloc"
	"github.com/Usir233/GENYEE/internal/common"
	"github.com/Usir233/GENYEE/internal/config"
	"github.com/Usir233/GENYEE/internal/datasource"
	confluenceConnector "github.com/Usir233/GENYEE/internal/datasource/connector/confluence"
	dingtalkConnector "github.com/Usir233/GENYEE/internal/datasource/connector/dingtalk"
	"github.com/Usir233/GENYEE/internal/datasource/connector/feishu/core"
	"github.com/Usir233/GENYEE/internal/datasource/connector/feishu/drive"
	"github.com/Usir233/GENYEE/internal/datasource/connector/feishu/wiki"
	gitlabConnector "github.com/Usir233/GENYEE/internal/datasource/connector/gitlab"
	imaConnector "github.com/Usir233/GENYEE/internal/datasource/connector/ima"
	nextcloudConnector "github.com/Usir233/GENYEE/internal/datasource/connector/nextcloud"
	notionConnector "github.com/Usir233/GENYEE/internal/datasource/connector/notion"
	rssConnector "github.com/Usir233/GENYEE/internal/datasource/connector/rss"
	seafileConnector "github.com/Usir233/GENYEE/internal/datasource/connector/seafile"
	yuqueConnector "github.com/Usir233/GENYEE/internal/datasource/connector/yuque"
	imPkg "github.com/Usir233/GENYEE/internal/im"
	"github.com/Usir233/GENYEE/internal/im/dingtalk"
	"github.com/Usir233/GENYEE/internal/im/feishu"
	"github.com/Usir233/GENYEE/internal/im/mattermost"
	"github.com/Usir233/GENYEE/internal/im/qqbot"
	"github.com/Usir233/GENYEE/internal/im/slack"
	"github.com/Usir233/GENYEE/internal/im/telegram"
	"github.com/Usir233/GENYEE/internal/im/wechat"
	"github.com/Usir233/GENYEE/internal/im/wecom"
	"github.com/Usir233/GENYEE/internal/im/yunzhijia"
	"github.com/Usir233/GENYEE/internal/infrastructure/docparser"
	infra_web_search "github.com/Usir233/GENYEE/internal/infrastructure/web_search"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/mcp"
	"github.com/Usir233/GENYEE/internal/models/utils/ollama"
	"github.com/Usir233/GENYEE/internal/tracing/langfuse"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
)

const (
	redisPingTimeout       = 5 * time.Second
	neo4jVerifyAuthTimeout = 10 * time.Second
)

// initLangfuse initializes the Langfuse ingestion client.
func initLangfuse() (*langfuse.Manager, error) {
	cfg := langfuse.LoadConfigFromEnv()
	return langfuse.Init(cfg)
}

func initRedisClient() (*redis.Client, error) {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		logger.Infof(context.Background(), "[Redis] No REDIS_ADDR configured, Redis disabled (Lite mode)")
		return nil, nil
	}
	db, err := strconv.Atoi(os.Getenv("REDIS_DB"))
	if err != nil {
		db = 0
	}

	client := redis.NewClient(&redis.Options{
		Addr:      redisAddr,
		Username:  os.Getenv("REDIS_USERNAME"),
		Password:  os.Getenv("REDIS_PASSWORD"),
		DB:        db,
		TLSConfig: common.RedisTLSConfig(),
	})

	pingCtx, cancel := context.WithTimeout(context.Background(), redisPingTimeout)
	defer cancel()
	if _, err = client.Ping(pingCtx).Result(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("connect to redis: %w", err)
	}

	return client, nil
}

func registerRedisCleanup(client *redis.Client, cleaner interfaces.ResourceCleaner) {
	if client == nil || cleaner == nil {
		return
	}
	cleaner.RegisterWithName("RedisClient", func() error {
		return client.Close()
	})
}

// initAntsPool initializes a managed goroutine pool for concurrent task execution.
func initAntsPool(_ *config.Config) (*ants.Pool, error) {
	poolSize := os.Getenv("CONCURRENCY_POOL_SIZE")
	if poolSize == "" {
		poolSize = "5"
	}
	poolSizeInt, err := strconv.Atoi(poolSize)
	if err != nil {
		return nil, err
	}
	return ants.NewPool(poolSizeInt, ants.WithPreAlloc(true))
}

func registerPoolCleanup(pool *ants.Pool, cleaner interfaces.ResourceCleaner) {
	if pool == nil || cleaner == nil {
		return
	}
	cleaner.RegisterWithName("AntsPool", func() error {
		pool.Release()
		return nil
	})
}

func registerLangfuseCleanup(mgr *langfuse.Manager, cleaner interfaces.ResourceCleaner) {
	if mgr == nil || cleaner == nil {
		return
	}
	cleaner.RegisterWithName("Langfuse", func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return mgr.Shutdown(ctx)
	})
}

func registerMCPCleanup(mgr *mcp.MCPManager, cleaner interfaces.ResourceCleaner) {
	if mgr == nil || cleaner == nil {
		return
	}
	cleaner.RegisterWithName("MCPManager", func() error {
		mgr.Shutdown()
		return nil
	})
}

// initDocReaderClient initializes the DocumentReader client (gRPC or HTTP).
func initDocReaderClient(_ *config.Config) (interfaces.DocumentReader, error) {
	addr := strings.TrimSpace(os.Getenv("DOCREADER_ADDR"))
	transport := strings.TrimSpace(os.Getenv("DOCREADER_TRANSPORT"))
	if transport == "" {
		transport = "grpc"
	}
	if addr == "" {
		logger.Infof(context.Background(), "[DocConverter] No DOCREADER_ADDR configured, starting disconnected")
	}
	transport = strings.ToLower(transport)
	switch transport {
	case "http", "https":
		if addr != "" && !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
			addr = "http://" + addr
		}
		return docparser.NewHTTPDocumentReader(addr)
	default:
		return docparser.NewGRPCDocumentReader(addr)
	}
}

func registerDocReaderCleanup(reader interfaces.DocumentReader, cleaner interfaces.ResourceCleaner) {
	if reader == nil || cleaner == nil {
		return
	}
	closer, ok := reader.(io.Closer)
	if !ok {
		return
	}
	cleaner.RegisterWithName("DocReaderClient", closer.Close)
}

func registerStreamManagerCleanup(streams interfaces.StreamManager, cleaner interfaces.ResourceCleaner) {
	if streams == nil || cleaner == nil {
		return
	}
	closer, ok := streams.(io.Closer)
	if !ok {
		return
	}
	cleaner.RegisterWithName("StreamManager", closer.Close)
}

// initOllamaService initializes the Ollama service client.
func initOllamaService() (*ollama.OllamaService, error) {
	return ollama.GetOllamaService()
}

func initNeo4jClient() (neo4j.Driver, error) {
	ctx := context.Background()
	if strings.ToLower(os.Getenv("NEO4J_ENABLE")) != "true" {
		logger.Debugf(ctx, "NOT SUPPORT RETRIEVE GRAPH")
		return nil, nil
	}
	uri := os.Getenv("NEO4J_URI")
	username := os.Getenv("NEO4J_USERNAME")
	password := os.Getenv("NEO4J_PASSWORD")

	maxRetries := 30
	retryInterval := 2 * time.Second

	var driver neo4j.Driver
	var err error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		driver, err = neo4j.NewDriver(uri, neo4j.BasicAuth(username, password, ""))
		if err != nil {
			logger.Warnf(ctx, "Failed to create Neo4j driver (attempt %d/%d): %v", attempt, maxRetries, err)
			time.Sleep(retryInterval)
			continue
		}

		verifyCtx, cancel := context.WithTimeout(ctx, neo4jVerifyAuthTimeout)
		err = driver.VerifyAuthentication(verifyCtx, nil)
		cancel()
		if err == nil {
			if attempt > 1 {
				logger.Infof(ctx, "Successfully connected to Neo4j after %d attempts", attempt)
			}
			return driver, nil
		}

		logger.Warnf(ctx, "Failed to verify Neo4j authentication (attempt %d/%d): %v", attempt, maxRetries, err)
		closeCtx, closeCancel := context.WithTimeout(ctx, 5*time.Second)
		if closeErr := driver.Close(closeCtx); closeErr != nil {
			logger.Debugf(ctx, "Close failed Neo4j driver: %v", closeErr)
		}
		closeCancel()
		time.Sleep(retryInterval)
	}

	return nil, fmt.Errorf("failed to connect to Neo4j after %d attempts: %w", maxRetries, err)
}

func registerNeo4jCleanup(driver neo4j.Driver, cleaner interfaces.ResourceCleaner) {
	if driver == nil || cleaner == nil {
		return
	}
	cleaner.RegisterWithName("Neo4jDriver", func() error {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupStepTimeout)
		defer cancel()
		return driver.Close(ctx)
	})
}

type asynqCleanupParams struct {
	dig.In

	Cleaner           interfaces.ResourceCleaner
	Enqueuer          interfaces.TaskEnqueuer
	Inspector         *asynq.Inspector
	CoreServer        *asynq.Server `name:"coreAsynqServer"`
	PostProcessServer *asynq.Server `name:"postProcessAsynqServer"`
	EnrichmentServer  *asynq.Server `name:"enrichmentAsynqServer"`
	MaintenanceServer *asynq.Server `name:"maintenanceAsynqServer"`
	SharedServer      *asynq.Server `name:"sharedAsynqServer"`
	WikiServer        *asynq.Server `name:"wikiAsynqServer"`
}

func registerAsynqCleanup(params asynqCleanupParams) {
	if params.Cleaner == nil {
		return
	}
	params.Cleaner.RegisterWithName("AsynqClients", func() error {
		var errs error
		if closer, ok := params.Enqueuer.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				errs = errors.Join(errs, fmt.Errorf("close asynq client: %w", err))
			}
		}
		if params.Inspector != nil {
			if err := params.Inspector.Close(); err != nil {
				errs = errors.Join(errs, fmt.Errorf("close asynq inspector: %w", err))
			}
		}
		return errs
	})

	servers := []*asynq.Server{
		params.CoreServer,
		params.PostProcessServer,
		params.EnrichmentServer,
		params.MaintenanceServer,
		params.SharedServer,
		params.WikiServer,
	}
	params.Cleaner.RegisterWithName("AsynqServers", func() error {
		var wg sync.WaitGroup
		for _, srv := range servers {
			if srv == nil {
				continue
			}
			wg.Add(1)
			go func(s *asynq.Server) {
				defer wg.Done()
				s.Shutdown()
			}(srv)
		}
		wg.Wait()
		return nil
	})
}

// registerWebSearchProviders registers all web search provider types to the registry.
func registerWebSearchProviders(registry *infra_web_search.Registry) {
	for _, spec := range []infra_web_search.Spec{
		{Type: "duckduckgo", Search: infra_web_search.NewDuckDuckGoProvider},
		{Type: "google", Search: infra_web_search.NewGoogleProvider},
		{Type: "bing", Search: infra_web_search.NewBingProvider},
		{Type: "tavily", Search: infra_web_search.NewTavilyProvider},
		{Type: "ollama", Search: infra_web_search.NewOllamaProvider},
		{Type: "baidu", Search: infra_web_search.NewBaiduProvider},
		{Type: "searxng", Search: infra_web_search.NewSearxngProvider},
		{Type: "keenable", Search: infra_web_search.NewKeenableProvider},
		{Type: "zhipu", Search: infra_web_search.NewZhipuProvider},
		{Type: "exa", Search: infra_web_search.NewExaProvider},
		{Type: "metaso", Search: infra_web_search.NewMetasoProvider},
		{Type: "bocha", Search: infra_web_search.NewBochaProvider},
	} {
		registry.Add(spec)
	}
	registry.Add(infra_web_search.Spec{Type: "brave", Filters: true, Search: infra_web_search.NewBraveProvider})
	registry.Add(infra_web_search.Spec{Type: "serply", Filters: true, Search: infra_web_search.NewSerplyProvider})
	registry.Add(infra_web_search.Spec{
		Type: "deepseek", Search: infra_web_search.NewDeepSeekProvider, Fetch: infra_web_search.NewDeepSeekFetch,
	})
	registry.Add(infra_web_search.Spec{Type: "searchapi", Filters: true, Search: infra_web_search.NewSearchAPIProvider})
}

// registerIMService registers adapter factories, loads enabled channels, and
// wires the process-lifetime shutdown hook.
func registerIMService(imService *imPkg.Service, cleaner interfaces.ResourceCleaner) {
	imService.RegisterAdapterFactory("wecom", wecom.NewFactory())
	imService.RegisterAdapterFactory("feishu", feishu.NewFactory(feishu.RegionFeishu))
	imService.RegisterAdapterFactory("lark", feishu.NewFactory(feishu.RegionLark))
	imService.RegisterAdapterFactory("slack", slack.NewFactory())
	imService.RegisterAdapterFactory("telegram", telegram.NewFactory())
	imService.RegisterAdapterFactory("dingtalk", dingtalk.NewFactory())
	imService.RegisterAdapterFactory("mattermost", mattermost.NewFactory())
	imService.RegisterAdapterFactory("wechat", wechat.NewFactory())
	imService.RegisterAdapterFactory("qqbot", qqbot.NewFactory())
	imService.RegisterAdapterFactory("yunzhijia", yunzhijia.NewFactory())

	if err := imService.LoadAndStartChannels(); err != nil {
		logger.Warnf(context.Background(), "[IM] Failed to load channels from database: %v", err)
	}

	cleaner.RegisterWithName("IMService", func() error {
		imService.Stop()
		return nil
	})
}

// initConnectorRegistry creates and populates the connector registry with all available connectors.
func initConnectorRegistry() (*datasource.ConnectorRegistry, error) {
	registry := datasource.NewConnectorRegistry()

	var errs error
	if err := registry.Register(wiki.NewConnector(core.RegionFeishu)); err != nil {
		errs = errors.Join(errs, fmt.Errorf("register feishu connector: %w", err))
	}
	if err := registry.Register(wiki.NewConnector(core.RegionLark)); err != nil {
		errs = errors.Join(errs, fmt.Errorf("register lark connector: %w", err))
	}
	if err := registry.Register(drive.NewDriveConnector(core.RegionFeishuDrive)); err != nil {
		errs = errors.Join(errs, fmt.Errorf("register feishu_drive connector: %w", err))
	}
	if err := registry.Register(drive.NewDriveConnector(core.RegionLarkDrive)); err != nil {
		errs = errors.Join(errs, fmt.Errorf("register lark_drive connector: %w", err))
	}
	if err := registry.Register(notionConnector.NewConnector()); err != nil {
		errs = errors.Join(errs, fmt.Errorf("register notion connector: %w", err))
	}
	if err := registry.Register(confluenceConnector.NewConnector()); err != nil {
		errs = errors.Join(errs, fmt.Errorf("register confluence connector: %w", err))
	}
	if err := registry.Register(yuqueConnector.NewConnector()); err != nil {
		errs = errors.Join(errs, fmt.Errorf("register yuque connector: %w", err))
	}
	if err := registry.Register(dingtalkConnector.NewConnector()); err != nil {
		errs = errors.Join(errs, fmt.Errorf("register dingtalk connector: %w", err))
	}
	if err := registry.Register(imaConnector.NewConnector()); err != nil {
		errs = errors.Join(errs, fmt.Errorf("register ima connector: %w", err))
	}
	if err := registry.Register(rssConnector.NewConnector()); err != nil {
		errs = errors.Join(errs, fmt.Errorf("register rss connector: %w", err))
	}
	if err := registry.Register(gitlabConnector.NewConnector()); err != nil {
		errs = errors.Join(errs, fmt.Errorf("register gitlab connector: %w", err))
	}
	if err := registry.Register(seafileConnector.NewConnector()); err != nil {
		errs = errors.Join(errs, fmt.Errorf("register seafile connector: %w", err))
	}
	if err := registry.Register(nextcloudConnector.NewConnector()); err != nil {
		errs = errors.Join(errs, fmt.Errorf("register nextcloud connector: %w", err))
	}

	if errs != nil {
		return nil, errs
	}
	return registry, nil
}

func startDataSourceScheduler(scheduler *datasource.Scheduler, cleaner interfaces.ResourceCleaner) {
	if err := scheduler.Start(context.Background()); err != nil {
		logger.Warnf(context.Background(), "[Container] data source scheduler start failed: %v", err)
	}

	cleaner.RegisterWithName("DataSourceScheduler", func() error {
		scheduler.StopWithin(cleanupStepTimeout)
		return nil
	})
}

func startHousekeepingService(svc *service.HousekeepingService, cleaner interfaces.ResourceCleaner) {
	if svc == nil {
		return
	}
	if err := svc.Start(context.Background()); err != nil {
		logger.Warnf(context.Background(), "[Container] housekeeping start failed: %v", err)
	}
	cleaner.RegisterWithName("KnowledgeHousekeeping", func() error {
		svc.StopWithin(cleanupStepTimeout)
		return nil
	})
}

func startTenantSkillReaper(svc *service.TenantSkillService, cleaner interfaces.ResourceCleaner) {
	if svc == nil {
		return
	}
	if err := svc.Start(context.Background()); err != nil {
		logger.Warnf(context.Background(), "[Container] tenant skill reaper start failed: %v", err)
	}
	cleaner.RegisterWithName("TenantSkillReaper", func() error {
		svc.StopWithin(cleanupStepTimeout)
		return nil
	})
}

func startForkSnapshotReaper(reaper *service.ForkSnapshotReaper, cleaner interfaces.ResourceCleaner) {
	if reaper == nil {
		logger.Warnf(context.Background(), "[Container] fork snapshot reaper unavailable")
		return
	}
	if cleaner == nil {
		logger.Warnf(context.Background(), "[Container] fork snapshot reaper start failed: resource cleaner missing")
		return
	}
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if _, err := reaper.ReapOnce(context.Background()); err != nil {
					logger.Warnf(context.Background(), "[ForkSnapshotReaper] reap failed: %v", err)
				}
			case <-stop:
				return
			}
		}
	}()
	cleaner.RegisterWithName("ForkSnapshotReaper", func() error {
		close(stop)
		return nil
	})
}

func startAgentPromptReaper(runs *service.SuspendedRunService, cleaner interfaces.ResourceCleaner) {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if _, err := runs.Reap(context.Background(), time.Now()); err != nil {
					logger.Warnf(context.Background(), "[AgentPromptReaper] reap failed: %v", err)
				}
			case <-stop:
				return
			}
		}
	}()
	cleaner.RegisterWithName("AgentPromptReaper", func() error {
		close(stop)
		return nil
	})
}

func startUserMachineGC(gc *sandboxalloc.UserMachineGC, cleaner interfaces.ResourceCleaner) {
	if gc == nil || cleaner == nil {
		logger.Warnf(context.Background(), "[Container] user machine GC unavailable")
		return
	}
	stop := make(chan struct{})
	go func() {
		timer := time.NewTimer(2 * time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				if _, err := gc.RunOnce(context.Background()); err != nil {
					logger.Warnf(context.Background(), "[UserMachineGC] run failed: %v", err)
				}
				timer.Reset(sandboxalloc.UserMachineGCInterval)
			case <-stop:
				return
			}
		}
	}()
	cleaner.RegisterWithName("UserMachineGC", func() error {
		close(stop)
		return nil
	})
}

func startTemporaryDocumentCleanup(svc interfaces.TemporaryDocumentService, cleaner interfaces.ResourceCleaner) {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := svc.CleanupExpired(context.Background()); err != nil {
					logger.Warnf(context.Background(), "[TemporaryDocument] cleanup failed: %v", err)
				}
			case <-stop:
				return
			}
		}
	}()
	cleaner.RegisterWithName("TemporaryDocumentCleanup", func() error {
		close(stop)
		return nil
	})
}

func startAuditLogRetention(
	runner *service.AuditLogRetentionRunner, cleaner interfaces.ResourceCleaner,
) {
	runner.Start(context.Background())
	cleaner.RegisterWithName("AuditLogRetentionRunner", func() error {
		runner.StopWithin(cleanupStepTimeout)
		return nil
	})
}
