// Package complexity implements a computational sensor that measures
// cyclomatic complexity of Go functions using the go/ast parser. Functions
// exceeding a configurable threshold are reported as warning-severity
// findings.
//
// Cyclomatic complexity counts the number of linearly independent paths
// through a function. High complexity correlates with bugs and makes
// functions harder to test and maintain.
//
// The complexity is calculated by counting decision points: if, for, range,
// switch, case, &&, ||, and function literals (closures) that capture
// control flow. Each decision point adds 1 to the base complexity of 1.
package complexity

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"bedrock-ai/internal/harness"
)

// Config controls the complexity sensor thresholds.
type Config struct {
	// MaxComplexity is the maximum cyclomatic complexity allowed before a
	// warning is emitted. Default: 15.
	MaxComplexity int
	// RootDir is the root directory to scan. Default: ".".
	RootDir string
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{MaxComplexity: 15, RootDir: "."}
}

// Sensor is a computational maintainability sensor that flags functions
// with high cyclomatic complexity.
type Sensor struct {
	cfg Config
}

// Option configures a Sensor.
type Option func(*Sensor)

// WithConfig overrides the default configuration.
func WithConfig(cfg Config) Option {
	return func(s *Sensor) { s.cfg = cfg }
}

// New creates a complexity sensor with the given options.
func New(opts ...Option) *Sensor {
	s := &Sensor{cfg: DefaultConfig()}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Name implements harness.Sensor.
func (s *Sensor) Name() string { return "maintainability.complexity" }

// Category implements harness.Sensor.
func (s *Sensor) Category() harness.Category { return harness.CategoryMaintainability }

// Mode implements harness.Sensor.
func (s *Sensor) Mode() harness.ExecutionMode { return harness.ModeComputational }

// Run implements harness.Sensor.
func (s *Sensor) Run() ([]harness.Finding, error) {
	var findings []harness.Finding
	fset := token.NewFileSet()

	err := filepath.WalkDir(s.cfg.RootDir, func(path string, d os.DirEntry, walkErr error) error {
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

		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil
		}

		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok {
				return true
			}
			cc := cyclomaticComplexity(fn)
			if cc > s.cfg.MaxComplexity {
				pos := fset.Position(fn.Pos())
				name := funcName(fn)
				findings = append(findings, harness.Finding{
					Check:     s.Name(),
					Category:  harness.CategoryMaintainability,
					Mode:      harness.ModeComputational,
					Direction: harness.DirectionFeedback,
					Severity:  harness.SeverityWarning,
					File:      pos.Filename,
					Line:      pos.Line,
					Message:   fmt.Sprintf("function %s has cyclomatic complexity %d (max %d)", name, cc, s.cfg.MaxComplexity),
					Suggest:   fmt.Sprintf("refactor %s by extracting helper functions or using early returns to reduce branching", name),
				})
			}
			return true
		})
		return nil
	})

	return findings, err
}

// funcName returns a human-readable name for a function declaration.
func funcName(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		recv := typeString(fn.Recv.List[0].Type)
		return fmt.Sprintf("(%s).%s", recv, fn.Name.Name)
	}
	return fn.Name.Name
}

// typeString returns a short string representation of an AST type.
func typeString(t ast.Expr) string {
	switch v := t.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.StarExpr:
		return "*" + typeString(v.X)
	case *ast.IndexExpr:
		return typeString(v.X)
	default:
		return "?"
	}
}

// cyclomaticComplexity computes the McCabe cyclomatic complexity of a
// function by counting decision points.
func cyclomaticComplexity(fn *ast.FuncDecl) int {
	cc := 1
	ast.Inspect(fn, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt:
			cc++
		case *ast.CaseClause:
			cc++
		case *ast.CommClause:
			cc++
		case *ast.BinaryExpr:
			// && and || add a path each.
			if be, ok := n.(*ast.BinaryExpr); ok {
				if be.Op == token.LAND || be.Op == token.LOR {
					cc++
				}
			}
		}
		return true
	})
	return cc
}
