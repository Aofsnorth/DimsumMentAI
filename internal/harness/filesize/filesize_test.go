package filesize

import (
	"os"
	"path/filepath"
	"strings"
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
	if s.Name() != "maintainability.filesize" {
		t.Errorf("Name() = %q, want %q", s.Name(), "maintainability.filesize")
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

func TestSensor_DetectsLargeFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// Write a file with 10 lines, threshold 5.
	writeFile(t, root, "big.go", strings.Repeat("package foo\n", 1)+strings.Repeat("// line\n", 10))
	s := New(WithConfig(Config{MaxLines: 5, MaxTestLines: 100, MaxBytes: 100000, RootDir: root}))
	findings, err := s.Run()
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected findings for oversized file")
	}
	found := false
	for _, f := range findings {
		if f.Severity == harness.SeverityWarning && strings.Contains(f.Message, "lines") {
			found = true
		}
	}
	if !found {
		t.Error("expected a line-count warning finding")
	}
}

func TestSensor_NoFindingsForSmallFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "small.go", "package foo\n")
	s := New(WithConfig(Config{MaxLines: 500, MaxTestLines: 800, MaxBytes: 20000, RootDir: root}))
	findings, err := s.Run()
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings, got %d", len(findings))
	}
}

func TestSensor_DetectsLargeBytes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "big.go", "package foo\n"+strings.Repeat("x", 200))
	s := New(WithConfig(Config{MaxLines: 500, MaxTestLines: 800, MaxBytes: 50, RootDir: root}))
	findings, err := s.Run()
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	found := false
	for _, f := range findings {
		if strings.Contains(f.Message, "bytes") {
			found = true
		}
	}
	if !found {
		t.Error("expected a byte-size warning finding")
	}
}

func TestSensor_TestFileUsesSeparateThreshold(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// A test file with 10 lines; MaxLines=5 but MaxTestLines=100.
	writeFile(t, root, "foo_test.go", "package foo\n"+strings.Repeat("// line\n", 10))
	s := New(WithConfig(Config{MaxLines: 5, MaxTestLines: 100, MaxBytes: 100000, RootDir: root}))
	findings, err := s.Run()
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	for _, f := range findings {
		if strings.Contains(f.Message, "lines") {
			t.Errorf("test file should not trigger line warning with lenient threshold: %s", f.Message)
		}
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

func TestSensor_SkipsNonGoFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "readme.txt", strings.Repeat("line\n", 1000))
	s := New(WithConfig(Config{MaxLines: 5, RootDir: root}))
	findings, err := s.Run()
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("non-Go files should be ignored, got %d", len(findings))
	}
}

func TestDefaultConfig(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	if cfg.MaxLines <= 0 || cfg.MaxTestLines <= 0 || cfg.MaxBytes <= 0 {
		t.Error("DefaultConfig should have positive thresholds")
	}
}
