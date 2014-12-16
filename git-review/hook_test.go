// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHookCommitMsg(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	// Check that hook adds Change-Id.
	write(t, gt.client+"/msg.txt", "Test message.\n")
	testMain(t, "hook-invoke", "commit-msg", gt.client+"/msg.txt")
	data, err := ioutil.ReadFile(gt.client + "/msg.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("\n\nChange-Id: ")) {
		t.Fatalf("after hook-invoke commit-msg, missing Change-Id:\n%s", data)
	}

	// Check that hook is no-op when Change-Id is already present.
	testMain(t, "hook-invoke", "commit-msg", gt.client+"/msg.txt")
	data1, err := ioutil.ReadFile(gt.client + "/msg.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, data1) {
		t.Fatalf("second hook-invoke commit-msg changed Change-Id:\nbefore:\n%s\n\nafter:\n%s", data, data1)
	}

	// Check that hook fails when message is empty.
	write(t, gt.client+"/empty.txt", "\n\n# just a file with\n# comments\n")
	testMainDied(t, "hook-invoke", "commit-msg", gt.client+"/empty.txt")
	const want = "git-review: empty commit message\n"
	if got := stderr.String(); got != want {
		t.Fatalf("unexpected output:\ngot: %q\nwant: %q", got, want)
	}
}

func TestHooks(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	gt.removeStubHooks()
	testMain(t, "hooks") // install hooks

	data, err := ioutil.ReadFile(gt.client + "/.git/hooks/commit-msg")
	if err != nil {
		t.Fatalf("hooks did not write commit-msg hook: %v", err)
	}
	if string(data) != "#!/bin/sh\nexec git-review hook-invoke commit-msg \"$@\"\n" {
		t.Fatalf("invalid commit-msg hook:\n%s", string(data))
	}
}

func TestHooksOverwriteOldCommitMsg(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	write(t, gt.client+"/.git/hooks/commit-msg", oldCommitMsgHook)
	testMain(t, "hooks") // install hooks
	data, err := ioutil.ReadFile(gt.client + "/.git/hooks/commit-msg")
	if err != nil {
		t.Fatalf("hooks did not write commit-msg hook: %v", err)
	}
	if string(data) == oldCommitMsgHook {
		t.Fatalf("hooks left old commit-msg hook in place")
	}
	if string(data) != "#!/bin/sh\nexec git-review hook-invoke commit-msg \"$@\"\n" {
		t.Fatalf("invalid commit-msg hook:\n%s", string(data))
	}
}

func TestHookCommitMsgFromGit(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	trun(t, gt.pwd, "go", "build", "-o", gt.client+"/git-review")
	path := os.Getenv("PATH")
	defer os.Setenv("PATH", path)
	os.Setenv("PATH", gt.client+string(filepath.ListSeparator)+path)

	gt.removeStubHooks()
	testMain(t, "hooks") // install hooks
	write(t, gt.client+"/file", "more data")
	trun(t, gt.client, "git", "add", "file")
	trun(t, gt.client, "git", "commit", "-m", "mymsg")

	log := trun(t, gt.client, "git", "log", "-n", "1")
	if !strings.Contains(log, "mymsg") {
		t.Fatalf("did not find mymsg in git log output:\n%s", log)
	}
	// The 4 spaces are because git indents the commit message proper.
	if !strings.Contains(log, "\n    \n    Change-Id:") {
		t.Fatalf("did not find Change-Id in git log output:\n%s", log)
	}
}
