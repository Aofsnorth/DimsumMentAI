package tododebt

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

func TestSensor_Name(t *testing.T) {
	t.Parallel()
	s := New()
	if s.Name() != "maintainability.tododebt" {
		t.Errorf("Name() = %q", s.Name())
	}
}

func TestSensor_Category(t *testing.T) {
	t.Parallel()
	s := New()
	if s.Category() != harness.CategoryMaintainability {
		t.Errorf("Category() = %q", s.Category())
	}
}

func TestSensor_Mode(t *testing.T) {
	t.Parallel()
	s := New()
	if s.Mode() != harness.ModeComputational {
		t.Errorf("Mode() = %q", s.Mode())
	}
}

func TestSensor_DetectsTODO(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "code.go", `package foo

// TODO: implement this later
func Foo() {}
`)
	s := New(WithConfig(Config{RootDir: root}))
	findings, err := s.Run()
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected TODO finding")
	}
	if findings[0].Severity != harness.SeverityInfo {
		t.Errorf("expected info severity, got %s", findings[0].Severity)
	}
}

func TestSensor_DetectsMultipleMarkers(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "code.go", `package foo

// TODO: first
// FIXME: second
// HACK: third
func Foo() {}
`)
	s := New(WithConfig(Config{RootDir: root}))
	findings, err := s.Run()
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(findings) != 3 {
		t.Errorf("expected 3 findings, got %d", len(findings))
	}
}

func TestSensor_IgnoresNonCommentLines(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "code.go", `package foo

func Foo() {
	_ = "TODO this is a string not a comment"
}
`)
	s := New(WithConfig(Config{RootDir: root}))
	findings, err := s.Run()
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("should not detect markers in non-comment lines, got %d", len(findings))
	}
}

func TestSensor_EmptyDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := New(WithConfig(Config{RootDir: root}))
	findings, err := s.Run()
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("empty dir should produce no findings, got %d", len(findings))
	}
}

func TestSensor_MaxFindingsLimit(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "code.go", `package foo

// TODO: one
// TODO: two
// TODO: three
// TODO: four
// TODO: five
func Foo() {}
`)
	s := New(WithConfig(Config{RootDir: root, MaxFindings: 2}))
	findings, err := s.Run()
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(findings) > 2 {
		t.Errorf("should limit findings to 2, got %d", len(findings))
	}
}

func TestDefaultConfig(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	if cfg.RootDir != "." {
		t.Errorf("DefaultConfig RootDir = %q, want %q", cfg.RootDir, ".")
	}
}
