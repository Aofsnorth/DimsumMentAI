// Package tododebt implements a computational sensor that scans Go source
// files for TODO, FIXME, HACK, XXX, and BUG comments and reports them as
// info-severity findings. This provides visibility into accumulated
// technical debt without blocking the build.
//
// The sensor is a maintainability feedback control: it surfaces debt so
// that it can be tracked and addressed over time, rather than letting it
// accumulate invisibly.
package tododebt

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"bedrock-ai/internal/harness"
)

// markerPattern matches TODO/FIXME/HACK/XXX/BUG markers at the start of a
// comment's content (after // or /*).
var markerPattern = regexp.MustCompile(`(?i)\b(TODO|FIXME|HACK|XXX|BUG)\b`)

// Config controls the TODO-debt sensor behaviour.
type Config struct {
	// RootDir is the root directory to scan. Default: ".".
	RootDir string
	// MaxFindings limits the number of findings reported (0 = unlimited).
	// This prevents flooding the output in codebases with many markers.
	MaxFindings int
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{RootDir: ".", MaxFindings: 100}
}

// Sensor is a computational maintainability sensor that tracks TODO/FIXME
// markers as technical-debt indicators.
type Sensor struct {
	cfg Config
}

// Option configures a Sensor.
type Option func(*Sensor)

// WithConfig overrides the default configuration.
func WithConfig(cfg Config) Option {
	return func(s *Sensor) { s.cfg = cfg }
}

// New creates a TODO-debt sensor with the given options.
func New(opts ...Option) *Sensor {
	s := &Sensor{cfg: DefaultConfig()}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Name implements harness.Sensor.
func (s *Sensor) Name() string { return "maintainability.tododebt" }

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

		fileFindings, err := s.scanFile(path)
		if err != nil {
			return nil
		}
		findings = append(findings, fileFindings...)

		if s.cfg.MaxFindings > 0 && len(findings) >= s.cfg.MaxFindings {
			if len(findings) > s.cfg.MaxFindings {
				findings = findings[:s.cfg.MaxFindings]
			}
			return filepath.SkipAll
		}
		return nil
	})

	return findings, err
}

// scanFile reads a file line by line and reports any TODO/FIXME markers.
func (s *Sensor) scanFile(path string) ([]harness.Finding, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var findings []harness.Finding
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		// Only scan comment lines for markers.
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "/*") && !strings.HasPrefix(trimmed, "*") {
			continue
		}
		loc := markerPattern.FindStringSubmatchIndex(line)
		if loc == nil {
			continue
		}
		marker := strings.ToUpper(line[loc[2]:loc[3]])
		findings = append(findings, harness.Finding{
			Check:     s.Name(),
			Category:  harness.CategoryMaintainability,
			Mode:      harness.ModeComputational,
			Direction: harness.DirectionFeedback,
			Severity:  harness.SeverityInfo,
			File:      path,
			Line:      lineNum,
			Message:   fmt.Sprintf("%s marker found: %s", marker, strings.TrimSpace(trimmed)),
			Suggest:   fmt.Sprintf("track this %s in your issue tracker or resolve it", marker),
		})
	}
	return findings, scanner.Err()
}
