// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

func cmdSync(args []string) {
	expectZeroArgs(args, "sync")

	// Get current branch and commit ID for fixup after pull.
	b := CurrentBranch()
	b.NeedOriginBranch("sync")
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

type syncBranchStatus struct {
	Local      string
	Parent     string
	Branch     string
	ParentHash string
	BranchHash string
	Conflicts  []string
}

func syncBranchStatusFile() string {
	return filepath.Join(repoRoot(), ".git/codereview-sync-branch-status")
}

func readSyncBranchStatus() *syncBranchStatus {
	data, err := ioutil.ReadFile(syncBranchStatusFile())
	if err != nil {
		dief("cannot sync-branch: reading status: %v", err)
	}
	status := new(syncBranchStatus)
	err = json.Unmarshal(data, status)
	if err != nil {
		dief("cannot sync-branch: reading status: %v", err)
	}
	return status
}

func writeSyncBranchStatus(status *syncBranchStatus) {
	js, err := json.MarshalIndent(status, "", "\t")
	if err != nil {
		dief("cannot sync-branch: writing status: %v", err)
	}
	if err := ioutil.WriteFile(syncBranchStatusFile(), js, 0666); err != nil {
		dief("cannot sync-branch: writing status: %v", err)
	}
}

func cmdSyncBranch(args []string) {
	os.Setenv("GIT_EDITOR", ":") // do not bring up editor during merge, commit

	var cont bool
	flags.BoolVar(&cont, "continue", false, "continue after merge conflicts")
	flags.Parse(args)
	if len(flag.Args()) > 0 {
		fmt.Fprintf(stderr(), "Usage: %s sync-branch %s [-continue]\n", progName, globalFlags)
		exit(2)
	}

	parent := config()["parent-branch"]
	if parent == "" {
		dief("cannot sync-branch: codereview.cfg does not list parent-branch")
	}

	branch := config()["branch"]
	if parent == "" {
		dief("cannot sync-branch: codereview.cfg does not list branch")
	}

	b := CurrentBranch()
	if b.DetachedHead() {
		dief("cannot sync-branch: on detached head")
	}
	if len(b.Pending()) > 0 {
		dief("cannot sync-branch: pending changes exist\n" +
			"\trun 'git codereview pending' to see them")
	}

	if cont {
		if _, err := os.Stat(syncBranchStatusFile()); err != nil {
			dief("cannot sync-branch -continue: no pending sync-branch status file found")
		}
		syncBranchContinue(" -continue", b, readSyncBranchStatus())
		return
	}

	if _, err := cmdOutputErr("git", "rev-parse", "--abbrev-ref", "MERGE_HEAD"); err == nil {
		diePendingMerge("sync-branch")
	}

	// Don't sync with staged or unstaged changes.
	// rebase is going to complain if we don't, and we can give a nicer error.
	checkStaged("sync")
	checkUnstaged("sync")

	// Make sure client is up-to-date on current branch.
	// Note that this does a remote fetch of b.OriginBranch() (aka branch).
	cmdSync(nil)

	// Pull down parent commits too.
	quiet := "-q"
	if *verbose > 0 {
		quiet = "-v"
	}
	run("git", "fetch", quiet, "origin", "refs/heads/"+parent+":refs/remotes/origin/"+parent)

	// Write the status file to make sure we can, before starting a merge.
	status := &syncBranchStatus{
		Local:      b.Name,
		Parent:     parent,
		ParentHash: gitHash("origin/" + parent),
		Branch:     branch,
		BranchHash: gitHash("origin/" + branch),
	}
	writeSyncBranchStatus(status)

	// Start the merge.
	_, err := cmdOutputErr("git", "merge", "origin/"+parent)

	// Resolve codereview.cfg the right way (never take it from the merge).
	cmdOutputDir(repoRoot(), "git", "checkout", "HEAD", "--", "codereview.cfg")

	if err != nil {
		// Check whether the only listed file is codereview.cfg and try again if so.
		// Build list of unmerged files.
		for _, s := range nonBlankLines(cmdOutputDir(repoRoot(), "git", "status", "-b", "--porcelain")) {
			// Unmerged status is anything with a U and also AA and DD.
			if len(s) >= 4 && s[2] == ' ' && (s[0] == 'U' || s[1] == 'U' || s[0:2] == "AA" || s[0:2] == "DD") {
				status.Conflicts = append(status.Conflicts, s[3:])
			}
		}
		if len(status.Conflicts) == 0 {
			// Must have been codereview.cfg that was the problem.
			// Try continuing the merge.
			// Note that as of Git 2.12, git merge --continue is a synonym for git commit,
			// but older Gits do not have merge --continue.
			var out string
			out, err = cmdOutputErr("git", "commit", "-m", "TEMPORARY MERGE MESSAGE")
			if err != nil {
				printf("git commit failed with no apparent unmerged files:\n%s\n", out)
			}
		} else {
			writeSyncBranchStatus(status)
		}
	}

	if err != nil {
		if len(status.Conflicts) == 0 {
			dief("cannot sync-branch: git merge failed but no conflicts found\n" +
				"(unexpected error, please ask for help!)")
		}
		dief("sync-branch: merge conflicts in:\n\t- %s\n\n"+
			"Please fix them (use 'git status' to see the list again),\n"+
			"then 'git add' or 'git rm' to resolve them,\n"+
			"and then 'git sync-branch -continue' to continue.\n"+
			"Or run 'git merge --abort' to give up on this sync-branch.\n",
			strings.Join(status.Conflicts, "\n\t- "))
	}

	syncBranchContinue("", b, status)
}

func diePendingMerge(cmd string) {
	dief("cannot %s: found pending merge\n"+
		"Run 'git codereview sync-branch -continue' if you fixed\n"+
		"merge conflicts after a previous sync-branch operation.\n"+
		"Or run 'git merge --abort' to give up on the sync-branch.\n",
		cmd)
}

func syncBranchContinue(flag string, b *Branch, status *syncBranchStatus) {
	if h := gitHash("origin/" + status.Parent); h != status.ParentHash {
		dief("cannot sync-branch%s: parent hash changed: %.7s -> %.7s", flag, status.ParentHash, h)
	}
	if h := gitHash("origin/" + status.Branch); h != status.BranchHash {
		dief("cannot sync-branch%s: branch hash changed: %.7s -> %.7s", flag, status.BranchHash, h)
	}
	if b.Name != status.Local {
		dief("cannot sync-branch%s: branch changed underfoot: %s -> %s", flag, status.Local, b.Name)
	}

	branch := status.Branch
	parent := status.Parent
	branchHash := status.BranchHash
	parentHash := status.ParentHash

	prefix := ""
	if strings.HasPrefix(branch, "dev.") || strings.HasPrefix(branch, "release-branch.") {
		prefix = "[" + branch + "] "
	}
	msg := fmt.Sprintf("%sall: merge %s (%.7s) into %s", prefix, parent, parentHash, branch)

	if flag != "" {
		// Need to commit the merge.

		// Check that the state of the client is the way we left it before any merge conflicts.
		mergeHead, err := cmdOutputErr("git", "rev-parse", "MERGE_HEAD")
		if err != nil {
			dief("cannot sync-branch%s: no pending merge\n"+
				"If you accidentally ran 'git merge --continue',\n"+
				"then use 'git reset --hard HEAD^' to undo.\n", flag)
		}
		mergeHead = trim(mergeHead)
		if mergeHead != parentHash {
			dief("cannot sync-branch%s: MERGE_HEAD is %.7s, but origin/%s is %.7s", flag, mergeHead, parent, parentHash)
		}
		head := gitHash("HEAD")
		if head != branchHash {
			dief("cannot sync-branch%s: HEAD is %.7s, but origin/%s is %.7s", flag, head, branch, branchHash)
		}

		if HasUnstagedChanges() {
			dief("cannot sync-branch%s: unstaged changes (unresolved conflicts)\n"+
				"\tUse 'git status' to see them, 'git add' or 'git rm' to resolve them,\n"+
				"\tand then run 'git sync-branch -continue' again.\n", flag)
		}

		run("git", "commit", "-m", msg)
	}

	// Amend the merge message, which may be auto-generated by git
	// or may have been written by us during the post-conflict commit above,
	// to use our standard format and list the incorporated CLs.

	// Merge must never sync codereview.cfg,
	// because it contains the parent and branch config.
	// Force the on-branch copy back while amending the commit.
	cmdOutputDir(repoRoot(), "git", "checkout", "origin/"+branch, "--", "codereview.cfg")

	conflictMsg := ""
	if len(status.Conflicts) > 0 {
		conflictMsg = "Conflicts:\n\n- " + strings.Join(status.Conflicts, "\n- ") + "\n\n"
	}
	msg = fmt.Sprintf("%s\n\n%sMerge List:\n\n%s", msg, conflictMsg,
		cmdOutput("git", "log", "--format=format:+ %cd %h %s", "--date=short", "HEAD^1..HEAD^2"))
	run("git", "commit", "--amend", "-m", msg)

	fmt.Fprintf(stderr(), "\n")

	cmdPending([]string{"-c", "-l"})
	fmt.Fprintf(stderr(), "\n* Merge commit created.\nRun 'git codereview mail' to send for review.\n")
}
