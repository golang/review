// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestChange(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	t.Logf("main -> main")
	testMain(t, "change", "main")
	testRan(t, "git checkout -q main")
	branchpoint := strings.TrimSpace(trun(t, gt.client, "git", "rev-parse", "HEAD"))

	testCommitMsg = "foo: my commit msg"
	t.Logf("main -> work")
	testMain(t, "change", "work")
	testRan(t, "git checkout -q -b work HEAD",
		"git branch -q --set-upstream-to origin/main")

	t.Logf("work -> main")
	testMain(t, "change", "main")
	testRan(t, "git checkout -q main")

	t.Logf("main -> work with staged changes")
	write(t, gt.client+"/file", "new content", 0644)
	trun(t, gt.client, "git", "add", "file")
	testMain(t, "change", "work")
	testRan(t, "git checkout -q work",
		"git commit -q --allow-empty -m foo: my commit msg")

	t.Logf("work -> work2")
	testMain(t, "change", "work2")
	testRan(t, "git checkout -q -b work2 "+branchpoint,
		"git branch -q --set-upstream-to origin/main")

	t.Logf("work2 -> dev.branch")
	testMain(t, "change", "dev.branch")
	testRan(t, "git checkout -q -t -b dev.branch origin/dev.branch")

	testMain(t, "pending", "-c")
	testPrintedStdout(t, "tracking dev.branch")
	testMain(t, "change", "main")
	testMain(t, "change", "work2")

	t.Logf("server work × 2")
	gt.serverWork(t)
	gt.serverWork(t)
	testMain(t, "sync")

	t.Logf("-> work with server ahead")
	testMain(t, "change", "work")
	testPrintedStderr(t, "warning: 2 commits behind origin/main; run 'git codereview sync' to update")
}

func TestChangeHEAD(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	testMainDied(t, "change", "HeAd")
	testPrintedStderr(t, "invalid branch name \"HeAd\": ref name HEAD is reserved for git")
}

func TestMessageRE(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"blah", false},
		{"[release-branch.go1.4] blah", false},
		{"[release-branch.go1.4] math: fix cosine", true},
		{"math: fix cosine", true},
		{"math/rand: make randomer", true},
		{"math/rand, crypto/rand: fix random sources", true},
		{"cmd/internal/rsc.io/x86: update from external repo", true},
	} {
		got := messageRE.MatchString(c.in)
		if got != c.want {
			t.Errorf("MatchString(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestChangeAmendCommit(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	testCommitMsg = "foo: amended commit message"
	gt.work(t)

	write(t, gt.client+"/file", "new content in work to be amend", 0644)
	trun(t, gt.client, "git", "add", "file")
	testMain(t, "change")
}

func TestChangeFailAmendWithMultiplePending(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	testCommitMsg = "foo: amended commit message"
	gt.work(t)
	gt.work(t)

	write(t, gt.client+"/file", "new content in work to be amend", 0644)
	trun(t, gt.client, "git", "add", "file")
	testMainDied(t, "change")
	testPrintedStderr(t, "multiple changes pending")
}

func TestChangeCL(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	srv := newGerritServer(t)
	defer srv.done()

	// Ensure that 'change' with a CL accepts we have gerrit. Test address is injected by newGerritServer.
	write(t, gt.server+"/codereview.cfg", "gerrit: on", 0644)
	trun(t, gt.server, "git", "add", "codereview.cfg")
	trun(t, gt.server, "git", "commit", "-m", "codereview.cfg on main")
	trun(t, gt.client, "git", "pull")
	defer srv.done()

	hash1 := trim(trun(t, gt.server, "git", "rev-parse", "dev.branch"))
	hash2 := trim(trun(t, gt.server, "git", "rev-parse", "release.branch"))
	trun(t, gt.server, "git", "update-ref", "refs/changes/00/100/1", hash1)
	trun(t, gt.server, "git", "update-ref", "refs/changes/00/100/2", hash2)
	trun(t, gt.server, "git", "update-ref", "refs/changes/00/100/3", hash1)
	srv.setReply("/a/changes/100", gerritReply{f: func() gerritReply {
		changeJSON := `{
			"current_revision": "HASH",
			"revisions": {
				"HASH": {
					"_number": 3
				}
			}
		}`
		changeJSON = strings.Replace(changeJSON, "HASH", hash1, -1)
		return gerritReply{body: ")]}'\n" + changeJSON}
	}})

	checkChangeCL := func(arg, ref, hash string) {
		testMain(t, "change", "main")
		testMain(t, "change", arg)
		testRan(t,
			fmt.Sprintf("git fetch -q origin %s", ref),
			"git checkout -q FETCH_HEAD")
		if hash != trim(trun(t, gt.client, "git", "rev-parse", "HEAD")) {
			t.Fatalf("hash do not match for CL %s", arg)
		}
	}

	checkChangeCL("100/1", "refs/changes/00/100/1", hash1)
	checkChangeCL("100/2", "refs/changes/00/100/2", hash2)
	checkChangeCL("100", "refs/changes/00/100/3", hash1)

	// turn off gerrit, make it look like we are on GitHub
	write(t, gt.server+"/codereview.cfg", "nothing: here", 0644)
	trun(t, gt.server, "git", "add", "codereview.cfg")
	trun(t, gt.server, "git", "commit", "-m", "new codereview.cfg on main")
	testMain(t, "change", "main")
	trun(t, gt.client, "git", "pull", "-r")
	trun(t, gt.client, "git", "remote", "set-url", "origin", "https://github.com/google/not-a-project")

	testMain(t, "change", "-n", "123")
	testNoStdout(t)
	testPrintedStderr(t,
		"git fetch -q origin pull/123/head",
		"git checkout -q FETCH_HEAD",
	)
}

func TestChangeWithMessage(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	testMain(t, "change", "new_branch")
	testMain(t, "change", "-m", "foo: some commit message")
	testRan(t, "git commit -q --allow-empty -m foo: some commit message")
}

func TestChangeWithSignoff(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	testMain(t, "change", "new_branch")
	// There are no staged changes, hence an empty commit will be created.
	// Hence we also need a commit message.
	testMain(t, "change", "-s", "-m", "foo: bar")
	testRan(t, "git commit -q --allow-empty -m foo: bar -s")
}
