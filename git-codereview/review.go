// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TODO(rsc): Document multi-change branch behavior.

// Command git-codereview provides a simple command-line user interface for
// working with git repositories and the Gerrit code review system.
// See "git-codereview help" for details.
package main // import "golang.org/x/review/git-codereview"

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

var (
	flags   *flag.FlagSet
	verbose = new(count) // installed as -v below
	noRun   = new(bool)
)

func initFlags() {
	flags = flag.NewFlagSet("", flag.ExitOnError)
	flags.Usage = func() {
		fmt.Fprintf(stderr(), usage, os.Args[0], os.Args[0])
	}
	flags.Var(verbose, "v", "report commands")
	flags.BoolVar(noRun, "n", false, "print but do not run commands")
}

const globalFlags = "[-n] [-v]"

const usage = `Usage: %s <command> ` + globalFlags + `
Type "%s help" for more information.
`

const help = `Usage: %s <command> ` + globalFlags + `

The git-codereview command is a wrapper for the git command that provides a
simple interface to the "single-commit feature branch" development model.

See the docs for details: https://godoc.org/golang.org/x/review/git-codereview

The -v flag prints all commands that make changes.
The -n flag prints all commands that would be run, but does not run them.

Available commands:

	change [name]
		Create a change commit, or amend an existing change commit,
		with the staged changes. If a branch name is provided, check
		out that branch (creating it if it does not exist).
		Does not amend the existing commit when switching branches.
		If -q is specified, skip the editing of an extant pending
		change's commit message.
		If -a is specified, automatically add any unstaged changes in
		tracked files during commit.

	gofmt [-l]
		Run gofmt on all tracked files in the staging area and the
		working tree.
		If -l is specified, list files that need formatting.
		Otherwise, reformat files in place.

	help
		Show this help text.

	hooks
		Install Git commit hooks for Gerrit and gofmt.
		Every other operation except help also does this,
		if they are not already installed.

	mail [-f] [-r reviewer,...] [-cc mail,...]
		Upload change commit to the code review server and send mail
		requesting a code review.
		If -f is specified, upload even if there are staged changes.
		The -r and -cc flags identify the email addresses of people to
		do the code review and to be CC'ed about the code review.
		Multiple addresses are given as a comma-separated list.

	mail -diff
		Show the changes but do not send mail or upload.

	pending [-l]
		Show the status of all pending changes and staged, unstaged,
		and untracked files in the local repository.
		If -l is specified, only use locally available information.

	submit
		Push the pending change to the Gerrit server and tell Gerrit to
		submit it to the master branch.

	sync
		Fetch changes from the remote repository and merge them into
		the current branch, rebasing the change commit on top of them.


`

func main() {
	initFlags()

	if len(os.Args) < 2 {
		flags.Usage()
		if dieTrap != nil {
			dieTrap()
		}
		os.Exit(2)
	}
	command, args := os.Args[1], os.Args[2:]

	if command == "help" {
		fmt.Fprintf(stdout(), help, os.Args[0])
		return
	}

	installHook()

	switch command {
	case "branchpoint":
		branchpoint(args)
	case "change":
		change(args)
	case "gofmt":
		gofmt(args)
	case "hook-invoke":
		hookInvoke(args)
	case "hooks":
		// done - installHook already ran
	case "mail", "m":
		mail(args)
	case "pending":
		pending(args)
	case "submit":
		submit(args)
	case "sync":
		doSync(args)
	case "test-loadAuth": // for testing only
		loadAuth()
	default:
		flags.Usage()
	}
}

func expectZeroArgs(args []string, command string) {
	flags.Parse(args)
	if len(flags.Args()) > 0 {
		fmt.Fprintf(stderr(), "Usage: %s %s %s\n", os.Args[0], command, globalFlags)
		os.Exit(2)
	}
}

func run(command string, args ...string) {
	if err := runErr(command, args...); err != nil {
		if *verbose == 0 {
			// If we're not in verbose mode, print the command
			// before dying to give context to the failure.
			fmt.Fprintf(stderr(), "(running: %s)\n", commandString(command, args))
		}
		dief("%v", err)
	}
}

func runErr(command string, args ...string) error {
	return runDirErr("", command, args...)
}

var runLogTrap []string

func runDirErr(dir, command string, args ...string) error {
	if *verbose > 0 || *noRun {
		fmt.Fprintln(stderr(), commandString(command, args))
	}
	if *noRun {
		return nil
	}
	if runLogTrap != nil {
		runLogTrap = append(runLogTrap, strings.TrimSpace(command+" "+strings.Join(args, " ")))
	}
	cmd := exec.Command(command, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout()
	cmd.Stderr = stderr()
	return cmd.Run()
}

// getOutput runs the specified command and returns its combined standard
// output and standard error outputs.
// It dies on command errors.
// NOTE: It should only be used to run commands that return information,
// **not** commands that make any actual changes.
func getOutput(command string, args ...string) string {
	s, err := getOutputErr(command, args...)
	if err != nil {
		fmt.Fprintf(stderr(), "%v\n%s\n", commandString(command, args), s)
		dief("%v", err)
	}
	return s
}

// Given a command and its arguments, getOutputErr returns the same
// trimmed output as getOutput, but it returns any error instead of exiting.
func getOutputErr(command string, args ...string) (string, error) {
	// NOTE: We only show these non-state-modifying commands with -v -v.
	// Otherwise things like 'git sync -v' show all our internal "find out about
	// the git repo" commands, which is confusing if you are just trying to find
	// out what git sync means.
	if *verbose > 1 {
		fmt.Fprintln(stderr(), commandString(command, args))
	}
	b, err := exec.Command(command, args...).CombinedOutput()
	return string(bytes.TrimSpace(b)), err
}

// getLines is like getOutput but it returns only non-empty output lines,
// with leading and trailing spaces removed.
// NOTE: It should only be used to run commands that return information,
// **not** commands that make any actual changes.
func getLines(command string, args ...string) []string {
	var s []string
	for _, l := range strings.Split(getOutput(command, args...), "\n") {
		if len(strings.TrimSpace(l)) > 0 {
			s = append(s, l)
		}
	}
	return s
}

func commandString(command string, args []string) string {
	return strings.Join(append([]string{command}, args...), " ")
}

var dieTrap func()

func dief(format string, args ...interface{}) {
	printf(format, args...)
	die()
}

func die() {
	if dieTrap != nil {
		dieTrap()
	}
	os.Exit(1)
}

func verbosef(format string, args ...interface{}) {
	if *verbose > 0 {
		printf(format, args...)
	}
}

var stdoutTrap, stderrTrap *bytes.Buffer

func stdout() io.Writer {
	if stdoutTrap != nil {
		return stdoutTrap
	}
	return os.Stdout
}

func stderr() io.Writer {
	if stderrTrap != nil {
		return stderrTrap
	}
	return os.Stderr
}

func printf(format string, args ...interface{}) {
	fmt.Fprintf(stderr(), "%s: %s\n", os.Args[0], fmt.Sprintf(format, args...))
}

// count is a flag.Value that is like a flag.Bool and a flag.Int.
// If used as -name, it increments the count, but -name=x sets the count.
// Used for verbose flag -v.
type count int

func (c *count) String() string {
	return fmt.Sprint(int(*c))
}

func (c *count) Set(s string) error {
	switch s {
	case "true":
		*c++
	case "false":
		*c = 0
	default:
		n, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("invalid count %q", s)
		}
		*c = count(n)
	}
	return nil
}

func (c *count) IsBoolFlag() bool {
	return true
}
