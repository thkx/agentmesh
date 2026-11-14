package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	cliexecutor "github.com/thkx/agentmesh/internal/agent/executor/cli"
	agentruntime "github.com/thkx/agentmesh/internal/agent/runtime"
	"github.com/thkx/agentmesh/internal/agent/scheduler"
	agentstate "github.com/thkx/agentmesh/internal/agent/state"
	"github.com/thkx/agentmesh/internal/agentcatalog"
	"github.com/thkx/agentmesh/internal/airesource"
	"github.com/thkx/agentmesh/internal/coding"
	"github.com/thkx/agentmesh/internal/config"
	appcredential "github.com/thkx/agentmesh/internal/credential"
	"github.com/thkx/agentmesh/internal/domain/execution"
	executionmanager "github.com/thkx/agentmesh/internal/execution/manager"
	executionrepository "github.com/thkx/agentmesh/internal/execution/repository"
	"github.com/thkx/agentmesh/internal/domain/provider"
	goalservice "github.com/thkx/agentmesh/internal/goal"
	"github.com/thkx/agentmesh/internal/goalevaluation"
	memoryservice "github.com/thkx/agentmesh/internal/memory"
	"github.com/thkx/agentmesh/internal/modelgateway"
	anthropicadapter "github.com/thkx/agentmesh/internal/modelgateway/adapters/anthropic"
	geminiadapter "github.com/thkx/agentmesh/internal/modelgateway/adapters/gemini"
	ollamaadapter "github.com/thkx/agentmesh/internal/modelgateway/adapters/ollama"
	openaiadapter "github.com/thkx/agentmesh/internal/modelgateway/adapters/openai"
	"github.com/thkx/agentmesh/internal/planning"
	"github.com/thkx/agentmesh/internal/planworkflow"
	"github.com/thkx/agentmesh/internal/platform/credentialstore"
	"github.com/thkx/agentmesh/internal/platform/cryptography"
	"github.com/thkx/agentmesh/internal/platform/database"
	"github.com/thkx/agentmesh/internal/platform/logger"
	"github.com/thkx/agentmesh/internal/reflection"
	"github.com/thkx/agentmesh/internal/tools"
	"github.com/thkx/agentmesh/internal/tools/audit"
	"github.com/thkx/agentmesh/internal/tools/builtin"
	toolpolicy "github.com/thkx/agentmesh/internal/tools/policy"
	httptransport "github.com/thkx/agentmesh/internal/transport/http"
	workflowcheckpoint "github.com/thkx/agentmesh/internal/workflow/checkpoint"
	workflowpolicy "github.com/thkx/agentmesh/internal/workflow/policy"
	wfruntime "github.com/thkx/agentmesh/internal/workflow/runtime"
	"github.com/thkx/agentmesh/internal/workspace"
)

// buildRuntime constructs isolated provider instances and the model registry from the persisted catalog.
func buildRuntime(ctx context.Context, providerItems []provider.Provider, modelItems []provider.Model, credentials appcredential.Resolver) (*provider.ProviderRegistry, *provider.ModelRegistry, *modelgateway.Registry, *modelgateway.Gateway, *modelgateway.ProviderRuntime, error) {
	providers := provider.NewProviderRegistry()
	models := provider.NewModelRegistry()
	adapters := modelgateway.NewRegistry()
	factory := modelgateway.NewAdapterFactory()

	if err := factory.Register(provider.TypeOpenAI, func(p provider.Provider) (modelgateway.Model, error) {
		resolved, err := credentials.Resolve(ctx, p)
		if err != nil {
			return nil, err
		}
		key := resolved.Secret.Reveal()
		baseURL := p.BaseURL
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		return openaiadapter.NewWithBaseURL(key, baseURL)
	}); err != nil {
		return nil, nil, nil, nil, nil, err
	}
	if err := factory.Register(provider.TypeAnthropic, func(p provider.Provider) (modelgateway.Model, error) {
		resolved, err := credentials.Resolve(ctx, p)
		if err != nil {
			return nil, err
		}
		key := resolved.Secret.Reveal()
		baseURL := p.BaseURL
		if baseURL == "" {
			baseURL = "https://api.anthropic.com"
		}
		return anthropicadapter.NewWithBaseURL(key, baseURL)
	}); err != nil {
		return nil, nil, nil, nil, nil, err
	}
	if err := factory.Register(provider.TypeGemini, func(p provider.Provider) (modelgateway.Model, error) {
		resolved, err := credentials.Resolve(ctx, p)
		if err != nil {
			return nil, err
		}
		key := resolved.Secret.Reveal()
		baseURL := p.BaseURL
		if baseURL == "" {
			baseURL = "https://generativelanguage.googleapis.com/"
		}
		return geminiadapter.NewWithBaseURL(ctx, key, baseURL)
	}); err != nil {
		return nil, nil, nil, nil, nil, err
	}
	if err := factory.Register(provider.TypeOllama, func(p provider.Provider) (modelgateway.Model, error) {
		return ollamaadapter.New(p.BaseURL, &http.Client{Timeout: 0})
	}); err != nil {
		return nil, nil, nil, nil, nil, err
	}

	for _, p := range providerItems {
		if err := providers.Register(p); err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("register provider %s: %w", p.ID, err)
		}
		if !p.Enabled {
			continue
		}
		adapter, err := factory.Build(p)
		if err != nil {
			if errors.Is(err, appcredential.ErrCredentialUnavailable) {
				continue
			}
			return nil, nil, nil, nil, nil, err
		}
		if err := adapters.Register(p.ID, adapter); err != nil {
			return nil, nil, nil, nil, nil, err
		}
	}
	for _, m := range modelItems {
		if _, err := providers.Get(m.ProviderID); err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("model %s: %w", m.ID, err)
		}
		if err := models.Register(m); err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("register model %s: %w", m.ID, err)
		}
	}
	gateway, err := modelgateway.NewGatewayWithProviders(adapters, models, providers)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	runtime, err := modelgateway.NewProviderRuntime(providers, adapters, factory)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	return providers, models, adapters, gateway, runtime, nil
}

func Bootstrap(ctx context.Context, configPath string) (*Container, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("load application config: %w", err)
	}
	apiToken := strings.TrimSpace(os.Getenv("AGENTMESH_API_TOKEN"))
	if apiToken != "" && len(apiToken) < 32 {
		return nil, fmt.Errorf("AGENTMESH_API_TOKEN must contain at least 32 characters")
	}
	host := strings.Trim(cfg.Server.Host, "[]")
	ip := net.ParseIP(host)
	loopback := host == "localhost" || (ip != nil && ip.IsLoopback())
	if !loopback && apiToken == "" {
		return nil, fmt.Errorf("AGENTMESH_API_TOKEN is required when server host is not loopback")
	}

	log, err := logger.New(cfg.Log.Level, cfg.Log.Format)
	if err != nil {
		return nil, fmt.Errorf("initialize logger: %w", err)
	}

	log.InfoContext(ctx, "starting AgentMesh",
		"environment", cfg.App.Environment,
	)

	sqliteDB, err := database.OpenSQLite(ctx, cfg.Database)
	if err != nil {
		return nil, fmt.Errorf("initialize database: %w", err)
	}

	if err := database.RunMigrations(sqliteDB.DB); err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize database migrations: %w", err)
	}

	log.InfoContext(ctx, "database initialized")

	runtimeRepo, err := database.NewRuntimeRepository(sqliteDB.DB)
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize runtime repository: %w", err)
	}

	configuredProviders := make([]provider.Provider, 0, len(cfg.Providers))
	for _, item := range cfg.Providers {
		kind := provider.KindCloud
		if strings.EqualFold(item.Kind, "local") || strings.EqualFold(item.Type, "ollama") {
			kind = provider.KindLocal
		}
		configuredProviders = append(configuredProviders, provider.Provider{
			ID: provider.ID(item.ID), Type: provider.Type(strings.ToLower(item.Type)), Name: item.Name,
			Kind: kind, BaseURL: item.BaseURL, CredentialRef: item.APIKeyEnv, Enabled: item.Enabled,
		})
	}
	configuredModels := make([]provider.Model, 0, len(cfg.Models))
	for _, item := range cfg.Models {
		configuredModels = append(configuredModels, provider.Model{
			ID: item.ID, ProviderID: provider.ID(item.ProviderID), Name: item.Name, DisplayName: item.DisplayName,
			ContextWindow: item.ContextWindow, MaxOutputTokens: item.MaxOutputTokens,
			SupportsStreaming: item.SupportsStreaming, Enabled: item.Enabled,
		})
	}
	if err := runtimeRepo.Sync(ctx, configuredProviders, configuredModels); err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("persist provider/model configuration: %w", err)
	}
	persistedProviders, err := runtimeRepo.ListProviders(ctx)
	if err != nil {
		_ = sqliteDB.Close()
		return nil, err
	}
	persistedModels, err := runtimeRepo.ListModels(ctx)
	if err != nil {
		_ = sqliteDB.Close()
		return nil, err
	}

	databasePath := cfg.Database.DSN
	if strings.HasPrefix(databasePath, "file:") {
		if parsed, parseErr := url.Parse(databasePath); parseErr == nil && parsed.Path != "" {
			databasePath = parsed.Path
		}
	}
	keyDir := filepath.Join(filepath.Dir(databasePath), "keys")
	keyProvider := cryptography.FileKeyProvider{Path: filepath.Join(keyDir, "credential.key")}
	credentialStore, err := credentialstore.NewSQLiteStore(sqliteDB.DB, keyProvider)
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize credential store: %w", err)
	}
	resourceKey, err := keyProvider.Key()
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("load AI resource encryption key: %w", err)
	}
	resourceCipher, err := cryptography.NewAESGCM(resourceKey)
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize AI resource encryption: %w", err)
	}
	aiResources, err := airesource.NewService(sqliteDB.DB, resourceCipher)
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize AI resource service: %w", err)
	}
	aiRouter, err := airesource.NewRouter(aiResources)
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize AI router: %w", err)
	}
	aiHTTP, err := airesource.NewHTTPHandler(aiResources, aiRouter, apiToken)
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize AI resource HTTP handler: %w", err)
	}
	credentialResolver, err := appcredential.NewResolver(credentialStore, appcredential.OSEnvironment{})
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize credential resolver: %w", err)
	}
	providers, models, adapters, gateway, providerRuntime, err := buildRuntime(ctx, persistedProviders, persistedModels, credentialResolver)
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize provider runtime: %w", err)
	}

	credentialManager, err := appcredential.NewManager(credentialStore, credentialResolver, providers, providerRuntime)
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize credential manager: %w", err)
	}

	workspaceManager, err := workspace.NewManager(cfg.Agent.WorkspaceRoot)
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize workspace manager: %w", err)
	}
	cliPaths := map[execution.Provider]string{
		execution.ProviderClaude:   cfg.Agent.CLI.Claude,
		execution.ProviderCodex:    cfg.Agent.CLI.Codex,
		execution.ProviderGemini:   cfg.Agent.CLI.Gemini,
		execution.ProviderOpenCode: cfg.Agent.CLI.OpenCode,
	}
	agentExec := cliexecutor.New(cliexecutor.DefaultAdapters(cliPaths), cfg.Agent.WorkspaceRoot, workspaceManager)
	toolRegistry := tools.NewRegistry()
	if err := builtin.Register(toolRegistry, builtin.Config{MaxFileBytes: cfg.Agent.Tools.MaxFileBytes, MaxOutputBytes: cfg.Agent.Tools.MaxOutputBytes, AllowedCommands: cfg.Agent.Tools.AllowedCommands, AllowedHTTPDomains: cfg.Agent.Tools.AllowedHTTPDomains, SandboxImage: cfg.Agent.Tools.SandboxImage, SandboxNetworkEnabled: cfg.Agent.Tools.SandboxNetworkEnabled, CommandTimeout: cfg.Agent.Tools.CommandTimeout}); err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("register built-in tools: %w", err)
	}
	toolAudit, err := audit.NewSQLite(sqliteDB.DB)
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize tool audit store: %w", err)
	}
	toolPolicy := toolpolicy.Static{ReadEnabled: true, WriteEnabled: true, ExecuteEnabled: true, RequireApprovalForWrite: true, RequireApprovalForExecute: true}
	toolEngine := tools.NewEngine(toolRegistry, toolPolicy, toolAudit)
	toolEngine.SetApprovalStore(toolAudit)
	agentRuntime, err := agentruntime.New(gateway, toolEngine, workspaceManager, agentruntime.Config{DefaultMaxSteps: cfg.Agent.Runtime.MaxSteps, DefaultMaxToolCalls: cfg.Agent.Runtime.MaxToolCalls, MaxContextChars: cfg.Agent.Runtime.MaxContextChars, MaxContextTokens: cfg.Agent.Runtime.MaxContextTokens, MaxConcurrent: cfg.Agent.Runtime.MaxConcurrent, DefaultTimeout: time.Duration(cfg.Agent.Runtime.TimeoutSeconds) * time.Second})
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize agent runtime: %w", err)
	}
	stateStore, err := agentstate.NewSQLiteStore(sqliteDB.DB)
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize agent state store: %w", err)
	}
	agentRuntime.SetStateStore(stateStore)

	codingRepo, err := coding.NewRepository(sqliteDB.DB)
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize coding task repository: %w", err)
	}
	codingService, err := coding.NewService(codingRepo, agentRuntime, cfg.Agent.WorkspaceRoot, cfg.Agent.Tools.SandboxImage, cfg.Agent.Tools.SandboxNetworkEnabled)
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize coding task service: %w", err)
	}
	if _, err := codingService.Recover(ctx); err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("recover interrupted coding tasks: %w", err)
	}

	schedulerStore, err := scheduler.NewSQLiteStore(sqliteDB.DB)
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize agent scheduler store: %w", err)
	}
	executionStore, err := executionrepository.NewSQLiteStore(sqliteDB.DB)
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize execution repository: %w", err)
	}
	executionManager, err := executionmanager.New(executionStore)
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize execution manager: %w", err)
	}
	agentExecutionAdapter, err := executionmanager.NewAgentAdapter(schedulerStore, agentRuntime, cfg.Scheduler.MaxAttempts)
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize agent execution adapter: %w", err)
	}
	if err := executionManager.Register(execution.RuntimeAgent, agentExecutionAdapter); err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("register agent execution adapter: %w", err)
	}
	codingExecutionAdapter, err := executionmanager.NewCodingAdapter(codingService)
	if err != nil { _ = sqliteDB.Close(); return nil, fmt.Errorf("initialize coding execution adapter: %w", err) }
	if err := executionManager.Register(execution.RuntimeCoding, codingExecutionAdapter); err != nil { _ = sqliteDB.Close(); return nil, fmt.Errorf("register coding execution adapter: %w", err) }
	cliExecutionAdapter, err := executionmanager.NewCLIAdapter(agentExec, executionStore)
	if err != nil { _ = sqliteDB.Close(); return nil, fmt.Errorf("initialize CLI execution adapter: %w", err) }
	if err := executionManager.Register(execution.RuntimeCLI, cliExecutionAdapter); err != nil { _ = sqliteDB.Close(); return nil, fmt.Errorf("register CLI execution adapter: %w", err) }
	agentExecutionObserver, err := executionmanager.NewAgentObserver(executionStore)
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize agent execution observer: %w", err)
	}
	toolLedger, err := scheduler.NewToolLedger(sqliteDB.DB)
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize tool invocation ledger: %w", err)
	}
	agentRuntime.SetToolLedger(toolLedger)
	agentDefinitionRepo, err := agentcatalog.NewSQLiteRepository(sqliteDB.DB)
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize agent definition repository: %w", err)
	}
	agentDefinitions, err := agentcatalog.NewService(agentDefinitionRepo, agentRuntime)
	if err != nil {
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize agent definition service: %w", err)
	}
	workerCount := cfg.Scheduler.Workers
	if workerCount <= 0 {
		workerCount = 1
	}
	workerCtx, workerCancel := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	var workerWG sync.WaitGroup
	var primaryWorker *scheduler.Worker
	for i := 0; i < workerCount; i++ {
		workerID := cfg.Scheduler.WorkerID
		if workerID != "" {
			workerID = fmt.Sprintf("%s-%d", workerID, i+1)
		}
		worker, workerErr := scheduler.NewWorker(schedulerStore, agentRuntime, scheduler.WorkerConfig{Logger: log, ID: workerID, PollInterval: cfg.Scheduler.PollInterval, LeaseDuration: cfg.Scheduler.LeaseDuration, Observer: agentExecutionObserver})
		if workerErr != nil {
			workerCancel()
			workerWG.Wait()
			_ = sqliteDB.Close()
			return nil, fmt.Errorf("initialize agent scheduler worker: %w", workerErr)
		}
		if primaryWorker == nil {
			primaryWorker = worker
		}
		workerWG.Add(1)
		go func(w *scheduler.Worker) {
			defer workerWG.Done()
			if runErr := w.Run(workerCtx); runErr != nil && !errors.Is(runErr, context.Canceled) {
				log.Error("agent scheduler worker stopped", "error", runErr)
			}
		}(worker)
	}

	goalRepo, err := database.NewGoalRepository(sqliteDB.DB)
	if err != nil {
		workerCancel()
		workerWG.Wait()
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize goal repository: %w", err)
	}
	planRepo, err := database.NewPlanRepository(sqliteDB.DB)
	if err != nil {
		workerCancel()
		workerWG.Wait()
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize plan repository: %w", err)
	}
	goalService, err := goalservice.NewService(goalRepo)
	if err != nil {
		workerCancel()
		workerWG.Wait()
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize goal service: %w", err)
	}
	llmPlanner, err := planning.NewLLMPlanner(gateway, 0)
	if err != nil {
		workerCancel()
		workerWG.Wait()
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize LLM planner: %w", err)
	}
	planningService, err := planning.NewService(goalService, llmPlanner, planRepo)
	if err != nil {
		workerCancel()
		workerWG.Wait()
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize planning service: %w", err)
	}

	workflowStore, err := workflowcheckpoint.NewSQLite(sqliteDB.DB)
	if err != nil {
		workerCancel()
		workerWG.Wait()
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize workflow checkpoint store: %w", err)
	}
	workflowEngine, err := wfruntime.New(workflowStore, agentRuntime, workflowpolicy.AllowAll{})
	if err != nil {
		workerCancel()
		workerWG.Wait()
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize workflow engine: %w", err)
	}
	workflowExecutionAdapter, err := executionmanager.NewWorkflowAdapter(workflowEngine, cfg.Scheduler.MaxAttempts)
	if err != nil { workerCancel(); workerWG.Wait(); _ = sqliteDB.Close(); return nil, fmt.Errorf("initialize workflow execution adapter: %w", err) }
	if err := executionManager.Register(execution.RuntimeWorkflow, workflowExecutionAdapter); err != nil { workerCancel(); workerWG.Wait(); _ = sqliteDB.Close(); return nil, fmt.Errorf("register workflow execution adapter: %w", err) }
	if report, reconcileErr := executionManager.Reconcile(ctx, 500); reconcileErr != nil {
		log.WarnContext(ctx, "execution startup reconciliation failed", "error", reconcileErr)
	} else if report.Recovered > 0 || report.NeedsAttention > 0 {
		log.InfoContext(ctx, "execution startup reconciliation completed", "scanned", report.Scanned, "recovered", report.Recovered, "needs_attention", report.NeedsAttention)
	}
	if report, reconcileErr := executionManager.Reconcile(ctx, 500); reconcileErr != nil {
		log.WarnContext(ctx, "execution startup reconciliation failed", "error", reconcileErr)
	} else if report.Recovered > 0 || report.NeedsAttention > 0 {
		log.InfoContext(ctx, "execution startup reconciliation completed", "scanned", report.Scanned, "recovered", report.Recovered, "needs_attention", report.NeedsAttention)
	}
	planExecutionRepo, err := planworkflow.NewSQLiteRepository(sqliteDB.DB)
	if err != nil {
		workerCancel()
		workerWG.Wait()
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize plan execution repository: %w", err)
	}
	profileValidator := planworkflow.ProfileValidatorFuncs{
		ModelExists:     func(id string) bool { _, ok := models.Get(id); return ok },
		WorkspaceExists: func(id string) bool { _, resolveErr := workspaceManager.Resolve(id); return resolveErr == nil },
	}
	planExecutionService, err := planworkflow.NewService(goalService, planningService, planExecutionRepo, workflowEngine, profileValidator, cfg.Scheduler.MaxAttempts)
	if err != nil {
		workerCancel()
		workerWG.Wait()
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize plan execution service: %w", err)
	}
	evaluationRepo, err := database.NewGoalEvaluationRepository(sqliteDB.DB)
	if err != nil {
		workerCancel()
		workerWG.Wait()
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize goal evaluation repository: %w", err)
	}
	goalEvaluator, err := goalevaluation.NewLLMEvaluator(gateway, 0)
	if err != nil {
		workerCancel()
		workerWG.Wait()
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize goal evaluator: %w", err)
	}
	goalEvaluationService, err := goalevaluation.NewService(goalService, workflowEngine, evaluationRepo, goalEvaluator)
	if err != nil {
		workerCancel()
		workerWG.Wait()
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize goal evaluation service: %w", err)
	}
	reflectionRepo, err := database.NewReflectionRepository(sqliteDB.DB)
	if err != nil {
		workerCancel()
		workerWG.Wait()
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize reflection repository: %w", err)
	}
	llmReflector, err := reflection.NewLLMReflector(gateway, 0)
	if err != nil {
		workerCancel()
		workerWG.Wait()
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize LLM reflector: %w", err)
	}
	reflectionService, err := reflection.NewService(goalService, workflowEngine, goalEvaluationService, reflectionRepo, llmReflector)
	if err != nil {
		workerCancel()
		workerWG.Wait()
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize reflection service: %w", err)
	}
	memoryRepo, err := database.NewMemoryRepository(sqliteDB.DB)
	if err != nil {
		workerCancel()
		workerWG.Wait()
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize memory repository: %w", err)
	}
	memoryService, err := memoryservice.NewService(memoryRepo, reflectionRepo)
	if err != nil {
		workerCancel()
		workerWG.Wait()
		_ = sqliteDB.Close()
		return nil, fmt.Errorf("initialize memory service: %w", err)
	}
	planExecutionService.SetMemoryRetriever(memoryService)
	workflowWorkers := make([]*wfruntime.Worker, 0, workerCount)
	for i := 0; i < workerCount; i++ {
		workerID := cfg.Scheduler.WorkerID
		if workerID != "" {
			workerID = fmt.Sprintf("%s-workflow-%d", workerID, i+1)
		}
		workflowWorker, workerErr := wfruntime.NewWorker(workflowStore, workflowEngine, wfruntime.WorkerConfig{ID: workerID, PollInterval: cfg.Scheduler.PollInterval, LeaseDuration: cfg.Scheduler.LeaseDuration, Logger: log})
		if workerErr != nil {
			workerCancel()
			workerWG.Wait()
			_ = sqliteDB.Close()
			return nil, fmt.Errorf("initialize workflow worker: %w", workerErr)
		}
		workflowWorkers = append(workflowWorkers, workflowWorker)
		workerWG.Add(1)
		go func(w *wfruntime.Worker) {
			defer workerWG.Done()
			if runErr := w.Run(workerCtx); runErr != nil && !errors.Is(runErr, context.Canceled) {
				log.Error("workflow worker stopped", "error", runErr)
			}
		}(workflowWorker)
	}
	go func() { workerWG.Wait(); close(workerDone) }()

	container := &Container{
		Config: cfg, Logger: log, DB: sqliteDB.DB, RuntimeRepository: runtimeRepo, GoalService: goalService, PlanningService: planningService, PlanExecutionService: planExecutionService, GoalEvaluationService: goalEvaluationService, ReflectionService: reflectionService, MemoryService: memoryService,
		CredentialStore: credentialStore, CredentialResolver: credentialResolver, CredentialManager: credentialManager,
		Providers: providers, Models: models, Adapters: adapters, Gateway: gateway, ProviderRuntime: providerRuntime, AgentExecutor: agentExec, AgentRuntime: agentRuntime, ExecutionManager: executionManager, WorkflowEngine: workflowEngine, WorkflowWorkers: workflowWorkers, SchedulerStore: schedulerStore, SchedulerWorker: primaryWorker, workerCancel: workerCancel, workerDone: workerDone, Workspaces: workspaceManager, CodingService: codingService, ToolRegistry: toolRegistry, ToolEngine: toolEngine, ToolAudit: toolAudit, AgentDefinitions: agentDefinitions, AIResources: aiResources, AIRouter: aiRouter,
	}
	container.HTTPHandler = httptransport.NewRouter(httptransport.Dependencies{
		DB: sqliteDB.DB, Providers: providers, Models: models, Adapters: adapters,
		ProviderRuntime: providerRuntime, Repository: runtimeRepo, Gateway: gateway, CredentialManager: credentialManager, AgentExecutor: agentExec, AgentRuntime: agentRuntime, ExecutionManager: executionManager, WorkflowEngine: workflowEngine, GoalService: goalService, PlanningService: planningService, PlanExecutionService: planExecutionService, GoalEvaluationService: goalEvaluationService, ReflectionService: reflectionService, MemoryService: memoryService, SchedulerStore: schedulerStore, SchedulerMaxAttempts: cfg.Scheduler.MaxAttempts, WorkflowMaxAttempts: cfg.Scheduler.MaxAttempts, Workspaces: workspaceManager, CodingService: codingService, ToolRegistry: toolRegistry, ToolEngine: toolEngine, ToolAudit: toolAudit, AgentDefinitions: agentDefinitions, AIResourceHandler: aiHTTP, APIToken: apiToken,
	})
	return container, nil
}
