# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

gosh is an educational Unix shell implemented in Go. It follows bash semantics and uses explicit system calls (no libraries outside the Go standard library). Clarity over cleverness.

## Commands

```bash
go build -o gosh .        # build
go test ./...              # run all tests
go test ./lexer/ -v        # run one package's tests
go test ./lexer/ -run TestSingleQuotes  # run a single test
```

Test interactively: `echo -e 'echo hello | tr a-z A-Z\nexit' | ./gosh`

## Architecture

Shell input flows through four phases, each with a clean boundary:

```
Input → Lexer → []Token → Parser → AST → [Expander] → Executor
```

- **Lexer** (`lexer/`): Converts raw input to tokens. Handles single quotes (literal), double quotes (with `\` escapes for `" \ $ \``), backslash escapes, and operator recognition (`|`, `<`, `>`, `>>`, `;`, `&&`, `||`).

- **Parser** (`parser/`): Recursive descent parser producing an AST. Grammar: `list → pipeline ((; | && | ||) pipeline)*`, `pipeline → command (| command)*`, `command → (word | redirect)+`. AST nodes: `List` (sequence with operators), `Pipeline` (pipe-connected commands), `SimpleCmd` (args + redirections).

- **Executor** (`main.go`): Walks the AST. Spawns processes via `os.StartProcess` (not `os/exec.Cmd`). Wires pipes with `os.Pipe()`. Applies `<`, `>`, `>>` redirections by opening files and passing them as the child's stdin/stdout. All pipe fds are closed in the parent after children start to ensure EOF propagation.

## Design Principles

- Use `os.StartProcess` / `syscall` for process management, not `os/exec.Cmd` — the plumbing should be visible
- `exec.LookPath` is currently used for PATH resolution (will be replaced with manual walk)
- Phases are separate packages with explicit data passed between them (tokens, AST nodes)
- Redirections override pipe defaults (e.g., `sort < file | head` uses file as sort's stdin)
- Exit status follows bash convention: last command in pipeline determines status

## Planned Milestones

M1–M5 (done): REPL, lexer, parser, pipes, redirections. Remaining: M6 (variables/environment), M7 (glob expansion), M8 (signals/process groups), M9 (builtins), M10 (control flow: `&&`, `||`, `;` semantics).
