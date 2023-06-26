// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"strings"
	"testing"
)

func TestReword(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	gt.work(t)
	gt.work(t)
	gt.work(t)
	trun(t, gt.client, "git", "tag", "MSG3")
	gt.work(t)
	trun(t, gt.client, "git", "tag", "MSG4")
	const fakeName = "Grace R. Emlin"
	os.Setenv("GIT_AUTHOR_NAME", fakeName)
	gt.work(t)
	os.Unsetenv("GIT_AUTHOR_NAME")

	write(t, gt.client+"/file", "pending work", 0644) // would make git checkout unhappy

	testMainDied(t, "rebase-work")
	testNoStdout(t)
	testPrintedStderr(t, "cannot rebase with uncommitted work")

	os.Setenv("GIT_EDITOR", "sed -i.bak -e s/msg/MESSAGE/")
	defer os.Unsetenv("GIT_EDITOR")

	testMain(t, "reword", "MSG3", "MSG4")
	testNoStdout(t)
	testPrintedStderr(t, "editing messages (new texts logged in",
		".git"+string(os.PathSeparator)+"REWORD_MSGS in case of failure)")

	testMain(t, "pending", "-c", "-l", "-s")
	testNoStderr(t)
	testPrintedStdout(t,
		"msg #2",
		"MESSAGE #3",
		"MESSAGE #4",
		"msg #5",
	)

	testMain(t, "reword") // reword all
	testNoStdout(t)
	testPrintedStderr(t, "editing messages (new texts logged in",
		".git"+string(os.PathSeparator)+"REWORD_MSGS in case of failure)")

	testMain(t, "pending", "-c", "-l", "-s")
	testNoStderr(t)
	testPrintedStdout(t,
		"MESSAGE #2",
		"MESSAGE #3",
		"MESSAGE #4",
		"MESSAGE #5",
	)

	out := trun(t, gt.client, "git", "log", "-n1")
	if !strings.Contains(out, fakeName) {
		t.Fatalf("reword lost author name (%s):\n%s", fakeName, out)
	}

	write(t, gt.client+"/codereview.cfg", "issuerepo: my/issues\ngerrit: on\n", 0644)

	os.Setenv("GIT_EDITOR", "sed -i.bak -e 's/Change-Id:.*/Fixes #12345/'")
	testMain(t, "reword", "HEAD") // editing single commit message
	out = trun(t, gt.client, "git", "log", "-n1", "HEAD")
	if !strings.Contains(out, "Fixes my/issues#12345") || !strings.Contains(out, "Change-Id:") {
		t.Fatalf("reword single commit did not run commit message hook:\n%s", out)
	}

	trun(t, gt.client, "git", "reset", "--hard", "MSG4")
	testMain(t, "reword") // editing all commit messages
	out = trun(t, gt.client, "git", "log", "-n1", "HEAD")
	if !strings.Contains(out, "Fixes my/issues#12345") || !strings.Contains(out, "Change-Id:") {
		t.Fatalf("reword multiple commits did not run commit message hook:\n%s", out)
	}
}
