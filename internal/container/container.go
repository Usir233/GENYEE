// Package container implements dependency injection container setup
// Provides centralized configuration for services, repositories, and handlers
// This package is responsible for wiring up all dependencies and ensuring proper lifecycle management
package container

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	modelruntime "github.com/Usir233/GENYEE/internal/models/runtime"

	"github.com/redis/go-redis/v9"
	"go.uber.org/dig"
	"gorm.io/gorm"

	"github.com/Usir233/GENYEE/internal/agent/approval"
	"github.com/Usir233/GENYEE/internal/agent/hitl"
	"github.com/Usir233/GENYEE/internal/agent/operations"
	"github.com/Usir233/GENYEE/internal/agent/tools"
	"github.com/Usir233/GENYEE/internal/application/repository"
	neo4jRepo "github.com/Usir233/GENYEE/internal/application/repository/retriever/neo4j"
	"github.com/Usir233/GENYEE/internal/application/service"
	chatpipeline "github.com/Usir233/GENYEE/internal/application/service/chat_pipeline"
	"github.com/Usir233/GENYEE/internal/application/service/credential"
	"github.com/Usir233/GENYEE/internal/application/service/memory"
	"github.com/Usir233/GENYEE/internal/application/service/retriever"
	"github.com/Usir233/GENYEE/internal/application/service/sandboxalloc"
	"github.com/Usir233/GENYEE/internal/browserskill"
	"github.com/Usir233/GENYEE/internal/config"
	"github.com/Usir233/GENYEE/internal/datasource"
	"github.com/Usir233/GENYEE/internal/event"
	"github.com/Usir233/GENYEE/internal/handler"
	"github.com/Usir233/GENYEE/internal/handler/session"
	imPkg "github.com/Usir233/GENYEE/internal/im"
	"github.com/Usir233/GENYEE/internal/infrastructure/docparser"
	infra_web_search "github.com/Usir233/GENYEE/internal/infrastructure/web_search"
	"github.com/Usir233/GENYEE/internal/logger"
	"github.com/Usir233/GENYEE/internal/mcp"
	"github.com/Usir233/GENYEE/internal/mcpserver"
	"github.com/Usir233/GENYEE/internal/models/api"
	"github.com/Usir233/GENYEE/internal/models/embedding"
	"github.com/Usir233/GENYEE/internal/models/limiter"
	"github.com/Usir233/GENYEE/internal/router"
	"github.com/Usir233/GENYEE/internal/sandbox"
	"github.com/Usir233/GENYEE/internal/sandbox/admission"
	"github.com/Usir233/GENYEE/internal/stream"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	secutils "github.com/Usir233/GENYEE/internal/utils"
)

// BuildContainer constructs the dependency injection container and registers
// all components, services, repositories, handlers, and shutdown hooks.
func BuildContainer(container *dig.Container) *dig.Container {
	if err := modelruntime.Initialize(config.ConfigDir()); err != nil {
		logger.Warnf(context.Background(), "Load models catalog overlay failed: %v", err)
	}
	ctx := context.Background()
	logger.Debugf(ctx, "[Container] Starting container initialization...")

	must(container.Provide(NewResourceCleaner, dig.As(new(interfaces.ResourceCleaner))))

	logger.Debugf(ctx, "[Container] Registering core infrastructure...")
	must(container.Provide(config.LoadConfig))
	must(container.Provide(initLangfuse))
	must(container.Provide(initDatabase))
	must(container.Provide(initFileService))
	must(container.Provide(initRedisClient))
	must(container.Provide(initAntsPool))

	// Register early infrastructure cleanups first so LIFO Cleanup tears them
	// down last after background workers and schedulers have drained.
	must(container.Invoke(registerLangfuseCleanup))
	must(container.Invoke(registerDatabaseCleanup))
	must(container.Invoke(registerRedisCleanup))
	must(container.Invoke(registerPoolCleanup))

	logger.Debugf(ctx, "[Container] Registering retrieval engine registry...")
	must(container.Provide(initRetrieveEngineRegistry))

	logger.Debugf(ctx, "[Container] Registering external service clients...")
	must(container.Provide(initDocReaderClient))
	must(container.Invoke(registerDocReaderCleanup))
	must(container.Provide(docparser.NewImageResolver))
	must(container.Provide(initOllamaService))
	must(container.Provide(initNeo4jClient))
	must(container.Invoke(registerNeo4jCleanup))
	must(container.Provide(stream.NewStreamManager))
	must(container.Invoke(registerStreamManagerCleanup))
	logger.Debugf(ctx, "[Container] Initializing DuckDB...")
	must(container.Provide(NewDuckDB))
	must(container.Invoke(registerDuckDBCleanup))
	logger.Debugf(ctx, "[Container] DuckDB registered")

	logger.Debugf(ctx, "[Container] Registering repositories...")
	must(container.Provide(repository.NewTenantRepository))
	must(container.Provide(repository.NewTenantAPIKeyRepository))
	must(container.Provide(repository.NewTenantMemberRepository))
	must(container.Provide(repository.NewTenantInvitationRepository))
	must(container.Provide(repository.NewAuditLogRepository))
	must(container.Provide(repository.NewKnowledgeBaseRepository))
	must(container.Provide(repository.NewKnowledgeRepository))
	must(container.Provide(repository.NewKnowledgeSpanRepository))
	must(container.Provide(repository.NewChunkRepository))
	must(container.Provide(repository.NewKnowledgeTagRepository))
	must(container.Provide(repository.NewSessionRepository))
	must(container.Provide(repository.NewMessageRepository))
	must(container.Provide(repository.NewMessageSuggestionRepository))
	must(container.Provide(repository.NewModelRepository))
	must(container.Provide(repository.NewUserRepository))
	must(container.Provide(repository.NewAuthTokenRepository))
	must(container.Provide(repository.NewSystemSettingRepository))
	must(container.Provide(repository.NewModelCatalogRepository))
	must(container.Provide(neo4jRepo.NewNeo4jRepository))
	must(container.Provide(neo4jRepo.NewGraphSearchRepository))
	must(container.Provide(repository.NewGraphSearchEvidenceRepository))
	must(container.Provide(repository.NewMCPServiceRepository))
	must(container.Provide(repository.NewMCPToolApprovalRepository))
	must(container.Provide(repository.NewAgentPromptRepository))
	must(container.Provide(repository.NewAgentGrantRepository))
	must(container.Provide(repository.NewCredentialVaultRepository))
	must(container.Provide(repository.NewAgentRunSuspensionRepository))
	must(container.Provide(repository.NewMCPOAuthRepository))
	must(container.Provide(repository.NewTenantSandboxConfigRepository))
	must(container.Provide(repository.NewTenantSkillRepository))
	must(container.Provide(repository.NewCustomAgentRepository))
	must(container.Provide(repository.NewOrganizationRepository))
	must(container.Provide(repository.NewKBShareRepository))
	must(container.Provide(repository.NewAgentShareRepository))
	must(container.Provide(repository.NewEmbedChannelRepository))
	must(container.Provide(repository.NewMCPEndpointRepository))
	must(container.Provide(repository.NewTenantDisabledSharedAgentRepository))
	must(container.Provide(repository.NewUserResourceFavoriteRepository))
	must(container.Provide(service.NewWebSearchStateService))
	must(container.Provide(repository.NewDataSourceRepository))
	must(container.Provide(repository.NewSyncLogRepository))
	must(container.Provide(repository.NewWikiPageRepository))
	must(container.Provide(repository.NewMemoryRepository))
	must(container.Provide(repository.NewTaskPendingOpsRepository))
	must(container.Provide(repository.NewTaskDeadLetterRepository))
	must(container.Provide(repository.NewSandboxAllocationRepository))

	logger.Debugf(ctx, "[Container] Registering MCP manager...")
	must(container.Provide(mcp.NewMCPManager))
	must(container.Invoke(registerMCPCleanup))
	must(container.Provide(mcp.NewOAuthManager))

	logger.Debugf(ctx, "[Container] Registering sandbox manager...")
	must(container.Provide(newSandboxManager))
	must(container.Provide(provideHostApprovalModeLoader))
	must(container.Provide(provideHostProjectDirsLoader))
	must(container.Provide(provideHostApprovalModes))
	must(container.Provide(hostProjectLookup))
	must(container.Provide(hostModeLookup))
	must(container.Provide(provideHostSandboxManager))
	must(container.Provide(service.NewTenantSandboxConfigLoader))
	must(container.Provide(service.NewForkBootstrapperFromRepos))
	must(container.Provide(func(b *service.ForkBootstrapper) sandbox.SessionBootstrapper {
		if b == nil {
			return nil
		}
		return b
	}))
	must(container.Provide(newAdmissionRegistry))
	must(container.Provide(newSessionBindingStore))
	must(container.Provide(newTenantSandboxResolver))
	must(container.Provide(func(
		repo interfaces.SandboxAllocationRepository,
		configs repository.TenantSandboxConfigRepository,
		sessions interfaces.SessionRepository,
	) *sandboxalloc.Service {
		return sandboxalloc.New(repo, configs, sessions, sandboxRedisNamespace())
	}))
	must(container.Provide(newBackgroundActivityRegistry))
	must(container.Provide(newBackgroundNoticeInbox))
	must(container.Provide(service.NewBackgroundWork))

	logger.Debugf(ctx, "[Container] Registering business services...")
	must(container.Provide(service.NewTenantService))
	must(container.Provide(service.NewTenantAPIKeyService))
	must(container.Provide(service.NewTenantMemberService))
	must(container.Provide(service.NewTenantInvitationService))
	must(container.Provide(service.NewAuditLogService))
	must(container.Provide(service.NewAuditLogRetentionRunner))
	must(container.Provide(service.NewKnowledgeBaseService))
	must(container.Provide(service.NewGraphSearchService))
	must(container.Provide(service.NewOrganizationService))
	must(container.Provide(service.NewKBShareService))
	must(container.Provide(service.NewAgentShareService))
	must(container.Provide(service.NewKnowledgeService))
	must(container.Provide(service.NewSpanTracker))
	must(container.Provide(service.NewChunkService))
	must(container.Provide(service.NewKnowledgeTagService))
	must(container.Provide(embedding.NewBatchEmbedder))
	must(container.Provide(service.NewModelService))
	must(container.Provide(service.NewDatasetService))
	must(container.Provide(service.NewEvaluationService))
	must(container.Provide(service.NewUserService))
	must(container.Provide(service.NewSystemSettingService))
	must(container.Provide(service.NewModelCatalogService))
	must(container.Provide(func(
		repo repository.TenantSandboxConfigRepository,
		agents interfaces.CustomAgentRepository,
		skills repository.TenantSkillRepository,
		files interfaces.StorageBackendResolver,
	) *service.TenantSandboxConfigService {
		return service.NewTenantSandboxConfigService(repo, agents, buildGlobalSandboxConfig(), skills, files)
	}))
	must(container.Provide(func(s *service.TenantSandboxConfigService) service.WorkspaceSandboxPolicy {
		return s
	}))

	must(container.Provide(service.NewChunkExtractService, dig.Name("chunkExtractor")))
	must(container.Provide(service.NewDataTableSummaryService, dig.Name("dataTableSummary")))
	must(container.Provide(service.NewImageMultimodalService, dig.Name("imageMultimodal")))
	must(container.Provide(service.NewKnowledgePostProcessService, dig.Name("knowledgePostProcess")))
	must(container.Provide(service.NewKnowledgeAutoTagService, dig.Name("knowledgeAutoTag")))
	must(container.Provide(service.NewKnowledgeBaseProfileService))
	must(container.Provide(func(s *service.KnowledgeBaseProfileService) interfaces.KnowledgeBaseProfileService {
		return s
	}))
	must(container.Provide(func(s *service.KnowledgeBaseProfileService) interfaces.TaskHandler { return s },
		dig.Name("knowledgeBaseProfile")))

	must(container.Provide(service.NewMessageService))
	must(container.Provide(service.NewMessageSuggestionService))
	must(container.Provide(service.NewMCPServiceService))
	must(container.Provide(service.NewMCPToolApprovalService))
	must(container.Provide(service.NewCustomAgentService))
	must(container.Provide(service.NewUserResourceFavoriteService))
	must(container.Provide(service.NewWikiPageService))
	must(container.Provide(service.NewWikiIngestService, dig.Name("wikiIngest")))
	must(container.Provide(service.NewWikiLintService))
	must(container.Provide(service.NewEmbedChannelService))
	must(container.Provide(service.NewMCPEndpointService))
	must(container.Provide(mcpserver.NewServer))

	logger.Debugf(ctx, "[Container] Registering web search registry and providers...")
	must(container.Provide(infra_web_search.NewRegistry))
	must(container.Invoke(registerWebSearchProviders))
	must(container.Provide(repository.NewWebSearchProviderRepository))
	must(container.Provide(repository.NewVectorStoreRepository))
	must(container.Provide(repository.NewStorageBackendRepository))
	must(container.Provide(repository.NewResourceRepository))
	must(container.Provide(repository.NewTemporaryDocumentRepository))
	must(container.Provide(service.NewResourceCatalogWithStorage))
	must(container.Provide(retriever.NewVectorStoreRepoOwnership))
	must(container.Provide(service.NewWebSearchService))
	must(container.Provide(service.NewWebSearchProviderService))
	must(container.Provide(NewEngineFactory))
	must(container.Provide(func(r interfaces.RetrieveEngineRegistry) (interfaces.StoreRegistry, error) {
		sr, ok := r.(*retriever.RetrieveEngineRegistry)
		if !ok {
			return nil, fmt.Errorf("registry does not implement StoreRegistry")
		}
		return sr, nil
	}))
	must(container.Provide(service.NewVectorStoreService))
	must(container.Provide(service.NewStorageBackendServiceWithResources))
	must(container.Provide(func(s *service.StorageBackendService) interfaces.StorageBackendService { return s }))
	must(container.Provide(func(s *service.StorageBackendService) interfaces.StorageBackendResolver { return s }))
	must(container.Invoke(func(
		svc *service.TenantSandboxConfigService,
		repo interfaces.SandboxAllocationRepository,
		reg admission.Registry,
	) {
		svc.WithAllocationRepo(repo).WithAdmission(reg)
	}))

	logger.Debugf(ctx, "[Container] Registering event bus and agent service...")
	must(container.Provide(event.NewEventBus))
	must(container.Provide(service.NewSessionSandboxPinner))
	must(container.Provide(func(
		cfg *config.Config,
		prompts interfaces.AgentPromptRepository,
		suspensions interfaces.AgentRunSuspensionRepository,
		rdb *redis.Client,
	) *hitl.Broker {
		opts := []hitl.Option{hitl.WithSuspensionStore(suspensions)}
		if cfg != nil && cfg.Agent != nil {
			opts = append(opts, hitl.WithSuspendExpiry(cfg.Agent.PromptExpiry()))
		}
		return hitl.NewBroker(prompts, rdb, opts...)
	}))
	must(container.Provide(service.NewSuspendedRunService))
	must(container.Provide(func(
		cfg *config.Config, s interfaces.MCPToolApprovalService, broker *hitl.Broker,
		grants interfaces.AgentGrantRepository,
	) *approval.Gate {
		return approval.NewGate(cfg, &approval.Adapter{Svc: s}, broker).WithGrants(grants)
	}))
	must(container.Provide(func(g *approval.Gate) approval.MCPApproval { return g }))
	must(container.Provide(credential.NewVault))
	must(container.Provide(credential.NewHandleStore))
	must(container.Provide(func(
		g *approval.Gate, v *credential.Vault, h *credential.HandleStore,
	) *credential.Submitter {
		return credential.NewSubmitter(g, v, h)
	}))
	must(container.Provide(func(cleaner interfaces.ResourceCleaner, db *gorm.DB) (*browserskill.Manager, error) {
		manager := browserskill.NewManager(browserskill.NewStore(db))
		if err := manager.ValidateConfiguration(); err != nil {
			return nil, err
		}
		cleaner.RegisterWithName("BrowserSkill", func() error { manager.Close(); return nil })
		return manager, nil
	}))
	must(container.Provide(operations.NewHandlerDispatcher))
	must(container.Provide(service.NewAgentCollaboration))
	must(container.Provide(service.NewNotificationService))
	must(container.Provide(service.NewAutomationService))
	must(container.Provide(service.NewAgentService))

	logger.Debugf(ctx, "[Container] Registering memory service...")
	must(container.Provide(memory.NewMemoryService))

	logger.Debugf(ctx, "[Container] Registering session service...")
	must(container.Provide(service.NewSessionService))
	must(container.Provide(service.NewTenantSkillService))
	must(container.Provide(service.NewUserEnvService))

	must(container.Provide(service.NewArtifactCollectorFromSandboxManager))

	must(container.Provide(func(
		pinner *service.SessionSandboxPinner,
		host service.HostSandboxManager,
	) *service.HostSessionResolver {
		return service.NewHostSessionResolver(pinner, host.Manager, host.Desktop)
	}))
	must(container.Provide(func(
		mgr sandbox.Manager,
		resolver sandbox.TenantSandboxResolver,
		pinner *service.SessionSandboxPinner,
		host *service.HostSessionResolver,
	) *service.PinnedSessionSandbox {
		return service.NewPinnedSessionSandbox(pinner, resolver, mgr, host)
	}))
	must(container.Provide(newShellJobService))
	must(container.Provide(func(
		mgr sandbox.Manager,
		pinned *service.PinnedSessionSandbox,
		activity interfaces.ActivityRegistry,
	) *service.WorkspaceCheckpointer {
		if runner, ok := mgr.(service.SandboxShellRunner); ok {
			return service.NewWorkspaceCheckpointer(runner).WithActivityRegistry(activity)
		}
		return service.NewWorkspaceCheckpointer(pinned).WithActivityRegistry(activity)
	}))
	must(container.Provide(func(
		mgr sandbox.Manager,
		pinned *service.PinnedSessionSandbox,
	) session.SandboxIDLookup {
		if lookup, ok := mgr.(session.SandboxIDLookup); ok {
			return lookup
		}
		if pinned == nil {
			return nil
		}
		return pinned
	}))
	must(container.Provide(func(
		mgr sandbox.Manager,
		pinned *service.PinnedSessionSandbox,
	) service.SessionForkSandboxPort {
		if port, ok := mgr.(service.SessionForkSandboxPort); ok {
			return port
		}
		if pinned == nil {
			return nil
		}
		return pinned
	}))
	must(container.Provide(service.NewSessionForkServiceFromRepos))
	must(container.Invoke(func(f *service.SessionForkService, a *sandboxalloc.Service) {
		f.WithAllocations(a)
	}))
	must(container.Provide(func(
		mgr sandbox.Manager,
		pinned *service.PinnedSessionSandbox,
	) service.SessionRewindSandboxPort {
		if port, ok := mgr.(service.SessionRewindSandboxPort); ok {
			return port
		}
		if pinned == nil {
			return nil
		}
		return pinned
	}))
	must(container.Provide(service.NewSessionBusyGate))
	must(container.Provide(service.NewSessionRewindServiceFromRepos))

	must(container.Provide(service.NewSandboxTerminalService))
	must(container.Provide(service.NewSandboxDesktopTicketStore))
	must(container.Provide(service.NewSandboxDesktopLastStore))
	must(container.Provide(service.NewSandboxDesktopService))

	logger.Debugf(ctx, "[Container] Registering task enqueuer...")
	redisAvailable := os.Getenv("REDIS_ADDR") != ""
	if redisAvailable {
		must(container.Provide(router.NewAsyncqClient, dig.As(new(interfaces.TaskEnqueuer))))
		must(container.Provide(router.NewCoreAsynqServer, dig.Name("coreAsynqServer")))
		must(container.Provide(router.NewPostProcessAsynqServer, dig.Name("postProcessAsynqServer")))
		must(container.Provide(router.NewEnrichmentAsynqServer, dig.Name("enrichmentAsynqServer")))
		must(container.Provide(router.NewMaintenanceAsynqServer, dig.Name("maintenanceAsynqServer")))
		must(container.Provide(router.NewSharedAsynqServer, dig.Name("sharedAsynqServer")))
		must(container.Provide(router.NewWikiAsynqServer, dig.Name("wikiAsynqServer")))
		must(container.Provide(router.NewAsynqInspector))
		must(container.Provide(router.NewAsynqTaskInspector))
		must(container.Invoke(registerModelConcurrencyLimiter))
	} else {
		syncExec := router.NewSyncTaskExecutor()
		must(container.Provide(func() interfaces.TaskEnqueuer { return syncExec }))
		must(container.Provide(func() *router.SyncTaskExecutor { return syncExec }))
		must(container.Provide(router.NewNoopTaskInspector))
		must(container.Invoke(registerLiteModelConcurrencyLimiter))
	}
	must(container.Provide(service.NewTemporaryDocumentService))
	must(container.Invoke(startTemporaryDocumentCleanup))

	logger.Debugf(ctx, "[Container] Registering data source sync framework...")
	must(container.Provide(initConnectorRegistry))
	must(container.Provide(datasource.NewScheduler))
	must(container.Provide(service.NewDataSourceService))
	must(container.Invoke(startDataSourceScheduler))
	must(container.Invoke(startAuditLogRetention))
	must(container.Provide(service.NewHousekeepingService))
	must(container.Invoke(startHousekeepingService))
	must(container.Provide(chatpipeline.NewEventManager))
	must(container.Invoke(chatpipeline.NewPluginSearch))
	must(container.Invoke(chatpipeline.NewPluginRerank))
	must(container.Invoke(chatpipeline.NewPluginWebFetch))
	must(container.Invoke(chatpipeline.NewPluginMerge))
	must(container.Invoke(chatpipeline.NewPluginDataAnalysis))
	must(container.Invoke(chatpipeline.NewPluginIntoChatMessage))
	must(container.Invoke(chatpipeline.NewPluginChatCompletion))
	must(container.Invoke(chatpipeline.NewPluginChatCompletionStream))
	must(container.Invoke(chatpipeline.NewPluginFilterTopK))
	must(container.Invoke(chatpipeline.NewPluginQueryUnderstand))
	must(container.Invoke(chatpipeline.NewPluginLoadHistory))
	must(container.Invoke(chatpipeline.NewPluginMemoryRecall))
	must(container.Invoke(chatpipeline.NewPluginExtractEntity))
	must(container.Invoke(chatpipeline.NewPluginSearchEntity))
	must(container.Invoke(chatpipeline.NewPluginSearchParallel))
	must(container.Invoke(chatpipeline.NewPluginWikiBoost))
	must(container.Invoke(chatpipeline.NewPluginMemoryAffinity))
	logger.Debugf(ctx, "[Container] Chat pipeline plugins registered")

	must(container.Invoke(func(
		p *service.SessionSandboxPinner, skills *service.TenantSkillService, a *sandboxalloc.Service,
	) {
		p.WithPlacer(a)
		skills.WithPlacer(a)
	}))
	must(container.Provide(sandboxalloc.NewWorkspaceCleaner))
	must(container.Invoke(func(
		s interfaces.SessionService,
		c *sandboxalloc.WorkspaceCleaner,
		resolver sandbox.TenantSandboxResolver,
	) {
		if handler.Edition == "lite" || resolver == nil {
			return
		}
		if svc, ok := s.(interface {
			WithWorkspaceCleaner(*sandboxalloc.WorkspaceCleaner)
		}); ok {
			svc.WithWorkspaceCleaner(c)
		}
	}))
	must(container.Invoke(func(
		u interfaces.UserService,
		c *sandboxalloc.WorkspaceCleaner,
		resolver sandbox.TenantSandboxResolver,
	) {
		if handler.Edition == "lite" || resolver == nil {
			return
		}
		if svc, ok := u.(interface {
			WithSandboxReleaser(service.SandboxOwnerReleaser)
		}); ok {
			svc.WithSandboxReleaser(c)
		}
	}))
	must(container.Invoke(func(
		cfgSvc *service.TenantSandboxConfigService,
		cleaner *sandboxalloc.WorkspaceCleaner,
		resolver sandbox.TenantSandboxResolver,
	) {
		if handler.Edition == "lite" || resolver == nil {
			return
		}
		cfgSvc.WithUserMachineReleaser(cleaner)
	}))
	must(container.Invoke(startTenantSkillReaper))
	must(
		container.Invoke(
			func(r *service.AgentCollaboration, s interfaces.SessionService, cleaner interfaces.ResourceCleaner) {
				r.Bind(s)
				cleaner.RegisterWithName("AgentCollaboration", func() error {
					stopCtx, cancel := context.WithTimeout(context.Background(), cleanupStepTimeout)
					defer cancel()
					return r.Runtime.Stop(stopCtx)
				})
			},
		),
	)
	must(container.Provide(func(
		sessions interfaces.SessionRepository,
		resolver sandbox.TenantSandboxResolver,
		mgr sandbox.Manager,
	) *service.ForkSnapshotReaper {
		return service.NewForkSnapshotReaperFromRepos(
			sessions, service.NewResolverForkSnapshotDeleter(resolver, mgr),
		)
	}))
	must(container.Invoke(startForkSnapshotReaper))
	must(container.Provide(func(
		svc *sandboxalloc.Service, cleaner *sandboxalloc.WorkspaceCleaner,
		configs repository.TenantSandboxConfigRepository, redisClient *redis.Client,
	) *sandboxalloc.UserMachineGC {
		var client redis.UniversalClient
		if redisClient != nil {
			client = redisClient
		}
		return sandboxalloc.NewUserMachineGC(svc, cleaner, configs, client)
	}))
	must(container.Invoke(startUserMachineGC))

	logger.Debugf(ctx, "[Container] Registering HTTP handlers...")
	must(container.Provide(handler.NewTenantHandler))
	must(container.Provide(handler.NewTenantMemberHandler))
	must(container.Provide(handler.NewTenantInvitationHandler))
	must(container.Provide(handler.NewAuditLogHandler))
	must(container.Provide(handler.NewKnowledgeBaseHandler))
	must(container.Provide(handler.NewGraphSearchHandler))
	must(container.Provide(handler.NewKnowledgeHandler))
	must(container.Provide(handler.NewChunkHandler))
	must(container.Provide(handler.NewFAQHandler))
	must(container.Provide(handler.NewTagHandler))
	must(container.Provide(session.NewHandler))
	must(container.Provide(handler.NewMessageHandler))
	must(container.Provide(handler.NewMessageSuggestionHandler))
	must(container.Provide(handler.NewModelHandler))
	must(container.Provide(handler.NewSandboxConfigHandler))
	must(container.Provide(func(
		s *service.TenantSkillService, streams interfaces.StreamManager,
	) *handler.SandboxSkillHandler {
		return handler.NewSandboxSkillHandler(s, streams)
	}))
	must(container.Provide(handler.NewMeEnvVarHandler))
	must(container.Provide(handler.NewEvaluationHandler))
	must(container.Provide(handler.NewInitializationHandler))
	must(container.Provide(handler.NewAuthHandler))
	must(container.Provide(service.NewDocumentProgressService))
	must(container.Provide(handler.NewSystemHandler))
	must(container.Provide(func(
		mcpService interfaces.MCPServiceService,
		toolApprovals interfaces.MCPToolApprovalService,
		gate *approval.Gate,
		models interfaces.ModelService,
		agents interfaces.AgentShareService,
		grants interfaces.AgentGrantRepository,
		secureForms *credential.Submitter,
	) *handler.MCPServiceHandler {
		return handler.NewMCPServiceHandler(mcpService, toolApprovals, gate, models, agents).
			WithGrants(grants).WithSecureForms(secureForms)
	}))
	must(container.Provide(handler.NewCredentialVaultHandler))
	must(container.Invoke(func(agents interfaces.AgentService, handles *credential.HandleStore) {
		if handles == nil {
			return
		}
		if s, ok := agents.(interface{ SetSecretHandles(tools.SecretHandles) }); ok {
			s.SetSecretHandles(handles)
		}
	}))
	must(container.Invoke(func(
		sessions *session.Handler, mcp *handler.MCPServiceHandler,
		broker *hitl.Broker, runs *service.SuspendedRunService, rewind *service.SessionRewindService,
		collaboration *service.AgentCollaboration, automations *service.AutomationService,
		sessionService interfaces.SessionService,
		messages *handler.MessageHandler, gate *approval.Gate, streams interfaces.StreamManager,
		cleaner interfaces.ResourceCleaner,
	) {
		rewind.SetSuspendedRuns(runs)
		sessions.SetSuspendedRuns(broker, runs)
		sessions.SetCollaborationStopper(collaboration.Stop)
		sessions.SetDelegatedPromptRouter(func(ctx context.Context, p *types.AgentPrompt) bool {
			return automations.HandlesPrompt(ctx, p) || collaboration.HandlesPrompt(ctx, p)
		})
		messages.DelegatedPrompts = collaboration.PendingPrompts
		messages.CollaborationStatuses = collaboration.HistoryStatuses
		collaboration.BindRecovery(broker, gate, streams)
		collaboration.ParentEvents = sessions.CollaborationEvents
		collaboration.ChildEvents = sessions.CollaborationEvents
		collaboration.ParentSteer = sessions.CollaborationSteer
		runs.RecoverAnswered = func(ctx context.Context, p *types.AgentPrompt) (bool, error) {
			if ok, err := automations.RecoverAnswered(ctx, p); ok || err != nil {
				return ok, err
			}
			return collaboration.RecoverAnswered(ctx, p)
		}
		automations.Bind(sessionService, broker, runs, streams)
		automations.Events = sessions.CollaborationEvents
		automations.StopDelegation = collaboration.StopOwned
		broker.SetRunHooks(runs.Hooks())
		runs.StopDelegation = func(ctx context.Context, p *types.AgentPrompt) error {
			return collaboration.StopPrompt(ctx, p)
		}
		mcp.SetRunResumer(sessions)
		mcp.SetScheduledPromptRouter(automations.HandlesPrompt)
		collaboration.Runtime.Start(context.Background())

		autoCtx, autoCancel := context.WithCancel(context.Background())
		automations.Start(autoCtx)
		cleaner.RegisterWithName("AutomationService", func() error {
			autoCancel()
			return nil
		})
	}))
	must(container.Invoke(startAgentPromptReaper))
	must(container.Provide(handler.NewMCPCredentialsHandler))
	must(container.Provide(handler.NewMCPOAuthHandler))
	must(container.Provide(handler.NewModelCredentialsHandler))
	must(container.Provide(handler.NewWebSearchProviderCredentialsHandler))
	must(container.Provide(handler.NewDataSourceCredentialsHandler))
	must(container.Provide(handler.NewWebSearchHandler))
	must(container.Provide(handler.NewWebSearchProviderHandler))
	must(container.Provide(handler.NewVectorStoreHandler))
	must(container.Provide(handler.NewStorageBackendHandler))
	must(container.Provide(handler.NewCustomAgentHandler))
	must(container.Provide(handler.NewUserResourceFavoriteHandler))
	must(container.Provide(func(
		s *service.TenantSkillService, agents interfaces.AgentShareService,
	) *handler.SkillHandler {
		return handler.NewSkillHandler(s, s, agents)
	}))
	must(container.Provide(handler.NewOrganizationHandler))
	must(container.Provide(handler.NewMemoryHandler))
	must(container.Provide(handler.NewAutomationHandler))
	must(container.Provide(handler.NewDataSourceHandler))
	must(container.Provide(handler.NewWikiPageHandler))

	logger.Debugf(ctx, "[Container] Registering IM integration...")
	must(container.Provide(imPkg.NewService))
	must(container.Invoke(registerIMService))
	must(container.Provide(handler.NewIMHandler))
	must(container.Provide(handler.NewEmbedChannelHandler))
	must(container.Provide(handler.NewMCPEndpointHandler))

	must(container.Invoke(registerChatLocalImageResolver))

	logger.Debugf(ctx, "[Container] Registering router and starting task server...")
	must(container.Provide(router.NewRouter))
	if redisAvailable {
		must(container.Invoke(router.RunAsynqServer))
		must(container.Invoke(registerAsynqCleanup))
	} else {
		must(container.Invoke(router.RegisterSyncHandlers))
	}
	must(container.Invoke(recoverPendingWikiTasks))

	must(container.Invoke(func(cleaner interfaces.ResourceCleaner, _ *browserskill.Manager) {
		cleaner.Promote("BrowserSkill")
	}))
	must(container.Invoke(startShellJobSweeper))
	must(container.Invoke(func(cleaner interfaces.ResourceCleaner, _ *service.ShellJobService) {
		cleaner.Promote("LocalShellJobs")
	}))

	logger.Infof(ctx, "[Container] Container initialization completed successfully")
	return container
}

// registerChatLocalImageResolver wires the chat package's LocalImageResolver hook.
func registerChatLocalImageResolver(
	tenantRepo interfaces.TenantRepository,
	storageResolver interfaces.StorageBackendResolver,
	resourceCatalog interfaces.ResourceCatalog,
) {
	api.LocalImageResolver = func(storageURL string) ([]byte, bool) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		physicalPath, resource, err := resourceCatalog.ResolvePath(ctx, storageURL)
		if err != nil {
			return nil, false
		}
		tenantID := secutils.ParseTenantIDFromStoragePath(physicalPath)
		if resource != nil {
			tenantID = resource.TenantID
		}
		if tenantID == 0 {
			return nil, false
		}
		tenant, err := tenantRepo.GetTenantByID(ctx, tenantID)
		if err != nil || tenant == nil {
			return nil, false
		}
		baseDir := strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR"))
		if baseDir == "" {
			baseDir = "/data/files"
		}
		backendID, inner, scoped := types.ParseStorageBackendPath(physicalPath)
		if resource != nil && resource.StorageBackendID != "" {
			backendID = resource.StorageBackendID
		}
		providerPath := physicalPath
		if scoped {
			providerPath = inner
		}
		provider := types.ParseProviderScheme(providerPath)
		if provider == "" {
			provider = "local"
		}
		fileSvc, _, err := storageResolver.ResolveFileService(ctx, tenant, backendID, provider, baseDir)
		if err != nil {
			return nil, false
		}
		rc, err := fileSvc.GetFile(ctx, physicalPath)
		if err != nil {
			return nil, false
		}
		defer func() {
			if closeErr := rc.Close(); closeErr != nil {
				logger.Debugf(ctx, "Close local image stream: %v", closeErr)
			}
		}()
		const maxChatLocalImageBytes = 50 << 20 // 50 MiB cap to prevent OOM on corrupted/huge blobs
		data, err := io.ReadAll(io.LimitReader(rc, maxChatLocalImageBytes+1))
		if err != nil || len(data) > maxChatLocalImageBytes {
			return nil, false
		}
		return data, true
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

const defaultModelMaxConcurrency = 32

func resolveModelMaxConcurrency(ss interfaces.SystemSettingService) int {
	if ss == nil {
		return defaultModelMaxConcurrency
	}
	return int(ss.GetInt(context.Background(), "model.max_concurrency",
		"GENYEE_MODEL_MAX_CONCURRENCY", int64(defaultModelMaxConcurrency)))
}

func registerModelConcurrencyLimiter(rdb *redis.Client, ss interfaces.SystemSettingService) {
	limit := resolveModelMaxConcurrency(ss)
	limiter.SetGovernor(limiter.NewRedisLimiter(rdb), limit)
	if limit <= 0 {
		logger.Infof(context.Background(),
			"[ModelLimiter] background concurrency governor DISABLED (model.max_concurrency<=0)")
		return
	}
	logger.Infof(
		context.Background(),
		"[ModelLimiter] background model concurrency governed per-model, limit=%d (distributed via redis)",
		limit,
	)
}

func registerLiteModelConcurrencyLimiter(ss interfaces.SystemSettingService) {
	limit := resolveModelMaxConcurrency(ss)
	limiter.SetGovernor(limiter.NewLocalLimiter(), limit)
	if limit <= 0 {
		logger.Infof(context.Background(),
			"[ModelLimiter] background concurrency governor DISABLED (model.max_concurrency<=0)")
		return
	}
	logger.Infof(
		context.Background(),
		"[ModelLimiter] background model concurrency governed per-model, limit=%d (in-process, lite mode)",
		limit,
	)
}
