// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	hookFile = filepath.FromSlash(".git/hooks/commit-msg")
	verbose  = flag.Bool("v", false, "verbose output")
)

const usage = `Usage: %s [-v] <command>
Type "%s help" for more information.
`

const help = `Usage: %s [-v] <command>

The review command is a wrapper for the git command that provides a simple
interface to the "single-commit feature branch" development model.

Available comands:

	create <name>
		Create a local branch with the provided name
		and commit the staged changes to it.

	commit
		Amend local branch HEAD commit with the staged changes.

	diff
		View differences between remote branch HEAD and
		the local branch HEAD.
		(The differences introduced by this change.)

	upload
		Upload HEAD commit to the code review server.

	sync
		Fetch changes from the remote repository and merge them to the
		current branch, rebasing the HEAD commit (if any) on top of
		them. If the HEAD commit has been submitted, switch back to the
		master branch and delete the feature branch.

	pending 
		Show local branches and their head commits.

`

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, usage, os.Args[0], os.Args[0])
		os.Exit(2)
	}
	flag.Parse()

	goToRepoRoot()
	installHook()

	switch flag.Arg(0) {
	case "help":
		fmt.Fprintf(os.Stdout, help, os.Args[0])
	case "create", "cr":
		name := flag.Arg(1)
		if name == "" {
			flag.Usage()
		}
		create(name)
	case "commit", "co":
		commit()
	case "diff", "d":
		diff()
	case "upload", "u":
		upload()
	case "sync", "s":
		sync()
	case "pending", "p":
		pending()
	default:
		flag.Usage()
	}
}

func create(name string) {
	if !hasStagedChanges() {
		dief("No staged changes. Did you forget to \"git add\" your files?\n")
	}
	if !isOnMaster() {
		dief("You must run create from the master branch.\n" +
			"(Try \"review sync\" or \"git checkout master\" first.)\n")
	}
	run("git", "checkout", "-q", "-b", name)
	if err := runErr("git", "commit", "-q"); err != nil {
		verbosef("Commit failed: %v\nSwitching back to master.", err)
		run("git", "checkout", "-q", "master")
		run("git", "branch", "-q", "-d", name)
	}
}

func commit() {
	if !hasStagedChanges() {
		dief("No staged changes. Did you forget to \"git add\" your files?\n")
	}
	if isOnMaster() {
		dief("Can't commit to master branch.\n")
	}
	run("git", "commit", "-q", "--amend", "-C", "HEAD")
}

func diff() {
	run("git", "diff", "HEAD^", "HEAD")
}

func upload() {
	if isOnMaster() {
		dief("Can't upload from master branch.\n")
	}
	run("git", "push", "-q", "origin", "HEAD:refs/for/master")
}

func sync() {
	run("git", "fetch", "-q")
	if isOnMaster() {
		run("git", "merge", "-q", "--ff-only", "origin/master")
		return
	}
	if commitSubmitted() {
		b := currentBranch()
		run("git", "checkout", "-q", "master")
		run("git", "merge", "-q", "--ff-only", "origin/master")
		run("git", "branch", "-q", "-d", b)
	}
	run("git", "rebase", "-q", "origin/master")
}

func pending() {
	dief("not implemented\n")
}

var stagedRe = regexp.MustCompile(`^[ACDMR]  `)

func commitSubmitted() bool {
	b, err := exec.Command("git", "branch", "-r", "--contains").CombinedOutput()
	if err != nil {
		dief("%s\nchecking for HEAD on remote branch: %v\n", b, err)
	}
	for _, s := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(s) == "origin/master" {
			return true
		}
	}
	return false
}

func hasStagedChanges() bool {
	for _, s := range gitStatus() {
		if stagedRe.MatchString(s) {
			return true
		}
	}
	return false
}

func currentBranch() string {
	const p = "## "
	for _, s := range gitStatus() {
		if strings.HasPrefix(s, p) {
			return strings.TrimPrefix(s, p)
		}
	}
	dief("Could not find current branch with 'git status'.\n")
	panic("unreachable")
}

func gitStatus() []string {
	b, err := exec.Command("git", "status", "-b", "--porcelain").CombinedOutput()
	if err != nil {
		dief("%s\ngit status failed: %v\n", b, err)
	}
	return strings.Split(string(b), "\n")
}

func isOnMaster() bool {
	branch, err := exec.Command("git", "branch").CombinedOutput()
	if err != nil {
		dief("%s\nchecking current branch: %v\n", branch, err)
	}
	for _, s := range strings.Split(string(branch), "\n") {
		if strings.HasPrefix(s, "* ") {
			return s == "* master"
		}
	}
	return false
}

func goToRepoRoot() {
	prevDir, err := os.Getwd()
	if err != nil {
		dief("could not get current directory: %v\n", err)
	}
	for {
		if _, err := os.Stat(".git"); err == nil {
			return
		}
		if err := os.Chdir(".."); err != nil {
			dief("could not chdir: %v\n", err)
		}
		currentDir, err := os.Getwd()
		if err != nil {
			dief("could not get current directory: %v\n", err)
		}
		if currentDir == prevDir {
			dief("Git root not found. Run from within the Git tree please.\n")
		}
		prevDir = currentDir
	}
}

func installHook() {
	_, err := os.Stat(hookFile)
	if err == nil {
		return
	}
	if !os.IsNotExist(err) {
		dief("checking for hook file: %v\n", err)
	}
	verbosef("Presubmit hook to add Change-Id to commit messages is missing.\n"+
		"Automatically creating it at %v.\n", hookFile)
	hookContent := []byte(commitMsgHook)
	if err := ioutil.WriteFile(hookFile, hookContent, 0700); err != nil {
		dief("writing hook file: %v\n", err)
	}
}

func dief(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format, args...)
	os.Exit(1)
}

func run(command string, args ...string) {
	if err := runErr(command, args...); err != nil {
		if !*verbose {
			// If we're not in verbose mode, print the command
			// before dying to give context to the failure.
			fmt.Fprintln(os.Stderr, commandString(command, args))
		}
		dief("%v\n", err)
	}
}

func runErr(command string, args ...string) error {
	if *verbose {
		fmt.Fprintln(os.Stderr, commandString(command, args))
	}
	cmd := exec.Command(command, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func verbosef(format string, args ...interface{}) {
	if *verbose {
		fmt.Fprintf(os.Stderr, format, args...)
	}
}

func commandString(command string, args []string) string {
	return strings.Join(append([]string{command}, args...), " ")
}
