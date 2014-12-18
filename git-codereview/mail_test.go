// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "testing"

func TestMail(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()
	gt.work(t)

	// fake auth information to avoid Gerrit error
	auth.host = "gerrit.fake"
	auth.user = "not-a-user"
	defer func() {
		auth.host = ""
		auth.user = ""
	}()

	testMain(t, "mail")
	testRan(t,
		"git push -q origin HEAD:refs/for/master",
		"git tag -f work.mailed")
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
	write(t, gt.client+"/"+b.Branchpoint()+"..HEAD", "foo")

	testMain(t, "mail", "-diff")
}
