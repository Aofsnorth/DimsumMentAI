package complexity

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
	if s.Name() != "maintainability.complexity" {
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

func TestSensor_DetectsHighComplexity(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "complex.go", `package foo

func ComplexFunc(x int) int {
	if x == 1 {
		if x == 2 {
			if x == 3 {
				return 1
			}
		}
	}
	for i := 0; i < 10; i++ {
		if i > 5 {
			return i
		}
	}
	switch x {
	case 1:
		return 1
	case 2:
		return 2
	}
	return 0
}
`)
	s := New(WithConfig(Config{MaxComplexity: 5, RootDir: root}))
	findings, err := s.Run()
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected complexity finding")
	}
	if findings[0].Severity != harness.SeverityWarning {
		t.Errorf("expected warning severity, got %s", findings[0].Severity)
	}
}

func TestSensor_NoFindingsForSimpleFunction(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "simple.go", `package foo

func SimpleFunc(x int) int {
	return x + 1
}
`)
	s := New(WithConfig(Config{MaxComplexity: 15, RootDir: root}))
	findings, err := s.Run()
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for simple function, got %d", len(findings))
	}
}

func TestSensor_IgnoresTestFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "complex_test.go", `package foo

func TestComplex(t *testing.T) {
	if true {
		if true {
			if true {
				if true {
					if true {
						if true {
							if true {
							}
						}
					}
				}
			}
		}
	}
}
`)
	s := New(WithConfig(Config{MaxComplexity: 3, RootDir: root}))
	findings, err := s.Run()
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("test files should be ignored, got %d", len(findings))
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

func TestCyclomaticComplexity_BinaryOps(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "binary.go", `package foo

func BinaryFunc(a, b, c bool) bool {
	return a && b || c
}
`)
	s := New(WithConfig(Config{MaxComplexity: 2, RootDir: root}))
	findings, err := s.Run()
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	// a && b || c → base 1 + 2 binary ops = 3, which exceeds 2.
	if len(findings) == 0 {
		t.Error("expected complexity finding for binary operators")
	}
}

func TestDefaultConfig(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	if cfg.MaxComplexity <= 0 {
		t.Error("DefaultConfig MaxComplexity should be positive")
	}
}
