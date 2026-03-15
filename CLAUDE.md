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

Test interactively: `printf 'echo hello | tr a-z A-Z\nexit\n' | ./gosh`

## Architecture

Shell input flows through four phases, each with a clean boundary:

```
Input → Lexer → []Token → Parser → AST → Expander → Executor
```

- **Lexer** (`lexer/`): Converts raw input to tokens. Handles single quotes (literal), double quotes (with `\` escapes for `" \ $ \``), backslash escapes, and operator recognition (`|`, `<`, `>`, `>>`, `;`, `&&`, `||`). Each WORD token carries `Parts []WordPart` preserving quoting context (`Unquoted`, `SingleQuoted`, `DoubleQuoted`) for the expander.

- **Parser** (`parser/`): Recursive descent parser producing an AST. Grammar: `list → pipeline ((; | && | ||) pipeline)*`, `pipeline → command (| command)*`, `command → (assign)* (word | redirect)+`. AST nodes: `List` (sequence with operators), `Pipeline` (pipe-connected commands), `SimpleCmd` (assignments + args as `[]lexer.Word` + redirections). Recognizes `NAME=VALUE` assignments before command words.

- **Expander** (`expander/`): Two-phase expansion on the AST: (1) variable expansion (`$VAR`, `${VAR}`, `$?`, `$$`) respecting quoting — no expansion in SingleQuoted parts; (2) glob expansion (`*`, `?`, `[...]`) on unquoted args only, using `filepath.Glob`. Builds glob patterns that escape metacharacters in quoted parts.

- **Executor** (`main.go`): Walks the AST. Spawns processes via `os.StartProcess` with `SysProcAttr{Setpgid: true}` for process group isolation. Wires pipes with `os.Pipe()`. Applies `<`, `>`, `>>` redirections. Manages terminal foreground group via `tcsetpgrp` (TIOCSPGRP ioctl). Runs builtins in-process for standalone commands; in pipelines they fall through to external lookup. Implements `&&`/`||` short-circuit evaluation.

## Key Design Details

- `os.StartProcess` / `syscall` for process management, not `os/exec.Cmd` — the plumbing should be visible
- `exec.LookPath` is used for PATH resolution
- Phases are separate packages with explicit data passed between them (tokens, AST nodes with `lexer.Word` parts)
- Redirections override pipe defaults (e.g., `sort < file | head` uses file as sort's stdin)
- Exit status: last pipeline command determines status; signal kills → 128+signum
- Process groups: each pipeline gets its own group; shell ignores SIGINT/SIGTSTP/SIGTTOU
- Terminal control only when interactive (`isatty` via TIOCGPGRP probe)
- Builtins (cd, pwd, echo, exit, export, unset, true, false) run in-process with redirect support
- `shellState` holds variables, export set, last exit status, and terminal info
