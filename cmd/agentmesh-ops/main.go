// Command agentmesh-ops provides authenticated local service status and shutdown controls.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	base := fs.String("url", "http://127.0.0.1:8080", "AgentMesh base URL")
	timeout := fs.Duration("timeout", 5*time.Second, "request timeout")
	_ = fs.Parse(os.Args[2:])
	if os.Args[1] != "status" && os.Args[1] != "shutdown" {
		usage()
		os.Exit(2)
	}
	token, tokenErr := readAPIToken()
	if tokenErr != nil {
		fatal(tokenErr.Error())
	}
	if token == "" {
		fatal("API token is required via AGENTMESH_API_TOKEN or stdin")
	}
	parsedURL, parseErr := url.Parse(*base)
	if parseErr != nil || parsedURL.Host == "" || parsedURL.User != nil || (parsedURL.Scheme != "https" && parsedURL.Scheme != "http") {
		fatal("url must be an HTTPS or loopback HTTP base URL")
	}
	if parsedURL.Scheme == "http" {
		host := parsedURL.Hostname()
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			fatal("API token will only be sent over HTTPS to remote hosts")
		}
	}
	endpoint := strings.TrimRight(*base, "/")
	method := http.MethodGet
	switch os.Args[1] {
	case "status":
		endpoint += "/api/v1/operations/health"
	case "shutdown":
		method = http.MethodPost
		endpoint += "/api/v1/operations/shutdown"
	default:
		usage()
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		fatal(err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: *timeout}
	resp, err := client.Do(req)
	if err != nil {
		fatal("request failed: " + err.Error())
	}
	defer resp.Body.Close()
	var payload map[string]any
	if err = json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		fatal("invalid operations response")
	}
	encoded, _ := json.MarshalIndent(payload, "", "  ")
	fmt.Println(string(encoded))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		os.Exit(1)
	}
}

// readAPIToken prefers the optional environment override. Otherwise it reads
// the token from stdin, allowing callers to pipe a hidden interactive prompt.
func readAPIToken() (string, error) {
	if value := strings.TrimSpace(os.Getenv("AGENTMESH_API_TOKEN")); value != "" {
		return value, nil
	}
	value, err := io.ReadAll(io.LimitReader(os.Stdin, 4097))
	if err != nil {
		return "", fmt.Errorf("read API token from stdin: %w", err)
	}
	if len(value) > 4096 {
		return "", fmt.Errorf("API token is too long")
	}
	return strings.TrimSpace(string(value)), nil
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: agentmesh-ops <status|shutdown> [--url http://127.0.0.1:8080] [--timeout 5s]\nProvide the API token via AGENTMESH_API_TOKEN or stdin.")
}
func fatal(message string) { fmt.Fprintln(os.Stderr, "agentmesh-ops:", message); os.Exit(1) }
