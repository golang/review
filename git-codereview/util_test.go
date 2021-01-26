// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
)

var gitversion = "unknown git version" // git version for error logs

type gitTest struct {
	pwd         string // current directory before test
	tmpdir      string // temporary directory holding repos
	server      string // server repo root
	client      string // client repo root
	nwork       int    // number of calls to work method
	nworkServer int    // number of calls to serverWork method
	nworkOther  int    // number of calls to serverWorkUnrelated method
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
	cachedConfig = nil
}

// doWork simulates commit 'n' touching 'file' in 'dir'
func doWork(t *testing.T, n int, dir, file, changeid string, msg string) {
	t.Helper()
	write(t, dir+"/"+file, fmt.Sprintf("new content %d", n), 0644)
	trun(t, dir, "git", "add", file)
	suffix := ""
	if n > 1 {
		suffix = fmt.Sprintf(" #%d", n)
	}
	if msg != "" {
		msg += "\n\n"
	}
	cmsg := fmt.Sprintf("%smsg%s\n\nChange-Id: I%d%s\n", msg, suffix, n, changeid)
	trun(t, dir, "git", "commit", "-m", cmsg)
}

func (gt *gitTest) work(t *testing.T) {
	t.Helper()
	if gt.nwork == 0 {
		trun(t, gt.client, "git", "checkout", "-b", "work")
		trun(t, gt.client, "git", "branch", "--set-upstream-to", "origin/main")
		trun(t, gt.client, "git", "tag", "work") // make sure commands do the right thing when there is a tag of the same name
	}

	// make local change on client
	gt.nwork++
	doWork(t, gt.nwork, gt.client, "file", "23456789", "")
}

func (gt *gitTest) workFile(t *testing.T, file string) {
	t.Helper()
	// make local change on client in the specific file
	gt.nwork++
	doWork(t, gt.nwork, gt.client, file, "23456789", "")
}

func (gt *gitTest) serverWork(t *testing.T) {
	t.Helper()
	// make change on server
	// duplicating the sequence of changes in gt.work to simulate them
	// having gone through Gerrit and submitted with possibly
	// different commit hashes but the same content.
	gt.nworkServer++
	doWork(t, gt.nworkServer, gt.server, "file", "23456789", "")
}

func (gt *gitTest) serverWorkUnrelated(t *testing.T, msg string) {
	t.Helper()
	// make unrelated change on server
	// this makes history different on client and server
	gt.nworkOther++
	doWork(t, gt.nworkOther, gt.server, "otherfile", "9999", msg)
}

func newGitTest(t *testing.T) (gt *gitTest) {
	t.Helper()
	// The Linux builders seem not to have git in their paths.
	// That makes this whole repo a bit useless on such systems,
	// but make sure the tests don't fail.
	_, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("cannot find git in path: %v", err)
	}

	tmpdir, err := ioutil.TempDir("", "git-codereview-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if gt == nil {
			os.RemoveAll(tmpdir)
		}
	}()

	gitversion = trun(t, tmpdir, "git", "--version")

	server := tmpdir + "/git-origin"

	mkdir(t, server)
	write(t, server+"/file", "this is main", 0644)
	write(t, server+"/.gitattributes", "* -text\n", 0644)
	trun(t, server, "git", "init", ".")
	trun(t, server, "git", "config", "user.name", "gopher")
	trun(t, server, "git", "config", "user.email", "gopher@example.com")
	trun(t, server, "git", "add", "file", ".gitattributes")
	trun(t, server, "git", "commit", "-m", "initial commit")

	// Newer gits use a default branch name of main.
	// Older ones used master.
	// So the branch name now may be main or master.
	// We would like to assume main for the tests.
	// Newer gits would let us do
	//	git init --initial-branch=main .
	// above, but older gits don't have initial-branch.
	// And we don't trust older gits to handle a no-op branch rename.
	// So rename it to something different, and then to main.
	// Then we'll be in a known state.
	trun(t, server, "git", "branch", "-M", "certainly-not-main")
	trun(t, server, "git", "branch", "-M", "main")

	for _, name := range []string{"dev.branch", "release.branch"} {
		trun(t, server, "git", "checkout", "main")
		trun(t, server, "git", "checkout", "-b", name)
		write(t, server+"/file."+name, "this is "+name, 0644)
		cfg := "branch: " + name + "\n"
		if name == "dev.branch" {
			cfg += "parent-branch: main\n"
		}
		write(t, server+"/codereview.cfg", cfg, 0644)
		trun(t, server, "git", "add", "file."+name, "codereview.cfg")
		trun(t, server, "git", "commit", "-m", "on "+name)
	}
	trun(t, server, "git", "checkout", "main")

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
	if _, err := os.Stat(client + "/.git/hooks"); os.IsNotExist(err) {
		mkdir(t, client+"/.git/hooks")
	}
	for _, h := range hookFiles {
		write(t, client+"/.git/hooks/"+h, "#!/bin/sh\nexit 0\n", 0755)
	}

	trun(t, client, "git", "config", "core.editor", "false")
	pwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chdir(client); err != nil {
		t.Fatal(err)
	}

	return &gitTest{
		pwd:    pwd,
		tmpdir: tmpdir,
		server: server,
		client: client,
	}
}

func (gt *gitTest) enableGerrit(t *testing.T) {
	t.Helper()
	write(t, gt.server+"/codereview.cfg", "gerrit: myserver\n", 0644)
	trun(t, gt.server, "git", "add", "codereview.cfg")
	trun(t, gt.server, "git", "commit", "-m", "add gerrit")
	trun(t, gt.client, "git", "pull", "-r")
}

func (gt *gitTest) removeStubHooks() {
	os.RemoveAll(gt.client + "/.git/hooks/")
}

func mkdir(t *testing.T, dir string) {
	if err := os.Mkdir(dir, 0777); err != nil {
		t.Helper()
		t.Fatal(err)
	}
}

func chdir(t *testing.T, dir string) {
	if err := os.Chdir(dir); err != nil {
		t.Helper()
		t.Fatal(err)
	}
}

func write(t *testing.T, file, data string, perm os.FileMode) {
	if err := ioutil.WriteFile(file, []byte(data), perm); err != nil {
		t.Helper()
		t.Fatal(err)
	}
}

func read(t *testing.T, file string) []byte {
	b, err := ioutil.ReadFile(file)
	if err != nil {
		t.Helper()
		t.Fatal(err)
	}
	return b
}

func remove(t *testing.T, file string) {
	if err := os.RemoveAll(file); err != nil {
		t.Helper()
		t.Fatal(err)
	}
}

func trun(t *testing.T, dir string, cmdline ...string) string {
	cmd := exec.Command(cmdline[0], cmdline[1:]...)
	cmd.Dir = dir
	setEnglishLocale(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Helper()
		if cmdline[0] == "git" {
			t.Fatalf("in %s/, ran %s with %s:\n%v\n%s", filepath.Base(dir), cmdline, gitversion, err, out)
		}
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
	mainCanDie bool
)

func testMainDied(t *testing.T, args ...string) {
	t.Helper()
	mainCanDie = true
	testMain(t, args...)
	if !died {
		t.Fatalf("expected to die, did not\nstdout:\n%sstderr:\n%s", testStdout, testStderr)
	}
}

func testMain(t *testing.T, args ...string) {
	t.Helper()
	*noRun = false
	*verbose = 0
	cachedConfig = nil

	t.Logf("git-codereview %s", strings.Join(args, " "))

	canDie := mainCanDie
	mainCanDie = false // reset for next invocation

	defer func() {
		runLog = runLogTrap
		testStdout = stdoutTrap
		testStderr = stderrTrap

		exitTrap = nil
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
				msg = fmt.Sprintf("panic: %v\n%s", err, debug.Stack())
			}
			t.Fatalf("%s\nstdout:\n%sstderr:\n%s", msg, testStdout, testStderr)
		}
	}()

	exitTrap = func() {
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
		t.Helper()
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
		t.Helper()
		t.Fatalf("wrong output\n%s%s:\n%s", &errors, name, all)
	}
}

func testHideRevHashes(t *testing.T) {
	for _, b := range []*bytes.Buffer{testStdout, testStderr} {
		out := b.Bytes()
		out = regexp.MustCompile(`\b[0-9a-f]{7}\b`).ReplaceAllLiteral(out, []byte("REVHASH"))
		out = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`).ReplaceAllLiteral(out, []byte("DATE"))
		b.Reset()
		b.Write(out)
	}
}

func testPrintedStdout(t *testing.T, messages ...string) {
	t.Helper()
	testPrinted(t, testStdout, "stdout", messages...)
}

func testPrintedStderr(t *testing.T, messages ...string) {
	t.Helper()
	testPrinted(t, testStderr, "stderr", messages...)
}

func testNoStdout(t *testing.T) {
	if testStdout.Len() != 0 {
		t.Helper()
		t.Fatalf("unexpected stdout:\n%s", testStdout)
	}
}

func testNoStderr(t *testing.T) {
	if testStderr.Len() != 0 {
		t.Helper()
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
		t.Helper()
		t.Fatalf("starting fake gerrit: %v", err)
	}

	auth.initialized = true
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
	auth.initialized = false
	auth.host = ""
	auth.url = ""
	auth.project = ""
	auth.user = ""
	auth.password = ""
}

type gerritReply struct {
	status int
	body   string
	json   interface{}
	f      func() gerritReply
}

func (s *gerritServer) setReply(path string, reply gerritReply) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reply[path] = reply
}

func (s *gerritServer) setJSON(id, json string) {
	s.setReply("/a/changes/proj~main~"+id, gerritReply{body: ")]}'\n" + json})
}

func (s *gerritServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/a/changes/" {
		s.serveChangesQuery(w, req)
		return
	}
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
	if reply.json != nil {
		body, err := json.Marshal(reply.json)
		if err != nil {
			dief("%v", err)
		}
		reply.body = ")]}'\n" + string(body)
	}
	if len(reply.body) > 0 {
		w.Write([]byte(reply.body))
	}
}

func (s *gerritServer) serveChangesQuery(w http.ResponseWriter, req *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	qs := req.URL.Query()["q"]
	if len(qs) > 10 {
		http.Error(w, "too many queries", 500)
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, ")]}'\n")
	end := ""
	if len(qs) > 1 {
		fmt.Fprintf(&buf, "[")
		end = "]"
	}
	sep := ""
	for _, q := range qs {
		fmt.Fprintf(&buf, "%s[", sep)
		if strings.HasPrefix(q, "change:") {
			reply, ok := s.reply[req.URL.Path+strings.TrimPrefix(q, "change:")]
			if ok {
				if reply.json != nil {
					body, err := json.Marshal(reply.json)
					if err != nil {
						dief("%v", err)
					}
					reply.body = ")]}'\n" + string(body)
				}
				body := reply.body
				i := strings.Index(body, "\n")
				if i > 0 {
					body = body[i+1:]
				}
				fmt.Fprintf(&buf, "%s", body)
			}
		}
		fmt.Fprintf(&buf, "]")
		sep = ","
	}
	fmt.Fprintf(&buf, "%s", end)
	w.Write(buf.Bytes())
}

func TestUsage(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()

	testMainDied(t)
	testPrintedStderr(t, "Usage: git-codereview <command>")

	testMainDied(t, "not-a-command")
	testPrintedStderr(t, "Usage: git-codereview <command>")

	// During tests we configure the flag package to panic on error
	// instead of
	testMainDied(t, "mail", "-not-a-flag")
	testPrintedStderr(t, "flag provided but not defined")
}
