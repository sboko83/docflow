// Package templates exposes the Markdown templates embedded in the docflow
// binary.
package templates

import "embed"

// Files contains the templates shipped with docflow.
//
//go:embed *.md
var Files embed.FS
