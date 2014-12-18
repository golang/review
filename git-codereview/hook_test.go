// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
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
	const want = "git-codereview: empty commit message\n"
	if got := stderr.String(); got != want {
		t.Fatalf("unexpected output:\ngot: %q\nwant: %q", got, want)
	}
}

func TestHookPreCommit(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	// Write out a non-Go file.
	testMain(t, "change", "mybranch")
	write(t, gt.client+"/msg.txt", "A test message.")
	trun(t, gt.client, "git", "add", "msg.txt")
	testMain(t, "hook-invoke", "pre-commit") // should be no-op

	if err := os.MkdirAll(gt.client+"/test/bench", 0755); err != nil {
		t.Fatal(err)
	}
	write(t, gt.client+"/bad.go", badGo)
	write(t, gt.client+"/good.go", goodGo)
	write(t, gt.client+"/test/bad.go", badGo)
	write(t, gt.client+"/test/good.go", goodGo)
	write(t, gt.client+"/test/bench/bad.go", badGo)
	write(t, gt.client+"/test/bench/good.go", goodGo)
	trun(t, gt.client, "git", "add", ".")

	testMainDied(t, "hook-invoke", "pre-commit")
	testPrintedStderr(t, "gofmt needs to format these files (run 'git gofmt'):",
		"bad.go", "!good.go", "!test/bad", "test/bench/bad.go")

	write(t, gt.client+"/broken.go", brokenGo)
	trun(t, gt.client, "git", "add", "broken.go")
	testMainDied(t, "hook-invoke", "pre-commit")
	testPrintedStderr(t, "gofmt needs to format these files (run 'git gofmt'):",
		"bad.go", "!good.go", "!test/bad", "test/bench/bad.go",
		"gofmt reported errors:", "broken.go")
}

func TestHookPreCommitUnstaged(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()
	gt.work(t)

	write(t, gt.client+"/bad.go", badGo)
	write(t, gt.client+"/good.go", goodGo)

	// The pre-commit hook is being asked about files in the index.
	// Make sure it is not looking at files in the working tree (current directory) instead.
	// There are three possible kinds of file: good, bad (misformatted), and broken (syntax error).
	// There are also three possible places files live: the most recent commit, the index,
	// and the working tree. We write a sequence of files that cover all possible
	// combination of kinds of file in the various places. For example,
	// good-bad-broken.go is a good file in the most recent commit,
	// a bad file in the index, and a broken file in the working tree.
	// After creating these files, we check that the gofmt hook reports
	// about the index only.

	const N = 3
	name := []string{"good", "bad", "broken"}
	content := []string{goodGo, badGo, brokenGo}
	var wantErr []string
	var allFiles []string
	writeFiles := func(n int) {
		allFiles = nil
		wantErr = nil
		for i := 0; i < N*N*N; i++ {
			// determine n'th digit of 3-digit base-N value i
			j := i
			for k := 0; k < (3 - 1 - n); k++ {
				j /= N
			}
			file := fmt.Sprintf("%s-%s-%s.go", name[i/N/N], name[(i/N)%N], name[i%N])
			allFiles = append(allFiles, file)
			write(t, gt.client+"/"+file, content[j%N])

			switch {
			case strings.Contains(file, "-bad-"):
				wantErr = append(wantErr, "\t"+file+"\n")
			case strings.Contains(file, "-broken-"):
				wantErr = append(wantErr, "\t"+file+":")
			default:
				wantErr = append(wantErr, "!"+file)
			}
		}
	}

	// committed files
	writeFiles(0)
	trun(t, gt.client, "git", "add", ".")
	trun(t, gt.client, "git", "commit", "-m", "msg")

	// staged files
	writeFiles(1)
	trun(t, gt.client, "git", "add", ".")

	// unstaged files
	writeFiles(2)

	wantErr = append(wantErr, "gofmt reported errors", "gofmt needs to format these files")

	testMainDied(t, "hook-invoke", "pre-commit")
	testPrintedStderr(t, wantErr...)
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
	if string(data) != "#!/bin/sh\nexec git-codereview hook-invoke commit-msg \"$@\"\n" {
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
	if string(data) != "#!/bin/sh\nexec git-codereview hook-invoke commit-msg \"$@\"\n" {
		t.Fatalf("invalid commit-msg hook:\n%s", string(data))
	}
}

func TestHookCommitMsgFromGit(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	trun(t, gt.pwd, "go", "build", "-o", gt.client+"/git-codereview")
	path := os.Getenv("PATH")
	defer os.Setenv("PATH", path)
	os.Setenv("PATH", gt.client+string(filepath.ListSeparator)+path)

	gt.removeStubHooks()
	testMain(t, "hooks") // install hooks
	testMain(t, "change", "mybranch")
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
