// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "flag"

func doSync(args []string) {
	if len(args) != 0 {
		flag.Usage()
	}

	// Fetch remote changes.
	run("git", "fetch", "-q")

	// If we're on master or there's no pending change, just fast-forward.
	branch := CurrentBranch()
	if branch.Name == "master" || branch.ChangeID == "" {
		run("git", "merge", "-q", "--ff-only", "origin/master")
		return
	}

	// Don't sync with staged changes.
	// TODO(adg): should we handle unstaged changes also?
	if hasStagedChanges() {
		dief("you have staged changes. Run 'review change' before sync.")
	}

	// Sync current branch to master.
	run("git", "rebase", "-q", "origin/master")

	// If the change commit has been submitted,
	// roll back change leaving any changes unstaged.
	if branch.Submitted() && hasPendingCommit(branch.Name) {
		run("git", "reset", "HEAD^")
	}
}
