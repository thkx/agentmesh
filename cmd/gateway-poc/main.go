package main

import (
	"context"
	"fmt"
	"log"

	"github.com/thkx/agentmesh/internal/domain/provider"
	"github.com/thkx/agentmesh/internal/modelgateway"

	openaiadapter "github.com/thkx/agentmesh/internal/modelgateway/adapters/openai"
)

func main() {
	ctx := context.Background()

	// 1. Register model configuration.
	resolver, err := modelgateway.NewStaticResolver(
		provider.Model{
			ID:                "openai-gpt5",
			ProviderID:        provider.IDOpenAI,
			Name:              "gpt-5",
			DisplayName:       "GPT-5",
			SupportsStreaming: true,
			Enabled:           true,
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	// 2. Register provider adapters.
	registry := modelgateway.NewRegistry()

	// TODO: Register the actual OpenAI adapter in M1.0.2.
	openAIAdapter, err := openaiadapter.New("openAIAPIKey")
	if err != nil {
		log.Panicf("initialize openai adapter: %v", err)
	}

	if err := registry.Register(provider.IDOpenAI, openAIAdapter); err != nil {
		log.Panicf("register openai adapter: %v", err)
	}

	// 3. Create Gateway.
	gateway, err := modelgateway.NewGateway(registry, resolver)
	if err != nil {
		log.Fatal(err)
	}

	// 4. Generate.
	response, err := gateway.Generate(ctx, modelgateway.GenerateRequest{
		ModelID: "openai-gpt5",
		Messages: []modelgateway.Message{
			{
				Role:    modelgateway.RoleUser,
				Content: "Explain Go context.",
			},
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(response.Content)
	fmt.Println("Usage:", response.Usage.TotalTokens)
}
