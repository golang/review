// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSync(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	testMain(t, "change", "work")

	// check for error with unstaged changes
	write(t, gt.client+"/file1", "", 0644)
	trun(t, gt.client, "git", "add", "file1")
	write(t, gt.client+"/file1", "actual content", 0644)
	testMainDied(t, "sync")
	testPrintedStderr(t, "cannot sync: unstaged changes exist",
		"git status", "git stash", "git add", "git-codereview change")
	testNoStdout(t)

	// check for error with staged changes
	trun(t, gt.client, "git", "add", "file1")
	testMainDied(t, "sync")
	testPrintedStderr(t, "cannot sync: staged changes exist",
		"git status", "!git stash", "!git add", "git-codereview change")
	testNoStdout(t)

	// check for success after stash
	trun(t, gt.client, "git", "stash")
	testMain(t, "sync")
	testNoStdout(t)
	testNoStderr(t)

	// make server 1 step ahead of client
	write(t, gt.server+"/file", "new content", 0644)
	trun(t, gt.server, "git", "add", "file")
	trun(t, gt.server, "git", "commit", "-m", "msg")

	// check for success
	testMain(t, "sync")
	testNoStdout(t)
	testNoStderr(t)
}

func TestSyncRebase(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	// Suppress --reapply-cherry-picks hint.
	trun(t, gt.client, "git", "config", "advice.skippedCherryPicks", "false")

	// client 3 ahead
	gt.work(t)
	gt.work(t)
	gt.work(t)

	b := CurrentBranch()
	if len(b.Pending()) != 3 {
		t.Fatalf("have %d pending CLs, want 3", len(b.Pending()))
	}
	top := b.Pending()[0].Hash

	// check for success for sync no-op
	testMain(t, "sync")
	testNoStdout(t)
	testNoStderr(t)

	b = CurrentBranch()
	if len(b.Pending()) != 3 {
		t.Fatalf("have %d pending CLs after no-op sync, want 3", len(b.Pending()))
	}
	if b.Pending()[0].Hash != top {
		t.Fatalf("CL hashes changed during no-op sync")
	}

	// submit first two CLs - gt.serverWork does same thing gt.work does, but on server

	gt.serverWork(t)
	gt.serverWorkUnrelated(t, "") // wedge in unrelated work to get different hashes
	gt.serverWork(t)

	testMain(t, "sync")
	testNoStdout(t)
	testNoStderr(t)

	// there should be one left, and it should be a different hash
	b = CurrentBranch()
	if len(b.Pending()) != 1 {
		t.Fatalf("have %d pending CLs after submitting two, want 1", len(b.Pending()))
	}
	if b.Pending()[0].Hash == top {
		t.Fatalf("CL hashes DID NOT change during sync after submit")
	}

	// submit final change
	gt.serverWork(t)

	testMain(t, "sync")
	testNoStdout(t)
	testNoStderr(t)

	// there should be none left
	b = CurrentBranch()
	if len(b.Pending()) != 0 {
		t.Fatalf("have %d pending CLs after final sync, want 0", len(b.Pending()))
	}

	// sync -v prints git output.
	// also exercising -v parsing.
	testMain(t, "sync", "-v=true")
	testNoStdout(t)
	testPrintedStderr(t, "git -c advice.skippedCherryPicks=false pull -q -r origin main")

	testMain(t, "sync", "-v=1")
	testNoStdout(t)
	testPrintedStderr(t, "git -c advice.skippedCherryPicks=false pull -q -r origin main")

	testMain(t, "sync", "-v")
	testNoStdout(t)
	testPrintedStderr(t, "git -c advice.skippedCherryPicks=false pull -q -r origin main")

	testMain(t, "sync", "-v=false")
	testNoStdout(t)
	testNoStderr(t)
}

func TestBranchConfig(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()
	gt.work(t) // do the main-branch work setup now to avoid unwanted change below

	trun(t, gt.client, "git", "checkout", "dev.branch")
	testMain(t, "pending", "-c", "-l")
	// The !+ means reject any output with a +, which introduces a pending commit.
	// There should be no pending commits.
	testPrintedStdout(t, "dev.branch (current branch, tracking dev.branch)", "!+")

	// If we make a branch with raw git,
	// the codereview.cfg should help us see the tracking info
	// even though git doesn't know the right upstream.
	trun(t, gt.client, "git", "checkout", "-b", "mywork", "HEAD^0")
	if out, err := cmdOutputDirErr(gt.client, "git", "rev-parse", "--abbrev-ref", "@{u}"); err == nil {
		t.Fatalf("git knows @{u} but should not:\n%s", out)
	}
	testMain(t, "pending", "-c", "-l")
	testPrintedStdout(t, "mywork (current branch, tracking dev.branch)", "!+")
	// Pending should have set @{u} correctly for us.
	if out, err := cmdOutputDirErr(gt.client, "git", "rev-parse", "--abbrev-ref", "@{u}"); err != nil {
		t.Fatalf("git does not know @{u} but should: %v\n%s", err, out)
	} else if out = strings.TrimSpace(out); out != "origin/dev.branch" {
		t.Fatalf("git @{u} = %q, want %q", out, "origin/dev.branch")
	}

	// Even if we add a pending commit, we should see the right tracking info.
	// The !codereview.cfg makes sure we do not see the codereview.cfg-changing
	// commit from the server in the output. (That would happen if we were printing
	// new commits relative to main instead of relative to dev.branch.)
	gt.work(t)
	testMain(t, "pending", "-c", "-l")
	testHideRevHashes(t)
	testPrintedStdout(t, "mywork REVHASH..REVHASH (current branch, tracking dev.branch)", "!codereview.cfg")

	// If we make a new branch using the old work HEAD
	// then we should be back to something tracking main.
	trun(t, gt.client, "git", "checkout", "-b", "mywork2", "work^0")
	gt.work(t)
	testMain(t, "pending", "-c", "-l")
	testHideRevHashes(t)
	testPrintedStdout(t, "mywork2 REVHASH..REVHASH (current branch)", "!codereview.cfg")

	// Now look at all branches, which should use the appropriate configs
	// from the commits on each branch.
	testMain(t, "pending", "-l")
	testHideRevHashes(t)
	testPrintedStdout(t, "mywork2 REVHASH..REVHASH (current branch)",
		"mywork REVHASH..REVHASH (tracking dev.branch)",
		"work REVHASH..REVHASH\n") // the \n checks for not having a (tracking main)
}

func TestDetachedHead(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()
	gt.work(t) // do the main-branch work setup now to avoid unwanted change below

	trun(t, gt.client, "git", "checkout", "HEAD^0") // enter detached HEAD mode with one pending commit

	// Pending should succeed and just print very little.
	testMain(t, "pending", "-c", "-l")
	testPrintedStdout(t, "HEAD (detached, remote branch unknown)", "!+")
	testNoStderr(t)

	// Sync, branchpoint should fail - branch unknown
	// (there is no "upstream" coming from git, and there's no branch line
	// in codereview.cfg on main in the test setup).
	for _, cmd := range []string{"sync", "branchpoint"} {
		testMainDied(t, cmd)
		testNoStdout(t)
		testPrintedStderr(t, "cannot "+cmd+": no origin branch (in detached HEAD mode)")
	}

	// If we switch to dev.branch, which does have a branch line,
	// detached HEAD mode should be able to find the branchpoint.
	trun(t, gt.client, "git", "checkout", "dev.branch")
	gt.work(t)
	trun(t, gt.client, "git", "checkout", "HEAD^0")
}

func TestSyncBranch(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	gt.serverWork(t)
	gt.serverWork(t)
	trun(t, gt.server, "git", "checkout", "dev.branch")
	gt.serverWorkUnrelated(t, "")
	gt.serverWorkUnrelated(t, "")
	gt.serverWorkUnrelated(t, "")
	trun(t, gt.server, "git", "checkout", "main")

	testMain(t, "change", "dev.branch")
	testMain(t, "sync-branch")
	testHideRevHashes(t)
	testPrintedStdout(t, "[dev.branch] all: merge main (REVHASH) into dev.branch",
		"Merge List:",
		"+ DATE REVHASH msg #2",
		"+ DATE REVHASH",
	)
	testPrintedStderr(t, "* Merge commit created.",
		"Run 'git codereview mail' to send for review.")
}

func TestSyncBranchWorktree(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	gt.serverWork(t)
	gt.serverWork(t)
	trun(t, gt.server, "git", "checkout", "dev.branch")
	gt.serverWorkUnrelated(t, "")
	gt.serverWorkUnrelated(t, "")
	gt.serverWorkUnrelated(t, "")
	trun(t, gt.server, "git", "checkout", "main")

	wt := filepath.Join(gt.tmpdir, "git-worktree")
	trun(t, gt.client, "git", "worktree", "add", "-b", "dev.branch", wt, "origin/dev.branch")
	if err := os.Chdir(wt); err != nil {
		t.Fatal(err)
	}

	testMain(t, "sync-branch")
	testHideRevHashes(t)
	testPrintedStdout(t, "[dev.branch] all: merge main (REVHASH) into dev.branch")
}

func TestSyncBranchMergeBack(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	// server does not default to having a codereview.cfg on main,
	// but sync-branch requires one.
	var mainCfg = []byte("branch: main\n")
	os.WriteFile(filepath.Join(gt.server, "codereview.cfg"), mainCfg, 0666)
	trun(t, gt.server, "git", "add", "codereview.cfg")
	trun(t, gt.server, "git", "commit", "-m", "config for main", "codereview.cfg")

	gt.serverWork(t)
	gt.serverWork(t)
	trun(t, gt.server, "git", "checkout", "dev.branch")
	gt.serverWorkUnrelated(t, "work on dev.branch#1")
	gt.serverWorkUnrelated(t, "work on dev.branch#2")
	gt.serverWorkUnrelated(t, "work on dev.branch#3")
	trun(t, gt.server, "git", "checkout", "main")
	testMain(t, "change", "dev.branch")

	// Merge back should fail because there are
	// commits in the parent that have not been merged here yet.
	testMainDied(t, "sync-branch", "--merge-back-to-parent")
	testNoStdout(t)
	testPrintedStderr(t, "parent has new commits")

	// Bring them in, creating a merge commit.
	testMain(t, "sync-branch")

	// Merge back should still fail - merge commit not submitted yet.
	testMainDied(t, "sync-branch", "--merge-back-to-parent")
	testNoStdout(t)
	testPrintedStderr(t, "pending changes exist")

	// Push the changes back to the server.
	// (In a real system this would happen through Gerrit.)
	trun(t, gt.client, "git", "push", "origin")

	devHash := trim(trun(t, gt.client, "git", "rev-parse", "HEAD"))

	// Nothing should be pending.
	testMain(t, "pending", "-c")
	testPrintedStdout(t, "!+")

	// This time should work.
	testMain(t, "sync-branch", "--merge-back-to-parent")
	testPrintedStdout(t,
		"\n\tall: REVERSE MERGE dev.branch ("+devHash[:7]+") into main",
		"This commit is a REVERSE MERGE.",
		"It merges dev.branch back into its parent branch, main.",
		"This marks the end of development on dev.branch.",
		"Merge List:",
		"msg #2",
		"!config for main",
	)
	testPrintedStderr(t, "Merge commit created.")

	data, err := os.ReadFile(filepath.Join(gt.client, "codereview.cfg"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, mainCfg) {
		t.Fatalf("codereview.cfg not restored:\n%s", data)
	}

	// Check pending knows what branch it is on.
	testMain(t, "pending", "-c")
	testHideRevHashes(t)
	testPrintedStdout(t,
		"dev.branch REVHASH..REVHASH (current branch)", // no "tracking dev.branch" anymore
		"REVHASH (merge=REVHASH)",
		"Merge List:",
		"!config for main",
	)

	// Check that mail sends the merge to the right place!
	testMain(t, "mail", "-n")
	testNoStdout(t)
	testPrintedStderr(t,
		"git push -q origin HEAD:refs/for/main",
		"git tag --no-sign -f dev.branch.mailed",
	)
}

func TestSyncBranchConflict(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	gt.serverWork(t)
	gt.serverWork(t)
	trun(t, gt.server, "git", "checkout", "dev.branch")
	gt.serverWork(t)
	trun(t, gt.server, "git", "checkout", "main")

	testMain(t, "change", "dev.branch")

	testMainDied(t, "sync-branch")
	testNoStdout(t)
	testPrintedStderr(t,
		"git-codereview: sync-branch: merge conflicts in:",
		"	- file",
		"Please fix them (use 'git status' to see the list again),",
		"then 'git add' or 'git rm' to resolve them,",
		"and then 'git sync-branch -continue' to continue.",
		"Or run 'git merge --abort' to give up on this sync-branch.",
	)

	// Other client-changing commands should fail now.
	testDisallowed := func(cmd ...string) {
		t.Helper()
		testMainDied(t, cmd...)
		testNoStdout(t)
		testPrintedStderr(t,
			"git-codereview: cannot "+cmd[0]+": found pending merge",
			"Run 'git codereview sync-branch -continue' if you fixed",
			"merge conflicts after a previous sync-branch operation.",
			"Or run 'git merge --abort' to give up on the sync-branch.",
		)
	}
	testDisallowed("change", "main")
	testDisallowed("sync-branch")

	// throw away server changes to resolve merge
	trun(t, gt.client, "git", "checkout", "HEAD", "file")

	// Still cannot change branches even with conflicts resolved.
	testDisallowed("change", "main")
	testDisallowed("sync-branch")

	testMain(t, "sync-branch", "-continue")
	testHideRevHashes(t)
	testPrintedStdout(t,
		"[dev.branch] all: merge main (REVHASH) into dev.branch",
		"+ REVHASH (merge=REVHASH)",
		"Conflicts:",
		"- file",
		"Merge List:",
		"+ DATE REVHASH msg #2",
		"+ DATE REVHASH",
	)
	testPrintedStderr(t,
		"* Merge commit created.",
		"Run 'git codereview mail' to send for review.",
	)

	// Check that pending only shows the merge, not more commits.
	testMain(t, "pending", "-c", "-l", "-s")
	n := strings.Count(testStdout.String(), "+")
	if n != 1 {
		t.Fatalf("git pending shows %d commits, want 1:\n%s", n, testStdout.String())
	}
	testNoStderr(t)

	// Check that mail sends the merge to the right place!
	testMain(t, "mail", "-n")
	testNoStdout(t)
	testPrintedStderr(t,
		"git push -q origin HEAD:refs/for/dev.branch",
		"git tag --no-sign -f dev.branch.mailed",
	)
}
