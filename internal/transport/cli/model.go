package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

type Model struct {
	ID            string `json:"id"`
	ProviderID    string `json:"provider_id"`
	Name          string `json:"name"`
	DisplayName   string `json:"display_name"`
	Status        string `json:"status"`
	ContextWindow int64  `json:"context_window"`
	MaxOutput     int64  `json:"max_output_tokens"`
}

type GenerateMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type GenerateRequest struct {
	Messages    []GenerateMessage `json:"messages"`
	MaxTokens   int64             `json:"max_tokens,omitempty"`
	Temperature *float64          `json:"temperature,omitempty"`
	TopP        *float64          `json:"top_p,omitempty"`
}

type GenerateUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

type GenerateResponse struct {
	ID           string        `json:"id"`
	Model        string        `json:"model"`
	OutputText   string        `json:"output_text"`
	Content      string        `json:"content"`
	FinishReason string        `json:"finish_reason"`
	Usage        GenerateUsage `json:"usage"`
}

func (r GenerateResponse) Text() string {
	if r.OutputText != "" {
		return r.OutputText
	}

	return r.Content
}

func newModelCommand(opts *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "model",
		Short: "Manage and invoke models",
	}

	cmd.AddCommand(newModelListCommand(opts))
	cmd.AddCommand(newModelGetCommand(opts))
	cmd.AddCommand(newModelGenerateCommand(opts))
	cmd.AddCommand(newModelTestCommand(opts))

	return cmd
}

func newModelListCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available models",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClientFromOptions(opts)
			if err != nil {
				return err
			}

			var response jsonList[Model]

			if err := client.Get(
				cmd.Context(),
				"models",
				&response,
			); err != nil {
				return err
			}

			models := response.Items()

			if opts.output == "json" {
				return printJSON(models)
			}

			if len(models) == 0 {
				printMessage("No models configured.")
				return nil
			}

			rows := make([][]string, 0, len(models))

			for _, model := range models {
				name := model.DisplayName
				if name == "" {
					name = model.Name
				}

				rows = append(rows, []string{
					model.ID,
					name,
					model.ProviderID,
					model.Status,
					strconv.FormatInt(model.ContextWindow, 10),
				})
			}

			printTable(
				[]string{
					"ID",
					"NAME",
					"PROVIDER",
					"STATUS",
					"CONTEXT WINDOW",
				},
				rows,
			)

			return nil
		},
	}
}

func newModelGetCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "get MODEL_ID",
		Short: "Show model details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClientFromOptions(opts)
			if err != nil {
				return err
			}

			var model Model

			path := fmt.Sprintf(
				"models/%s",
				escapePathSegment(args[0]),
			)

			if err := client.Get(
				cmd.Context(),
				path,
				&model,
			); err != nil {
				return err
			}

			if opts.output == "json" {
				return printJSON(model)
			}

			printTable(
				[]string{"FIELD", "VALUE"},
				[][]string{
					{"ID", model.ID},
					{"NAME", model.Name},
					{"DISPLAY NAME", model.DisplayName},
					{"PROVIDER", model.ProviderID},
					{"STATUS", model.Status},
					{
						"CONTEXT WINDOW",
						strconv.FormatInt(model.ContextWindow, 10),
					},
					{
						"MAX OUTPUT TOKENS",
						strconv.FormatInt(model.MaxOutput, 10),
					},
				},
			)

			return nil
		},
	}
}

func newModelGenerateCommand(opts *rootOptions) *cobra.Command {
	var (
		prompt      string
		maxTokens   int64
		temperature float64
		topP        float64
	)

	cmd := &cobra.Command{
		Use:   "generate MODEL_ID",
		Short: "Generate text using a model",
		Long: `Generate text using a configured model.

Example:
  agentmesh-cli model generate gpt-4.1 --prompt "Explain Go interfaces"
`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(prompt) == "" {
				return fmt.Errorf("--prompt is required")
			}

			if maxTokens < 0 {
				return fmt.Errorf("--max-tokens cannot be negative")
			}

			if temperature < 0 || temperature > 2 {
				return fmt.Errorf("--temperature must be between 0 and 2")
			}

			if topP < 0 || topP > 1 {
				return fmt.Errorf("--top-p must be between 0 and 1")
			}

			client, err := newClientFromOptions(opts)
			if err != nil {
				return err
			}

			request := GenerateRequest{
				Messages: []GenerateMessage{
					{
						Role:    "user",
						Content: prompt,
					},
				},
			}

			if cmd.Flags().Changed("max-tokens") {
				request.MaxTokens = maxTokens
			}

			if cmd.Flags().Changed("temperature") {
				request.Temperature = &temperature
			}

			if cmd.Flags().Changed("top-p") {
				request.TopP = &topP
			}

			var response GenerateResponse

			path := fmt.Sprintf(
				"models/%s/generate",
				escapePathSegment(args[0]),
			)

			if err := client.Post(
				cmd.Context(),
				path,
				request,
				&response,
			); err != nil {
				return err
			}

			if opts.output == "json" {
				return printJSON(response)
			}

			fmt.Println(response.Text())

			if response.Usage.TotalTokens > 0 {
				fmt.Println()
				fmt.Printf(
					"Tokens: input=%d output=%d total=%d\n",
					response.Usage.InputTokens,
					response.Usage.OutputTokens,
					response.Usage.TotalTokens,
				)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(
		&prompt,
		"prompt",
		"",
		"Input prompt",
	)

	cmd.Flags().Int64Var(
		&maxTokens,
		"max-tokens",
		0,
		"Maximum output tokens",
	)

	cmd.Flags().Float64Var(
		&temperature,
		"temperature",
		0,
		"Sampling temperature (0-2)",
	)

	cmd.Flags().Float64Var(
		&topP,
		"top-p",
		0,
		"Top-p sampling value (0-1)",
	)

	return cmd
}

func newModelTestCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "test MODEL_ID",
		Short: "Test model availability",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClientFromOptions(opts)
			if err != nil {
				return err
			}

			request := GenerateRequest{
				Messages: []GenerateMessage{
					{
						Role:    "user",
						Content: "Reply with exactly: AgentMesh OK",
					},
				},
				MaxTokens: 32,
			}

			var response GenerateResponse

			path := fmt.Sprintf(
				"models/%s/generate",
				escapePathSegment(args[0]),
			)

			if err := client.Post(
				cmd.Context(),
				path,
				request,
				&response,
			); err != nil {
				return err
			}

			if strings.TrimSpace(response.Text()) == "" {
				return fmt.Errorf("model returned an empty response")
			}

			if opts.output == "json" {
				return printJSON(response)
			}

			fmt.Printf("Model: %s\n", args[0])
			fmt.Println("Status: SUCCESS")
			fmt.Printf("Response: %s\n", response.Text())

			return nil
		},
	}
}
