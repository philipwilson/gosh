package main

import (
	"fmt"
	"os"
	"sort"
)

// builtinHelp holds the synopsis and long description for a builtin.
type builtinHelp struct {
	Synopsis string // e.g. "cd [dir]"
	Desc     string // multi-line description
}

// helpEntries maps builtin names to their help text.
var helpEntries = map[string]builtinHelp{
	".": {
		Synopsis: ". FILENAME [ARGS]",
		Desc: `Execute commands from FILENAME in the current shell.

    Read and execute commands from FILENAME in the current
    shell environment. The file does not need to be executable.
    Arguments are set as positional parameters.

    This is a synonym for 'source'.

    Exit Status:
    Returns the status of the last command executed from FILENAME.`,
	},
	"source": {
		Synopsis: "source FILENAME [ARGS]",
		Desc: `Execute commands from FILENAME in the current shell.

    Read and execute commands from FILENAME in the current
    shell environment. The file does not need to be executable.
    Arguments are set as positional parameters.

    Exit Status:
    Returns the status of the last command executed from FILENAME.`,
	},
	"[": {
		Synopsis: "[ EXPRESSION ]",
		Desc: `Evaluate conditional expression.

    This is a synonym for the 'test' builtin, but the last
    argument must be a literal ']' to match the opening '['.

    See 'help test' for details on supported expressions.`,
	},
	"alias": {
		Synopsis: "alias [name[=value] ...]",
		Desc: `Define or display aliases.

    Without arguments, prints all aliases in the form
    alias name='value'. With name=value, defines an alias.

    Aliases are expanded before parsing. If an alias value
    ends with a space, the next word is also alias-expanded.

    Exit Status:
    Returns 0 unless a name is not found.`,
	},
	"bg": {
		Synopsis: "bg [%jobspec]",
		Desc: `Resume a stopped job in the background.

    Place the job identified by JOBSPEC in the background,
    as if it had been started with '&'. If JOBSPEC is not
    supplied, the current job is used.

    Exit Status:
    Returns 0 unless job control is not enabled or an error occurs.`,
	},
	"break": {
		Synopsis: "break [n]",
		Desc: `Exit from a for, while, until, or select loop.

    Exit from within a FOR, WHILE, UNTIL, or SELECT loop.
    If N is specified, break N enclosing loops (not yet
    supported — currently always breaks one level).

    Exit Status:
    Returns 0 unless N is not greater than or equal to 1.`,
	},
	"cd": {
		Synopsis: "cd [dir]",
		Desc: `Change the current directory to DIR.

    The default DIR is the value of the HOME shell variable.
    'cd -' changes to the previous directory (OLDPWD).

    The variables PWD and OLDPWD are updated.

    Exit Status:
    Returns 0 if the directory is changed; non-zero otherwise.`,
	},
	"command": {
		Synopsis: "command [-vV] name [arg ...]",
		Desc: `Execute a command, bypassing function lookup.

    Runs COMMAND with ARGS, suppressing shell function lookup.
    With -v, prints the resolution of NAME (alias, builtin,
    file path). With -V, prints a verbose description.

    Exit Status:
    Returns the exit status of COMMAND, or 127 if not found.`,
	},
	"continue": {
		Synopsis: "continue [n]",
		Desc: `Resume the next iteration of a for, while, until, or select loop.

    Skip remaining commands in the current loop iteration and
    begin the next one.

    Exit Status:
    Returns 0.`,
	},
	"declare": {
		Synopsis: "declare [-aAirx] [-g] [name[=value] ...]",
		Desc: `Set variable attributes and values.

    Declare variables and give them attributes. If no names
    are given, display all variables with the given attributes.

    Options:
      -a    Indexed array
      -A    Associative array
      -i    Integer attribute (auto-evaluates assignments)
      -r    Readonly (cannot be unset or reassigned)
      -x    Export (passed to child processes)
      -g    Global (skip local scope in functions)
      -p    Display attributes and values

    Exit Status:
    Returns 0 unless an invalid option or error occurs.`,
	},
	"debug-ast": {
		Synopsis: "debug-ast [on|off]",
		Desc: `Toggle display of the AST before execution.

    When enabled, prints the parsed abstract syntax tree to
    stderr before each command is executed. Useful for
    understanding how the parser interprets your input.

    Without arguments, toggles the current state.

    Exit Status:
    Returns 0.`,
	},
	"debug-expanded": {
		Synopsis: "debug-expanded [on|off]",
		Desc: `Toggle display of the expanded AST before execution.

    When enabled, prints the AST after expansion (variable
    substitution, globbing, word splitting, etc.) to stderr.
    Useful for understanding how expansions transform commands.

    Without arguments, toggles the current state.

    Exit Status:
    Returns 0.`,
	},
	"debug-tokens": {
		Synopsis: "debug-tokens [on|off]",
		Desc: `Toggle display of tokens during lexing.

    When enabled, prints the token stream produced by the
    lexer to stderr. Useful for understanding how input is
    tokenized before parsing.

    Without arguments, toggles the current state.

    Exit Status:
    Returns 0.`,
	},
	"disown": {
		Synopsis: "disown [-a] [%jobspec ...]",
		Desc: `Remove jobs from the job table.

    Without arguments, removes the current job. Each JOBSPEC
    removes that job from the table so it will not be reported
    by 'jobs' or reaped on completion.

    Options:
      -a    Remove all jobs

    Exit Status:
    Returns 0 unless a JOBSPEC does not identify a valid job.`,
	},
	"echo": {
		Synopsis: "echo [-n] [arg ...]",
		Desc: `Write arguments to standard output.

    Display the ARGs, separated by spaces, followed by a newline.

    Options:
      -n    Do not append a trailing newline

    Exit Status:
    Returns 0.`,
	},
	"eval": {
		Synopsis: "eval [arg ...]",
		Desc: `Execute arguments as a shell command.

    Combine ARGs into a single string, then read and execute
    the result as a shell command.

    Exit Status:
    Returns the exit status of the executed command.`,
	},
	"exec": {
		Synopsis: "exec [-] [command [args ...]]",
		Desc: `Replace the shell with the given command.

    If COMMAND is supplied, the shell is replaced by it (no
    new process is created). If no COMMAND, redirections take
    effect in the current shell:
      exec > file    # redirect stdout permanently
      exec 2>&1      # merge stderr into stdout

    Exit Status:
    Returns 127 if COMMAND is not found; does not return on
    success (the shell process is replaced).`,
	},
	"exit": {
		Synopsis: "exit [n]",
		Desc: `Exit the shell.

    Exits the shell with a status of N. If N is omitted,
    the exit status is that of the last command executed.

    The EXIT trap is executed before the shell terminates.`,
	},
	"export": {
		Synopsis: "export [name[=value] ...]",
		Desc: `Set export attribute for shell variables.

    Mark each NAME for automatic export to the environment of
    subsequently executed commands. If VALUE is given, assign
    it before exporting.

    Without arguments, lists all exported variables.

    Exit Status:
    Returns 0 unless an invalid name is given.`,
	},
	"false": {
		Synopsis: "false",
		Desc: `Return an unsuccessful result.

    Exit Status:
    Always returns 1.`,
	},
	"fg": {
		Synopsis: "fg [%jobspec]",
		Desc: `Move a job to the foreground.

    Place the job identified by JOBSPEC in the foreground,
    making it the current job. If JOBSPEC is not supplied,
    the current job is used.

    Exit Status:
    Returns the status of the command placed in the foreground.`,
	},
	"getopts": {
		Synopsis: "getopts optstring name [arg ...]",
		Desc: `Parse option arguments.

    Used by shell scripts to parse positional parameters as
    options. OPTSTRING contains option letters; a colon after
    a letter means it takes an argument. Each call places the
    next option in NAME, the argument in OPTARG, and advances
    OPTIND.

    A leading ':' in OPTSTRING enables silent error reporting.

    Exit Status:
    Returns 0 if an option is found, 1 when options are exhausted.`,
	},
	"help": {
		Synopsis: "help [builtin]",
		Desc: `Display help for builtin commands.

    Without arguments, lists all builtin commands with short
    descriptions. With a BUILTIN name, shows detailed help
    for that command.

    Exit Status:
    Returns 0 unless BUILTIN is not found.`,
	},
	"history": {
		Synopsis: "history",
		Desc: `Display the command history list.

    Shows the command history with line numbers.

    Exit Status:
    Returns 0.`,
	},
	"jobs": {
		Synopsis: "jobs",
		Desc: `Display status of jobs.

    Lists the active jobs. Each job is shown with its job
    number, state (Running/Stopped/Done), and command text.

    Exit Status:
    Returns 0.`,
	},
	"kill": {
		Synopsis: "kill [-s signal | -signal] pid|%job ...",
		Desc: `Send a signal to a job or process.

    Send the specified SIGNAL to the specified PID or JOBSPEC.
    If no signal is specified, SIGTERM is sent.

    Use 'kill -l' to list all available signals.

    Job specs (%N) signal the entire process group.

    Exit Status:
    Returns 0 if at least one signal was successfully sent.`,
	},
	"let": {
		Synopsis: "let expression [expression ...]",
		Desc: `Evaluate arithmetic expressions.

    Each EXPRESSION is evaluated as an arithmetic expression.
    Supports standard C-like operators: +, -, *, /, %,
    comparisons, logical/bitwise operators, assignment (=,
    +=, -=, etc.), and pre/post increment/decrement.

    Exit Status:
    Returns 0 if the last expression evaluates to non-zero,
    1 if it evaluates to zero.`,
	},
	"local": {
		Synopsis: "local [name[=value] ...] [-a name] [-A name]",
		Desc: `Define local variables in a function.

    Create a local variable with optional VALUE. Local
    variables are visible only within the function and its
    callees (dynamic scoping). When the function returns,
    the previous value is restored.

    Options:
      -a    Declare as indexed array
      -A    Declare as associative array

    Exit Status:
    Returns 0 unless used outside a function or an error occurs.`,
	},
	"printf": {
		Synopsis: "printf FORMAT [ARGS ...]",
		Desc: `Format and print data.

    Write the formatted ARGS under control of the FORMAT string.

    Format specifiers: %s, %d, %x, %X, %o, %c, %%
    Escape sequences: \n, \t, \\, \0NNN, \xHH

    The format string is reused if there are more arguments
    than format specifiers.

    Exit Status:
    Returns 0.`,
	},
	"pwd": {
		Synopsis: "pwd",
		Desc: `Print the current working directory.

    Prints the absolute pathname of the current working
    directory.

    Exit Status:
    Returns 0 unless an error occurs.`,
	},
	"read": {
		Synopsis: "read [-r] [-p prompt] [-a array] [name ...]",
		Desc: `Read a line from standard input.

    Read a line and split it into fields using IFS. The first
    field is assigned to the first NAME, and so on, with any
    leftover fields assigned to the last NAME. With no NAMEs,
    the line is stored in REPLY.

    Options:
      -r           Do not treat backslashes as escape characters
      -p prompt    Print PROMPT before reading (to stderr)
      -a array     Assign words to ARRAY instead of NAMEs

    Exit Status:
    Returns 0 unless end-of-file is reached.`,
	},
	"readonly": {
		Synopsis: "readonly [name[=value] ...]",
		Desc: `Mark variables as read-only.

    Mark each NAME as readonly, so its value cannot be changed
    or unset. Equivalent to 'declare -r'.

    Without arguments, lists all readonly variables.

    Exit Status:
    Returns 0 unless an invalid name is given.`,
	},
	"return": {
		Synopsis: "return [n]",
		Desc: `Return from a shell function.

    Exit a function with a return value of N. If N is omitted,
    the return status is that of the last command.

    Exit Status:
    Returns N, or the status of the last command if N is omitted.`,
	},
	"set": {
		Synopsis: "set [-euxo option] [-- arg ...]",
		Desc: `Set or unset shell options and positional parameters.

    Without arguments, prints all shell variables. With options:

      -e    Exit immediately on command failure (errexit)
      -u    Treat unset variables as errors (nounset)
      -x    Print commands before execution (xtrace)
      -o pipefail  Return rightmost non-zero pipeline status

    Use '+' instead of '-' to unset an option.
    'set -- args' sets positional parameters.
    'set -o' lists all options.

    Exit Status:
    Returns 0 unless an invalid option is given.`,
	},
	"shopt": {
		Synopsis: "shopt [-su] [optname ...]",
		Desc: `Set and unset shell options.

    Without options, display all shell options with their status.

    Options:
      -s    Enable (set) each named option
      -u    Disable (unset) each named option

    Available options:
      extglob      Extended pattern matching: ?(p), *(p), +(p), @(p), !(p)
      failglob     Non-matching globs produce an error
      nocaseglob   Case-insensitive pathname expansion
      nullglob     Non-matching globs expand to nothing

    Without -s or -u, show the value of each named option
    and return 1 if any is off.

    Exit Status:
    Returns 0 unless an invalid option name is given.`,
	},
	"shift": {
		Synopsis: "shift [n]",
		Desc: `Shift positional parameters.

    Rename positional parameters $N+1, $N+2, ... to $1, $2, ...
    If N is not given, it is assumed to be 1.

    Exit Status:
    Returns 0 unless N is greater than $# or less than 0.`,
	},
	"test": {
		Synopsis: "test EXPRESSION",
		Desc: `Evaluate conditional expression.

    Evaluate EXPRESSION and return 0 (true) or 1 (false).

    String tests: -z STRING, -n STRING, S1 = S2, S1 != S2
    Integer tests: N1 -eq N2, -ne, -lt, -le, -gt, -ge
    File tests: -e FILE, -f, -d, -r, -w, -x, -s
    Logical: ! EXPR, EXPR -a EXPR, EXPR -o EXPR, ( EXPR )

    Exit Status:
    Returns 0 if EXPRESSION is true, 1 if false, 2 on error.`,
	},
	"trap": {
		Synopsis: "trap [command] [signal ...]",
		Desc: `Trap signals and other events.

    Set COMMAND to be executed when SIGNAL is received.
    If COMMAND is '-', reset the signal to its default.
    If COMMAND is '', ignore the signal.
    Without arguments, list all traps.

    Signals: INT, TERM, HUP, QUIT, USR1, USR2, KILL, STOP,
             CONT, TSTP, PIPE, ALRM, ABRT
    Pseudo-signals: EXIT, ERR, RETURN

    Exit Status:
    Returns 0 unless a SIGNAL is invalid.`,
	},
	"true": {
		Synopsis: "true",
		Desc: `Return a successful result.

    Exit Status:
    Always returns 0.`,
	},
	"type": {
		Synopsis: "type name [name ...]",
		Desc: `Display information about command type.

    For each NAME, indicate how it would be interpreted if
    used as a command name: alias, function, builtin, or
    external file.

    Exit Status:
    Returns 0 if all names are found, 1 if any are not.`,
	},
	"typeset": {
		Synopsis: "typeset [-aAirx] [-g] [name[=value] ...]",
		Desc: `Set variable attributes and values.

    A synonym for 'declare'. See 'help declare'.`,
	},
	"unalias": {
		Synopsis: "unalias [-a] name [name ...]",
		Desc: `Remove alias definitions.

    Remove each NAME from the alias list. With -a, remove
    all aliases.

    Exit Status:
    Returns 0 unless a NAME is not an existing alias.`,
	},
	"unset": {
		Synopsis: "unset [name ...]",
		Desc: `Unset values and attributes of shell variables.

    For each NAME, remove the corresponding variable.

    Exit Status:
    Returns 0 unless a NAME is readonly.`,
	},
	"wait": {
		Synopsis: "wait [pid|%jobspec ...]",
		Desc: `Wait for job completion.

    Wait for each specified process or job and return its exit
    status. Without arguments, wait for all background jobs.

    Exit Status:
    Returns the status of the last waited-for process, or 127
    if the process or job is not found.`,
	},
}

// builtinHelps prints the --help usage text to stdout.
func printUsage(w *os.File) {
	fmt.Fprintf(w, `Usage: gosh [options] [script [args...]]
       gosh [options] -c command [args...]

gosh - An educational Unix shell implemented in Go

Options:
  -c command       Execute command string and exit
  -h, --help       Show this help message and exit
  --version        Show version information and exit
  --debug-ast      Print the AST before execution
  --debug-tokens   Print the token stream during lexing
  --debug-expanded Print the expanded AST before execution

Arguments:
  script           Script file to execute
  args             Arguments passed to script or command

Examples:
  gosh                          # Start interactive shell
  gosh script.sh arg1 arg2      # Execute script with arguments
  gosh -c 'echo $1' _ hello     # Execute command with arguments

Type 'help' inside the shell to see builtin commands.
`)
}

// builtinHelpCmd implements the help builtin.
func builtinHelpCmd(state *shellState, args []string, stdin, stdout, stderr *os.File) int {
	if len(args) > 0 {
		name := args[0]
		if h, ok := helpEntries[name]; ok {
			fmt.Fprintf(stdout, "%s: %s\n%s\n", name, h.Synopsis, h.Desc)
			return 0
		}
		fmt.Fprintf(stderr, "gosh: help: no help topics match '%s'\n", name)
		return 1
	}

	// List all builtins.
	fmt.Fprintf(stdout, "gosh %s\n", version)
	fmt.Fprintln(stdout, "These shell commands are defined internally. Type 'help name' to find out")
	fmt.Fprintln(stdout, "more about the function 'name'.")
	fmt.Fprintln(stdout)

	// Collect unique builtin names.
	seen := make(map[string]bool)
	var names []string
	for name := range builtins {
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)

	// Print in two columns.
	mid := (len(names) + 1) / 2
	for i := 0; i < mid; i++ {
		left := names[i]
		leftSyn := left
		if h, ok := helpEntries[left]; ok {
			leftSyn = h.Synopsis
		}
		if i+mid < len(names) {
			right := names[i+mid]
			rightSyn := right
			if h, ok := helpEntries[right]; ok {
				rightSyn = h.Synopsis
			}
			fmt.Fprintf(stdout, " %-38s %s\n", truncate(leftSyn, 38), truncate(rightSyn, 38))
		} else {
			fmt.Fprintf(stdout, " %s\n", truncate(leftSyn, 38))
		}
	}
	return 0
}

// truncate shortens a string to at most n characters, adding "..." if needed.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

// printVersion prints version information.
func printVersion(w *os.File) {
	fmt.Fprintf(w, "gosh %s\n", version)
}

func init() {
	builtins["help"] = builtinHelpCmd
}
