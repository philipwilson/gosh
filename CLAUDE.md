# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

gosh is an educational Unix shell implemented in Go. It follows bash semantics and uses explicit system calls (no libraries outside the Go standard library). Clarity over cleverness.

## Commands

```bash
go build -ldflags "-X main.version=$(git describe --tags --always --dirty)" -o gosh .  # build with version
go build -o gosh .        # build (version defaults to "dev")
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

- **Lexer** (`lexer/`): Converts raw input to tokens. Handles single quotes (literal), double quotes (with `\` escapes for `" \ $ \``), backslash escapes, comments (unquoted `#` skips rest of line), and operator recognition (`|`, `<`, `>`, `>>`, `>&N`, `<&N`, `;`, `&&`, `||`, `&`). A single digit before `>`, `>>`, or `<` is absorbed as the fd number (e.g., `2>file`). Each WORD token carries `Parts []WordPart` preserving quoting context (`Unquoted`, `SingleQuoted`, `DoubleQuoted`, `CmdSubst`, `CmdSubstDQ`) for the expander. `$(cmd)` supports nesting and respects internal quoting; `` `cmd` `` supports `\` escapes. Redirect tokens carry an `Fd` field (-1 = use default).

- **Parser** (`parser/`): Recursive descent parser producing an AST. Grammar: `list → pipeline ((; | && | || | &) pipeline)*`, `pipeline → command (| command)*`, `command → (assign)* (word | redirect)+`. AST nodes: `List` (sequence with operators), `Pipeline` (pipe-connected commands), `SimpleCmd` (assignments + args as `[]lexer.Word` + redirections). Recognizes `NAME=VALUE` assignments before command words. `&` marks the preceding pipeline for background execution.

- **Expander** (`expander/`): Four-phase expansion on the AST: (1) tilde expansion (`~` → `$HOME`, `~user` → user's home dir, only unquoted); (2) command substitution (`$(cmd)` and `` `cmd` ``) via a `SubstFunc` callback that recursively lex→parse→expand→executes, capturing stdout through a pipe — `CmdSubst` results are Unquoted (subject to globs), `CmdSubstDQ` results are DoubleQuoted (no globs); (3) variable expansion (`$VAR`, `${VAR}`, `$?`, `$$`) respecting quoting — no expansion in SingleQuoted parts; (4) glob expansion (`*`, `?`, `[...]`) on unquoted args only, using `filepath.Glob`. Builds glob patterns that escape metacharacters in quoted parts.

- **Executor** (`exec.go`): Walks the AST. Spawns processes via `os.StartProcess` with `SysProcAttr{Setpgid: true}` for process group isolation. Wires pipes with `os.Pipe()`. Applies redirections (`<`, `>`, `>>`, `2>`, `2>&1`, etc.) using a `[3]*os.File` fd table (stdin/stdout/stderr). Manages terminal foreground group via `tcsetpgrp` (TIOCSPGRP ioctl). Runs builtins in-process for standalone commands; in pipelines they fall through to external lookup. Implements `&&`/`||` short-circuit evaluation. Per-command assignments are temporary for builtins (save/restore). Uses `syscall.Wait4` with `WUNTRACED` to detect stopped processes (Ctrl-Z). Background pipelines (`&`) run without terminal foreground and are tracked in the job table.

## Key Design Details

- `os.StartProcess` / `syscall` for process management, not `os/exec.Cmd` — the plumbing should be visible. Source split: `main.go` (state/REPL), `exec.go` (execution), `builtins.go` (builtins), `jobs.go` (job table), `terminal.go` (ioctl wrappers), `complete.go` (tab completion)
- `exec.LookPath` is used for PATH resolution
- Phases are separate packages with explicit data passed between them (tokens, AST nodes with `lexer.Word` parts)
- Redirections override pipe defaults (e.g., `sort < file | head` uses file as sort's stdin). Supports fd-specific redirects (`2>file`, `2>>file`) and fd duplication (`2>&1`, `>&2`)
- Exit status: last pipeline command determines status; signal kills → 128+signum
- Process groups: each pipeline gets its own group; SIGTTOU uses `signal.Ignore` (SIG_IGN required for `tcsetpgrp` from background); SIGINT/SIGTSTP use `signal.Notify` (caught handler resets to SIG_DFL across exec, so children respond to Ctrl-C/Ctrl-Z); SIGCHLD left at SIG_DFL
- Terminal control only when interactive (`isatty` via TIOCGPGRP probe)
- Job control: stopped (Ctrl-Z) and background (`&`) jobs tracked in a job table (`jobs.go`). `fg` resumes in foreground with `SIGCONT` + `tcsetpgrp`, `bg` resumes in background. Finished background jobs are reaped before each prompt via `Wait4(-1, WNOHANG)`.
- Builtins (cd, pwd, echo, exit, export, unset, true, false, history, jobs, fg, bg) run in-process with redirect support; cd supports `-` (OLDPWD) and updates PWD/OLDPWD
- Debug builtins (`debug-tokens`, `debug-ast`, `debug-expanded`) toggle printing of tokens, pre-expansion AST, and post-expansion AST to stderr
- `shellState` holds variables, export set, last exit status, editor, terminal info, and job table

## Line Editor

The `editor/` package provides interactive line editing and command history:

- **Terminal control** (`terminal_darwin.go`, `terminal_linux.go`): Platform-specific `tcgetattr`/`tcsetattr` via ioctl. Raw mode disables ICANON, ECHO, ISIG, IEXTEN, ICRNL, OPOST; sets VMIN=1/VTIME=0. Raw mode is active only during editing — restored before running child processes.
- **History** (`history.go`): Persists to `~/.gosh_history`. Skips consecutive duplicates and blank lines. Capped at 1000 entries. File created with mode 0600.
- **Editor** (`editor.go`): Emacs-style key bindings — Ctrl-A/E (Home/End), Ctrl-B/F (Left/Right), Ctrl-K/U (kill to EOL/BOL), Ctrl-W (kill word), Ctrl-L (clear screen), Ctrl-C (cancel), Ctrl-D (EOF on empty / delete char). Up/Down arrows navigate history. Escape sequences decoded for arrow keys, Home/End, Delete.
- **Tab completion** (`editor.go` + `complete.go`): Editor accepts a `CompleteFunc` callback from the shell. Command-position words (first word or after `|`, `;`, `&`) complete from builtins + PATH executables. Argument-position words complete filenames (directories get `/` suffix). Single match inserts directly with trailing space or `/`. Multiple matches insert the longest common prefix; double-tab displays all candidates in columns (using `TIOCGWINSZ` for terminal width).
- Non-interactive mode (piped input) falls back to `bufio.Scanner` with no editing.
