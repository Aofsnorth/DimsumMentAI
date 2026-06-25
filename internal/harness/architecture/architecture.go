// Package architecture implements architecture-fitness functions for the
// DimsumMentAI project. These are computational sensors that enforce module
// boundaries and dependency-direction rules by parsing Go import statements.
//
// The rules encode the intended layering:
//
//	cmd/                 → may import anything in internal/
//	internal/harness/    → may not import other internal/ packages (it is the
//	                       meta-layer that observes the rest)
//	internal/ai/         → may not import internal/bot (AI is a leaf service)
//	internal/config/     → may not import internal/bot or internal/ai
//	internal/event/      → may not import internal/bot or internal/ai
//	internal/bot/        → may import internal/ai, internal/config,
//	                       internal/event, internal/handler
//	internal/handler/    → may import internal/event, internal/config
//	internal/bot/action/ → may import internal/bot (action dispatch)
//
// Violations produce error-severity findings with a suggested fix so that
// coding agents can self-correct.
package architecture

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"bedrock-ai/internal/harness"
)

// DependencyRule defines a forbidden import from one package to another.
type DependencyRule struct {
	// Package is the internal package path prefix that must not contain the
	// Forbidden import prefix.
	Package    string
	Forbidden  string
	Suggestion string
}

// DefaultRules returns the dependency rules that encode the project's
// intended layering. These can be extended or overridden by callers.
func DefaultRules(moduleName string) []DependencyRule {
	mi := moduleName + "/internal/"
	return []DependencyRule{
		{mi + "harness", mi + "bot", "harness must not depend on bot — it is the meta-layer"},
		{mi + "harness", mi + "ai", "harness must not depend on ai — it is the meta-layer"},
		{mi + "harness", mi + "handler", "harness must not depend on handler — it is the meta-layer"},
		{mi + "ai", mi + "bot", "ai must not depend on bot — AI is a leaf service layer"},
		{mi + "config", mi + "bot", "config must not depend on bot — config is a leaf"},
		{mi + "config", mi + "ai", "config must not depend on ai — config is a leaf"},
		{mi + "event", mi + "bot", "event must not depend on bot — event is a leaf"},
		{mi + "event", mi + "ai", "event must not depend on ai — event is a leaf"},
	}
}

// Sensor is a computational architecture-fitness sensor. It scans Go source
// files for import-path violations against the configured rules.
type Sensor struct {
	moduleName string
	rootDir    string
	rules      []DependencyRule
}

// Option configures a Sensor.
type Option func(*Sensor)

// WithRules overrides the default dependency rules.
func WithRules(rules []DependencyRule) Option {
	return func(s *Sensor) { s.rules = rules }
}

// WithRootDir overrides the root directory to scan (default: ".").
func WithRootDir(dir string) Option {
	return func(s *Sensor) { s.rootDir = dir }
}

// New creates an architecture-fitness sensor for the given Go module name.
func New(moduleName string, opts ...Option) *Sensor {
	s := &Sensor{
		moduleName: moduleName,
		rootDir:    ".",
		rules:      DefaultRules(moduleName),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Name implements harness.Sensor.
func (s *Sensor) Name() string { return "architecture.fitness" }

// Category implements harness.Sensor.
func (s *Sensor) Category() harness.Category { return harness.CategoryArchitecture }

// Mode implements harness.Sensor.
func (s *Sensor) Mode() harness.ExecutionMode { return harness.ModeComputational }

// Run implements harness.Sensor. It walks the Go source tree, parses import
// declarations, and reports any rule violations as findings.
func (s *Sensor) Run() ([]harness.Finding, error) {
	var findings []harness.Finding

	err := filepath.WalkDir(s.rootDir, func(path string, d os.DirEntry, walkErr error) error {
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
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fileFindings, err := s.checkFile(path)
		if err != nil {
			return fmt.Errorf("check %s: %w", path, err)
		}
		findings = append(findings, fileFindings...)
		return nil
	})
	if err != nil {
		return findings, err
	}

	return findings, nil
}

// checkFile parses a single Go file and checks its imports against the rules.
func (s *Sensor) checkFile(path string) ([]harness.Finding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	// Determine which internal package this file belongs to.
	filePkg := s.packagePrefix(path)
	if filePkg == "" {
		return nil, nil
	}

	var findings []harness.Finding
	for _, imp := range file.Imports {
		impPath := strings.Trim(imp.Path.Value, `"`)
		for _, rule := range s.rules {
			if !strings.HasPrefix(filePkg, rule.Package) {
				continue
			}
			if !strings.HasPrefix(impPath, rule.Forbidden) {
				continue
			}
			findings = append(findings, harness.Finding{
				Check:     s.Name(),
				Category:  harness.CategoryArchitecture,
				Mode:      harness.ModeComputational,
				Direction: harness.DirectionFeedback,
				Severity:  harness.SeverityError,
				File:      path,
				Line:      fset.Position(imp.Pos()).Line,
				Message:   fmt.Sprintf("package %q imports forbidden %q", filePkg, impPath),
				Suggest:   rule.Suggestion,
			})
		}
	}
	return findings, nil
}

// packagePrefix returns the internal/ package path prefix for a file, or ""
// if the file is not under internal/.
func (s *Sensor) packagePrefix(filePath string) string {
	clean := filepath.ToSlash(filePath)
	internalMarker := "/internal/"
	idx := strings.Index(clean, internalMarker)
	if idx < 0 {
		return ""
	}
	rest := clean[idx+1:] // "internal/..."
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 {
		return s.moduleName + "/" + rest
	}
	return s.moduleName + "/internal/" + parts[1]
}
