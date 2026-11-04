package cli

import (
	"context"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
)

type Provider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Status  string `json:"status"`
	BaseURL string `json:"base_url"`
}

type ProviderTestResult struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	LatencyMS int64  `json:"latency_ms"`
}

func newProviderCommand(opts *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provider",
		Short: "Manage model providers",
	}

	cmd.AddCommand(newProviderListCommand(opts))
	cmd.AddCommand(newProviderTestCommand(opts))

	return cmd
}

func newProviderListCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured providers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClientFromOptions(opts)
			if err != nil {
				return err
			}

			var response jsonList[Provider]

			if err := client.Get(
				cmd.Context(),
				"providers",
				&response,
			); err != nil {
				return err
			}

			providers := response.Items()

			if opts.output == "json" {
				return printJSON(providers)
			}

			if len(providers) == 0 {
				printMessage("No providers configured.")
				return nil
			}

			rows := make([][]string, 0, len(providers))

			for _, provider := range providers {
				rows = append(rows, []string{
					provider.ID,
					provider.Name,
					provider.Type,
					provider.Status,
					provider.BaseURL,
				})
			}

			printTable(
				[]string{"ID", "NAME", "TYPE", "STATUS", "BASE URL"},
				rows,
			)

			return nil
		},
	}
}

func newProviderTestCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "test PROVIDER_ID",
		Short: "Check provider runtime readiness",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClientFromOptions(opts)
			if err != nil {
				return err
			}

			providerID := args[0]

			var result ProviderTestResult

			path := fmt.Sprintf(
				"providers/%s/test",
				escapePathSegment(providerID),
			)

			if err := client.Post(
				cmd.Context(),
				path,
				nil,
				&result,
			); err != nil {
				return err
			}

			if opts.output == "json" {
				return printJSON(result)
			}

			status := "FAILED"
			if result.Success {
				status = "SUCCESS"
			}

			printTable(
				[]string{"PROVIDER", "STATUS", "LATENCY", "MESSAGE"},
				[][]string{{
					providerID,
					status,
					strconv.FormatInt(result.LatencyMS, 10) + " ms",
					result.Message,
				}},
			)

			if !result.Success {
				return fmt.Errorf("provider connectivity test failed")
			}

			return nil
		},
	}
}

// Keep this compile-time assertion close to the provider API types.
var _ context.Context
