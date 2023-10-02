// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestSubmitErrors(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	srv := newGerritServer(t)
	defer srv.done()

	t.Logf("> no commit")
	testMainDied(t, "submit")
	testPrintedStderr(t, "cannot submit: no changes pending")
	write(t, gt.client+"/file1", "", 0644)
	trun(t, gt.client, "git", "add", "file1")
	trun(t, gt.client, "git", "commit", "-m", "msg\n\nDO NOT SUBMIT\n\nChange-Id: I123456789\n")

	// Gerrit checks this too, but add a local check.
	t.Logf("> do not submit")
	testMainDied(t, "submit")
	testPrintedStderr(t, "DO NOT SUBMIT")
	trun(t, gt.client, "git", "commit", "--amend", "-m", "msg\n\nChange-Id: I123456789\n")

	t.Logf("> staged changes")
	write(t, gt.client+"/file1", "asdf", 0644)
	trun(t, gt.client, "git", "add", "file1")
	testMainDied(t, "submit")
	testPrintedStderr(t, "cannot submit: staged changes exist",
		"git status", "!git stash", "!git add", "git-codereview change")
	testNoStdout(t)

	t.Logf("> unstaged changes")
	write(t, gt.client+"/file1", "actual content", 0644)
	testMainDied(t, "submit")
	testPrintedStderr(t, "cannot submit: unstaged changes exist",
		"git status", "git stash", "git add", "git-codereview change")
	testNoStdout(t)
	testRan(t)
	trun(t, gt.client, "git", "add", "file1")
	trun(t, gt.client, "git", "commit", "--amend", "--no-edit")

	t.Logf("> not found")
	testMainDied(t, "submit")
	testPrintedStderr(t, "change not found on Gerrit server")

	const id = "I123456789"

	t.Logf("> malformed json")
	srv.setJSON(id, "XXX")
	testMainDied(t, "submit")
	testRan(t) // nothing
	testPrintedStderr(t, "malformed json response")

	t.Logf("> unexpected change status")
	srv.setJSON(id, `{"status": "UNEXPECTED"}`)
	testMainDied(t, "submit")
	testRan(t) // nothing
	testPrintedStderr(t, "cannot submit: unexpected Gerrit change status \"UNEXPECTED\"")

	t.Logf("> already merged")
	srv.setJSON(id, `{"status": "MERGED"}`)
	testMainDied(t, "submit")
	testRan(t) // nothing
	testPrintedStderr(t, "cannot submit: change already submitted, run 'git sync'")

	t.Logf("> abandoned")
	srv.setJSON(id, `{"status": "ABANDONED"}`)
	testMainDied(t, "submit")
	testRan(t) // nothing
	testPrintedStderr(t, "cannot submit: change abandoned")

	t.Logf("> missing approval")
	srv.setJSON(id, `{"status": "NEW", "labels": {"Code-Review": {}, "Color": {"Optional": true}}}`)
	testMainDied(t, "submit")
	testRan(t) // nothing
	testPrintedStderr(t, "cannot submit: change missing Code-Review approval")

	t.Logf("> rejection")
	srv.setJSON(id, `{"status": "NEW", "labels": {"Code-Review": {"rejected": {}}}}`)
	testMainDied(t, "submit")
	testRan(t) // nothing
	testPrintedStderr(t, "cannot submit: change has Code-Review rejection")

	t.Logf("> submit with unexpected status")
	const newJSON = `{"status": "NEW", "labels": {"Code-Review": {"approved": {}}}}`
	srv.setJSON(id, newJSON)
	srv.setReply("/a/changes/proj~main~I123456789/submit", gerritReply{body: ")]}'\n" + newJSON})
	testMainDied(t, "submit")
	testRan(t, "git push -q origin HEAD:refs/for/main")
	testPrintedStderr(t, "submit error: unexpected post-submit Gerrit change status \"NEW\"")
}

func TestSubmitTimeout(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()
	srv := newGerritServer(t)
	defer srv.done()

	gt.work(t)

	setJSON := func(json string) {
		srv.setReply("/a/changes/proj~main~I123456789", gerritReply{body: ")]}'\n" + json})
	}

	t.Log("> submit with timeout")
	const submittedJSON = `{"status": "SUBMITTED", "mergeable": true, "labels": {"Code-Review": {"approved": {}}}}`
	setJSON(submittedJSON)
	srv.setReply("/a/changes/proj~main~I123456789/submit", gerritReply{body: ")]}'\n" + submittedJSON})
	testMainDied(t, "submit")
	testRan(t, "git push -q origin HEAD:refs/for/main")
	testPrintedStderr(t, "cannot submit: timed out waiting for change to be submitted by Gerrit")
}

func TestSubmit(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()
	srv := newGerritServer(t)
	defer srv.done()

	gt.work(t)
	trun(t, gt.client, "git", "tag", "-f", "work.mailed")
	clientHead := strings.TrimSpace(trun(t, gt.client, "git", "log", "-n", "1", "--format=format:%H"))

	write(t, gt.server+"/file", "another change", 0644)
	trun(t, gt.server, "git", "add", "file")
	trun(t, gt.server, "git", "commit", "-m", "conflict")
	serverHead := strings.TrimSpace(trun(t, gt.server, "git", "log", "-n", "1", "--format=format:%H"))

	t.Log("> submit")
	var (
		newJSON       = `{"status": "NEW", "mergeable": true, "current_revision": "` + clientHead + `", "labels": {"Code-Review": {"approved": {}}}}`
		submittedJSON = `{"status": "SUBMITTED", "mergeable": true, "current_revision": "` + clientHead + `", "labels": {"Code-Review": {"approved": {}}}}`
		mergedJSON    = `{"status": "MERGED", "mergeable": true, "current_revision": "` + serverHead + `", "labels": {"Code-Review": {"approved": {}}}}`
	)
	submitted := false
	npoll := 0
	srv.setReply("/a/changes/proj~main~I123456789", gerritReply{f: func() gerritReply {
		if !submitted {
			return gerritReply{body: ")]}'\n" + newJSON}
		}
		if npoll++; npoll <= 2 {
			return gerritReply{body: ")]}'\n" + submittedJSON}
		}
		return gerritReply{body: ")]}'\n" + mergedJSON}
	}})
	srv.setReply("/a/changes/proj~main~I123456789/submit", gerritReply{f: func() gerritReply {
		if submitted {
			return gerritReply{status: 409}
		}
		submitted = true
		return gerritReply{body: ")]}'\n" + submittedJSON}
	}})

	testMain(t, "submit", "-n")
	testPrintedStderr(t, "submitting")
	testPrintedStderr(t, "stopped before submit")

	testMain(t, "pending", "-c", "-l")
	testPrintedStdout(t, "(current branch)")
	testPrintedStdout(t, "Files in this change:")

	testMain(t, "submit")
	testRan(t,
		"git fetch -q",
		"git checkout -q -B work "+serverHead+" --")
}

func TestSubmitMultiple(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	srv := newGerritServer(t)
	defer srv.done()

	cl1, cl2 := testSubmitMultiple(t, gt, srv)
	testMain(t, "submit", cl1.CurrentRevision, cl2.CurrentRevision)
}

func TestSubmitMultipleNamed(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	srv := newGerritServer(t)
	defer srv.done()

	_, _ = testSubmitMultiple(t, gt, srv)
	testMain(t, "submit", "HEAD^", "HEAD")
}

func TestSubmitInteractive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("see golang.org/issue/13406")
	}

	gt := newGitTest(t)
	defer gt.done()

	os.Setenv("GIT_EDITOR", ":")
	testMain(t, "submit", "-i")
	testPrintedStderr(t, "nothing to submit")

	srv := newGerritServer(t)
	defer srv.done()

	cl1, cl2 := testSubmitMultiple(t, gt, srv)

	os.Setenv("GIT_EDITOR", "echo > ")
	testMain(t, "submit", "-i")
	if cl1.Status != "NEW" {
		t.Fatalf("want cl1.Status == NEW; got %v", cl1.Status)
	}
	if cl2.Status != "NEW" {
		t.Fatalf("want cl2.Status == NEW; got %v", cl2.Status)
	}

	os.Setenv("GIT_EDITOR", "( echo "+cl1.CurrentRevision+"; echo; echo '# comment' ) > ")
	testMain(t, "submit", "-i")
	if cl1.Status != "MERGED" {
		t.Fatalf("want cl1.Status == MERGED; got %v", cl1.Status)
	}
	if cl2.Status != "NEW" {
		t.Fatalf("want cl2.Status == NEW; got %v", cl2.Status)
	}
}

func testSubmitMultiple(t *testing.T, gt *gitTest, srv *gerritServer) (*GerritChange, *GerritChange) {
	write(t, gt.client+"/file1", "", 0644)
	trun(t, gt.client, "git", "add", "file1")
	trun(t, gt.client, "git", "commit", "-m", "msg\n\nChange-Id: I0000001\n")
	hash1 := strings.TrimSpace(trun(t, gt.client, "git", "log", "-n", "1", "--format=format:%H"))

	write(t, gt.client+"/file2", "", 0644)
	trun(t, gt.client, "git", "add", "file2")
	trun(t, gt.client, "git", "commit", "-m", "msg\n\nChange-Id: I0000002\n")
	hash2 := strings.TrimSpace(trun(t, gt.client, "git", "log", "-n", "1", "--format=format:%H"))

	testMainDied(t, "submit")
	testPrintedStderr(t, "cannot submit: multiple changes pending")

	cl1 := GerritChange{
		Status:          "NEW",
		CurrentRevision: hash1,
		Labels:          map[string]*GerritLabel{"Code-Review": {Approved: new(GerritAccount)}},
	}
	cl2 := GerritChange{
		Status:          "NEW",
		CurrentRevision: hash2,
		Labels:          map[string]*GerritLabel{"Code-Review": {Approved: new(GerritAccount)}},
	}

	srv.setReply("/a/changes/proj~main~I0000001", gerritReply{f: func() gerritReply {
		return gerritReply{json: cl1}
	}})
	srv.setReply("/a/changes/proj~main~I0000001/submit", gerritReply{f: func() gerritReply {
		if cl1.Status != "NEW" {
			return gerritReply{status: 409}
		}
		cl1.Status = "MERGED"
		return gerritReply{json: cl1}
	}})
	srv.setReply("/a/changes/proj~main~I0000002", gerritReply{f: func() gerritReply {
		return gerritReply{json: cl2}
	}})
	srv.setReply("/a/changes/proj~main~I0000002/submit", gerritReply{f: func() gerritReply {
		if cl2.Status != "NEW" {
			return gerritReply{status: 409}
		}
		cl2.Status = "MERGED"
		return gerritReply{json: cl2}
	}})
	return &cl1, &cl2
}
