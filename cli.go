package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// Output streams; replaced in tests.
var (
	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr
	stdin  io.Reader = os.Stdin
)

// Exit codes. They are part of the CLI contract (see `jmp --help`).
const (
	exitOK       = 0
	exitError    = 1 // runtime failure (I/O, corrupt data, invalid value)
	exitUsage    = 2 // bad command line
	exitNotFound = 3 // no match / record not found
	exitSync     = 4 // sync could not reach or update the remote
)

// cliError carries an exit code. A silent error has already been reported.
type cliError struct {
	code   int
	err    error
	silent bool
}

func (e *cliError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *cliError) Unwrap() error { return e.err }

func usageErr(err error) error { return &cliError{code: exitUsage, err: err} }

func codeErr(code int, format string, a ...any) error {
	return &cliError{code: code, err: fmt.Errorf(format, a...)}
}

func silentExit(code int) error { return &cliError{code: code, silent: true} }

// ---- language ----

var useZh bool

// detectLang picks the message language from JMP_LANG, then the usual locale
// variables. Anything starting with "zh" selects Chinese; otherwise English.
func detectLang() {
	useZh = false
	for _, k := range []string{"JMP_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			useZh = strings.HasPrefix(strings.ToLower(v), "zh")
			return
		}
	}
}

// T returns the English or Chinese variant of a user-facing string.
func T(en, zh string) string {
	if useZh {
		return zh
	}
	return en
}

// ---- output ----

// info prints a status line to stdout. Suppressed by --quiet and --json, so
// it must never carry data a script would want to parse.
func info(format string, a ...any) {
	if quiet || jsonOut {
		return
	}
	fmt.Fprintf(stdout, format+"\n", a...)
}

// detail prints an extra line when --verbose is set.
func detail(format string, a ...any) {
	if !verbose || quiet || jsonOut {
		return
	}
	fmt.Fprintf(stdout, format+"\n", a...)
}

func warn(format string, a ...any) {
	fmt.Fprintf(stderr, "jmp: "+T("warning: ", "警告: ")+format+"\n", a...)
}

func printJSON(v any) {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// ---- entry point ----

// run executes the CLI and returns the process exit code.
func run(args []string) int {
	detectLang()
	root := buildRoot()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	err := root.Execute()
	if err == nil {
		return exitOK
	}
	code := exitError
	var ce *cliError
	if errors.As(err, &ce) {
		code = ce.code
		if ce.silent {
			return code
		}
	} else if isCobraUsageError(err) {
		code = exitUsage
	}
	if jsonOut {
		printJSON(map[string]any{"error": err.Error(), "code": code})
		return code
	}
	fmt.Fprintf(stderr, "jmp: %s\n", err)
	if code == exitUsage {
		fmt.Fprintln(stderr, T("Try 'jmp --help' for more information.", "运行 'jmp --help' 查看用法。"))
	}
	return code
}

// isCobraUsageError recognises the usage errors cobra produces itself, before
// any of our own code (and its typed errors) runs.
func isCobraUsageError(err error) bool {
	msg := err.Error()
	for _, p := range []string{"unknown command", "unknown flag", "unknown shorthand flag"} {
		if strings.HasPrefix(msg, p) {
			return true
		}
	}
	return false
}

// strictArgs makes every command validate its positional arguments (cobra's
// default silently accepts extras on subcommands) and reports violations with
// the usage exit code.
func strictArgs(cmd *cobra.Command) {
	orig := cmd.Args
	if orig == nil {
		orig = cobra.NoArgs
	}
	cmd.Args = func(c *cobra.Command, args []string) error {
		if err := orig(c, args); err != nil {
			return usageErr(err)
		}
		return nil
	}
	for _, sub := range cmd.Commands() {
		strictArgs(sub)
	}
}
