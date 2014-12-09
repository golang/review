// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "testing"

func TestChange(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	t.Logf("master -> master")
	testMain(t, "change", "master")
	testRan(t, "git checkout -q master")

	testCommitMsg = "my commit msg"
	t.Logf("master -> work")
	testMain(t, "change", "work")
	testRan(t, "git checkout -q -b work",
		"git branch --set-upstream-to origin/master",
		"git commit -q --allow-empty -m my commit msg")

	t.Logf("work -> master")
	testMain(t, "change", "master")
	testRan(t, "git checkout -q master")

	t.Logf("master -> dev.branch")
	testMain(t, "change", "dev.branch")
	testRan(t, "git checkout -q -t -b dev.branch origin/dev.branch")
}
