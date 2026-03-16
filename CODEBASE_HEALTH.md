# Codebase Health

## Health Risks

- The central `shellState` object carries environment data, control-flow flags, traps, jobs, functions, prompt state, and editor state. That concentration of mutable state increases coupling and makes changes riskier. Recent refactoring (state.go/traps.go split, clone() extraction) has reduced this; further subsystem extraction (e.g., trap manager) has limited value.
- Interactive and runtime-heavy surfaces are under-tested, especially the line editor, completion, terminal/ioctl behavior, signal handling, and job control.

## Resolved

- ~~Job control marks a pipeline as done when an individual PID exits.~~ Fixed: per-PID tracking with `reaped[]bool` and `statuses[]int`; job is Done only when all PIDs are reaped. Duplicate Wait4 logic in `fg` consolidated into `job.waitPids()`.
- ~~Signal trap handling is not synchronized.~~ Verified: `pendingSignals` and `traps` are fully mutex-protected. Minor gap: `trapRunning` bool is unprotected, but risk is negligible (trap execution is single-threaded).
- ~~Script/stdin scanners don't check errors or handle long lines.~~ Verified: all Scanner sites have 1MB buffers and check `scanner.Err()`.
- ~~shellState cloning duplicated across execSubshell and cloneShellState.~~ Fixed: shared `clone()` method on shellState.

## Priority Next Steps

1. Add higher-level integration coverage for editor, completion, traps, and job control, then enforce `go test`, `go test -race`, and `go vet` in CI.
