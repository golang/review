// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type gitTest struct {
	pwd    string // current directory before test
	tmpdir string // temporary directory holding repos
	server string // server repo root
	client string // client repo root
}

func (gt *gitTest) done() {
	os.RemoveAll(gt.tmpdir)
	os.Chdir(gt.pwd)
}

func (gt *gitTest) work(t *testing.T) {
	trun(t, gt.client, "git", "checkout", "-b", "work")
	trun(t, gt.client, "git", "branch", "--set-upstream-to", "origin/master")

	// make local change on client
	write(t, gt.client+"/file", "new content")
	trun(t, gt.client, "git", "add", "file")
	trun(t, gt.client, "git", "commit", "-m", "msg\n\nChange-Id: I123456789\n")
}

func newGitTest(t *testing.T) *gitTest {
	tmpdir, err := ioutil.TempDir("", "git-review-test")
	if err != nil {
		t.Fatal(err)
	}

	server := tmpdir + "/git-origin"

	mkdir(t, server)
	write(t, server+"/file", "this is master")
	trun(t, server, "git", "init", ".")
	trun(t, server, "git", "add", "file")
	trun(t, server, "git", "commit", "-m", "on master")

	for _, name := range []string{"dev.branch", "release.branch"} {
		trun(t, server, "git", "checkout", "master")
		trun(t, server, "git", "branch", name)
		write(t, server+"/file", "this is "+name)
		trun(t, server, "git", "commit", "-a", "-m", "on "+name)
	}

	client := tmpdir + "/git-client"
	mkdir(t, client)
	trun(t, client, "git", "clone", server, ".")
	trun(t, client, "git", "config", "core.editor", "false")
	pwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chdir(client); err != nil {
		t.Fatal(err)
	}

	gt := &gitTest{
		pwd:    pwd,
		tmpdir: tmpdir,
		server: server,
		client: client,
	}

	return gt
}

func mkdir(t *testing.T, dir string) {
	if err := os.Mkdir(dir, 0777); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, file, data string) {
	if err := ioutil.WriteFile(file, []byte(data), 0666); err != nil {
		t.Fatal(err)
	}
}

func trun(t *testing.T, dir string, cmdline ...string) string {
	cmd := exec.Command(cmdline[0], cmdline[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("in %s/, ran %s: %v\n%s", filepath.Base(dir), cmdline, err, out)
	}
	return string(out)
}

var (
	runLog []string
	stderr *bytes.Buffer
	stdout *bytes.Buffer
	died   bool
)

var mainCanDie bool

func testMainDied(t *testing.T, args ...string) {
	mainCanDie = true
	testMain(t, args...)
	if !died {
		t.Fatalf("expected to die, did not\nstdout:\n%sstderr:\n%s", stdout, stderr)
	}
}

func testMainCanDie(t *testing.T, args ...string) {
	mainCanDie = true
	testMain(t, args...)
}

func testMain(t *testing.T, args ...string) {
	t.Logf("git-review %s", strings.Join(args, " "))

	canDie := mainCanDie
	mainCanDie = false // reset for next invocation

	defer func() {
		runLog = runLogTrap
		stdout = stdoutTrap
		stderr = stderrTrap

		dieTrap = nil
		runLogTrap = nil
		stdoutTrap = nil
		stderrTrap = nil
		if err := recover(); err != nil {
			if died && canDie {
				return
			}
			var msg string
			if died {
				msg = "died"
			} else {
				msg = fmt.Sprintf("panic: %v", err)
			}
			t.Fatalf("%s\nstdout:\n%sstderr:\n%s", msg, stdout, stderr)
		}
	}()

	dieTrap = func() {
		died = true
		panic("died")
	}
	died = false
	runLogTrap = []string{} // non-nil, to trigger saving of commands
	stdoutTrap = new(bytes.Buffer)
	stderrTrap = new(bytes.Buffer)

	os.Args = append([]string{"git-review"}, args...)
	main()
}

func testRan(t *testing.T, cmds ...string) {
	if cmds == nil {
		cmds = []string{}
	}
	if !reflect.DeepEqual(runLog, cmds) {
		t.Errorf("ran:\n%s", strings.Join(runLog, "\n"))
		t.Errorf("wanted:\n%s", strings.Join(cmds, "\n"))
	}
}

func testPrinted(t *testing.T, buf *bytes.Buffer, name string, messages ...string) {
	all := buf.String()
	var errors bytes.Buffer
	for _, msg := range messages {
		if strings.HasPrefix(msg, "!") {
			if strings.Contains(all, msg[1:]) {
				fmt.Fprintf(&errors, "%s does (but should not) contain %q\n", name, msg[1:])
			}
			continue
		}
		if !strings.Contains(all, msg) {
			fmt.Fprintf(&errors, "%s does not contain %q\n", name, msg)
		}
	}
	if errors.Len() > 0 {
		t.Fatalf("wrong output\n%s%s:\n%s", &errors, name, all)
	}
}

func testPrintedStdout(t *testing.T, messages ...string) {
	testPrinted(t, stdout, "stdout", messages...)
}

func testPrintedStderr(t *testing.T, messages ...string) {
	testPrinted(t, stderr, "stderr", messages...)
}

func testNoStdout(t *testing.T) {
	if stdout.Len() != 0 {
		t.Fatalf("unexpected stdout:\n%s", stdout)
	}
}

func testNoStderr(t *testing.T) {
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr:\n%s", stderr)
	}
}

type gerritServer struct {
	l     net.Listener
	mu    sync.Mutex
	reply map[string]gerritReply
}

func newGerritServer(t *testing.T) *gerritServer {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("starting fake gerrit: %v", err)
	}

	auth.host = l.Addr().String()
	auth.url = "http://" + auth.host
	auth.project = "proj"
	auth.user = "gopher"
	auth.password = "PASSWORD"

	s := &gerritServer{l: l, reply: make(map[string]gerritReply)}
	go http.Serve(l, s)
	return s
}

func (s *gerritServer) done() {
	s.l.Close()
	auth.host = ""
	auth.url = ""
	auth.project = ""
	auth.user = ""
	auth.password = ""
}

type gerritReply struct {
	status int
	body   string
	f      func() gerritReply
}

func (s *gerritServer) setReply(path string, reply gerritReply) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reply[path] = reply
}

func (s *gerritServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reply, ok := s.reply[req.URL.Path]
	if !ok {
		http.NotFound(w, req)
		return
	}
	if reply.f != nil {
		reply = reply.f()
	}
	if reply.status != 0 {
		w.WriteHeader(reply.status)
	}
	if len(reply.body) > 0 {
		w.Write([]byte(reply.body))
	}
}
