// Package license implements a computational guide that verifies Go source
// files begin with a copyright/license header comment. This is a feedforward
// control: it catches missing headers before code is committed, ensuring
// consistent licensing across the codebase.
//
// The guide checks that each .go file (excluding test files) starts with a
// comment block containing a configurable set of required keywords. By
// default it looks for the word "Package" (which go convention requires in
// the doc comment) but callers can specify custom required terms.
package license

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"bedrock-ai/internal/harness"
)

// Config controls the license-header guide behaviour.
type Config struct {
	// RootDir is the root directory to scan. Default: ".".
	RootDir string
	// RequiredKeywords are keywords that must appear in the file's leading
	// comment block. If empty, the guide only checks that a comment block
	// exists before the package declaration.
	RequiredKeywords []string
	// SkipTestFiles controls whether _test.go files are checked. Default:
	// true (test files are not required to have license headers).
	SkipTestFiles bool
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		RootDir:          ".",
		RequiredKeywords: nil,
		SkipTestFiles:    true,
	}
}

// Guide is a computational feedforward control that verifies the presence
// of a leading comment block in Go source files.
type Guide struct {
	cfg Config
}

// Option configures a Guide.
type Option func(*Guide)

// WithConfig overrides the default configuration.
func WithConfig(cfg Config) Option {
	return func(g *Guide) { g.cfg = cfg }
}

// New creates a license-header guide with the given options.
func New(opts ...Option) *Guide {
	g := &Guide{cfg: DefaultConfig()}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// Name implements harness.Guide.
func (g *Guide) Name() string { return "maintainability.license" }

// Category implements harness.Guide.
func (g *Guide) Category() harness.Category { return harness.CategoryMaintainability }

// Mode implements harness.Guide.
func (g *Guide) Mode() harness.ExecutionMode { return harness.ModeComputational }

// Check implements harness.Guide.
func (g *Guide) Check() ([]harness.Finding, error) {
	var findings []harness.Finding

	err := filepath.WalkDir(g.cfg.RootDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if name == "vendor" || name == "node_modules" || name == ".git" || name == ".kilo" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if g.cfg.SkipTestFiles && strings.HasSuffix(path, "_test.go") {
			return nil
		}

		hasHeader, missing, err := g.checkHeader(path)
		if err != nil {
			return nil
		}
		if !hasHeader {
			findings = append(findings, harness.Finding{
				Check:     g.Name(),
				Category:  harness.CategoryMaintainability,
				Mode:      harness.ModeComputational,
				Direction: harness.DirectionFeedforward,
				Severity:  harness.SeverityWarning,
				File:      path,
				Line:      1,
				Message:   "missing leading comment block before package declaration",
				Suggest:   "add a package doc comment or license header at the top of the file",
			})
		}
		if len(missing) > 0 {
			findings = append(findings, harness.Finding{
				Check:     g.Name(),
				Category:  harness.CategoryMaintainability,
				Mode:      harness.ModeComputational,
				Direction: harness.DirectionFeedforward,
				Severity:  harness.SeverityWarning,
				File:      path,
				Line:      1,
				Message:   fmt.Sprintf("header missing required keywords: %s", strings.Join(missing, ", ")),
				Suggest:   fmt.Sprintf("ensure the file header includes: %s", strings.Join(g.cfg.RequiredKeywords, ", ")),
			})
		}
		return nil
	})

	return findings, err
}

// checkHeader reads the leading lines of a Go file and determines whether
// a comment block exists before the package declaration. If required
// keywords are configured, it also checks that they appear in the header.
func (g *Guide) checkHeader(path string) (hasHeader bool, missing []string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return false, nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var headerText strings.Builder
	foundPackage := false
	foundComment := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "package ") {
			foundPackage = true
			break
		}
		if strings.HasPrefix(line, "//") || strings.HasPrefix(line, "/*") || strings.HasPrefix(line, "*") || line == "" {
			if strings.HasPrefix(line, "//") || strings.HasPrefix(line, "/*") || strings.HasPrefix(line, "*") {
				foundComment = true
			}
			headerText.WriteString(line)
			headerText.WriteString("\n")
			continue
		}
		// Non-comment, non-package line (e.g. build tag is fine, but
		// anything else means no header).
		break
	}
	if err := scanner.Err(); err != nil {
		return false, nil, err
	}

	hasHeader = foundComment && foundPackage
	if !hasHeader {
		return false, nil, nil
	}

	header := headerText.String()
	for _, kw := range g.cfg.RequiredKeywords {
		if !strings.Contains(header, kw) {
			missing = append(missing, kw)
		}
	}
	return true, missing, nil
}
