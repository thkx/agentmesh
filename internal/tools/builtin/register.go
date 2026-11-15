package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	dockersandbox "github.com/thkx/agentmesh/internal/workspace/sandbox/docker"

	dom "github.com/thkx/agentmesh/internal/domain/tool"
	"github.com/thkx/agentmesh/internal/tools"
)

type Config struct {
	MaxFileBytes          int64
	MaxOutputBytes        int
	AllowedCommands       []string
	AllowedHTTPDomains    []string
	SandboxImage          string
	SandboxNetworkEnabled bool
	CommandTimeout        time.Duration
}
type inputPath struct {
	Path string `json:"path"`
}
type writeInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}
type shellInput struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}
type gitInput struct {
	Args []string `json:"args"`
}
type httpInput struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}
type result struct {
	Path     string   `json:"path,omitempty"`
	Content  string   `json:"content,omitempty"`
	Entries  []string `json:"entries,omitempty"`
	Stdout   string   `json:"stdout,omitempty"`
	Stderr   string   `json:"stderr,omitempty"`
	ExitCode int      `json:"exit_code,omitempty"`
}

func Register(r *tools.Registry, cfg Config) error {
	if cfg.MaxFileBytes <= 0 {
		cfg.MaxFileBytes = 2 << 20
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = 1 << 20
	}
	if cfg.CommandTimeout <= 0 {
		cfg.CommandTimeout = 30 * time.Second
	}
	specs := []tools.Tool{
		{Spec: dom.Spec{Name: "file.read", Description: "Read a UTF-8 file inside the active workspace", Permission: dom.PermissionRead, Timeout: 10 * time.Second, Input: dom.Schema{Type: "object", Properties: map[string]dom.Property{"path": {Type: "string"}}, Required: []string{"path"}}}, Handler: tools.HandlerFunc(func(ctx context.Context, b json.RawMessage) (any, error) {
			var in inputPath
			if err := json.Unmarshal(b, &in); err != nil {
				return nil, err
			}
			p, err := resolve(cfgRoot(ctx), in.Path, false)
			if err != nil {
				return nil, err
			}
			f, err := os.Open(p)
			if err != nil {
				return nil, err
			}
			defer f.Close()
			data, err := io.ReadAll(io.LimitReader(f, cfg.MaxFileBytes+1))
			if err != nil {
				return nil, err
			}
			if int64(len(data)) > cfg.MaxFileBytes {
				return nil, errors.New("file exceeds read limit")
			}
			return result{Path: in.Path, Content: string(data)}, nil
		})},
		{Spec: dom.Spec{Name: "file.write", Description: "Create or replace a UTF-8 file inside the active workspace", Permission: dom.PermissionWrite, Timeout: 10 * time.Second, Input: dom.Schema{Type: "object", Properties: map[string]dom.Property{"path": {Type: "string"}, "content": {Type: "string"}}, Required: []string{"path", "content"}}}, Handler: tools.HandlerFunc(func(ctx context.Context, b json.RawMessage) (any, error) {
			var in writeInput
			if err := json.Unmarshal(b, &in); err != nil {
				return nil, err
			}
			if strings.TrimSpace(in.Path) == "" || filepath.Clean(in.Path) == "." {
				return nil, errors.New("file path must name a file")
			}
			if !utf8.ValidString(in.Content) {
				return nil, errors.New("content must be valid UTF-8")
			}
			if int64(len(in.Content)) > cfg.MaxFileBytes {
				return nil, errors.New("file exceeds write limit")
			}
			p, err := resolve(cfgRoot(ctx), in.Path, true)
			if err != nil {
				return nil, err
			}

			tmp, err := os.CreateTemp(filepath.Dir(p), ".agentmesh-*")
			if err != nil {
				return nil, err
			}
			name := tmp.Name()
			defer os.Remove(name)
			if err = tmp.Chmod(0600); err == nil {
				_, err = tmp.WriteString(in.Content)
			}
			if closeErr := tmp.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				return nil, err
			}
			if err = os.Rename(name, p); err != nil {
				return nil, err
			}
			return result{Path: in.Path}, nil
		})},
		{Spec: dom.Spec{Name: "file.list", Description: "List entries in a directory inside the active workspace", Permission: dom.PermissionRead, Timeout: 10 * time.Second, Input: dom.Schema{Type: "object", Properties: map[string]dom.Property{"path": {Type: "string"}}}}, Handler: tools.HandlerFunc(func(ctx context.Context, b json.RawMessage) (any, error) {
			var in inputPath
			_ = json.Unmarshal(b, &in)
			if in.Path == "" {
				in.Path = "."
			}
			p, err := resolve(cfgRoot(ctx), in.Path, false)
			if err != nil {
				return nil, err
			}
			entries, err := os.ReadDir(p)
			if err != nil {
				return nil, err
			}
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			return result{Path: in.Path, Entries: names}, nil
		})},
		{Spec: dom.Spec{Name: "shell.exec", Description: "Run an explicitly allowlisted executable directly (without a shell) in the active workspace", Permission: dom.PermissionExecute, Timeout: cfg.CommandTimeout, Input: dom.Schema{Type: "object", Properties: map[string]dom.Property{"command": {Type: "string"}, "args": {Type: "array"}}, Required: []string{"command"}}}, Handler: tools.HandlerFunc(func(ctx context.Context, b json.RawMessage) (any, error) {
			var in shellInput
			if err := json.Unmarshal(b, &in); err != nil {
				return nil, err
			}
			if !allowed(cfg.AllowedCommands, in.Command) {
				return nil, errors.New("executable is not allowlisted")
			}
			root := cfgRoot(ctx)
			if _, err := os.Stat(root); err != nil {
				return nil, err
			}
			if strings.TrimSpace(cfg.SandboxImage) == "" {
				return nil, errors.New("shell execution is disabled: configure agent.tools.sandbox_image")
			}
			runner := dockersandbox.Runner{Image: cfg.SandboxImage, NetworkEnabled: cfg.SandboxNetworkEnabled}
			out, err := runner.Run(ctx, root, in.Command, in.Args...)
			if len(out) > cfg.MaxOutputBytes {
				return nil, errors.New("sandbox output exceeds limit")
			}
			return map[string]any{"output": string(out)}, err
		})},
		{Spec: dom.Spec{Name: "http.request", Description: "Make an HTTPS request to an explicitly allowlisted public host", Permission: dom.PermissionExecute, Timeout: cfg.CommandTimeout, Input: dom.Schema{Type: "object", Properties: map[string]dom.Property{"method": {Type: "string", Enum: []string{"GET", "POST", "PUT", "PATCH", "DELETE"}}, "url": {Type: "string"}, "headers": {Type: "object"}, "body": {Type: "string"}}, Required: []string{"method", "url"}}}, Handler: tools.HandlerFunc(func(ctx context.Context, b json.RawMessage) (any, error) {
			var in httpInput
			if err := json.Unmarshal(b, &in); err != nil {
				return nil, err
			}
			parsed, err := url.ParseRequestURI(in.URL)
			if err != nil || parsed.Scheme != "https" || parsed.User != nil {
				return nil, errors.New("only HTTPS URLs without userinfo are allowed")
			}
			host := strings.ToLower(parsed.Hostname())
			if !hostAllowed(cfg.AllowedHTTPDomains, host) {
				return nil, errors.New("HTTP host is not allowlisted")
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, errors.New("HTTP host did not resolve")
			}
			for _, ip := range ips {
				if ip.IP.IsPrivate() || ip.IP.IsLoopback() || ip.IP.IsLinkLocalUnicast() || ip.IP.IsLinkLocalMulticast() || ip.IP.IsUnspecified() {
					return nil, errors.New("private or local HTTP destinations are blocked")
				}
			}
			transport := &http.Transport{DialContext: func(dctx context.Context, network, address string) (net.Conn, error) {
				_, port, splitErr := net.SplitHostPort(address)
				if splitErr != nil {
					return nil, splitErr
				}
				for _, ip := range ips {
					conn, dialErr := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(dctx, network, net.JoinHostPort(ip.IP.String(), port))
					if dialErr == nil {
						return conn, nil
					}
				}
				return nil, errors.New("unable to connect to allowlisted host")
			}}
			client := &http.Client{Transport: transport, Timeout: cfg.CommandTimeout, CheckRedirect: func(req *http.Request, via []*http.Request) error { return errors.New("HTTP redirects are disabled") }}
			defer transport.CloseIdleConnections()
			req, err := http.NewRequestWithContext(ctx, in.Method, in.URL, strings.NewReader(in.Body))
			if err != nil {
				return nil, err
			}
			for k, v := range in.Headers {
				if strings.EqualFold(k, "host") || strings.EqualFold(k, "authorization") || strings.ContainsAny(k, "\r\n") || strings.ContainsAny(v, "\r\n") {
					return nil, errors.New("unsafe HTTP header")
				}
				req.Header.Set(k, v)
			}
			resp, err := client.Do(req)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			data, err := io.ReadAll(io.LimitReader(resp.Body, int64(cfg.MaxOutputBytes)+1))
			if err != nil {
				return nil, err
			}
			if len(data) > cfg.MaxOutputBytes {
				return nil, errors.New("HTTP response exceeds output limit")
			}
			return map[string]any{"status_code": resp.StatusCode, "headers": resp.Header, "body": string(data)}, nil
		})},
		{Spec: dom.Spec{Name: "git.run", Description: "Run a safe Git inspection command in the active workspace", Permission: dom.PermissionExecute, Timeout: cfg.CommandTimeout, Input: dom.Schema{Type: "object", Properties: map[string]dom.Property{"args": {Type: "array"}}, Required: []string{"args"}}}, Handler: tools.HandlerFunc(func(ctx context.Context, b json.RawMessage) (any, error) {
			var in gitInput
			if err := json.Unmarshal(b, &in); err != nil {
				return nil, err
			}
			if !safeGitArgs(in.Args) {
				return nil, errors.New("git subcommand is not permitted")
			}
			root := cfgRoot(ctx)
			cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "diff.external=", "-c", "core.pager=cat", "-C", root}, in.Args...)...)
			cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_PAGER=cat", "GIT_EXTERNAL_DIFF=")
			return run(cmd, cfg.MaxOutputBytes)
		})},
	}
	if strings.TrimSpace(cfg.SandboxImage) != "" {
		runner := dockersandbox.Runner{Image: cfg.SandboxImage, NetworkEnabled: cfg.SandboxNetworkEnabled}
		for _, item := range []struct {
			name string
			args []string
			desc string
		}{{"go.test", []string{"test", "./..."}, "Run go test ./... in an isolated Docker sandbox"}, {"go.build", []string{"build", "./..."}, "Run go build ./... in an isolated Docker sandbox"}, {"go.vet", []string{"vet", "./..."}, "Run go vet ./... in an isolated Docker sandbox"}, {"go.fmt", []string{"fmt", "./..."}, "Run go fmt ./... in an isolated Docker sandbox"}} {
			item := item
			specs = append(specs, tools.Tool{Spec: dom.Spec{Name: item.name, Description: item.desc, Permission: dom.PermissionExecute, Timeout: cfg.CommandTimeout, Input: dom.Schema{Type: "object", Properties: map[string]dom.Property{}, AdditionalProperties: false}}, Handler: tools.HandlerFunc(func(ctx context.Context, _ json.RawMessage) (any, error) {
				root := cfgRoot(ctx)
				out, err := runner.Run(ctx, root, "go", item.args...)
				if len(out) > cfg.MaxOutputBytes {
					return nil, errors.New("sandbox output exceeds limit")
				}
				return map[string]any{"output": string(out)}, err
			})})
		}
	}
	for _, t := range specs {
		if err := r.Register(t); err != nil {
			return err
		}
	}
	return nil
}

func cfgRoot(ctx context.Context) string { return tools.WorkspaceFromContext(ctx) }
func resolve(root, rel string, allowMissing bool) (string, error) {
	if root == "" {
		return "", errors.New("workspace is required")
	}
	if filepath.IsAbs(rel) {
		return "", errors.New("absolute paths are not allowed")
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes workspace")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(rootAbs, clean)
	parent := candidate
	if allowMissing {
		parent = filepath.Dir(candidate)
	}
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", err
	}
	base, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", err
	}
	relPath, err := filepath.Rel(base, resolved)
	if err != nil || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes workspace")
	}
	if !allowMissing {
		candidate, err = filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", err
		}
	} else {
		candidate = filepath.Join(resolved, filepath.Base(candidate))
	}
	return candidate, nil
}
func allowed(list []string, cmd string) bool {
	for _, v := range list {
		if v == cmd || filepath.Base(v) == cmd {
			return true
		}
	}
	return false
}
func safeGitArgs(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "status", "diff", "log", "show", "rev-parse", "ls-files":
	default:
		return false
	}
	for _, a := range args {
		lower := strings.ToLower(a)
		if lower == "--ext-diff" || lower == "--textconv" || lower == "--no-index" || strings.HasPrefix(lower, "--output") || lower == "-c" || strings.HasPrefix(lower, "--exec-path") || strings.HasPrefix(lower, "--git-dir") || strings.HasPrefix(lower, "--work-tree") {
			return false
		}
	}
	return true
}
func run(cmd *exec.Cmd, max int) (any, error) {
	var stdout, stderr limitedBuffer
	stdout.max = max
	stderr.max = max
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			return nil, err
		}
	}
	if stdout.exceeded || stderr.exceeded {
		return nil, errors.New("command output exceeds limit")
	}
	return result{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: code}, nil
}

type limitedBuffer struct {
	strings.Builder
	max      int
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := b.max - b.Len()
	if remaining <= 0 {
		b.exceeded = true
		return len(p), nil
	}
	if len(p) > remaining {
		b.exceeded = true
		_, _ = b.Builder.Write(p[:remaining])
		return len(p), nil
	}
	return b.Builder.Write(p)
}

func hostAllowed(allowlist []string, host string) bool {
	for _, raw := range allowlist {
		d := strings.ToLower(strings.TrimSpace(raw))
		d = strings.TrimPrefix(d, ".")
		if d != "" && (host == d || strings.HasSuffix(host, "."+d)) {
			return true
		}
	}
	return false
}
