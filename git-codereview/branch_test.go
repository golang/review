// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCurrentBranch(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	t.Logf("on main")
	checkCurrentBranch(t, "main", "origin/main", false, "", "")

	t.Logf("on newbranch")
	trun(t, gt.client, "git", "checkout", "--no-track", "-b", "newbranch")
	checkCurrentBranch(t, "newbranch", "origin/main", false, "", "")

	t.Logf("making change")
	write(t, gt.client+"/file", "i made a change", 0644)
	trun(t, gt.client, "git", "commit", "-a", "-m", "My change line.\n\nChange-Id: I0123456789abcdef0123456789abcdef\n")
	checkCurrentBranch(t, "newbranch", "origin/main", true, "I0123456789abcdef0123456789abcdef", "My change line.")

	t.Logf("on dev.branch")
	trun(t, gt.client, "git", "checkout", "-t", "-b", "dev.branch", "origin/dev.branch")
	checkCurrentBranch(t, "dev.branch", "origin/dev.branch", false, "", "")

	t.Logf("on newdev")
	trun(t, gt.client, "git", "checkout", "-t", "-b", "newdev", "origin/dev.branch")
	checkCurrentBranch(t, "newdev", "origin/dev.branch", false, "", "")

	t.Logf("making change")
	write(t, gt.client+"/file", "i made another change", 0644)
	trun(t, gt.client, "git", "commit", "-a", "-m", "My other change line.\n\nChange-Id: I1123456789abcdef0123456789abcdef\n")
	checkCurrentBranch(t, "newdev", "origin/dev.branch", true, "I1123456789abcdef0123456789abcdef", "My other change line.")

	t.Logf("detached head mode")
	trun(t, gt.client, "git", "checkout", "main^0")
	checkCurrentBranch(t, "HEAD", "", false, "", "")
	trun(t, gt.client, "git", "checkout", "dev.branch^0")
	checkCurrentBranch(t, "HEAD", "origin/dev.branch", false, "", "")
}

func checkCurrentBranch(t *testing.T, name, origin string, hasPending bool, changeID, subject string) {
	t.Helper()
	b := CurrentBranch()
	if b.Name != name {
		t.Errorf("b.Name = %q, want %q", b.Name, name)
	}
	if x := b.OriginBranch(); x != origin {
		t.Errorf("b.OriginBranch() = %q, want %q", x, origin)
	}
	if x := b.HasPendingCommit(); x != hasPending {
		t.Errorf("b.HasPendingCommit() = %v, want %v", x, hasPending)
	}
	if work := b.Pending(); len(work) > 0 {
		c := work[0]
		if x := c.ChangeID; x != changeID {
			t.Errorf("b.Pending()[0].ChangeID = %q, want %q", x, changeID)
		}
		if x := c.Subject; x != subject {
			t.Errorf("b.Pending()[0].Subject = %q, want %q", x, subject)
		}
	}
}

func TestLocalBranches(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	t.Logf("on main")
	checkLocalBranches(t, "main")

	t.Logf("on dev branch")
	trun(t, gt.client, "git", "checkout", "-b", "newbranch")
	checkLocalBranches(t, "main", "newbranch")

	t.Logf("detached head mode")
	trun(t, gt.client, "git", "checkout", "HEAD^0")
	checkLocalBranches(t, "HEAD", "main", "newbranch")

	t.Logf("worktree")
	wt := filepath.Join(gt.tmpdir, "git-worktree")
	trun(t, gt.client, "git", "worktree", "add", "-b", "wtbranch", wt)
	checkLocalBranches(t, "HEAD", "main", "newbranch", "wtbranch")
}

func checkLocalBranches(t *testing.T, want ...string) {
	var names []string
	branches := LocalBranches()
	for _, b := range branches {
		names = append(names, b.Name)
	}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("LocalBranches() = %v, want %v", names, want)
	}
}

func TestAmbiguousRevision(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()
	gt.work(t)

	t.Logf("creating file paths that conflict with revision parameters")
	mkdir(t, gt.client+"/origin")
	write(t, gt.client+"/origin/main..work", "Uh-Oh! SpaghettiOs", 0644)
	mkdir(t, gt.client+"/work..origin")
	write(t, gt.client+"/work..origin/main", "Be sure to drink your Ovaltine", 0644)

	b := CurrentBranch()
	b.Submitted("I123456789")
}

func TestBranchpoint(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	// Get hash corresponding to checkout (known to server).
	hash := strings.TrimSpace(trun(t, gt.client, "git", "rev-parse", "HEAD"))

	// Any work we do after this point should find hash as branchpoint.
	for i := 0; i < 4; i++ {
		testMain(t, "branchpoint")
		t.Logf("numCommits=%d", i)
		testPrintedStdout(t, hash)
		testNoStderr(t)

		gt.work(t)
	}
}

func TestRebaseWork(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	// Get hash corresponding to checkout (known to server).
	// Any work we do after this point should find hash as branchpoint.
	hash := strings.TrimSpace(trun(t, gt.client, "git", "rev-parse", "HEAD"))

	testMainDied(t, "rebase-work", "-n")
	testPrintedStderr(t, "no pending work")

	write(t, gt.client+"/file", "uncommitted", 0644)
	testMainDied(t, "rebase-work", "-n")
	testPrintedStderr(t, "cannot rebase with uncommitted work")

	gt.work(t)

	for i := 0; i < 4; i++ {
		testMain(t, "rebase-work", "-n")
		t.Logf("numCommits=%d", i)
		testPrintedStderr(t, "git rebase -i "+hash)

		gt.work(t)
	}
}

func TestBranchpointMerge(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	// commit more work on main
	write(t, gt.server+"/file", "more work", 0644)
	trun(t, gt.server, "git", "commit", "-m", "work", "file")

	// update client
	trun(t, gt.client, "git", "checkout", "main")
	trun(t, gt.client, "git", "pull")

	hash := strings.TrimSpace(trun(t, gt.client, "git", "rev-parse", "HEAD"))

	// Merge dev.branch but delete the codereview.cfg that comes in,
	// or else we'll think we are on the wrong branch.
	trun(t, gt.client, "git", "merge", "-m", "merge", "origin/dev.branch")
	trun(t, gt.client, "git", "rm", "codereview.cfg")
	trun(t, gt.client, "git", "commit", "-m", "rm codereview.cfg")

	// check branchpoint is old head (despite this commit having two parents)
	bp := CurrentBranch().Branchpoint()
	if bp != hash {
		t.Logf("branches:\n%s", trun(t, gt.client, "git", "branch", "-a", "-v"))
		t.Logf("log:\n%s", trun(t, gt.client, "git", "log", "--graph", "--decorate"))
		t.Logf("log origin/main..HEAD:\n%s", trun(t, gt.client, "git", "log", "origin/main..HEAD"))
		t.Fatalf("branchpoint=%q, want %q", bp, hash)
	}
}
