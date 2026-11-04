package main

import (
	"fmt"
	"os"

	"github.com/thkx/agentmesh/internal/transport/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
