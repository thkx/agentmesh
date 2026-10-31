package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

const (
	defaultServer  = "http://127.0.0.1:8080"
	defaultTimeout = 60 * time.Second
)

type rootOptions struct {
	server  string
	output  string
	timeout time.Duration
}

func Execute() error {
	cmd := NewRootCommand()
	return cmd.Execute()
}

func NewRootCommand() *cobra.Command {
	opts := &rootOptions{}

	cmd := &cobra.Command{
		Use:           "agentmesh-cli",
		Short:         "AgentMesh command-line client",
		Long:          "AgentMesh CLI - interact with an AgentMesh Runtime.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.PersistentFlags().StringVar(
		&opts.server,
		"server",
		defaultServer,
		"AgentMesh Runtime API address",
	)

	cmd.PersistentFlags().StringVar(
		&opts.output,
		"output",
		"table",
		"Output format: table or json",
	)

	cmd.PersistentFlags().DurationVar(
		&opts.timeout,
		"timeout",
		defaultTimeout,
		"HTTP request timeout",
	)

	if value := getenv("AGENTMESH_SERVER"); value != "" {
		opts.server = value
	}

	if value := getenv("AGENTMESH_OUTPUT"); value != "" {
		opts.output = value
	}

	cmd.AddCommand(newVersionCommand())
	cmd.AddCommand(newProviderCommand(opts))
	cmd.AddCommand(newModelCommand(opts))

	cmd.SetFlagErrorFunc(func(command *cobra.Command, err error) error {
		return fmt.Errorf("%w", err)
	})

	return cmd
}

func newClientFromOptions(opts *rootOptions) (*Client, error) {
	if opts.output != "table" && opts.output != "json" {
		return nil, fmt.Errorf(
			"invalid output format %q: expected table or json",
			opts.output,
		)
	}

	return NewClient(ClientConfig{
		BaseURL: opts.server,
		Timeout: opts.timeout,
	})
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print CLI version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("AgentMesh CLI")
			fmt.Println("Version: dev")
		},
	}
}

func getenv(key string) string {
	return lookupEnv(key)
}

func lookupEnv(key string) string {
	return os.Getenv(key)
}
