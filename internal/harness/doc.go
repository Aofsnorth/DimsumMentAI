// Package harness provides the engineering harness for the DimsumMentAI
// Minecraft Bedrock AI project.
//
// Harness Engineering is the discipline of building scaffolding around a
// system to regulate it towards a desired state. It combines:
//
//   - Guides (feedforward controls): anticipate issues and steer the system
//     before they occur — linters, formatting rules, conventions.
//   - Sensors (feedback controls): observe the system after changes and
//     report deviations — tests, architecture checks, coverage analysis.
//
// Both guides and sensors can be computational (deterministic, fast) or
// inferential (semantic, AI-based). This package defines the core
// abstractions that let the project compose, run, and report on any
// combination of harness checks.
//
// The three regulation categories implemented:
//
//   - Maintainability: code quality, complexity, style.
//   - Architecture fitness: module boundaries, dependency direction.
//   - Behaviour: functional correctness via tests.
//
// Design follows SOLID principles:
//
//   - Single Responsibility: each Sensor checks one dimension.
//   - Open/Closed: new sensors implement the Sensor interface without
//     modifying the Runner.
//   - Liskov: sensors are substitutable through the Sensor interface.
//   - Interface Segregation: Sensor, Guide, and Reporter are separate
//     small interfaces.
//   - Dependency Inversion: the Runner depends on Sensor and Reporter
//     abstractions, not concrete implementations.
package harness
