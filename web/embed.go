// Package web contains the browser-facing source assets embedded in the AgentMesh binary.
package web

import "embed"

// Assets embeds Dashboard and Workflow Editor assets. Keep all HTML, CSS and
// JavaScript source under the repository-root web/ directory.
//
//go:embed dashboard workflow-editor
var Assets embed.FS
