// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"
)

func change(args []string) {
	flags.Parse(args)
	if len(flags.Args()) > 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s change %s [branch]\n", os.Args[0], globalFlags)
		os.Exit(2)

	}

	// Checkout or create branch, if specified.
	checkedOut := false
	if branch := flags.Arg(0); branch != "" {
		checkoutOrCreate(branch)
		checkedOut = true
	}

	// Create or amend change commit.
	branch := CurrentBranch()
	if branch.Name == "master" {
		if checkedOut {
			// Permit "review change master".
			return
		}
		dief("can't commit to master branch (use '%s change branchname').", os.Args[0])
	}
	if branch.ChangeID == "" {
		// No change commit on this branch, create one.
		commitChanges(false)
		return
	}
	if checkedOut {
		// If we switched to an existing branch, don't amend the
		// commit. (The user can run 'review change' to do that.)
		return
	}
	// Amend the commit.
	commitChanges(true)
}

func commitChanges(amend bool) {
	if !hasStagedChanges() {
		printf("no staged changes. Did you forget to 'git add'?")
	}
	args := []string{"commit", "-q", "--allow-empty"}
	if amend {
		args = append(args, "--amend")
	}
	run("git", args...)
	printf("change updated.")
}

func checkoutOrCreate(branch string) {
	// If branch exists, check it out.
	for _, b := range localBranches() {
		if b == branch {
			run("git", "checkout", "-q", branch)
			printf("changed to branch %v.", branch)
			return
		}
	}

	// If it doesn't exist, create a new branch.
	if currentBranchName() != "master" {
		dief("can't create a new branch from non-master branch.")
	}
	run("git", "checkout", "-q", "-b", branch)
	printf("changed to new branch %v.", branch)
}
