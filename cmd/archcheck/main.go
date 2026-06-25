// Command archcheck runs the architecture-fitness sensor against the real
// project tree and prints any dependency-rule violations. It is the CLI
// entry point for the architecture fitness harness.
//
// Usage:
//
//	archcheck                  # scan current directory
//	archcheck -root ./internal # scan a specific directory
//	archcheck -json            # output findings as JSON
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"bedrock-ai/internal/harness"
	"bedrock-ai/internal/harness/architecture"
)

func main() {
	root := flag.String("root", ".", "root directory to scan")
	jsonOut := flag.Bool("json", false, "output findings as JSON")
	flag.Parse()

	sensor := architecture.New("bedrock-ai", architecture.WithRootDir(*root))
	findings, err := sensor.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "architecture sensor error: %v\n", err)
		os.Exit(2)
	}

	if *jsonOut {
		if err := json.NewEncoder(os.Stdout).Encode(findings); err != nil {
			fmt.Fprintf(os.Stderr, "encode JSON: %v\n", err)
			os.Exit(2)
		}
	} else {
		report(sensor, findings)
	}

	if hasErrors(findings) {
		os.Exit(1)
	}
}

func report(sensor harness.Sensor, findings []harness.Finding) {
	fmt.Printf("=== Architecture Fitness Sensor ===\n")
	fmt.Printf("Sensor: %s | Category: %s | Mode: %s\n",
		sensor.Name(), sensor.Category(), sensor.Mode())
	fmt.Printf("Findings: %d\n\n", len(findings))

	for _, f := range findings {
		fmt.Println(f.String())
		if f.Suggest != "" {
			fmt.Printf("  → %s\n", f.Suggest)
		}
	}

	if len(findings) == 0 {
		fmt.Println("✓ No architecture violations found.")
	} else if hasErrors(findings) {
		fmt.Printf("\n✗ %d error-severity violations found.\n", countErrors(findings))
	} else {
		fmt.Printf("\n⚠ %d warning/info findings (no errors).\n", len(findings))
	}
}

func hasErrors(findings []harness.Finding) bool {
	for _, f := range findings {
		if f.Severity == harness.SeverityError {
			return true
		}
	}
	return false
}

func countErrors(findings []harness.Finding) int {
	count := 0
	for _, f := range findings {
		if f.Severity == harness.SeverityError {
			count++
		}
	}
	return count
}
