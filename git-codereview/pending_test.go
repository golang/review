// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func TestPendingNone(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	testPending(t, `
		main (current branch)

	`)
}

func TestPendingNoneBranch(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	trun(t, gt.client, "git", "checkout", "--no-track", "-b", "work")

	testPending(t, `
		work (current branch)

	`)
}

func TestPendingBasic(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()
	gt.work(t)

	testPending(t, `
		work REVHASH..REVHASH (current branch)
		+ REVHASH
			msg

			Change-Id: I123456789

			Files in this change:
				file

	`)
}

func TestPendingComplex(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()
	gt.work(t)

	write(t, gt.server+"/file", "v2", 0644)
	trun(t, gt.server, "git", "commit", "-a", "-m", "v2")

	write(t, gt.server+"/file", "v3", 0644)
	trun(t, gt.server, "git", "commit", "-a", "-m", "v3")

	trun(t, gt.client, "git", "fetch")
	trun(t, gt.client, "git", "checkout", "-b", "work3ignored", "-t", "origin/main")

	write(t, gt.server+"/file", "v4", 0644)
	trun(t, gt.server, "git", "commit", "-a", "-m", "v4")

	trun(t, gt.client, "git", "fetch")
	trun(t, gt.client, "git", "checkout", "-b", "work2", "-t", "origin/main")
	write(t, gt.client+"/file", "modify", 0644)
	write(t, gt.client+"/file1", "new", 0644)
	trun(t, gt.client, "git", "add", "file", "file1")
	trun(t, gt.client, "git", "commit", "-m", "some changes")
	write(t, gt.client+"/file1", "modify", 0644)
	write(t, gt.client+"/afile1", "new", 0644)
	trun(t, gt.client, "git", "add", "file1", "afile1")
	write(t, gt.client+"/file1", "modify again", 0644)
	write(t, gt.client+"/file", "modify again", 0644)
	write(t, gt.client+"/bfile", "untracked", 0644)

	testPending(t, `
		work2 REVHASH..REVHASH (current branch)
		+ uncommitted changes
			Files untracked:
				bfile
			Files unstaged:
				file
				file1
			Files staged:
				afile1
				file1

		+ REVHASH
			some changes

			Files in this change:
				file
				file1

		work REVHASH..REVHASH (3 behind)
		+ REVHASH
			msg

			Change-Id: I123456789

			Files in this change:
				file

	`)

	testPendingArgs(t, []string{"-c"}, `
		work2 REVHASH..REVHASH (current branch)
		+ uncommitted changes
			Files untracked:
				bfile
			Files unstaged:
				file
				file1
			Files staged:
				afile1
				file1

		+ REVHASH
			some changes

			Files in this change:
				file
				file1

	`)

	testPendingArgs(t, []string{"-c", "-s"}, `
		work2 REVHASH..REVHASH (current branch)
		+ uncommitted changes
			Files untracked:
				bfile
			Files unstaged:
				file
				file1
			Files staged:
				afile1
				file1
		+ REVHASH some changes

	`)
}

func TestPendingMultiChange(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	gt.work(t)
	write(t, gt.client+"/file", "v2", 0644)
	trun(t, gt.client, "git", "commit", "-a", "-m", "v2")

	write(t, gt.client+"/file", "v4", 0644)
	trun(t, gt.client, "git", "add", "file")

	write(t, gt.client+"/file", "v5", 0644)
	write(t, gt.client+"/file2", "v6", 0644)

	testPending(t, `
		work REVHASH..REVHASH (current branch)
		+ uncommitted changes
			Files untracked:
				file2
			Files unstaged:
				file
			Files staged:
				file

		+ REVHASH
			v2

			Files in this change:
				file

		+ REVHASH
			msg

			Change-Id: I123456789

			Files in this change:
				file

	`)

	testPendingArgs(t, []string{"-s"}, `
		work REVHASH..REVHASH (current branch)
		+ uncommitted changes
			Files untracked:
				file2
			Files unstaged:
				file
			Files staged:
				file
		+ REVHASH v2
		+ REVHASH msg

	`)
}

func TestPendingGerrit(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()
	gt.work(t)

	srv := newGerritServer(t)
	defer srv.done()

	// Test error from Gerrit server.
	testPending(t, `
		work REVHASH..REVHASH (current branch)
		+ REVHASH
			msg

			Change-Id: I123456789

			Files in this change:
				file

	`)

	testPendingReply(srv, "I123456789", CurrentBranch().Pending()[0].Hash, "MERGED", 0)

	// Test local mode does not talk to any server.
	// Make client 1 behind server.
	// The '1 behind' should not show up, nor any Gerrit information.
	write(t, gt.server+"/file", "v4", 0644)
	trun(t, gt.server, "git", "add", "file")
	trun(t, gt.server, "git", "commit", "-m", "msg")
	testPendingArgs(t, []string{"-l"}, `
		work REVHASH..REVHASH (current branch)
		+ REVHASH
			msg

			Change-Id: I123456789

			Files in this change:
				file

	`)

	testPendingArgs(t, []string{"-l", "-s"}, `
		work REVHASH..REVHASH (current branch)
		+ REVHASH msg

	`)

	// Without -l, the 1 behind should appear, as should Gerrit information.
	testPending(t, `
		work REVHASH..REVHASH (current branch, all mailed, all submitted, 1 behind)
		+ REVHASH http://127.0.0.1:PORT/1234 (mailed, submitted)
			msg

			Change-Id: I123456789

			Code-Review:
				+1 Grace Emlin
				-2 George Opher
			Other-Label:
				+2 The Owner
			Files in this change:
				file

	`)

	testPendingArgs(t, []string{"-s"}, `
		work REVHASH..REVHASH (current branch, all mailed, all submitted, 1 behind)
		+ REVHASH msg (CL 1234 -2 +1, mailed, submitted)

	`)

	// Since pending did a fetch, 1 behind should show up even with -l.
	testPendingArgs(t, []string{"-l"}, `
		work REVHASH..REVHASH (current branch, 1 behind)
		+ REVHASH
			msg

			Change-Id: I123456789

			Files in this change:
				file

	`)

	testPendingArgs(t, []string{"-l", "-s"}, `
		work REVHASH..REVHASH (current branch, 1 behind)
		+ REVHASH msg

	`)
}

func TestPendingGerritMultiChange(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	gt.work(t)
	hash1 := CurrentBranch().Pending()[0].Hash

	write(t, gt.client+"/file", "v2", 0644)
	trun(t, gt.client, "git", "commit", "-a", "-m", "v2\n\nChange-Id: I2345")
	hash2 := CurrentBranch().Pending()[0].Hash

	write(t, gt.client+"/file", "v4", 0644)
	trun(t, gt.client, "git", "add", "file")

	write(t, gt.client+"/file", "v5", 0644)
	write(t, gt.client+"/file2", "v6", 0644)

	srv := newGerritServer(t)
	defer srv.done()

	testPendingReply(srv, "I123456789", hash1, "MERGED", 0)
	testPendingReply(srv, "I2345", hash2, "NEW", 99)

	testPending(t, `
		work REVHASH..REVHASH (current branch, all mailed)
		+ uncommitted changes
			Files untracked:
				file2
			Files unstaged:
				file
			Files staged:
				file

		+ REVHASH http://127.0.0.1:PORT/1234 (mailed, 99 unresolved comments)
			v2

			Change-Id: I2345

			Code-Review:
				+1 Grace Emlin
				-2 George Opher
			Other-Label:
				+2 The Owner
			Files in this change:
				file

		+ REVHASH http://127.0.0.1:PORT/1234 (mailed, submitted)
			msg

			Change-Id: I123456789

			Code-Review:
				+1 Grace Emlin
				-2 George Opher
			Other-Label:
				+2 The Owner
			Files in this change:
				file

	`)

	testPendingArgs(t, []string{"-s"}, `
		work REVHASH..REVHASH (current branch, all mailed)
		+ uncommitted changes
			Files untracked:
				file2
			Files unstaged:
				file
			Files staged:
				file
		+ REVHASH v2 (CL 1234 -2 +1, mailed, 99 unresolved comments)
		+ REVHASH msg (CL 1234 -2 +1, mailed, submitted)

	`)
}

func TestPendingGerritMultiChange15(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()
	srv := newGerritServer(t)
	defer srv.done()

	gt.work(t)
	hash1 := CurrentBranch().Pending()[0].Hash
	testPendingReply(srv, "I123456789", hash1, "MERGED", 0)

	for i := 1; i < 15; i++ {
		write(t, gt.client+"/file", fmt.Sprintf("v%d", i), 0644)
		trun(t, gt.client, "git", "commit", "-a", "-m", fmt.Sprintf("v%d\n\nChange-Id: I%010d", i, i))
		hash2 := CurrentBranch().Pending()[0].Hash
		testPendingReply(srv, fmt.Sprintf("I%010d", i), hash2, "NEW", 0)
	}

	testPendingArgs(t, []string{"-s"}, `
		work REVHASH..REVHASH (current branch, all mailed)
		+ REVHASH v14 (CL 1234 -2 +1, mailed)
		+ REVHASH v13 (CL 1234 -2 +1, mailed)
		+ REVHASH v12 (CL 1234 -2 +1, mailed)
		+ REVHASH v11 (CL 1234 -2 +1, mailed)
		+ REVHASH v10 (CL 1234 -2 +1, mailed)
		+ REVHASH v9 (CL 1234 -2 +1, mailed)
		+ REVHASH v8 (CL 1234 -2 +1, mailed)
		+ REVHASH v7 (CL 1234 -2 +1, mailed)
		+ REVHASH v6 (CL 1234 -2 +1, mailed)
		+ REVHASH v5 (CL 1234 -2 +1, mailed)
		+ REVHASH v4 (CL 1234 -2 +1, mailed)
		+ REVHASH v3 (CL 1234 -2 +1, mailed)
		+ REVHASH v2 (CL 1234 -2 +1, mailed)
		+ REVHASH v1 (CL 1234 -2 +1, mailed)
		+ REVHASH msg (CL 1234 -2 +1, mailed, submitted)

	`)
}

func testPendingReply(srv *gerritServer, id, rev, status string, unresolved int) {
	srv.setJSON(id, `{
		"id": "proj~main~`+id+`",
		"project": "proj",
		"current_revision": "`+rev+`",
		"status": "`+status+`",
		"unresolved_comment_count":`+fmt.Sprint(unresolved)+`,
		"_number": 1234,
		"owner": {"_id": 42},
		"labels": {
			"Code-Review": {
				"all": [
					{
						"_id": 42,
						"value": 0
					},
					{
						"_id": 43,
						"name": "George Opher",
						"value": -2
					},
					{
						"_id": 44,
						"name": "Grace Emlin",
						"value": 1
					}
				]
			},
			"Trybot-Spam": {
				"all": [
					{
						"_account_id": 42,
						"name": "The Owner",
						"value": 0
					}
				]
			},
			"Other-Label": {
				"all": [
					{
						"_id": 43,
						"name": "George Opher",
						"value": 0
					},
					{
						"_account_id": 42,
						"name": "The Owner",
						"value": 2
					}
				]
			}
		}
	}`)
}

func testPending(t *testing.T, want string) {
	t.Helper()
	testPendingArgs(t, nil, want)
}

func testPendingArgs(t *testing.T, args []string, want string) {
	t.Helper()
	// fake auth information to avoid Gerrit error
	if !auth.initialized {
		auth.initialized = true
		auth.host = "gerrit.fake"
		auth.user = "not-a-user"
		defer func() {
			auth.initialized = false
			auth.host = ""
			auth.user = ""
		}()
	}

	want = strings.Replace(want, "\n\t", "\n", -1)
	want = strings.Replace(want, "\n\t", "\n", -1)
	want = strings.TrimPrefix(want, "\n")

	testMain(t, append([]string{"pending"}, args...)...)
	out := testStdout.Bytes()

	out = regexp.MustCompile(`\b[0-9a-f]{7}\b`).ReplaceAllLiteral(out, []byte("REVHASH"))
	out = regexp.MustCompile(`\b127\.0\.0\.1:\d+\b`).ReplaceAllLiteral(out, []byte("127.0.0.1:PORT"))
	out = regexp.MustCompile(`(?m)[ \t]+$`).ReplaceAllLiteral(out, nil) // ignore trailing space differences

	if string(out) != want {
		t.Errorf("invalid pending output:\n%s\nwant:\n%s", out, want)
		if d, err := diff([]byte(want), out); err == nil {
			t.Errorf("diff want actual:\n%s", d)
		}
	}
}

func diff(b1, b2 []byte) (data []byte, err error) {
	f1, err := ioutil.TempFile("", "gofmt")
	if err != nil {
		return
	}
	defer os.Remove(f1.Name())
	defer f1.Close()

	f2, err := ioutil.TempFile("", "gofmt")
	if err != nil {
		return
	}
	defer os.Remove(f2.Name())
	defer f2.Close()

	f1.Write(b1)
	f2.Write(b2)

	data, err = exec.Command("diff", "-u", f1.Name(), f2.Name()).CombinedOutput()
	if len(data) > 0 {
		// diff exits with a non-zero status when the files don't match.
		// Ignore that failure as long as we get output.
		err = nil
	}
	return

}
