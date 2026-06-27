package license

import (
	"os"
	"path/filepath"
	"testing"

	"bedrock-ai/internal/harness"
)

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestGuide_Name(t *testing.T) {
	t.Parallel()
	g := New()
	if g.Name() != "maintainability.license" {
		t.Errorf("Name() = %q", g.Name())
	}
}

func TestGuide_Category(t *testing.T) {
	t.Parallel()
	g := New()
	if g.Category() != harness.CategoryMaintainability {
		t.Errorf("Category() = %q", g.Category())
	}
}

func TestGuide_Mode(t *testing.T) {
	t.Parallel()
	g := New()
	if g.Mode() != harness.ModeComputational {
		t.Errorf("Mode() = %q", g.Mode())
	}
}

func TestGuide_DetectsMissingHeader(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "noheader.go", "package foo\n\nfunc Bar() {}\n")
	g := New(WithConfig(Config{RootDir: root}))
	findings, err := g.Check()
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected finding for missing header")
	}
}

func TestGuide_NoFindingForFileWithHeader(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "withheader.go", `// Package foo provides foo utilities.
package foo

func Bar() {}
`)
	g := New(WithConfig(Config{RootDir: root}))
	findings, err := g.Check()
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("file with header should produce no findings, got %d", len(findings))
	}
}

func TestGuide_SkipsTestFilesByDefault(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "foo_test.go", "package foo\n\nfunc TestFoo(t *testing.T) {}\n")
	cfg := DefaultConfig()
	cfg.RootDir = root
	g := New(WithConfig(cfg))
	findings, err := g.Check()
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("test files should be skipped by default, got %d", len(findings))
	}
}

func TestGuide_ChecksTestFilesWhenConfigured(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "foo_test.go", "package foo\n\nfunc TestFoo(t *testing.T) {}\n")
	g := New(WithConfig(Config{RootDir: root, SkipTestFiles: false}))
	findings, err := g.Check()
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if len(findings) == 0 {
		t.Error("expected finding for test file when SkipTestFiles is false")
	}
}

func TestGuide_RequiredKeywords(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "code.go", `// Some random comment.
package foo
`)
	g := New(WithConfig(Config{RootDir: root, RequiredKeywords: []string{"Copyright"}}))
	findings, err := g.Check()
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	foundMissing := false
	for _, f := range findings {
		if f.Severity == harness.SeverityWarning && f.Message != "" {
			foundMissing = true
		}
	}
	if !foundMissing {
		t.Error("expected finding for missing required keyword")
	}
}

func TestGuide_RequiredKeywordsPresent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "code.go", `// Copyright 2026 DimsumMentAI. All rights reserved.
package foo
`)
	g := New(WithConfig(Config{RootDir: root, RequiredKeywords: []string{"Copyright"}}))
	findings, err := g.Check()
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("file with required keyword should produce no findings, got %d", len(findings))
	}
}

func TestGuide_EmptyDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	g := New(WithConfig(Config{RootDir: root}))
	findings, err := g.Check()
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("empty dir should produce no findings, got %d", len(findings))
	}
}

func TestGuide_SkipsNonGoFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "readme.txt", "no header here")
	g := New(WithConfig(Config{RootDir: root}))
	findings, err := g.Check()
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("non-Go files should be ignored, got %d", len(findings))
	}
}

func TestDefaultConfig(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	if cfg.RootDir != "." {
		t.Errorf("DefaultConfig RootDir = %q, want %q", cfg.RootDir, ".")
	}
	if !cfg.SkipTestFiles {
		t.Error("DefaultConfig SkipTestFiles should be true")
	}
}
