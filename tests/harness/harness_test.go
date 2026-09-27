package harness_test

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"bedrock-ai/internal/harness"
)

// --- Mock implementations for testing the harness framework itself ---

type mockSensor struct {
	name     string
	category harness.Category
	mode     harness.ExecutionMode
	findings []harness.Finding
	err      error
	calls    int32
}

func (m *mockSensor) Name() string                { return m.name }
func (m *mockSensor) Category() harness.Category  { return m.category }
func (m *mockSensor) Mode() harness.ExecutionMode { return m.mode }
func (m *mockSensor) Run() ([]harness.Finding, error) {
	atomic.AddInt32(&m.calls, 1)
	return m.findings, m.err
}

type mockGuide struct {
	name     string
	category harness.Category
	mode     harness.ExecutionMode
	findings []harness.Finding
	err      error
	calls    int32
}

func (m *mockGuide) Name() string                { return m.name }
func (m *mockGuide) Category() harness.Category  { return m.category }
func (m *mockGuide) Mode() harness.ExecutionMode { return m.mode }
func (m *mockGuide) Check() ([]harness.Finding, error) {
	atomic.AddInt32(&m.calls, 1)
	return m.findings, m.err
}

type mockReporter struct {
	result   harness.Result
	reported int32
	err      error
}

func (m *mockReporter) Report(r harness.Result) error {
	atomic.AddInt32(&m.reported, 1)
	m.result = r
	return m.err
}

// --- Tests ---

func TestRunner_NoChecks(t *testing.T) {
	t.Parallel()
	reporter := &mockReporter{}
	r := harness.NewRunner(reporter)
	result, err := r.Run()
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !result.Passed() {
		t.Error("empty run should pass")
	}
	if atomic.LoadInt32(&reporter.reported) != 1 {
		t.Error("reporter should be called exactly once")
	}
}

func TestRunner_SensorFindings(t *testing.T) {
	t.Parallel()
	reporter := &mockReporter{}
	r := harness.NewRunner(reporter)
	r.RegisterSensor(&mockSensor{
		name:     "test-sensor",
		category: harness.CategoryMaintainability,
		mode:     harness.ModeComputational,
		findings: []harness.Finding{
			{Check: "test-sensor", Severity: harness.SeverityWarning, Message: "minor issue"},
		},
	})

	result, err := r.Run()
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !result.Passed() {
		t.Error("run with only warnings should pass")
	}
	if result.WarningCount() != 1 {
		t.Errorf("WarningCount = %d, want 1", result.WarningCount())
	}
	if result.ErrorCount() != 0 {
		t.Errorf("ErrorCount = %d, want 0", result.ErrorCount())
	}
}

func TestRunner_ErrorFindingsFail(t *testing.T) {
	t.Parallel()
	reporter := &mockReporter{}
	r := harness.NewRunner(reporter)
	r.RegisterSensor(&mockSensor{
		name:     "test-sensor",
		category: harness.CategoryBehaviour,
		mode:     harness.ModeComputational,
		findings: []harness.Finding{
			{Check: "test-sensor", Severity: harness.SeverityError, Message: "fatal issue"},
		},
	})

	result, _ := r.Run()
	if result.Passed() {
		t.Error("run with error findings should fail")
	}
	if result.ErrorCount() != 1 {
		t.Errorf("ErrorCount = %d, want 1", result.ErrorCount())
	}
}

func TestRunner_SensorError(t *testing.T) {
	t.Parallel()
	reporter := &mockReporter{}
	r := harness.NewRunner(reporter)
	r.RegisterSensor(&mockSensor{
		name: "broken-sensor",
		err:  errors.New("sensor exploded"),
	})

	result, _ := r.Run()
	if result.Passed() {
		t.Error("run with execution error should fail")
	}
	if len(result.Errors) != 1 {
		t.Fatalf("Errors len = %d, want 1", len(result.Errors))
	}
}

func TestRunner_GuidesAndSensors(t *testing.T) {
	t.Parallel()
	reporter := &mockReporter{}
	r := harness.NewRunner(reporter)
	r.RegisterSensor(&mockSensor{
		name:     "sensor-1",
		category: harness.CategoryBehaviour,
		findings: []harness.Finding{{Check: "sensor-1", Severity: harness.SeverityInfo, Message: "ok"}},
	})
	r.RegisterGuide(&mockGuide{
		name:     "guide-1",
		category: harness.CategoryArchitecture,
		findings: []harness.Finding{{Check: "guide-1", Severity: harness.SeverityWarning, Message: "watch out"}},
	})

	result, _ := r.Run()
	if result.SensorCount != 1 {
		t.Errorf("SensorCount = %d, want 1", result.SensorCount)
	}
	if result.GuideCount != 1 {
		t.Errorf("GuideCount = %d, want 1", result.GuideCount)
	}
	if len(result.Findings) != 2 {
		t.Errorf("Findings len = %d, want 2", len(result.Findings))
	}
}

func TestRunner_AllChecksCalledOnce(t *testing.T) {
	t.Parallel()
	reporter := &mockReporter{}
	r := harness.NewRunner(reporter)
	s := &mockSensor{name: "s"}
	g := &mockGuide{name: "g"}
	r.RegisterSensor(s)
	r.RegisterGuide(g)

	if _, err := r.Run(); err != nil {
		t.Fatal(err)
	}

	if atomic.LoadInt32(&s.calls) != 1 {
		t.Errorf("sensor called %d times, want 1", s.calls)
	}
	if atomic.LoadInt32(&g.calls) != 1 {
		t.Errorf("guide called %d times, want 1", g.calls)
	}
}

func TestRunner_NilReporterSafe(t *testing.T) {
	t.Parallel()
	r := harness.NewRunner(nil)
	_, err := r.Run()
	if err != nil {
		t.Errorf("Run with nil reporter should not error: %v", err)
	}
}

func TestRunner_NilSensorIgnored(t *testing.T) {
	t.Parallel()
	r := harness.NewRunner(&mockReporter{})
	r.RegisterSensor(nil)
	r.RegisterGuide(nil)
	result, _ := r.Run()
	if result.SensorCount != 0 || result.GuideCount != 0 {
		t.Error("nil sensors/guides should be ignored")
	}
}

func TestFinding_Pass(t *testing.T) {
	t.Parallel()
	tests := []struct {
		severity harness.Severity
		want     bool
	}{
		{harness.SeverityError, false},
		{harness.SeverityWarning, true},
		{harness.SeverityInfo, true},
	}
	for _, tc := range tests {
		f := harness.Finding{Severity: tc.severity}
		if f.Pass() != tc.want {
			t.Errorf("Finding{Severity: %s}.Pass() = %v, want %v", tc.severity, f.Pass(), tc.want)
		}
	}
}

func TestFinding_String(t *testing.T) {
	t.Parallel()
	f := harness.Finding{Check: "test", Severity: harness.SeverityError, Message: "broken", File: "main.go", Line: 42}
	s := f.String()
	if s == "" {
		t.Error("String() should not be empty")
	}
}

func TestFinding_StringNoLocation(t *testing.T) {
	t.Parallel()
	f := harness.Finding{Check: "test", Severity: harness.SeverityInfo, Message: "note"}
	s := f.String()
	if s == "" {
		t.Error("String() should not be empty even without location")
	}
}

func TestResult_FindingsByCategory(t *testing.T) {
	t.Parallel()
	r := harness.Result{
		Findings: []harness.Finding{
			{Category: harness.CategoryMaintainability, Severity: harness.SeverityWarning},
			{Category: harness.CategoryMaintainability, Severity: harness.SeverityError},
			{Category: harness.CategoryArchitecture, Severity: harness.SeverityError},
			{Category: harness.CategoryBehaviour, Severity: harness.SeverityInfo},
		},
	}
	groups := r.FindingsByCategory()
	if len(groups[harness.CategoryMaintainability]) != 2 {
		t.Errorf("maintainability findings = %d, want 2", len(groups[harness.CategoryMaintainability]))
	}
	if len(groups[harness.CategoryArchitecture]) != 1 {
		t.Errorf("architecture findings = %d, want 1", len(groups[harness.CategoryArchitecture]))
	}
	if len(groups[harness.CategoryBehaviour]) != 1 {
		t.Errorf("behavior findings = %d, want 1", len(groups[harness.CategoryBehaviour]))
	}
}

func TestConsoleReporter(t *testing.T) {
	t.Parallel()
	var output string
	reporter := harness.NewConsoleReporter(func(s string) (int, error) {
		output += s + "\n"
		return len(s), nil
	})
	result := harness.Result{
		SensorCount: 2,
		GuideCount:  1,
		Findings: []harness.Finding{
			{Check: "test", Severity: harness.SeverityWarning, Message: "watch out"},
		},
	}
	if err := reporter.Report(result); err != nil {
		t.Fatalf("Report error: %v", err)
	}
	if output == "" {
		t.Error("ConsoleReporter should produce output")
	}
}

func TestNullReporter(t *testing.T) {
	t.Parallel()
	r := harness.NullReporter{}
	if err := r.Report(harness.Result{}); err != nil {
		t.Errorf("NullReporter should not error: %v", err)
	}
}

func TestRunner_ReporterError(t *testing.T) {
	t.Parallel()
	reporter := &mockReporter{err: errors.New("reporter broken")}
	r := harness.NewRunner(reporter)
	_, err := r.Run()
	if err == nil {
		t.Fatal("Run should propagate reporter error")
	}
	if !contains(err.Error(), "report") {
		t.Errorf("error should mention report: %s", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s != "" && stringContains(s, substr)
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- JSONReporter tests ---

func TestJSONReporter(t *testing.T) {
	t.Parallel()
	var buf strings.Builder
	reporter := harness.NewJSONReporter(&buf)
	result := harness.Result{
		SensorCount: 2,
		GuideCount:  1,
		Findings: []harness.Finding{
			{Check: "test.sensor", Category: harness.CategoryMaintainability, Severity: harness.SeverityWarning, Message: "watch out"},
			{Check: "test.guide", Category: harness.CategoryArchitecture, Severity: harness.SeverityError, Message: "broken", File: "main.go", Line: 42},
		},
	}
	if err := reporter.Report(result); err != nil {
		t.Fatalf("Report error: %v", err)
	}
	output := buf.String()
	if output == "" {
		t.Fatal("JSONReporter should produce output")
	}
	if !strings.Contains(output, "test.sensor") {
		t.Error("JSON output should contain sensor name")
	}
	if !strings.Contains(output, "test.guide") {
		t.Error("JSON output should contain guide name")
	}
	if !strings.Contains(output, `"passed": false`) {
		t.Error("JSON output should show passed=false for error findings")
	}
}

func TestJSONReporter_PassedResult(t *testing.T) {
	t.Parallel()
	var buf strings.Builder
	reporter := harness.NewJSONReporter(&buf)
	result := harness.Result{
		SensorCount: 1,
		Findings:    []harness.Finding{{Check: "ok", Severity: harness.SeverityInfo, Message: "all good"}},
	}
	if err := reporter.Report(result); err != nil {
		t.Fatalf("Report error: %v", err)
	}
	if !strings.Contains(buf.String(), `"passed": true`) {
		t.Error("JSON output should show passed=true for info-only findings")
	}
}

func TestJSONReporter_EmptyResult(t *testing.T) {
	t.Parallel()
	var buf strings.Builder
	reporter := harness.NewJSONReporter(&buf)
	if err := reporter.Report(harness.Result{}); err != nil {
		t.Fatalf("Report error: %v", err)
	}
	if !strings.Contains(buf.String(), `"findings": []`) {
		t.Error("JSON output should have empty findings array")
	}
}
