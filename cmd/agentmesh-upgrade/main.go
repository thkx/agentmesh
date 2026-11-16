// Command agentmesh-upgrade verifies signed release packages and applies them offline.
package main

import (
	"context"
	"crypto/ed25519"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/thkx/agentmesh/internal/operations/upgrade"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "check":
		fs := flag.NewFlagSet("check", flag.ExitOnError)
		manifestURL := fs.String("manifest-url", "", "HTTPS URL for signed release manifest")
		signatureURL := fs.String("signature-url", "", "HTTPS URL for Ed25519 signature")
		key := fs.String("public-key", "", "trusted Ed25519 public key (32 raw bytes)")
		_ = fs.Parse(os.Args[2:])
		if *manifestURL == "" || *signatureURL == "" || *key == "" {
			fatal("manifest-url, signature-url and public-key are required")
		}
		pub, err := os.ReadFile(*key)
		if err != nil {
			fatal(err.Error())
		}
		m, err := upgrade.CheckRelease(context.Background(), *manifestURL, *signatureURL, ed25519.PublicKey(pub))
		if err != nil {
			fatal(err.Error())
		}
		fmt.Printf("Signed release available: %s (%s/%s), published %s\n", m.Version, m.OS, m.Arch, m.PublishedAt.UTC().Format(time.RFC3339))
	case "stage":
		fs := flag.NewFlagSet("stage", flag.ExitOnError)
		manifest := fs.String("manifest", "", "signed release manifest JSON")
		signature := fs.String("signature", "", "base64 Ed25519 signature")
		binary := fs.String("binary", "", "release executable")
		key := fs.String("public-key", "", "trusted Ed25519 public key (32 raw bytes)")
		output := fs.String("output", "", "new staging directory")
		_ = fs.Parse(os.Args[2:])
		if *manifest == "" || *signature == "" || *binary == "" || *key == "" || *output == "" {
			fatal("manifest, signature, binary, public-key and output are required")
		}
		m, err := upgrade.Stage(*manifest, *signature, *binary, *key, *output)
		if err != nil {
			fatal(err.Error())
		}
		fmt.Printf("Verified release %s for %s/%s staged at %s\n", m.Version, m.OS, m.Arch, *output)
	case "verify":
		fs := flag.NewFlagSet("verify", flag.ExitOnError)
		stage := fs.String("stage", "", "staging directory")
		key := fs.String("public-key", "", "trusted Ed25519 public key (32 raw bytes)")
		_ = fs.Parse(os.Args[2:])
		if *stage == "" || *key == "" {
			fatal("stage and public-key are required")
		}
		raw, err := os.ReadFile(filepath.Join(*stage, "manifest.json"))
		if err != nil {
			fatal(err.Error())
		}
		sig, err := os.ReadFile(filepath.Join(*stage, "signature.ed25519"))
		if err != nil {
			fatal(err.Error())
		}
		pub, err := os.ReadFile(*key)
		if err != nil {
			fatal(err.Error())
		}
		m, err := upgrade.VerifyRelease(raw, string(sig), ed25519.PublicKey(pub), filepath.Join(*stage, "agentmesh"))
		if err != nil {
			fatal(err.Error())
		}
		fmt.Printf("Release verified: %s (%s/%s)\n", m.Version, m.OS, m.Arch)
	case "apply":
		fs := flag.NewFlagSet("apply", flag.ExitOnError)
		stage := fs.String("stage", "", "verified staging directory")
		key := fs.String("public-key", "", "trusted Ed25519 public key")
		target := fs.String("target", "", "existing AgentMesh executable path")
		backups := fs.String("backup-dir", "", "rollback backup directory")
		stopped := fs.Bool("confirm-stopped", false, "confirm the AgentMesh process has been stopped")
		_ = fs.Parse(os.Args[2:])
		if *stage == "" || *key == "" || *target == "" || *backups == "" {
			fatal("stage, public-key, target and backup-dir are required")
		}
		backup, err := upgrade.ApplyOffline(*stage, *target, *backups, *key, *stopped)
		if err != nil {
			fatal(err.Error())
		}
		fmt.Printf("Upgrade applied. Previous executable preserved at %s\n", backup)
	default:
		usage()
		os.Exit(2)
	}
}
func usage() {
	fmt.Fprintln(os.Stderr, "Usage: agentmesh-upgrade <check|stage|verify|apply> [flags]\nThe release manifest must be signed by a trusted Ed25519 key. Apply is offline and requires --confirm-stopped.")
}
func fatal(message string) { fmt.Fprintln(os.Stderr, "agentmesh-upgrade:", message); os.Exit(1) }
