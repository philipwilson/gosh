# gosh

An educational Unix shell implemented in Go. Follows bash semantics using explicit system calls (`os.StartProcess`, `syscall.Pipe`, `syscall.Dup2`, `syscall.Wait4`, ...) — no libraries outside the Go standard library.

~21,000 lines of Go. 680+ tests. Zero external dependencies.

## Why

Most shell tutorials stop at pipes. gosh goes further — it implements the full bash language (compound commands, arrays, traps, job control, parameter expansion, brace expansion, extended globs, ...) while keeping the plumbing visible. Every `fork`, `exec`, `pipe`, `dup2`, and `tcsetpgrp` is an explicit syscall, not hidden behind a library.

## Build

```bash
go build -ldflags "-X main.version=$(git describe --tags --always --dirty)" -o gosh .
```

Or simply:

```bash
go install .
```

Requires Go 1.22+. Runs on macOS and Linux (platform-specific terminal ioctls in `editor/terminal_darwin.go` and `editor/terminal_linux.go`).

## Usage

```bash
./gosh                      # interactive shell
./gosh script.sh            # run a script
./gosh -c 'echo hello'      # run a command string
echo 'ls | head' | ./gosh   # piped input
```

## What's implemented

### Core language
- Pipes, redirects (`<`, `>`, `>>`, `2>&1`, `&>`, `&>>`), here documents (`<<`, `<<-`, `<<'DELIM'`), here strings (`<<<`)
- `&&`, `||`, `;`, `&` (background), subshells `( )`, brace groups `{ }`
- `if`/`elif`/`else`/`fi`, `while`, `until`, `for`, `for (( ))`, `case`/`esac`, `select`
- Functions with `local`, dynamic scoping, recursion, `return`
- Single quotes, double quotes, `$'...'` ANSI-C quoting, backslash escapes

### Expansions
- Brace expansion: `{a,b,c}`, `{1..10}`, `{a..z}`, nesting
- Tilde expansion: `~`, `~user`
- Parameter expansion: `${var:-default}`, `${var:=assign}`, `${var:+alt}`, `${var:?err}`, `${#var}`, `${var#pat}`, `${var##pat}`, `${var%pat}`, `${var%%pat}`, `${var/find/rep}`, `${var//find/rep}`, `${var:offset:length}`, `${var^}`, `${var^^}`, `${var,}`, `${var,,}`, `${!var}` indirect, `${!arr[@]}` keys
- Arithmetic: `$(( ))`, `(( ))`, `let` — full C-like operators including ternary, assignment, pre/post increment
- Command substitution: `$(cmd)` (nested), `` `cmd` ``
- Process substitution: `<(cmd)`, `>(cmd)`
- Word splitting (POSIX IFS rules), pathname expansion (globbing)
- Extended globs via `shopt -s extglob`: `?(pat)`, `*(pat)`, `+(pat)`, `@(pat)`, `!(pat)`

### Arrays
- Indexed arrays: `arr=(a b c)`, `arr+=(d)`, `arr[N]=val`, `${arr[@]}`, `${#arr[@]}`
- Associative arrays: `declare -A map`, `map=([key]=val)`, `${map[key]}`, `${!map[@]}`

### Job control
- Background jobs (`&`), Ctrl-Z (stop), `jobs`, `fg`, `bg`, `wait`, `kill`, `disown`
- Process groups, terminal foreground control (`tcsetpgrp`)

### Builtins
`cd`, `pwd`, `echo`, `printf`, `test`/`[`, `read`, `export`, `unset`, `local`, `declare`/`typeset`/`readonly`, `alias`/`unalias`, `set`, `shopt`, `eval`, `source`/`.`, `exec`, `trap`, `shift`, `getopts`, `let`, `command`, `type`, `jobs`, `fg`, `bg`, `wait`, `kill`, `disown`, `history`, `help`, `true`, `false`, `exit`, `break`, `continue`, `return`

### Tests
- `test`/`[` with string, integer, and file tests
- `[[ ]]` with pattern matching (`==`, `!=`), regex (`=~` with `BASH_REMATCH`), `-v` variable test

### Other
- `set -euxo pipefail`, `PIPESTATUS` array
- Traps: `INT`, `TERM`, `HUP`, `QUIT`, `USR1`, `USR2` + `EXIT`, `ERR`, `RETURN` pseudo-signals
- `SIGWINCH` handling — terminal resize updates `LINES`/`COLUMNS` and redraws the editor
- Emacs-style line editing, tab completion (commands + filenames), persistent history
- `~/.goshrc` startup file
- `$RANDOM`, `$SECONDS`, `$$`, `$!`, `$?`, `$#`, `$@`

## Architecture

```
Input → Lexer → []Token → Heredoc Resolution → Parser → AST → Executor
                                                              (expands lazily)
```

Each phase is a separate package with explicit data passed between them:

| Package | Purpose |
|---------|---------|
| `lexer/` | Tokenizer. Handles quoting, operators, heredocs, process substitution. Each word carries `[]WordPart` with quoting context. |
| `parser/` | Recursive descent. Produces AST nodes: `SimpleCmd`, `IfCmd`, `WhileCmd`, `ForCmd`, `CaseCmd`, `FuncDef`, `ArithCmd`, `SubshellCmd`, `DblBracketCmd`, ... |
| `expander/` | Seven-phase expansion: brace → tilde → arithmetic → command substitution → variables → word splitting → globs. |
| `editor/` | Line editor with raw terminal control, emacs key bindings, history, tab completion, SIGWINCH handling. |
| `main.go` | REPL, script execution, alias expansion, multi-line input detection. |
| `exec.go` | AST walker. Spawns processes, wires pipes, applies redirects, manages process groups. |
| `builtins.go` | All builtin commands. |
| `jobs.go` | Job table, `fg`/`bg`/`wait` mechanics. |
| `terminal.go` | `isatty`, `tcsetpgrp` ioctls. |
| `dblbracket.go` | `[[ ]]` evaluator. |
| `test_builtin.go` | `test`/`[` evaluator. |
| `traps.go` | Signal name/number tables. |

## Testing

```bash
go test ./...                              # all tests
go test ./lexer/ -v                        # one package
go test ./lexer/ -run TestSingleQuotes     # one test
```

Interactive smoke test:

```bash
printf 'echo hello | tr a-z A-Z\nexit\n' | ./gosh
# HELLO
```

## License

MIT
