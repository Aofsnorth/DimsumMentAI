package architecture

import (
	"os"
	"path/filepath"
	"testing"

	"bedrock-ai/internal/harness"
)

func writeGoFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	return full
}

func TestSensor_Name(t *testing.T) {
	t.Parallel()
	s := New("bedrock-ai")
	if s.Name() != "architecture.fitness" {
		t.Errorf("Name() = %q, want %q", s.Name(), "architecture.fitness")
	}
}

func TestSensor_Category(t *testing.T) {
	t.Parallel()
	s := New("bedrock-ai")
	if s.Category() != harness.CategoryArchitecture {
		t.Errorf("Category() = %q, want %q", s.Category(), harness.CategoryArchitecture)
	}
}

func TestSensor_Mode(t *testing.T) {
	t.Parallel()
	s := New("bedrock-ai")
	if s.Mode() != harness.ModeComputational {
		t.Errorf("Mode() = %q, want %q", s.Mode(), harness.ModeComputational)
	}
}

func TestSensor_DetectsViolation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// Create a file in internal/ai/ that imports internal/bot — a violation.
	writeGoFile(t, root, "internal/ai/violation.go", `package ai

import "bedrock-ai/internal/bot"

var _ = bot.Bot{}
`)

	s := New("bedrock-ai", WithRootDir(root))
	findings, err := s.Run()
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected violation findings, got none")
	}
	found := false
	for _, f := range findings {
		if f.Severity == harness.SeverityError && f.Suggest != "" {
			found = true
		}
	}
	if !found {
		t.Error("expected an error-severity finding with a suggestion")
	}
}

func TestSensor_NoViolations(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// Create a clean file in internal/bot/ that imports internal/ai — allowed.
	writeGoFile(t, root, "internal/bot/clean.go", `package bot

import "bedrock-ai/internal/ai"

var _ = ai.Message{}
`)

	s := New("bedrock-ai", WithRootDir(root))
	findings, err := s.Run()
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no violations, got %d findings", len(findings))
	}
}

func TestSensor_IgnoresTestFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// A _test.go file with a violation should be ignored.
	writeGoFile(t, root, "internal/ai/violation_test.go", `package ai

import "bedrock-ai/internal/bot"

func TestViolation(t *testing.T) {
	_ = bot.Bot{}
}
`)

	s := New("bedrock-ai", WithRootDir(root))
	findings, err := s.Run()
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("test files should be ignored, got %d findings", len(findings))
	}
}

func TestSensor_IgnoresNonGoFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeGoFile(t, root, "internal/ai/data.txt", `this is not Go code`)

	s := New("bedrock-ai", WithRootDir(root))
	findings, err := s.Run()
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("non-Go files should be ignored, got %d findings", len(findings))
	}
}

func TestSensor_SkipsVendorDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeGoFile(t, root, "vendor/internal/ai/violation.go", `package ai

import "bedrock-ai/internal/bot"

var _ = bot.Bot{}
`)

	s := New("bedrock-ai", WithRootDir(root))
	findings, err := s.Run()
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("vendor dir should be skipped, got %d findings", len(findings))
	}
}

func TestDefaultRules(t *testing.T) {
	t.Parallel()
	rules := DefaultRules("bedrock-ai")
	if len(rules) == 0 {
		t.Fatal("DefaultRules should not be empty")
	}
	// Verify a known rule exists.
	found := false
	for _, r := range rules {
		if r.Package == "bedrock-ai/internal/ai" && r.Forbidden == "bedrock-ai/internal/bot" {
			found = true
		}
	}
	if !found {
		t.Error("DefaultRules should include ai→bot forbidden rule")
	}
}

func TestSensor_WithCustomRules(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeGoFile(t, root, "internal/foo/bar.go", `package foo

import "bedrock-ai/internal/baz"

var _ = baz.Thing{}
`)

	customRules := []DependencyRule{
		{Package: "bedrock-ai/internal/foo", Forbidden: "bedrock-ai/internal/baz", Suggestion: "foo must not import baz"},
	}
	s := New("bedrock-ai", WithRootDir(root), WithRules(customRules))
	findings, err := s.Run()
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("custom rule should detect violation")
	}
}

func TestSensor_EmptyDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := New("bedrock-ai", WithRootDir(root))
	findings, err := s.Run()
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("empty dir should produce no findings, got %d", len(findings))
	}
}
