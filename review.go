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
	if currentBranch() != "master" {
		dief("You must run create from the master branch.\n" +
			"(Try \"review sync\" or \"git checkout master\" first.)\n")
	}
	run("git", "checkout", "-q", "-b", name)
	if err := runErr("git", "commit", "-q"); err != nil {
		verbosef("Commit failed: %v\nSwitching back to master.\n", err)
		run("git", "checkout", "-q", "master")
		run("git", "branch", "-q", "-d", name)
	}
	// TODO(adg): check style of commit message
}

func commit() {
	if !hasStagedChanges() {
		dief("No staged changes. Did you forget to \"git add\" your files?\n")
	}
	if currentBranch() == "master" {
		dief("Can't commit to master branch.\n")
	}
	run("git", "commit", "-q", "--amend", "-C", "HEAD")
}

func diff() {
	run("git", "diff", "HEAD^", "HEAD")
}

func upload() {
	if currentBranch() == "master" {
		dief("Can't upload from master branch.\n")
	}
	run("git", "push", "-q", "origin", "HEAD:refs/for/master")
}

func sync() {
	run("git", "fetch", "-q")

	// If we're on master, just fast-forward.
	branch := currentBranch()
	if branch == "master" {
		run("git", "merge", "-q", "--ff-only", "origin/master")
		return
	}

	// Check that exactly this commit was submitted to master. If so,
	// switch back to master, fast-forward, delete the feature branch.
	if branchContains("origin/master", "HEAD") {
		run("git", "checkout", "-q", "master")
		run("git", "merge", "-q", "--ff-only", "origin/master")
		run("git", "branch", "-q", "-d", branch)
		return
	}

	// Check whether a rebased version of this commit was submitted to
	// master. If so, switch back to master, fast-forward, and
	// provide instructions for deleting the feature branch.
	// (We're not 100% sure that the feature branch HEAD was submitted,
	// so be cautious.)
	if headSubmitted() {
		run("git", "checkout", "-q", "master")
		run("git", "merge", "-q", "--ff-only", "origin/master")
		fmt.Fprintf(os.Stderr, "Switched back to master from %q, "+
			"which I think has been submitted.\n"+
			"If you agree, and no longer need branch %q, run:\n"+
			"\tgit branch -D %v\n",
			branch, branch, branch)
		return
	}

	// Bump master HEAD to that of origin/master, just in case the user
	// switches back to master with "git checkout master" later.
	if !branchContains("origin/master", "master") {
		run("git", "branch", "-f", "master", "origin/master")
	}

	// We have un-submitted changes on this feature branch; rebase.
	run("git", "rebase", "-q", "origin/master")
}

func pending() {
	dief("not implemented\n")
}

func branchContains(branch, rev string) bool {
	b, err := exec.Command("git", "branch", "-r", "--contains", rev).CombinedOutput()
	if err != nil {
		dief("%s\nchecking for %q on origin/master: %v\n", b, rev, err)
	}
	for _, s := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(s) == branch {
			return true
		}
	}
	return false
}

var stagedRe = regexp.MustCompile(`^[ACDMR]  `)

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

func headSubmitted() bool {
	s := "Change-Id: " + headChangeId()
	b, err := exec.Command("git", "log", "--grep", s).CombinedOutput()
	if err != nil {
		dief("%s\ngit log failed: %v\n", b, err)
	}
	return len(b) > 0
}

func headChangeId() string {
	b, err := exec.Command("git", "log", "-n", "1", "--format=format:%b").CombinedOutput()
	if err != nil {
		dief("%s\ngit log failed: %v\n", b, err)
	}
	const p = "Change-Id: "
	for _, s := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(s, p) {
			return strings.TrimSpace(strings.TrimPrefix(s, p))
		}
	}
	dief("No Change-Id line found in HEAD commit.\n")
	panic("unreachable")
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
