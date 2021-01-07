// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "strings"

func cmdSync(args []string) {
	expectZeroArgs(args, "sync")

	// Get current branch and commit ID for fixup after pull.
	b := CurrentBranch()
	var id string
	if work := b.Pending(); len(work) > 0 {
		id = work[0].ChangeID
	}

	// If this is a Gerrit repo, disable the status advice that
	// tells users to run 'git push' and so on, like the marked (<<<) lines:
	//
	//	% git status
	//	On branch master
	//	Your branch is ahead of 'origin/master' by 3 commits. <<<
	//	  (use "git push" to publish your local commits)      <<<
	//	...
	//
	// (This advice is inappropriate when using Gerrit.)
	if len(b.Pending()) > 0 && haveGerrit() {
		// Only disable if statusHints is unset in the local config.
		// This allows users who really want them to put them back
		// in the .git/config for the Gerrit-cloned repo.
		_, err := cmdOutputErr("git", "config", "--local", "advice.statusHints")
		if err != nil {
			run("git", "config", "--local", "advice.statusHints", "false")
		}
	}

	// Don't sync with staged or unstaged changes.
	// rebase is going to complain if we don't, and we can give a nicer error.
	checkStaged("sync")
	checkUnstaged("sync")

	// Pull remote changes into local branch.
	// We do this in one command so that people following along with 'git sync -v'
	// see fewer commands to understand.
	// We want to pull in the remote changes from the upstream branch
	// and rebase the current pending commit (if any) on top of them.
	// If there is no pending commit, the pull will do a fast-forward merge.
	if *verbose > 1 {
		run("git", "pull", "-q", "-r", "-v", "origin", strings.TrimPrefix(b.OriginBranch(), "origin/"))
	} else {
		run("git", "pull", "-q", "-r", "origin", strings.TrimPrefix(b.OriginBranch(), "origin/"))
	}

	b = CurrentBranch() // discard any cached information
	if len(b.Pending()) == 1 && b.Submitted(id) {
		// If the change commit has been submitted,
		// roll back change leaving any changes unstaged.
		// Pull should have done this for us, but check just in case.
		run("git", "reset", b.Branchpoint())
	}
}

func checkStaged(cmd string) {
	if HasStagedChanges() {
		dief("cannot %s: staged changes exist\n"+
			"\trun 'git status' to see changes\n"+
			"\trun 'git-codereview change' to commit staged changes", cmd)
	}
}

func checkUnstaged(cmd string) {
	if HasUnstagedChanges() {
		dief("cannot %s: unstaged changes exist\n"+
			"\trun 'git status' to see changes\n"+
			"\trun 'git stash' to save unstaged changes\n"+
			"\trun 'git add' and 'git-codereview change' to commit staged changes", cmd)
	}
}
