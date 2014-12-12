// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "testing"

func TestSync(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	testMain(t, "change", "work")

	// check for error with unstaged changes
	write(t, gt.client+"/file1", "")
	trun(t, gt.client, "git", "add", "file1")
	write(t, gt.client+"/file1", "actual content")
	testMainDied(t, "sync")
	testPrintedStderr(t, "cannot sync: unstaged changes exist",
		"git status", "git stash", "git add", "git-review change")
	testNoStdout(t)

	// check for error with staged changes
	trun(t, gt.client, "git", "add", "file1")
	testMainDied(t, "sync")
	testPrintedStderr(t, "cannot sync: staged changes exist",
		"git status", "!git stash", "!git add", "git-review change")
	testNoStdout(t)

	// check for success after stash
	trun(t, gt.client, "git", "stash")
	testMain(t, "sync")
	testNoStdout(t)
	testNoStderr(t)

	// make server 1 step ahead of client
	write(t, gt.server+"/file", "new content")
	trun(t, gt.server, "git", "add", "file")
	trun(t, gt.server, "git", "commit", "-m", "msg")

	// check for success
	testMain(t, "sync")
	testNoStdout(t)
	testNoStderr(t)
}

// TODO: Add TestSyncRebase?
