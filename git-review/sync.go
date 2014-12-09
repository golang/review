// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

func doSync(args []string) {
	expectZeroArgs(args, "sync")

	// Fetch remote changes.
	run("git", "fetch", "-q")

	// If there's no pending change, just fast-forward.
	b := CurrentBranch()
	if !b.HasPendingCommit() {
		run("git", "merge", "-q", "--ff-only", b.OriginBranch())
		return
	}

	// Don't sync with staged changes.
	// TODO(adg): should we handle unstaged changes also?
	if HasStagedChanges() {
		dief("run 'git-review change' to commit staged changes before sync.")
	}

	// Sync current branch to origin.
	id := b.ChangeID()
	run("git", "rebase", "-q", b.OriginBranch())

	// If the change commit has been submitted,
	// roll back change leaving any changes unstaged.
	if b.Submitted(id) && b.HasPendingCommit() {
		run("git", "reset", "HEAD^")
	}
}
