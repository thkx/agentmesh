package config

import (
	"fmt"
	"os"
	"strings"
)

// ResolveAPIToken returns the API token from AGENTMESH_API_TOKEN or a protected
// local file. Environment configuration takes precedence for automation.
func ResolveAPIToken(path string) (string, error) {
	if token := strings.TrimSpace(os.Getenv("AGENTMESH_API_TOKEN")); token != "" {
		if len(token) < 32 {
			return "", fmt.Errorf("AGENTMESH_API_TOKEN must contain at least 32 characters")
		}
		return token, nil
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("stat configured token file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("configured API token path must be a regular file, not a symlink")
	}
	if info.Mode().Perm()&0077 != 0 {
		return "", fmt.Errorf("configured API token file permissions must be 0600 or stricter")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read configured token file: %w", err)
	}
	if len(data) > 4096 {
		return "", fmt.Errorf("configured API token file exceeds 4096 bytes")
	}
	token := strings.TrimSpace(string(data))
	if len(token) < 32 {
		return "", fmt.Errorf("configured API token must contain at least 32 characters")
	}
	if strings.ContainsAny(token, "\r\n\x00") {
		return "", fmt.Errorf("configured API token must be a single line")
	}
	return token, nil
}
