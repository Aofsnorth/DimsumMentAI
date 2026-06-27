// Command harness is the unified CLI entry point for the DimsumMentAI
// engineering harness. It wires up all registered sensors and guides into
// the Runner, executes them concurrently, and reports the results.
//
// Usage:
//
//	harness                  # run all sensors & guides, text output
//	harness -json            # output results as JSON
//	harness -sensor arch     # run only the architecture sensor
//	harness -guide license   # run only the license guide
//	harness -list            # list all registered checks
//
// The command exits with code 0 if all checks pass, 1 if any error-severity
// findings are reported, and 2 on a fatal execution error.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"bedrock-ai/internal/harness"
	"bedrock-ai/internal/harness/architecture"
	"bedrock-ai/internal/harness/complexity"
	"bedrock-ai/internal/harness/filesize"
	"bedrock-ai/internal/harness/license"
	"bedrock-ai/internal/harness/tododebt"
)

const moduleName = "bedrock-ai"

func main() {
	jsonOut := flag.Bool("json", false, "output findings as JSON")
	listOnly := flag.Bool("list", false, "list all registered checks and exit")
	sensorFilter := flag.String("sensor", "", "run only the sensor with this name (substring match)")
	guideFilter := flag.String("guide", "", "run only the guide with this name (substring match)")
	rootDir := flag.String("root", ".", "root directory to scan")
	flag.Parse()

	// --- Build sensors and guides ---
	var runner *harness.Runner

	// Sensors (feedback controls)
	archSensor := architecture.New(moduleName, architecture.WithRootDir(*rootDir))

	fsCfg := filesize.DefaultConfig()
	fsCfg.RootDir = *rootDir
	fileSizeSensor := filesize.New(filesize.WithConfig(fsCfg))

	todoCfg := tododebt.DefaultConfig()
	todoCfg.RootDir = *rootDir
	todoSensor := tododebt.New(tododebt.WithConfig(todoCfg))

	ccCfg := complexity.DefaultConfig()
	ccCfg.RootDir = *rootDir
	complexitySensor := complexity.New(complexity.WithConfig(ccCfg))

	// Guides (feedforward controls)
	licCfg := license.DefaultConfig()
	licCfg.RootDir = *rootDir
	licenseGuide := license.New(license.WithConfig(licCfg))

	// Register all checks.
	allSensors := []harness.Sensor{archSensor, fileSizeSensor, todoSensor, complexitySensor}
	allGuides := []harness.Guide{licenseGuide}

	if *listOnly {
		listChecks(allSensors, allGuides)
		return
	}

	// Set reporter based on output mode.
	var reporter harness.Reporter
	if *jsonOut {
		reporter = harness.NewJSONReporter(os.Stdout)
	} else {
		reporter = harness.NewConsoleReporter(func(s string) (int, error) {
			fmt.Println(s)
			return len(s), nil
		})
	}
	runner = harness.NewRunner(reporter)

	// Apply filters.
	for _, s := range allSensors {
		if *sensorFilter == "" || strings.Contains(s.Name(), *sensorFilter) {
			runner.RegisterSensor(s)
		}
	}
	for _, g := range allGuides {
		if *guideFilter == "" || strings.Contains(g.Name(), *guideFilter) {
			runner.RegisterGuide(g)
		}
	}

	result, err := runner.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "harness error: %v\n", err)
		os.Exit(2)
	}

	if !result.Passed() {
		if !*jsonOut {
			fmt.Fprintf(os.Stderr, "\n✗ Harness FAILED with %d error(s).\n", result.ErrorCount())
		}
		os.Exit(1)
	}

	if !*jsonOut {
		fmt.Printf("\n✓ Harness PASSED — %d sensor(s), %d guide(s), %d warning(s), %d info.\n",
			result.SensorCount, result.GuideCount, result.WarningCount(), len(result.Findings)-result.ErrorCount()-result.WarningCount())
	}
}

// listChecks prints all registered sensors and guides.
func listChecks(sensors []harness.Sensor, guides []harness.Guide) {
	fmt.Println("Registered Sensors (feedback):")
	for _, s := range sensors {
		fmt.Printf("  %-35s [%s, %s]\n", s.Name(), s.Category(), s.Mode())
	}
	fmt.Println("\nRegistered Guides (feedforward):")
	for _, g := range guides {
		fmt.Printf("  %-35s [%s, %s]\n", g.Name(), g.Category(), g.Mode())
	}
}
