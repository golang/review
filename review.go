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
	"strings"
)

var hookFile = filepath.FromSlash(".git/hooks/commit-msg")

const usage = `Usage: %s [command]

The review command is a wrapper for the git command that provides a simple
interface to the "single-commit feature branch" development model.

Available comands:

	create <name>

		Create a local feature branch with the provided name
		and commit the staged changes to it.

	commit

		Amend feature branch HEAD with the staged changes.

	diff

		View differences between remote branch HEAD and
		the feature branch HEAD.
		(The differences introduced by this change.)

	upload

		Upload HEAD commit to the code review server.

	sync

		Fetch changes from the remote repository and merge them to the
		current branch, rebasing the HEAD commit (if any) on top of
		them.

	pending 

		Show local feature branches and their head commits.

`

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, usage, os.Args[0])
		os.Exit(2)
	}
	flag.Parse()

	goToRepoRoot()
	installHook()

	switch flag.Arg(0) {
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
		dief(`No staged changes. Did you forget to "git add" your files?\n`)
	}
	if !isOnMaster() {
		dief("You must run create from the master branch.\n")
	}
	fmt.Printf("Creating and checking out branch: %v\n", name)
	run("git", "checkout", "-b", name)
	fmt.Printf("Committing staged changes to branch.\n")
	run("git", "commit")
}

func commit() {
	if !hasStagedChanges() {
		dief(`No staged changes. Did you forget to "git add" your files?\n`)
	}
	if isOnMaster() {
		dief("Can't commit to master branch.\n")
	}
	fmt.Printf("Amending head commit with staged changes.\n")
	run("git", "commit", "--amend", "-C", "HEAD")
}

func diff() {
	run("git", "diff", "HEAD^", "HEAD")
}

func upload() {
	if isOnMaster() {
		dief("Can't upload from master branch.\n")
	}
	fmt.Printf("Pushing commit to Gerrit code review server.\n")
	run("git", "push", "origin", "HEAD:refs/for/master")
}

func sync() {
	fmt.Printf("Fetching changes from remote repo.\n")
	run("git", "fetch")
	if isOnMaster() {
		run("git", "pull", "--ff-only")
		return
	}
	fmt.Printf("Rebasing head commit atop origin/master.\n")
	run("git", "rebase", "origin/master")
}

func pending() {
	dief("not implemented\n")
}

func hasStagedChanges() bool {
	status, err := exec.Command("git", "status", "-s").CombinedOutput()
	if err != nil {
		dief("%s\nchecking for staged changes: %v\n", status, err)
	}
	for _, s := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(s, "A  ") ||
			strings.HasPrefix(s, "M  ") ||
			strings.HasPrefix(s, "D  ") {
			return true
		}
	}
	return false
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
	fmt.Printf("Presubmit hook to add Change-Id to commit messages is missing.\n"+
		"Now automatically creating it at %v.\n\n", hookFile)
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
	cmd := exec.Command(command, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		dief("%v\n", err)
	}
}
