package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	openaioption "github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"

	anthropic "github.com/anthropics/anthropic-sdk-go/v2"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/v2/option"
	"google.golang.org/genai"
)

type Config struct {
	Provider string
	Model    string
	APIKey   string
	Prompt   string
	Timeout  time.Duration
}

func loadConfig() (Config, error) {
	cfg := Config{
		Provider: strings.ToLower(strings.TrimSpace(os.Getenv("PROVIDER"))),
		Model:    strings.TrimSpace(os.Getenv("MODEL")),
		APIKey:   strings.TrimSpace(os.Getenv("API_KEY")),
		Prompt:   strings.TrimSpace(os.Getenv("PROMPT")),
		Timeout:  3 * time.Minute,
	}

	if cfg.Provider == "" {
		return cfg, errors.New("PROVIDER is required: openai, anthropic, gemini")
	}
	if cfg.Model == "" {
		return cfg, errors.New("MODEL is required")
	}
	if cfg.APIKey == "" {
		return cfg, errors.New("API_KEY is required")
	}
	if cfg.Prompt == "" {
		cfg.Prompt = "用 Go 语言解释什么是 Context，并给出一个简短示例。"
	}

	switch cfg.Provider {
	case "openai", "anthropic", "gemini":
	default:
		return cfg, fmt.Errorf("unsupported provider: %s", cfg.Provider)
	}

	return cfg, nil
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	fmt.Printf("Provider: %s\n", cfg.Provider)
	fmt.Printf("Model:    %s\n", cfg.Model)
	fmt.Printf("Prompt:   %s\n\n", cfg.Prompt)

	fmt.Println("========== Generate ==========")

	if err := generate(ctx, cfg); err != nil {
		log.Fatalf("generate failed: %v", err)
	}

	fmt.Println("\n========== Stream ==========")

	if err := stream(ctx, cfg); err != nil {
		log.Fatalf("stream failed: %v", err)
	}
}

func generate(ctx context.Context, cfg Config) error {
	switch cfg.Provider {
	case "openai":
		return openAIGenerate(ctx, cfg)
	case "anthropic":
		return anthropicGenerate(ctx, cfg)
	case "gemini":
		return geminiGenerate(ctx, cfg)
	default:
		return errors.New("unsupported provider")
	}
}

func stream(ctx context.Context, cfg Config) error {
	switch cfg.Provider {
	case "openai":
		return openAIStream(ctx, cfg)
	case "anthropic":
		return anthropicStream(ctx, cfg)
	case "gemini":
		return geminiStream(ctx, cfg)
	default:
		return errors.New("unsupported provider")
	}
}

// -------------------- OpenAI --------------------

func openAIGenerate(ctx context.Context, cfg Config) error {
	client := openai.NewClient(
		openaioption.WithAPIKey(cfg.APIKey),
	)

	resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
		Model: openai.ChatModel(cfg.Model),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(cfg.Prompt),
		},
	})
	if err != nil {
		return fmt.Errorf("openai responses: %w", err)
	}

	fmt.Println(resp.OutputText())
	return nil
}

func openAIStream(ctx context.Context, cfg Config) error {
	client := openai.NewClient(
		openaioption.WithAPIKey(cfg.APIKey),
	)

	s := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
		Model: openai.ChatModel(cfg.Model),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(cfg.Prompt),
		},
	})
	defer func() {
		_ = s.Close()
	}()

	for s.Next() {
		event := s.Current()
		if event.Delta != "" {
			fmt.Print(event.Delta)
		}
	}

	if err := s.Err(); err != nil {
		return fmt.Errorf("openai stream: %w", err)
	}

	fmt.Println()
	return nil
}

// -------------------- Anthropic --------------------

func anthropicGenerate(ctx context.Context, cfg Config) error {
	client := anthropic.NewClient(
		anthropicoption.WithAPIKey(cfg.APIKey),
	)

	resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(cfg.Model),
		MaxTokens: 2048,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(
				anthropic.NewTextBlock(cfg.Prompt),
			),
		},
	})
	if err != nil {
		return fmt.Errorf("anthropic messages: %w", err)
	}

	for _, block := range resp.Content {
		if textBlock, ok := block.AsAny().(anthropic.TextBlock); ok {
			fmt.Print(textBlock.Text)
		}
	}
	fmt.Println()

	return nil
}

func anthropicStream(ctx context.Context, cfg Config) error {
	client := anthropic.NewClient(
		anthropicoption.WithAPIKey(cfg.APIKey),
	)

	s := client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(cfg.Model),
		MaxTokens: 2048,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(
				anthropic.NewTextBlock(cfg.Prompt),
			),
		},
	})
	defer func() {
		_ = s.Close()
	}()

	for s.Next() {
		event := s.Current()

		switch e := event.AsAny().(type) {
		case anthropic.ContentBlockDeltaEvent:
			if delta, ok := e.Delta.AsAny().(anthropic.TextDelta); ok {
				fmt.Print(delta.Text)
			}
		}
	}

	if err := s.Err(); err != nil {
		return fmt.Errorf("anthropic stream: %w", err)
	}

	fmt.Println()
	return nil
}

// -------------------- Gemini --------------------

func newGeminiClient(ctx context.Context, cfg Config) (*genai.Client, error) {
	return genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  cfg.APIKey,
		Backend: genai.BackendGeminiAPI,
	})
}

func geminiGenerate(ctx context.Context, cfg Config) error {
	client, err := newGeminiClient(ctx, cfg)
	if err != nil {
		return fmt.Errorf("create gemini client: %w", err)
	}

	resp, err := client.Models.GenerateContent(
		ctx,
		cfg.Model,
		genai.Text(cfg.Prompt),
		nil,
	)
	if err != nil {
		return fmt.Errorf("gemini generate: %w", err)
	}

	fmt.Println(resp.Text())
	return nil
}

func geminiStream(ctx context.Context, cfg Config) error {
	client, err := newGeminiClient(ctx, cfg)
	if err != nil {
		return fmt.Errorf("create gemini client: %w", err)
	}

	for resp, err := range client.Models.GenerateContentStream(
		ctx,
		cfg.Model,
		genai.Text(cfg.Prompt),
		nil,
	) {
		if err != nil {
			return fmt.Errorf("gemini stream: %w", err)
		}

		if resp == nil {
			continue
		}

		for _, candidate := range resp.Candidates {
			if candidate == nil || candidate.Content == nil {
				continue
			}

			for _, part := range candidate.Content.Parts {
				if part != nil && part.Text != "" {
					fmt.Print(part.Text)
				}
			}
		}
	}

	fmt.Println()
	return nil
}
