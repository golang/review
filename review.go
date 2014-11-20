// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TODO(adg): change command-line parsing so this works as a git alias
// TODO(adg): rename 'upload' to 'mail'
// TODO(adg): recognize non-master remote branches
// TODO(adg): accept -a flag on 'commit' (like git commit -a)
// TODO(adg): check style of commit message
// TOOD(adg): print gerrit votes on 'pending'
// TODO(adg): add gofmt commit hook
// TODO(adg): print changed files on review sync
// TODO(adg): translate email addresses without @ by looking up somewhere

// Command review provides a simple command-line user interface for
// working with git repositories and the Gerrit code review system.
// See "review help" for details.
package main // import "golang.org/x/review"

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

var (
	verbose = flag.Bool("v", false, "verbose output")
	noRun   = flag.Bool("n", false, "print but do not run commands")
)

const usage = `Usage: %s [-n] [-v] <command>
Type "%s help" for more information.
`

const help = `Usage: %s [-n] [-v] <command>

The review command is a wrapper for the git command that provides a simple
interface to the "single-commit feature branch" development model.

Available comands:

	change [name]
		Create a change commit, or amend an existing change commit,
		with the staged changes. If a branch name is provided, check
		out that branch (creating it if it does not exist).
		(Does not amend the existing commit when switching branches.)

	pending [-r]
		Show local branches and their head commits.
		If -r is specified, show additional information from Gerrit.

	upload [-f] [-r reviewer,...] [-cc mail,...]
		Upload change commit to the code review server.
		If -f is specified, upload even if there are staged changes.

	upload -diff
		Show the changes but do not upload.

	sync
		Fetch changes from the remote repository and merge them into
		the current branch, rebasing the change commit on top of them.

	revert files...
		Revert the specified files to their state before the change
		commit. (Be careful! This will discard your changes!)

	gofmt
		TBD

`

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, usage, os.Args[0], os.Args[0])
	}
	flag.Parse()

	installHook()

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(2)
	}
	command, args := args[0], args[1:]

	switch command {
	case "help":
		fmt.Fprintf(os.Stdout, help, os.Args[0])
	case "change", "c":
		change(args)
	case "pending", "p":
		pending(args)
	case "upload", "up", "u":
		upload(args)
	case "sync", "s":
		doSync(args)
	case "revert":
		dief("revert not implemented")
	case "gofmt":
		dief("gofmt not implemented")
	default:
		flag.Usage()
	}
}

func run(command string, args ...string) {
	if err := runErr(command, args...); err != nil {
		if !*verbose {
			// If we're not in verbose mode, print the command
			// before dying to give context to the failure.
			fmt.Fprintln(os.Stderr, commandString(command, args))
		}
		dief("%v", err)
	}
}

func runErr(command string, args ...string) error {
	if *verbose || *noRun {
		fmt.Fprintln(os.Stderr, commandString(command, args))
	}
	if *noRun {
		return nil
	}
	cmd := exec.Command(command, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// getOutput runs the specified command and returns its combined standard
// output and standard error outputs.
// NOTE: It should only be used to run commands that return information,
// **not** commands that make any actual changes.
func getOutput(command string, args ...string) string {
	if *verbose {
		fmt.Fprintln(os.Stderr, commandString(command, args))
	}
	b, err := exec.Command(command, args...).CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n%s\n", commandString(command, args), b)
		dief("%v", err)
	}
	return string(bytes.TrimSpace(b))
}

// getLines is like getOutput but it returns non-empty output lines.
// NOTE: It should only be used to run commands that return information,
// **not** commands that make any actual changes.
func getLines(command string, args ...string) []string {
	var s []string
	for _, l := range strings.Split(getOutput(command, args...), "\n") {
		s = append(s, strings.TrimSpace(l))
	}
	return s
}

func commandString(command string, args []string) string {
	return strings.Join(append([]string{command}, args...), " ")
}

func dief(format string, args ...interface{}) {
	printf(format, args...)
	os.Exit(1)
}

func verbosef(format string, args ...interface{}) {
	if *verbose {
		printf(format, args...)
	}
}

func printf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "review: "+format+"\n", args...)
}
