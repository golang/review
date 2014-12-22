// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
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
		master (current branch)

	`)
}

func TestPendingNoneBranch(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	trun(t, gt.client, "git", "checkout", "-b", "work")

	testPending(t, `
		work (current branch)

	`)
}

func TestPendingBasic(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()
	gt.work(t)

	testPending(t, `
		work REVHASH (current branch)
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

	write(t, gt.server+"/file", "v2")
	trun(t, gt.server, "git", "commit", "-a", "-m", "v2")

	write(t, gt.server+"/file", "v3")
	trun(t, gt.server, "git", "commit", "-a", "-m", "v3")

	trun(t, gt.client, "git", "fetch")
	trun(t, gt.client, "git", "checkout", "-b", "work3ignored", "-t", "origin/master")

	write(t, gt.server+"/file", "v4")
	trun(t, gt.server, "git", "commit", "-a", "-m", "v4")

	trun(t, gt.client, "git", "fetch")
	trun(t, gt.client, "git", "checkout", "-b", "work2", "-t", "origin/master")
	write(t, gt.client+"/file", "modify")
	write(t, gt.client+"/file1", "new")
	trun(t, gt.client, "git", "add", "file", "file1")
	trun(t, gt.client, "git", "commit", "-m", "some changes")
	write(t, gt.client+"/file1", "modify")
	write(t, gt.client+"/afile1", "new")
	trun(t, gt.client, "git", "add", "file1", "afile1")
	write(t, gt.client+"/file1", "modify again")
	write(t, gt.client+"/file", "modify again")
	write(t, gt.client+"/bfile", "untracked")

	testPending(t, `
		work REVHASH (5 behind)
			msg
			
			Change-Id: I123456789
		
			Files in this change:
				file

		work2 REVHASH (current branch)
			some changes
		
			Files in this change:
				file
				file1
			Files staged:
				afile1
				file1
			Files unstaged:
				file
				file1
			Files untracked:
				bfile
		
	`)
}

func TestPendingErrors(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	gt.work(t)
	write(t, gt.client+"/file", "v2")
	trun(t, gt.client, "git", "commit", "-a", "-m", "v2")

	trun(t, gt.client, "git", "checkout", "master")
	write(t, gt.client+"/file", "v3")
	trun(t, gt.client, "git", "commit", "-a", "-m", "v3")

	testPending(t, `
		master REVHASH (current branch)
			ERROR: Branch contains 1 commit not on origin/master.
				Do not commit directly to master branch.
		
			v3
		
			Files in this change:
				file
		
		work REVHASH
			ERROR: Branch contains 2 commits not on origin/origin/master.
				There should be at most one.
				Use 'git change', not 'git commit'.
				Run 'git log origin/master..work' to list commits.
		
			msg
			
			Change-Id: I123456789
		
			Files in this change:
				file

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
		work REVHASH (current branch)
			msg
			
			Change-Id: I123456789
		
			Files in this change:
				file

	`)

	setJSON := func(json string) {
		srv.setReply("/a/changes/proj~master~I123456789", gerritReply{body: ")]}'\n" + json})
	}

	setJSON(`{
		"current_revision": "` + CurrentBranch().CommitHash() + `",
		"status": "MERGED",
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
			"Other-Label": {
				"all": [
					{
						"_account_id": 42,
						"name": "The Owner",
						"value": 2
					}
				]
			}
		}
	}`)

	testPending(t, `
		work REVHASH http://127.0.0.1:PORT/1234 (current branch, mailed, submitted)
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

}

func testPending(t *testing.T, want string) {
	// fake auth information to avoid Gerrit error
	if auth.host == "" {
		auth.host = "gerrit.fake"
		auth.user = "not-a-user"
		defer func() {
			auth.host = ""
			auth.user = ""
		}()
	}

	want = strings.Replace(want, "\n\t", "\n", -1)
	want = strings.Replace(want, "\n\t", "\n", -1)
	want = strings.TrimPrefix(want, "\n")

	testMain(t, "pending")
	out := testStdout.Bytes()

	out = regexp.MustCompile(`\b[0-9a-f]{7}\b`).ReplaceAllLiteral(out, []byte("REVHASH"))
	out = regexp.MustCompile(`\b127\.0\.0\.1:\d+\b`).ReplaceAllLiteral(out, []byte("127.0.0.1:PORT"))

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
