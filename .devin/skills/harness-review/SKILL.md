---
name: harness-review
description: >
  Inferential code review sensor for the DimsumMentAI harness engineering
  system. Performs semantic analysis that computational sensors cannot
  catch: misdiagnosis, brute-force fixes, over-engineering, redundant tests,
  and missing edge cases. Use when the user says "harness review", "review
  against harness", "inferential review", or after running `make harness`
  to get a deeper semantic check beyond linters and tests.
triggers:
  - harness review
  - review against harness
  - inferential review
  - semantic code review
  - deep review
---

# Harness Review — Inferential Sensor

## Purpose

This skill is the **inferential sensor** in the DimsumMentAI harness
engineering system. While computational sensors (linters, tests, arch
checks) catch structural issues deterministically, this sensor catches
**semantic** issues that require judgment:

- Misdiagnosis of root causes (fixing symptoms, not causes)
- Brute-force fixes (try/catch swallowing, nil checks instead of fixing logic)
- Over-engineered solutions (speculative abstractions, dead flexibility)
- Redundant or semantically duplicate tests
- Missing edge cases that computational tests don't cover
- Architecture violations that aren't expressible as import rules

## When to Use

Run this skill:
1. **After** `make harness` passes — this is a deeper check on top of
   computational sensors.
2. **Before** creating a PR — to catch semantic issues early.
3. **When** investigating a bug — to verify the fix addresses the root cause.

## Review Checklist

### 1. Root Cause Analysis (not symptom masking)
- [ ] Does the fix address the actual root cause, or just suppress the symptom?
- [ ] Are error paths handled with meaningful action, not just `return nil`?
- [ ] Is the fix specific to the bug, or does it over-broadly affect other paths?

### 2. SOLID Compliance
- [ ] **SRP**: Does each new function/struct have one responsibility?
- [ ] **OCP**: Can the code be extended without modification?
- [ ] **LSP**: Are interface implementations substitutable?
- [ ] **ISP**: Are interfaces small and focused?
- [ ] **DIP**: Does the code depend on abstractions, not concretions?

### 3. Clean Code
- [ ] Are names meaningful and consistent with codebase conventions?
- [ ] Is complexity (cyclomatic, cognitive) reasonable for the function?
- [ ] Are there dead code paths or unreachable branches?
- [ ] Is error handling at the right boundary (not try/catch everywhere)?

### 4. Test Quality
- [ ] Does the test actually test the behavior, not just the implementation?
- [ ] Are edge cases covered (empty, nil, max, negative, concurrent)?
- [ ] Is `t.Parallel()` used for concurrency safety?
- [ ] Does the test document the expected behavior clearly?

### 5. Architecture Fitness (semantic)
- [ ] Does the change respect the intended layering?
- [ ] Are there new circular dependency risks?
- [ ] Is the change in the right package (not leaking logic across layers)?

### 6. Harness Steering Loop
- [ ] If this fixes a bug, is there a test that reproduces it?
- [ ] If this is a recurring pattern, should a linter rule or AGENTS.md
      rule be added to prevent it in the future?
- [ ] Is the change documented if it introduces a new pattern?

## Output Format

For each finding, report:

```
[SEVERITY] Category: Brief description
  File: path/to/file.go:line
  Issue: What's wrong (semantic, not just structural)
  Suggest: How to fix it (actionable instruction for self-correction)
```

Severities: `ERROR` (must fix), `WARN` (should fix), `INFO` (consider).

## Integration with Harness

This skill complements the computational harness:
- `make harness` → computational guides + sensors (fast, deterministic)
- `harness-review` skill → this inferential sensor (semantic, judgment-based)

The steering loop: when this sensor finds a recurring issue, encode it as a
computational rule (linter config, architecture rule, or AGENTS.md rule) so
future occurrences are caught automatically.
