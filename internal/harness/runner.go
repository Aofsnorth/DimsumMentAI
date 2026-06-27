package harness

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// Runner orchestrates a collection of sensors and guides, executes them
// concurrently, aggregates findings, and emits results through a reporter.
//
// It depends only on the Sensor, Guide, and Reporter abstractions (DIP),
// and accepts new checks without modification (OCP).
type Runner struct {
	sensors  []Sensor
	guides   []Guide
	reporter Reporter
}

// NewRunner creates a Runner with the given reporter. The reporter must not
// be nil — use a NullReporter if no output is desired.
func NewRunner(reporter Reporter) *Runner {
	if reporter == nil {
		reporter = NullReporter{}
	}
	return &Runner{reporter: reporter}
}

// RegisterSensor adds a sensor to the runner. Sensors are executed
// concurrently on Run.
func (r *Runner) RegisterSensor(s Sensor) {
	if s == nil {
		return
	}
	r.sensors = append(r.sensors, s)
}

// RegisterGuide adds a guide to the runner. Guides are executed concurrently
// on Run.
func (r *Runner) RegisterGuide(g Guide) {
	if g == nil {
		return
	}
	r.guides = append(r.guides, g)
}

// Run executes all registered sensors and guides concurrently, aggregates
// findings, and reports the result. Returns the result and any fatal error
// that prevented the harness from running (individual check errors are
// captured in Result.Errors).
func (r *Runner) Run() (Result, error) {
	started := time.Now()

	type checkResult struct {
		findings []Finding
		err      error
		name     string
	}

	totalChecks := len(r.sensors) + len(r.guides)
	results := make([]checkResult, totalChecks)

	var wg sync.WaitGroup
	idx := 0

	// Run sensors concurrently.
	for _, s := range r.sensors {
		wg.Add(1)
		go func(i int, sensor Sensor) {
			defer wg.Done()
			findings, err := sensor.Run()
			results[i] = checkResult{findings: findings, err: err, name: sensor.Name()}
		}(idx, s)
		idx++
	}

	// Run guides concurrently.
	for _, g := range r.guides {
		wg.Add(1)
		go func(i int, guide Guide) {
			defer wg.Done()
			findings, err := guide.Check()
			results[i] = checkResult{findings: findings, err: err, name: guide.Name()}
		}(idx, g)
		idx++
	}

	wg.Wait()

	result := Result{
		StartedAt:   started,
		FinishedAt:  time.Now(),
		SensorCount: len(r.sensors),
		GuideCount:  len(r.guides),
	}

	for _, cr := range results {
		if cr.err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("%s: %w", cr.name, cr.err))
		}
		result.Findings = append(result.Findings, cr.findings...)
	}

	result.Duration = time.Since(started)

	if err := r.reporter.Report(result); err != nil {
		return result, fmt.Errorf("report: %w", err)
	}

	return result, nil
}

// --- Reporters ------------------------------------------------------------

// ConsoleReporter writes a human-readable summary to an io.Writer-like
// interface. It implements Reporter.
type ConsoleReporter struct {
	write func(string) (int, error)
}

// NewConsoleReporter creates a reporter that writes via the provided write
// function (e.g. fmt.Println or log.Output).
func NewConsoleReporter(write func(string) (int, error)) *ConsoleReporter {
	return &ConsoleReporter{write: write}
}

// Report implements Reporter.
func (c *ConsoleReporter) Report(result Result) error {
	header := fmt.Sprintf(
		"=== Harness Report ===\n"+
			"Duration: %s | Sensors: %d | Guides: %d\n"+
			"Errors: %d | Warnings: %d | Total findings: %d\n",
		result.Duration.Round(time.Millisecond),
		result.SensorCount,
		result.GuideCount,
		result.ErrorCount(),
		result.WarningCount(),
		len(result.Findings),
	)
	if _, err := c.write(header); err != nil {
		return err
	}

	for _, f := range result.Findings {
		if _, err := c.write(f.String()); err != nil {
			return err
		}
	}

	for _, e := range result.Errors {
		if _, err := c.write(fmt.Sprintf("[EXECUTION ERROR] %s", e)); err != nil {
			return err
		}
	}

	status := "PASS"
	if !result.Passed() {
		status = "FAIL"
	}
	_, err := c.write(fmt.Sprintf("Result: %s\n", status))
	return err
}

// NullReporter discards all output. Useful when the caller inspects the
// returned Result directly.
type NullReporter struct{}

// Report implements Reporter — does nothing.
func (NullReporter) Report(Result) error { return nil }

// --- JSONReporter ---------------------------------------------------------

// jsonResult is the JSON-serialisable representation of a harness Result.
type jsonResult struct {
	StartedAt   time.Time     `json:"started_at"`
	FinishedAt  time.Time     `json:"finished_at"`
	DurationMs  int64         `json:"duration_ms"`
	SensorCount int           `json:"sensor_count"`
	GuideCount  int           `json:"guide_count"`
	Passed      bool          `json:"passed"`
	Errors      []string      `json:"errors,omitempty"`
	Findings    []jsonFinding `json:"findings"`
}

type jsonFinding struct {
	Check     string `json:"check"`
	Category  string `json:"category"`
	Mode      string `json:"mode"`
	Direction string `json:"direction"`
	Severity  string `json:"severity"`
	File      string `json:"file,omitempty"`
	Line      int    `json:"line,omitempty"`
	Message   string `json:"message"`
	Suggest   string `json:"suggest,omitempty"`
}

// JSONReporter writes the harness result as JSON to an io.Writer. It
// implements Reporter and is suitable for machine-readable output (CI
// integration, SARIF conversion, dashboards).
type JSONReporter struct {
	w io.Writer
}

// NewJSONReporter creates a reporter that writes JSON to the given writer.
func NewJSONReporter(w io.Writer) *JSONReporter {
	return &JSONReporter{w: w}
}

// Report implements Reporter.
func (j *JSONReporter) Report(result Result) error {
	jr := jsonResult{
		StartedAt:   result.StartedAt,
		FinishedAt:  result.FinishedAt,
		DurationMs:  result.Duration.Milliseconds(),
		SensorCount: result.SensorCount,
		GuideCount:  result.GuideCount,
		Passed:      result.Passed(),
		Findings:    make([]jsonFinding, 0, len(result.Findings)),
	}
	for _, e := range result.Errors {
		jr.Errors = append(jr.Errors, e.Error())
	}
	for _, f := range result.Findings {
		jr.Findings = append(jr.Findings, jsonFinding{
			Check:     f.Check,
			Category:  string(f.Category),
			Mode:      string(f.Mode),
			Direction: string(f.Direction),
			Severity:  string(f.Severity),
			File:      f.File,
			Line:      f.Line,
			Message:   f.Message,
			Suggest:   f.Suggest,
		})
	}
	enc := json.NewEncoder(j.w)
	enc.SetIndent("", "  ")
	return enc.Encode(jr)
}
