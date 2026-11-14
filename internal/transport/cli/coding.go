package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/thkx/agentmesh/internal/coding"
)

func newCodingCommand(opts *rootOptions) *cobra.Command {
	root := &cobra.Command{Use: "coding", Short: "Manage isolated Coding Agent tasks"}
	create := &cobra.Command{Use: "create", Short: "Create a coding task", RunE: func(cmd *cobra.Command, args []string) error {
		id, _ := cmd.Flags().GetString("id")
		repo, _ := cmd.Flags().GetString("repo")
		model, _ := cmd.Flags().GetString("model")
		prompt, _ := cmd.Flags().GetString("prompt")
		title, _ := cmd.Flags().GetString("title")
		testProgram, _ := cmd.Flags().GetString("test-program")
		testArgs, _ := cmd.Flags().GetStringSlice("test-arg")
		maxRepairs, _ := cmd.Flags().GetInt("max-repairs")
		timeoutSeconds, _ := cmd.Flags().GetInt("task-timeout")
		client, e := newClientFromOptions(opts)
		if e != nil {
			return e
		}
		var task coding.Task
		e = client.Post(context.Background(), "coding/tasks", coding.CreateRequest{ID: id, Title: title, RepositoryPath: repo, ModelID: model, Prompt: prompt, TestCommand: coding.Command{Program: testProgram, Args: testArgs}, MaxRepairAttempts: maxRepairs, TimeoutSeconds: timeoutSeconds}, &task)
		if e != nil {
			return e
		}
		return printJSONOrValue(opts.output, task)
	}}
	create.Flags().String("id", "", "Unique task ID")
	_ = create.MarkFlagRequired("id")
	create.Flags().String("repo", "", "Local Git repository path")
	_ = create.MarkFlagRequired("repo")
	create.Flags().String("model", "", "AgentMesh model ID")
	_ = create.MarkFlagRequired("model")
	create.Flags().String("prompt", "", "Coding task prompt")
	_ = create.MarkFlagRequired("prompt")
	create.Flags().String("title", "", "Short task title")
	create.Flags().String("test-program", "", "Optional test executable (go, npm, pytest, cargo, etc.)")
	create.Flags().StringSlice("test-arg", nil, "Test argument; may be repeated")
	create.Flags().Int("max-repairs", 2, "Maximum self-repair attempts (0-5; zero uses default)")
	create.Flags().Int("task-timeout", 1800, "Task timeout in seconds")
	run := &cobra.Command{Use: "run ID", Args: cobra.ExactArgs(1), Short: "Execute a coding task", RunE: func(cmd *cobra.Command, args []string) error {
		client, e := newClientFromOptions(opts)
		if e != nil {
			return e
		}
		ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
		defer cancel()
		var task coding.Task
		if e = client.Post(ctx, "coding/tasks/"+args[0]+"/run", nil, &task); e != nil {
			return e
		}
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			if e = client.Get(ctx, "coding/tasks/"+args[0], &task); e != nil {
				return e
			}
			if task.Status == coding.StatusSucceeded {
				return printJSONOrValue(opts.output, task)
			}
			if task.Status == coding.StatusFailed || task.Status == coding.StatusCanceled {
				return fmt.Errorf("coding task %s: %s", task.Status, task.Error)
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("timed out waiting for coding task: %w", ctx.Err())
			case <-ticker.C:
			}
		}
	}}
	list := &cobra.Command{Use: "list", Short: "List coding tasks", RunE: func(cmd *cobra.Command, args []string) error {
		client, e := newClientFromOptions(opts)
		if e != nil {
			return e
		}
		var tasks []coding.Task
		if e = client.Get(context.Background(), "coding/tasks", &tasks); e != nil {
			return e
		}
		return printJSONOrValue(opts.output, tasks)
	}}
	root.AddCommand(create, run, list)
	return root
}
func printJSONOrValue(format string, v any) error {
	if format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	b, e := json.MarshalIndent(v, "", "  ")
	if e == nil {
		fmt.Println(string(b))
	}
	return e
}
