// Package filesize implements a computational sensor that detects Go source
// files exceeding a configurable line-count or byte-size threshold. Large
// files are a maintainability risk — they are harder to navigate, review, and
// test.
//
// The sensor walks the project tree (excluding vendor, node_modules, .git,
// and .kilo), measures each .go file, and emits warning-severity findings
// for files that exceed the configured limits. Test files (_test.go) are
// measured against a separate, more lenient threshold.
package filesize

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"bedrock-ai/internal/harness"
)

// Config controls the file-size sensor thresholds.
type Config struct {
	// MaxLines is the maximum number of lines allowed in a non-test Go file
	// before a warning is emitted. Default: 500.
	MaxLines int
	// MaxTestLines is the maximum number of lines allowed in a _test.go file
	// before a warning is emitted. Default: 800.
	MaxTestLines int
	// MaxBytes is the maximum file size in bytes for a non-test Go file.
	// Default: 20000 (20 KB).
	MaxBytes int64
	// RootDir is the root directory to scan. Default: ".".
	RootDir string
}

// DefaultConfig returns sensible default thresholds.
func DefaultConfig() Config {
	return Config{
		MaxLines:     500,
		MaxTestLines: 800,
		MaxBytes:     20000,
		RootDir:      ".",
	}
}

// Sensor is a computational maintainability sensor that flags oversized Go
// source files.
type Sensor struct {
	cfg Config
}

// Option configures a Sensor.
type Option func(*Sensor)

// WithConfig overrides the default configuration.
func WithConfig(cfg Config) Option {
	return func(s *Sensor) { s.cfg = cfg }
}

// New creates a file-size sensor with the given options.
func New(opts ...Option) *Sensor {
	s := &Sensor{cfg: DefaultConfig()}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Name implements harness.Sensor.
func (s *Sensor) Name() string { return "maintainability.filesize" }

// Category implements harness.Sensor.
func (s *Sensor) Category() harness.Category { return harness.CategoryMaintainability }

// Mode implements harness.Sensor.
func (s *Sensor) Mode() harness.ExecutionMode { return harness.ModeComputational }

// Run implements harness.Sensor.
func (s *Sensor) Run() ([]harness.Finding, error) {
	var findings []harness.Finding

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

		info, err := d.Info()
		if err != nil {
			return nil
		}

		isTest := strings.HasSuffix(path, "_test.go")
		maxLines := s.cfg.MaxLines
		if isTest {
			maxLines = s.cfg.MaxTestLines
		}

		lines, err := countLines(path)
		if err != nil {
			return nil
		}

		if lines > maxLines {
			findings = append(findings, harness.Finding{
				Check:     s.Name(),
				Category:  harness.CategoryMaintainability,
				Mode:      harness.ModeComputational,
				Direction: harness.DirectionFeedback,
				Severity:  harness.SeverityWarning,
				File:      path,
				Line:      1,
				Message:   fmt.Sprintf("file has %d lines (max %d)", lines, maxLines),
				Suggest:   fmt.Sprintf("consider splitting %s into smaller files with focused responsibilities", filepath.Base(path)),
			})
		}

		if info.Size() > s.cfg.MaxBytes && !isTest {
			findings = append(findings, harness.Finding{
				Check:     s.Name(),
				Category:  harness.CategoryMaintainability,
				Mode:      harness.ModeComputational,
				Direction: harness.DirectionFeedback,
				Severity:  harness.SeverityWarning,
				File:      path,
				Line:      1,
				Message:   fmt.Sprintf("file is %d bytes (max %d)", info.Size(), s.cfg.MaxBytes),
				Suggest:   fmt.Sprintf("consider splitting %s to reduce its size", filepath.Base(path)),
			})
		}

		return nil
	})

	return findings, err
}

// countLines returns the number of lines in a file.
func countLines(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strings.Count(string(data), "\n") + 1, nil
}
