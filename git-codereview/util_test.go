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
	pwd         string // current directory before test
	tmpdir      string // temporary directory holding repos
	server      string // server repo root
	client      string // client repo root
	nwork       int    // number of calls to work method
	nworkServer int    // number of calls to serverWork method
}

// resetReadOnlyFlagAll resets windows read-only flag
// set on path and any children it contains.
// The flag is set by git and has to be removed.
// os.Remove refuses to remove files with read-only flag set.
func resetReadOnlyFlagAll(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}

	if !fi.IsDir() {
		return os.Chmod(path, 0666)
	}

	fd, err := os.Open(path)
	if err != nil {
		return err
	}
	defer fd.Close()

	names, _ := fd.Readdirnames(-1)
	for _, name := range names {
		resetReadOnlyFlagAll(path + string(filepath.Separator) + name)
	}
	return nil
}

func (gt *gitTest) done() {
	os.Chdir(gt.pwd) // change out of gt.tmpdir first, otherwise following os.RemoveAll fails on windows
	resetReadOnlyFlagAll(gt.tmpdir)
	os.RemoveAll(gt.tmpdir)
}

func (gt *gitTest) work(t *testing.T) {
	if gt.nwork == 0 {
		trun(t, gt.client, "git", "checkout", "-b", "work")
		trun(t, gt.client, "git", "branch", "--set-upstream-to", "origin/master")
		trun(t, gt.client, "git", "tag", "work") // make sure commands do the right thing when there is a tag of the same name
	}

	// make local change on client
	gt.nwork++
	write(t, gt.client+"/file", fmt.Sprintf("new content %d", gt.nwork))
	trun(t, gt.client, "git", "add", "file")
	suffix := ""
	if gt.nwork > 1 {
		suffix = fmt.Sprintf(" #%d", gt.nwork)
	}
	trun(t, gt.client, "git", "commit", "-m", fmt.Sprintf("msg%s\n\nChange-Id: I%d23456789\n", suffix, gt.nwork))
}

func (gt *gitTest) serverWork(t *testing.T) {
	// make change on server
	// duplicating the changes of gt.work to simulate them
	// having gone through Gerrit and submitted with
	// different times and commit hashes but the same content.
	gt.nworkServer++
	write(t, gt.server+"/file", fmt.Sprintf("new content %d", gt.nworkServer))
	trun(t, gt.server, "git", "add", "file")
	suffix := ""
	if gt.nworkServer > 1 {
		suffix = fmt.Sprintf(" #%d", gt.nworkServer)
	}
	trun(t, gt.server, "git", "commit", "-m", fmt.Sprintf("msg%s\n\nChange-Id: I%d23456789\n", suffix, gt.nworkServer))

}

func newGitTest(t *testing.T) (gt *gitTest) {
	tmpdir, err := ioutil.TempDir("", "git-codereview-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if gt == nil {
			os.RemoveAll(tmpdir)
		}
	}()

	server := tmpdir + "/git-origin"

	mkdir(t, server)
	write(t, server+"/file", "this is master")
	write(t, server+"/.gitattributes", "* -text\n")
	trun(t, server, "git", "init", ".")
	trun(t, server, "git", "config", "user.name", "gopher")
	trun(t, server, "git", "config", "user.email", "gopher@example.com")
	trun(t, server, "git", "add", "file", ".gitattributes")
	trun(t, server, "git", "commit", "-m", "on master")

	for _, name := range []string{"dev.branch", "release.branch"} {
		trun(t, server, "git", "checkout", "master")
		trun(t, server, "git", "checkout", "-b", name)
		write(t, server+"/file."+name, "this is "+name)
		trun(t, server, "git", "add", "file."+name)
		trun(t, server, "git", "commit", "-m", "on "+name)
	}
	trun(t, server, "git", "checkout", "master")

	client := tmpdir + "/git-client"
	mkdir(t, client)
	trun(t, client, "git", "clone", server, ".")
	trun(t, client, "git", "config", "user.name", "gopher")
	trun(t, client, "git", "config", "user.email", "gopher@example.com")

	// write stub hooks to keep installHook from installing its own.
	// If it installs its own, git will look for git-codereview on the current path
	// and may find an old git-codereview that does just about anything.
	// In any event, we wouldn't be testing what we want to test.
	// Tests that want to exercise hooks need to arrange for a git-codereview
	// in the path and replace these with the real ones.
	for _, h := range hookFiles {
		write(t, client+"/.git/hooks/"+h, "#!/bin/bash\nexit 0\n")
	}

	trun(t, client, "git", "config", "core.editor", "false")
	pwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chdir(client); err != nil {
		t.Fatal(err)
	}

	gt = &gitTest{
		pwd:    pwd,
		tmpdir: tmpdir,
		server: server,
		client: client,
	}
	return gt
}

func (gt *gitTest) removeStubHooks() {
	for _, h := range hookFiles {
		os.RemoveAll(gt.client + "/.git/hooks/" + h)
	}
}

func mkdir(t *testing.T, dir string) {
	if err := os.Mkdir(dir, 0777); err != nil {
		t.Fatal(err)
	}
}

func chdir(t *testing.T, dir string) {
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, file, data string) {
	if err := ioutil.WriteFile(file, []byte(data), 0666); err != nil {
		t.Fatal(err)
	}
}

func remove(t *testing.T, file string) {
	if err := os.RemoveAll(file); err != nil {
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

// fromSlash is like filepath.FromSlash, but it ignores ! at the start of the path
// and " (staged)" at the end.
func fromSlash(path string) string {
	if len(path) > 0 && path[0] == '!' {
		return "!" + fromSlash(path[1:])
	}
	if strings.HasSuffix(path, " (staged)") {
		return fromSlash(path[:len(path)-len(" (staged)")]) + " (staged)"
	}
	return filepath.FromSlash(path)
}

var (
	runLog     []string
	testStderr *bytes.Buffer
	testStdout *bytes.Buffer
	died       bool
)

var mainCanDie bool

func testMainDied(t *testing.T, args ...string) {
	mainCanDie = true
	testMain(t, args...)
	if !died {
		t.Fatalf("expected to die, did not\nstdout:\n%sstderr:\n%s", testStdout, testStderr)
	}
}

func testMainCanDie(t *testing.T, args ...string) {
	mainCanDie = true
	testMain(t, args...)
}

func testMain(t *testing.T, args ...string) {
	*noRun = false
	*verbose = 0

	t.Logf("git-codereview %s", strings.Join(args, " "))

	canDie := mainCanDie
	mainCanDie = false // reset for next invocation

	defer func() {
		runLog = runLogTrap
		testStdout = stdoutTrap
		testStderr = stderrTrap

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
			t.Fatalf("%s\nstdout:\n%sstderr:\n%s", msg, testStdout, testStderr)
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

	os.Args = append([]string{"git-codereview"}, args...)
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
	testPrinted(t, testStdout, "stdout", messages...)
}

func testPrintedStderr(t *testing.T, messages ...string) {
	testPrinted(t, testStderr, "stderr", messages...)
}

func testNoStdout(t *testing.T) {
	if testStdout.Len() != 0 {
		t.Fatalf("unexpected stdout:\n%s", testStdout)
	}
}

func testNoStderr(t *testing.T) {
	if testStderr.Len() != 0 {
		t.Fatalf("unexpected stderr:\n%s", testStderr)
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

func (s *gerritServer) setJSON(id, json string) {
	s.setReply("/a/changes/proj~master~"+id, gerritReply{body: ")]}'\n" + json})
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
