package app

import (
	"context"
	"fmt"
	"net/http"

	"github.com/thkx/agentmesh/internal/config"
	"github.com/thkx/agentmesh/internal/domain/provider"
	"github.com/thkx/agentmesh/internal/modelgateway"
	"github.com/thkx/agentmesh/internal/platform/database"
	"github.com/thkx/agentmesh/internal/platform/logger"

	anthropicadapter "github.com/thkx/agentmesh/internal/modelgateway/adapters/anthropic"
	geminiadapter "github.com/thkx/agentmesh/internal/modelgateway/adapters/gemini"
	ollamaadapter "github.com/thkx/agentmesh/internal/modelgateway/adapters/ollama"
	openaiadapter "github.com/thkx/agentmesh/internal/modelgateway/adapters/openai"
)

type ProviderAdaptersConfig struct {
	OpenAIAPIKey    string
	AnthropicAPIKey string
	GeminiAPIKey    string
	OllamaBaseURL   string
}

func RegisterProviderAdapters(
	ctx context.Context,
	registry *modelgateway.Registry,
	cfg ProviderAdaptersConfig,
) error {
	openAI, err := openaiadapter.New(cfg.OpenAIAPIKey)
	if err != nil {
		return fmt.Errorf("initialize OpenAI: %w", err)
	}
	if err := registry.Register(provider.IDOpenAI, openAI); err != nil {
		return err
	}

	anthropic, err := anthropicadapter.New(cfg.AnthropicAPIKey)
	if err != nil {
		return fmt.Errorf("initialize Anthropic: %w", err)
	}
	if err := registry.Register(provider.IDAnthropic, anthropic); err != nil {
		return err
	}

	gemini, err := geminiadapter.New(ctx, cfg.GeminiAPIKey)
	if err != nil {
		return fmt.Errorf("initialize Gemini: %w", err)
	}
	if err := registry.Register(provider.IDGemini, gemini); err != nil {
		return err
	}

	ollama, err := ollamaadapter.New(
		cfg.OllamaBaseURL,
		&http.Client{},
	)
	if err != nil {
		return fmt.Errorf("initialize Ollama: %w", err)
	}
	if err := registry.Register(provider.IDOllama, ollama); err != nil {
		return err
	}

	return nil
}

func Bootstrap(ctx context.Context, configPath string) (*Container, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("load application config: %w", err)
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

	return &Container{
		Config: cfg,
		Logger: log,
		DB:     sqliteDB.DB,
	}, nil
}
