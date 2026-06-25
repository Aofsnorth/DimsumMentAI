package harness

import (
	"fmt"
	"time"
)

// --- Core types -----------------------------------------------------------

// Category classifies what dimension of the system a check regulates.
type Category string

const (
	CategoryMaintainability Category = "maintainability"
	CategoryArchitecture    Category = "architecture"
	CategoryBehaviour       Category = "behaviour"
)

// ExecutionMode describes whether a check is deterministic or semantic.
type ExecutionMode string

const (
	ModeComputational ExecutionMode = "computational"
	ModeInferential   ExecutionMode = "inferential"
)

// Direction describes whether a check prevents issues or detects them.
type Direction string

const (
	DirectionFeedforward Direction = "feedforward" // guide
	DirectionFeedback    Direction = "feedback"    // sensor
)

// Severity ranks the importance of a finding.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Finding represents a single issue discovered by a sensor or guide.
type Finding struct {
	Check     string   // name of the check that produced this finding
	Category  Category // regulation category
	Mode      ExecutionMode
	Direction Direction
	Severity  Severity
	File      string // optional: file path
	Line      int    // optional: line number
	Message   string // human-readable description
	Suggest   string // optional: suggested fix instruction for self-correction
}

// Pass returns true when the finding is not a failure.
func (f Finding) Pass() bool {
	return f.Severity != SeverityError
}

// String formats a finding for display.
func (f Finding) String() string {
	loc := f.File
	if f.Line > 0 {
		loc = fmt.Sprintf("%s:%d", f.File, f.Line)
	}
	if loc != "" {
		return fmt.Sprintf("[%s] %s: %s (%s)", f.Severity, f.Check, f.Message, loc)
	}
	return fmt.Sprintf("[%s] %s: %s", f.Severity, f.Check, f.Message)
}

// --- Interfaces (ISP: small, focused contracts) --------------------------

// Sensor is a feedback control that observes the system after a change and
// reports deviations. Implementations must be safe to call concurrently.
type Sensor interface {
	// Name returns a unique, human-readable identifier for the sensor.
	Name() string
	// Category returns the regulation category this sensor covers.
	Category() Category
	// Mode returns the execution mode (computational or inferential).
	Mode() ExecutionMode
	// Run executes the sensor and returns all findings it discovers.
	Run() ([]Finding, error)
}

// Guide is a feedforward control that anticipates issues and steers the
// system before they occur. Guides typically validate configuration,
// conventions, or structural rules without executing the full build.
type Guide interface {
	// Name returns a unique, human-readable identifier for the guide.
	Name() string
	// Category returns the regulation category this guide covers.
	Category() Category
	// Mode returns the execution mode (computational or inferential).
	Mode() ExecutionMode
	// Check validates the system against the guide's rules.
	Check() ([]Finding, error)
}

// Reporter formats and emits the aggregated harness results. Implementations
// may write to stdout, a file, JSON, or any other sink.
type Reporter interface {
	// Report renders the results of a harness run.
	Report(result Result) error
}

// --- Result ---------------------------------------------------------------

// Result captures the complete outcome of a harness run.
type Result struct {
	StartedAt   time.Time
	FinishedAt  time.Time
	Duration    time.Duration
	SensorCount int
	GuideCount  int
	Findings    []Finding
	Errors      []error
}

// Passed reports whether the overall run has no error-severity findings and
// no execution errors.
func (r Result) Passed() bool {
	if len(r.Errors) > 0 {
		return false
	}
	for _, f := range r.Findings {
		if !f.Pass() {
			return false
		}
	}
	return true
}

// ErrorCount returns the number of error-severity findings.
func (r Result) ErrorCount() int {
	count := 0
	for _, f := range r.Findings {
		if f.Severity == SeverityError {
			count++
		}
	}
	return count
}

// WarningCount returns the number of warning-severity findings.
func (r Result) WarningCount() int {
	count := 0
	for _, f := range r.Findings {
		if f.Severity == SeverityWarning {
			count++
		}
	}
	return count
}

// FindingsByCategory groups findings by their regulation category.
func (r Result) FindingsByCategory() map[Category][]Finding {
	groups := make(map[Category][]Finding)
	for _, f := range r.Findings {
		groups[f.Category] = append(groups[f.Category], f)
	}
	return groups
}
