// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"testing"
)

func TestMail(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()
	gt.work(t)

	h := CurrentBranch().Pending()[0].ShortHash

	// fake auth information to avoid Gerrit error
	auth.initialized = true
	auth.host = "gerrit.fake"
	auth.user = "not-a-user"
	defer func() {
		auth.initialized = false
		auth.host = ""
		auth.user = ""
	}()

	testMain(t, "mail")
	testRan(t,
		"git push -q origin HEAD:refs/for/main",
		"git tag --no-sign -f work.mailed "+h)
}

func TestDoNotMail(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()
	gt.work(t)
	trun(t, gt.client, "git", "commit", "--amend", "-m", "This is my commit.\n\nDO NOT MAIL\n")

	testMainDied(t, "mail")
	testPrintedStderr(t, "DO NOT MAIL")

	trun(t, gt.client, "git", "commit", "--amend", "-m", "fixup! This is my commit.")

	testMainDied(t, "mail")
	testPrintedStderr(t, "fixup! commit")

	trun(t, gt.client, "git", "commit", "--amend", "-m", "squash! This is my commit.")

	testMainDied(t, "mail")
	testPrintedStderr(t, "squash! commit")

	trun(t, gt.client, "git", "commit", "--amend", "-m", "This is my commit.\n\nDO NOT MAIL\n")

	// Do not mail even when the DO NOT MAIL is a parent of the thing we asked to mail.
	gt.work(t)
	testMainDied(t, "mail", "HEAD")
	testPrintedStderr(t, "DO NOT MAIL")
}

func TestDoNotMailTempFiles(t *testing.T) {
	// fake auth information to avoid Gerrit error
	auth.initialized = true
	auth.host = "gerrit.fake"
	auth.user = "not-a-user"
	defer func() {
		auth.initialized = false
		auth.host = ""
		auth.user = ""
	}()

	testFile := func(file string) {
		gt := newGitTest(t)
		defer gt.done()
		gt.work(t)
		gt.workFile(t, file)
		testMainDied(t, "mail", "HEAD")
		testPrintedStderr(t, "cannot mail temporary")
	}

	testFile("vim-backup.go~")
	testFile("#emacs-auto-save.go#")
	testFile(".#emacs-lock.go")

	// Do not mail when a parent of the thing we asked to mail has temporary files.
	gt := newGitTest(t)
	defer gt.done()
	gt.work(t)
	gt.workFile(t, "backup~")
	gt.work(t)
	testMainDied(t, "mail", "HEAD")
	testPrintedStderr(t, "cannot mail temporary")
}

func TestMailNonPrintables(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()
	gt.work(t)

	// fake auth information to avoid Gerrit error
	auth.initialized = true
	auth.host = "gerrit.fake"
	auth.user = "not-a-user"
	defer func() {
		auth.initialized = false
		auth.host = ""
		auth.user = ""
	}()

	trun(t, gt.client, "git", "commit", "--amend", "-m", "This is my commit.\x10\n\n")
	testMainDied(t, "mail")
	testPrintedStderr(t, "message with non-printable")

	// This should be mailed.
	trun(t, gt.client, "git", "commit", "--amend", "-m", "Printable unicode: \u263A \u0020. Spaces: \v \f \r \t\n\n")
	testMain(t, "mail", "HEAD")
}

func TestMailGitHub(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()
	gt.work(t)

	trun(t, gt.client, "git", "config", "remote.origin.url", "https://github.com/golang/go")

	testMainDied(t, "mail")
	testPrintedStderr(t, "git origin must be a Gerrit host, not GitHub: https://github.com/golang/go")
}

func TestMailAmbiguousRevision(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()
	gt.work(t)

	t.Logf("creating file that conflicts with revision parameter")
	b := CurrentBranch()
	mkdir(t, gt.client+"/origin")
	write(t, gt.client+"/"+b.Branchpoint()+"..HEAD", "foo", 0644)

	testMain(t, "mail", "-diff")
}

func TestMailMultiple(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	srv := newGerritServer(t)
	defer srv.done()

	gt.work(t)
	gt.work(t)
	gt.work(t)

	testMainDied(t, "mail")
	testPrintedStderr(t, "cannot mail: multiple changes pending")

	// Mail first two and test non-HEAD mail.
	h := CurrentBranch().Pending()[1].ShortHash
	testMain(t, "mail", "HEAD^")
	testRan(t,
		"git push -q origin "+h+":refs/for/main",
		"git tag --no-sign -f work.mailed "+h)

	// Mail HEAD.
	h = CurrentBranch().Pending()[0].ShortHash
	testMain(t, "mail", "HEAD")
	testRan(t,
		"git push -q origin HEAD:refs/for/main",
		"git tag --no-sign -f work.mailed "+h)
}

var reviewerLog = []string{
	"Fake 1 <r1@fake.com>",
	"Fake 1 <r1@fake.com>",
	"Fake 1 <r1@fake.com>",
	"Reviewer 1 <r1@golang.org>",
	"Reviewer 1 <r1@golang.org>",
	"Reviewer 1 <r1@golang.org>",
	"Reviewer 1 <r1@golang.org>",
	"Reviewer 1 <r1@golang.org>",
	"Other <other@golang.org>",
	"<anon@golang.org>",
	"Reviewer 2 <r2@new.example>",
	"Reviewer 2 <r2@old.example>",
	"Reviewer 2 <r2@old.example>",
	"Reviewer 2 <r2@old.example>",
}

func TestMailShort(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	// fake auth information to avoid Gerrit error
	auth.initialized = true
	auth.host = "gerrit.fake"
	auth.user = "not-a-user"
	defer func() {
		auth.initialized = false
		auth.host = ""
		auth.user = ""
	}()

	// Seed commit history with reviewers.
	for i, addr := range reviewerLog {
		write(t, gt.server+"/file", fmt.Sprintf("v%d", i), 0644)
		trun(t, gt.server, "git", "commit", "-a", "-m", "msg\n\nReviewed-by: "+addr+"\n")
	}
	trun(t, gt.client, "git", "pull")

	// Do some work.
	gt.work(t)

	h := CurrentBranch().Pending()[0].ShortHash

	testMain(t, "mail")
	testRan(t,
		"git push -q origin HEAD:refs/for/main",
		"git tag --no-sign -f work.mailed "+h)

	testMain(t, "mail", "-r", "r1")
	testRan(t,
		"git push -q origin HEAD:refs/for/main%r=r1@golang.org",
		"git tag --no-sign -f work.mailed "+h)

	testMain(t, "mail", "-r", "other,anon", "-cc", "r1,full@email.com")
	testRan(t,
		"git push -q origin HEAD:refs/for/main%r=other@golang.org,r=anon@golang.org,cc=r1@golang.org,cc=full@email.com",
		"git tag --no-sign -f work.mailed "+h)

	testMainDied(t, "mail", "-r", "other", "-r", "anon,r1,missing")
	testPrintedStderr(t, "unknown reviewer: missing")

	// Test shortOptOut.
	orig := shortOptOut
	defer func() { shortOptOut = orig }()
	shortOptOut = map[string]bool{"r2@old.example": true}
	testMain(t, "mail", "-r", "r2")
	testRan(t,
		"git push -q origin HEAD:refs/for/main%r=r2@new.example",
		"git tag --no-sign -f work.mailed "+h)
}

func TestWIP(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()
	gt.work(t)

	h := CurrentBranch().Pending()[0].ShortHash

	testMain(t, "mail", "-wip")
	testRan(t,
		"git push -q origin HEAD:refs/for/main%wip",
		"git tag --no-sign -f work.mailed "+h)
}

func TestMailTopic(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()
	gt.work(t)

	h := CurrentBranch().Pending()[0].ShortHash

	// fake auth information to avoid Gerrit error
	auth.initialized = true
	auth.host = "gerrit.fake"
	auth.user = "not-a-user"
	defer func() {
		auth.initialized = false
		auth.host = ""
		auth.user = ""
	}()

	testMainDied(t, "mail", "-topic", "contains,comma")
	testPrintedStderr(t, "topic may not contain a comma")

	testMain(t, "mail", "-topic", "test-topic")
	testRan(t,
		"git push -q origin HEAD:refs/for/main%topic=test-topic",
		"git tag --no-sign -f work.mailed "+h)
}

func TestMailHashtag(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()
	gt.work(t)

	h := CurrentBranch().Pending()[0].ShortHash

	// fake auth information to avoid Gerrit error
	auth.initialized = true
	auth.host = "gerrit.fake"
	auth.user = "not-a-user"
	defer func() {
		auth.initialized = false
		auth.host = ""
		auth.user = ""
	}()

	testMain(t, "mail", "-hashtag", "test1,test2")
	testRan(t,
		"git push -q origin HEAD:refs/for/main%hashtag=test1,hashtag=test2",
		"git tag --no-sign -f work.mailed "+h)
	testMain(t, "mail", "-hashtag", "")
	testRan(t,
		"git push -q origin HEAD:refs/for/main",
		"git tag --no-sign -f work.mailed "+h)

	testMainDied(t, "mail", "-hashtag", "test1,,test3")
	testPrintedStderr(t, "hashtag may not contain empty tags")
}

func TestMailEmpty(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	// fake auth information to avoid Gerrit error
	auth.initialized = true
	auth.host = "gerrit.fake"
	auth.user = "not-a-user"
	defer func() {
		auth.initialized = false
		auth.host = ""
		auth.user = ""
	}()

	testMain(t, "change", "work")
	testRan(t, "git checkout -q -b work HEAD",
		"git branch -q --set-upstream-to origin/main")

	t.Logf("creating empty change")
	testCommitMsg = "foo: this commit will be empty"
	testMain(t, "change")
	testRan(t, "git commit -q --allow-empty -m foo: this commit will be empty")

	h := CurrentBranch().Pending()[0].ShortHash

	testMainDied(t, "mail")
	testPrintedStderr(t, "cannot mail: commit "+h+" is empty")
}
