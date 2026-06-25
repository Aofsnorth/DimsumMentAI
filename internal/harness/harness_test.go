package harness

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
)

// --- Mock implementations for testing the harness framework itself ---

type mockSensor struct {
	name     string
	category Category
	mode     ExecutionMode
	findings []Finding
	err      error
	calls    int32
}

func (m *mockSensor) Name() string        { return m.name }
func (m *mockSensor) Category() Category  { return m.category }
func (m *mockSensor) Mode() ExecutionMode { return m.mode }
func (m *mockSensor) Run() ([]Finding, error) {
	atomic.AddInt32(&m.calls, 1)
	return m.findings, m.err
}

type mockGuide struct {
	name     string
	category Category
	mode     ExecutionMode
	findings []Finding
	err      error
	calls    int32
}

func (m *mockGuide) Name() string        { return m.name }
func (m *mockGuide) Category() Category  { return m.category }
func (m *mockGuide) Mode() ExecutionMode { return m.mode }
func (m *mockGuide) Check() ([]Finding, error) {
	atomic.AddInt32(&m.calls, 1)
	return m.findings, m.err
}

type mockReporter struct {
	result   Result
	reported int32
	err      error
}

func (m *mockReporter) Report(r Result) error {
	atomic.AddInt32(&m.reported, 1)
	m.result = r
	return m.err
}

// --- Tests ---

func TestRunner_NoChecks(t *testing.T) {
	t.Parallel()
	reporter := &mockReporter{}
	r := NewRunner(reporter)
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
	r := NewRunner(reporter)
	r.RegisterSensor(&mockSensor{
		name:     "test-sensor",
		category: CategoryMaintainability,
		mode:     ModeComputational,
		findings: []Finding{
			{Check: "test-sensor", Severity: SeverityWarning, Message: "minor issue"},
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
	r := NewRunner(reporter)
	r.RegisterSensor(&mockSensor{
		name:     "test-sensor",
		category: CategoryBehaviour,
		mode:     ModeComputational,
		findings: []Finding{
			{Check: "test-sensor", Severity: SeverityError, Message: "fatal issue"},
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
	r := NewRunner(reporter)
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
	r := NewRunner(reporter)
	r.RegisterSensor(&mockSensor{
		name:     "sensor-1",
		category: CategoryBehaviour,
		findings: []Finding{{Check: "sensor-1", Severity: SeverityInfo, Message: "ok"}},
	})
	r.RegisterGuide(&mockGuide{
		name:     "guide-1",
		category: CategoryArchitecture,
		findings: []Finding{{Check: "guide-1", Severity: SeverityWarning, Message: "watch out"}},
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
	r := NewRunner(reporter)
	s := &mockSensor{name: "s"}
	g := &mockGuide{name: "g"}
	r.RegisterSensor(s)
	r.RegisterGuide(g)

	r.Run()

	if atomic.LoadInt32(&s.calls) != 1 {
		t.Errorf("sensor called %d times, want 1", s.calls)
	}
	if atomic.LoadInt32(&g.calls) != 1 {
		t.Errorf("guide called %d times, want 1", g.calls)
	}
}

func TestRunner_NilReporterSafe(t *testing.T) {
	t.Parallel()
	r := NewRunner(nil)
	_, err := r.Run()
	if err != nil {
		t.Errorf("Run with nil reporter should not error: %v", err)
	}
}

func TestRunner_NilSensorIgnored(t *testing.T) {
	t.Parallel()
	r := NewRunner(&mockReporter{})
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
		severity Severity
		want     bool
	}{
		{SeverityError, false},
		{SeverityWarning, true},
		{SeverityInfo, true},
	}
	for _, tc := range tests {
		f := Finding{Severity: tc.severity}
		if f.Pass() != tc.want {
			t.Errorf("Finding{Severity: %s}.Pass() = %v, want %v", tc.severity, f.Pass(), tc.want)
		}
	}
}

func TestFinding_String(t *testing.T) {
	t.Parallel()
	f := Finding{Check: "test", Severity: SeverityError, Message: "broken", File: "main.go", Line: 42}
	s := f.String()
	if s == "" {
		t.Error("String() should not be empty")
	}
}

func TestFinding_StringNoLocation(t *testing.T) {
	t.Parallel()
	f := Finding{Check: "test", Severity: SeverityInfo, Message: "note"}
	s := f.String()
	if s == "" {
		t.Error("String() should not be empty even without location")
	}
}

func TestResult_FindingsByCategory(t *testing.T) {
	t.Parallel()
	r := Result{
		Findings: []Finding{
			{Category: CategoryMaintainability, Severity: SeverityWarning},
			{Category: CategoryMaintainability, Severity: SeverityError},
			{Category: CategoryArchitecture, Severity: SeverityError},
			{Category: CategoryBehaviour, Severity: SeverityInfo},
		},
	}
	groups := r.FindingsByCategory()
	if len(groups[CategoryMaintainability]) != 2 {
		t.Errorf("maintainability findings = %d, want 2", len(groups[CategoryMaintainability]))
	}
	if len(groups[CategoryArchitecture]) != 1 {
		t.Errorf("architecture findings = %d, want 1", len(groups[CategoryArchitecture]))
	}
	if len(groups[CategoryBehaviour]) != 1 {
		t.Errorf("behaviour findings = %d, want 1", len(groups[CategoryBehaviour]))
	}
}

func TestConsoleReporter(t *testing.T) {
	t.Parallel()
	var output string
	reporter := NewConsoleReporter(func(s string) (int, error) {
		output += s + "\n"
		return len(s), nil
	})
	result := Result{
		SensorCount: 2,
		GuideCount:  1,
		Findings: []Finding{
			{Check: "test", Severity: SeverityWarning, Message: "watch out"},
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
	r := NullReporter{}
	if err := r.Report(Result{}); err != nil {
		t.Errorf("NullReporter should not error: %v", err)
	}
}

func TestRunner_ReporterError(t *testing.T) {
	t.Parallel()
	reporter := &mockReporter{err: errors.New("reporter broken")}
	r := NewRunner(reporter)
	_, err := r.Run()
	if err == nil {
		t.Fatal("Run should propagate reporter error")
	}
	if !contains(err.Error(), "report") {
		t.Errorf("error should mention report: %s", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && fmt.Sprintf("%s", s) != "" && stringContains(s, substr)
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
