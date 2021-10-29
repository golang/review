// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Branch describes a Git branch.
type Branch struct {
	Name    string // branch name
	Current bool   // branch is currently checked out

	loadedPending bool      // following fields are valid
	originBranch  string    // upstream origin branch
	commitsAhead  int       // number of commits ahead of origin branch
	branchpoint   string    // latest commit hash shared with origin branch
	pending       []*Commit // pending commits, newest first (children before parents)

	config map[string]string // cached config; use Config instead
}

// A Commit describes a single pending commit on a Git branch.
type Commit struct {
	Hash      string   // commit hash
	ShortHash string   // abbreviated commit hash
	Parent    string   // parent hash (== Parents[0])
	Parents   []string // all parent hashes (merges have > 1)
	Tree      string   // tree hash
	Message   string   // commit message
	Subject   string   // first line of commit message
	ChangeID  string   // Change-Id in commit message ("" if missing)

	AuthorName  string // author name (from %an)
	AuthorEmail string // author email (from %ae)
	AuthorDate  string // author date as Unix timestamp string (from %at)

	// For use by pending command.
	g         *GerritChange // associated Gerrit change data
	gerr      error         // error loading Gerrit data
	committed []string      // list of files in this commit
}

// HasParent reports whether hash appears in c.Parents.
func (c *Commit) HasParent(hash string) bool {
	for _, p := range c.Parents {
		if p == hash {
			return true
		}
	}
	return false
}

// Config returns the configuration for the branch.
func (b *Branch) Config() map[string]string {
	if b.config != nil {
		return b.config
	}
	var cfgText string
	if b.Current {
		data, _ := ioutil.ReadFile(filepath.Join(repoRoot(), "codereview.cfg"))
		cfgText = string(data)
	} else {
		out, err := cmdOutputDirErr(repoRoot(), "git", "show", b.Name+":codereview.cfg")
		if err == nil {
			cfgText = out
		}
	}
	cfg, err := parseConfig(cfgText)
	if err != nil {
		verbosef("failed to load config for branch %v: %v", b.Name, err)
		cfg = make(map[string]string)
	}
	b.config = cfg
	return b.config
}

// CurrentBranch returns the current branch.
func CurrentBranch() *Branch {
	name := strings.TrimPrefix(trim(cmdOutput("git", "rev-parse", "--abbrev-ref", "HEAD")), "heads/")
	return &Branch{Name: name, Current: true}
}

// DetachedHead reports whether branch b corresponds to a detached HEAD
// (does not have a real branch name).
func (b *Branch) DetachedHead() bool {
	return b.Name == "HEAD"
}

// NeedOriginBranch exits with an error message
// if the origin branch is unknown.
// The cmd is the user command reported in the failure message.
func (b *Branch) NeedOriginBranch(cmd string) {
	if b.OriginBranch() == "" {
		why := ""
		if b.DetachedHead() {
			why = " (in detached HEAD mode)"
		}
		if cmd == "<internal branchpoint>" && exitTrap != nil {
			panic("NeedOriginBranch")
		}
		dief("cannot %s: no origin branch%s", cmd, why)
	}
}

// OriginBranch returns the name of the origin branch that branch b tracks.
// The returned name is like "origin/master" or "origin/dev.garbage" or
// "origin/release-branch.go1.4".
func (b *Branch) OriginBranch() string {
	if b.originBranch != "" {
		return b.originBranch
	}

	cfg := b.Config()["branch"]
	upstream := ""
	if cfg != "" {
		upstream = "origin/" + cfg
	}

	// Consult and possibly update git,
	// but only if we are actually on a real branch,
	// not in detached HEAD mode.
	if !b.DetachedHead() {
		gitUpstream := b.gitOriginBranch()
		if upstream == "" {
			upstream = gitUpstream
		}
		if upstream == "" {
			// Assume branch was created before we set upstream correctly.
			// See if origin/main exists; if so, use it.
			// Otherwise, fall back to origin/master.
			argv := []string{"git", "rev-parse", "--abbrev-ref", "origin/main"}
			cmd := exec.Command(argv[0], argv[1:]...)
			setEnglishLocale(cmd)
			if err := cmd.Run(); err == nil {
				upstream = "origin/main"
			} else {
				upstream = "origin/master"
			}
		}
		if gitUpstream != upstream && b.Current {
			// Best effort attempt to correct setting for next time,
			// and for "git status".
			exec.Command("git", "branch", "-u", upstream).Run()
		}
	}

	b.originBranch = upstream
	return b.originBranch
}

func (b *Branch) gitOriginBranch() string {
	argv := []string{"git", "rev-parse", "--abbrev-ref", b.Name + "@{u}"}
	cmd := exec.Command(argv[0], argv[1:]...)
	if runtime.GOOS == "windows" {
		// Workaround on windows. git for windows can't handle @{u} as same as
		// given. Disable glob for this command if running on Cygwin or MSYS2.
		envs := os.Environ()
		envs = append(envs, "CYGWIN=noglob "+os.Getenv("CYGWIN"))
		envs = append(envs, "MSYS=noglob "+os.Getenv("MSYS"))
		cmd.Env = envs
	}
	setEnglishLocale(cmd)

	out, err := cmd.CombinedOutput()
	if err == nil && len(out) > 0 {
		return string(bytes.TrimSpace(out))
	}

	// Have seen both "No upstream configured" and "no upstream configured".
	if strings.Contains(string(out), "upstream configured") {
		return ""
	}

	fmt.Fprintf(stderr(), "%v\n%s\n", commandString(argv[0], argv[1:]), out)
	dief("%v", err)
	panic("not reached")
}

func (b *Branch) FullName() string {
	if b.Name != "HEAD" {
		return "refs/heads/" + b.Name
	}
	return b.Name
}

// HasPendingCommit reports whether b has any pending commits.
func (b *Branch) HasPendingCommit() bool {
	b.loadPending()
	return b.commitsAhead > 0
}

// Pending returns b's pending commits, newest first (children before parents).
func (b *Branch) Pending() []*Commit {
	b.loadPending()
	return b.pending
}

// Branchpoint returns an identifier for the latest revision
// common to both this branch and its upstream branch.
func (b *Branch) Branchpoint() string {
	b.NeedOriginBranch("<internal branchpoint>")
	b.loadPending()
	return b.branchpoint
}

func gitHash(expr string) string {
	out, err := cmdOutputErr("git", "rev-parse", expr)
	if err != nil {
		dief("cannot resolve %s: %v\n%s", expr, err, out)
	}
	if strings.HasPrefix(expr, "-") {
		// Git has a bug where it will just echo these back with no error.
		// (Try "git rev-parse -qwerty".)
		// Reject them ourselves instead.
		dief("cannot resolve %s: invalid reference", expr)
	}
	return trim(out)
}

func (b *Branch) loadPending() {
	if b.loadedPending {
		return
	}
	b.loadedPending = true

	// In case of early return.
	// But avoid the git exec unless really needed.
	b.branchpoint = ""
	defer func() {
		if b.branchpoint == "" {
			b.branchpoint = gitHash("HEAD")
		}
	}()

	if b.DetachedHead() {
		return
	}

	// Note: This runs in parallel with "git fetch -q",
	// so the commands may see a stale version of origin/master.
	// The use of origin here is for identifying what the branch has
	// in common with origin (what's old on the branch).
	// Any new commits in origin do not affect that.

	// Note: --topo-order means child first, then parent.
	origin := b.OriginBranch()
	const numField = 9
	all := trim(cmdOutput("git", "log", "--topo-order",
		"--format=format:%H%x00%h%x00%P%x00%T%x00%B%x00%s%x00%an%x00%ae%x00%at%x00",
		origin+".."+b.FullName(), "--"))
	fields := strings.Split(all, "\x00")
	if len(fields) < numField {
		return // nothing pending
	}
	for i, field := range fields {
		fields[i] = strings.TrimLeft(field, "\r\n")
	}
Log:
	for i := 0; i+numField <= len(fields); i += numField {
		parents := strings.Fields(fields[i+2])
		c := &Commit{
			Hash:        fields[i],
			ShortHash:   fields[i+1],
			Parents:     parents,
			Tree:        fields[i+3],
			Message:     fields[i+4],
			Subject:     fields[i+5],
			AuthorName:  fields[i+6],
			AuthorEmail: fields[i+7],
			AuthorDate:  fields[i+8],
		}
		if len(c.Parents) > 0 {
			c.Parent = c.Parents[0]
		}
		if len(c.Parents) > 1 {
			// Found merge point.
			// Merges break the invariant that the last shared commit (the branchpoint)
			// is the parent of the final commit in the log output.
			// If c.Parent is on the origin branch, then since we are reading the log
			// in (reverse) topological order, we know that c.Parent is the actual branchpoint,
			// even if we later see additional commits on a different branch leading down to
			// a lower location on the same origin branch.
			// Check c.Merge (the second parent) too, so we don't depend on the parent order.
			for _, parent := range c.Parents {
				if strings.Contains(cmdOutput("git", "branch", "-a", "--contains", parent), " remotes/"+origin+"\n") {
					b.pending = append(b.pending, c)
					b.branchpoint = parent
					break Log
				}
			}
		}
		for _, line := range lines(c.Message) {
			// Note: Keep going even if we find one, so that
			// we take the last Change-Id line, just in case
			// there is a commit message quoting another
			// commit message.
			// I'm not sure this can come up at all, but just in case.
			if strings.HasPrefix(line, "Change-Id: ") {
				c.ChangeID = line[len("Change-Id: "):]
			}
		}
		b.pending = append(b.pending, c)
		b.branchpoint = c.Parent
	}
	b.commitsAhead = len(b.pending)
}

// CommitsBehind reports the number of commits present upstream
// that are not present in the current branch.
func (b *Branch) CommitsBehind() int {
	return len(lines(cmdOutput("git", "log", "--format=format:x", b.FullName()+".."+b.OriginBranch(), "--")))
}

// Submitted reports whether some form of b's pending commit
// has been cherry picked to origin.
func (b *Branch) Submitted(id string) bool {
	if id == "" {
		return false
	}
	line := "Change-Id: " + id
	out := cmdOutput("git", "log", "-n", "1", "-F", "--grep", line, b.Name+".."+b.OriginBranch(), "--")
	return strings.Contains(out, line)
}

var stagedRE = regexp.MustCompile(`^[ACDMR]  `)

// HasStagedChanges reports whether the working directory contains staged changes.
func HasStagedChanges() bool {
	for _, s := range nonBlankLines(cmdOutput("git", "status", "-b", "--porcelain")) {
		if stagedRE.MatchString(s) {
			return true
		}
	}
	return false
}

var unstagedRE = regexp.MustCompile(`^.[ACDMR]`)

// HasUnstagedChanges reports whether the working directory contains unstaged changes.
func HasUnstagedChanges() bool {
	for _, s := range nonBlankLines(cmdOutput("git", "status", "-b", "--porcelain")) {
		if unstagedRE.MatchString(s) {
			return true
		}
	}
	return false
}

// LocalChanges returns a list of files containing staged, unstaged, and untracked changes.
// The elements of the returned slices are typically file names, always relative to the root,
// but there are a few alternate forms. First, for renaming or copying, the element takes
// the form `from -> to`. Second, in the case of files with names that contain unusual characters,
// the files (or the from, to fields of a rename or copy) are quoted C strings.
// For now, we expect the caller only shows these to the user, so these exceptions are okay.
func LocalChanges() (staged, unstaged, untracked []string) {
	for _, s := range lines(cmdOutput("git", "status", "-b", "--porcelain")) {
		if len(s) < 4 || s[2] != ' ' {
			continue
		}
		switch s[0] {
		case 'A', 'C', 'D', 'M', 'R':
			staged = append(staged, s[3:])
		case '?':
			untracked = append(untracked, s[3:])
		}
		switch s[1] {
		case 'A', 'C', 'D', 'M', 'R':
			unstaged = append(unstaged, s[3:])
		}
	}
	return
}

// LocalBranches returns a list of all known local branches.
// If the current directory is in detached HEAD mode, one returned
// branch will have Name == "HEAD" and DetachedHead() == true.
func LocalBranches() []*Branch {
	var branches []*Branch
	current := CurrentBranch()
	for _, s := range nonBlankLines(cmdOutput("git", "branch", "-q")) {
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, "* ") {
			// * marks current branch in output.
			// Normally the current branch has a name like any other,
			// but in detached HEAD mode the branch listing shows
			// a localized (translated) textual description instead of
			// a branch name. Avoid language-specific differences
			// by using CurrentBranch().Name for the current branch.
			// It detects detached HEAD mode in a more portable way.
			// (git rev-parse --abbrev-ref HEAD returns 'HEAD').
			s = current.Name
		}
		// + marks a branch checked out in a worktree. Worktrees in detached
		// HEAD mode don't appear in the "git branch" output, so this is always
		// a normal name.
		s = strings.TrimPrefix(s, "+ ")
		branches = append(branches, &Branch{Name: s})
	}
	return branches
}

func OriginBranches() []string {
	var branches []string
	for _, line := range nonBlankLines(cmdOutput("git", "branch", "-a", "-q")) {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, " -> "); i >= 0 {
			line = line[:i]
		}
		name := strings.TrimSpace(strings.TrimPrefix(line, "* "))
		if strings.HasPrefix(name, "remotes/origin/") {
			branches = append(branches, strings.TrimPrefix(name, "remotes/"))
		}
	}
	return branches
}

// GerritChange returns the change metadata from the Gerrit server
// for the branch's pending change.
// The extra strings are passed to the Gerrit API request as o= parameters,
// to enable additional information. Typical values include "LABELS" and "CURRENT_REVISION".
// See https://gerrit-review.googlesource.com/Documentation/rest-api-changes.html for details.
func (b *Branch) GerritChange(c *Commit, extra ...string) (*GerritChange, error) {
	if !b.HasPendingCommit() {
		return nil, fmt.Errorf("no changes pending")
	}
	if c.ChangeID == "" {
		return nil, fmt.Errorf("missing Change-Id")
	}
	id := fullChangeID(b, c)
	for i, x := range extra {
		if i == 0 {
			id += "?"
		} else {
			id += "&"
		}
		id += "o=" + x
	}
	return readGerritChange(id)
}

// GerritChange returns the change metadata from the Gerrit server
// for the given changes, which each be be the result of fullChangeID(b, c) for some c.
// The extra strings are passed to the Gerrit API request as o= parameters,
// to enable additional information. Typical values include "LABELS" and "CURRENT_REVISION".
// See https://gerrit-review.googlesource.com/Documentation/rest-api-changes.html for details.
func (b *Branch) GerritChanges(ids []string, extra ...string) ([][]*GerritChange, error) {
	q := ""
	for _, id := range ids {
		if q != "" {
			q += "&"
		}
		if strings.HasSuffix(id, "~") {
			// result of fullChangeID(b, c) with missing Change-Id; don't send
			q += "q=is:closed+is:open" // cannot match anything
			continue
		}
		q += "q=change:" + url.QueryEscape(id)
	}
	if q == "" {
		return nil, fmt.Errorf("no changes found")
	}
	for _, x := range extra {
		q += "&o=" + url.QueryEscape(x)
	}
	return readGerritChanges(q)
}

// CommitByRev finds a unique pending commit by its git <rev>.
// It dies if rev cannot be resolved to a commit or that commit is not
// pending on b using the action ("mail", "submit") in the failure message.
func (b *Branch) CommitByRev(action, rev string) *Commit {
	// Parse rev to a commit hash.
	hash, err := cmdOutputErr("git", "rev-parse", "--verify", rev+"^{commit}")
	if err != nil {
		msg := strings.TrimPrefix(trim(err.Error()), "fatal: ")
		dief("cannot %s: %s", action, msg)
	}
	hash = trim(hash)

	// Check that hash is a pending commit.
	var c *Commit
	for _, c1 := range b.Pending() {
		if c1.Hash == hash {
			c = c1
			break
		}
	}
	if c == nil {
		dief("cannot %s: commit hash %q not found in the current branch", action, hash)
	}
	return c
}

// DefaultCommit returns the default pending commit for this branch.
// It dies if there is not exactly one pending commit,
// using the action (e.g. "mail", "submit") and optional extra instructions
// in the failure message.
func (b *Branch) DefaultCommit(action, extra string) *Commit {
	work := b.Pending()
	if len(work) == 0 {
		dief("cannot %s: no changes pending", action)
	}
	if len(work) >= 2 {
		var buf bytes.Buffer
		for _, c := range work {
			fmt.Fprintf(&buf, "\n\t%s %s", c.ShortHash, c.Subject)
		}
		if extra != "" {
			extra = "; " + extra
		}
		dief("cannot %s: multiple changes pending%s:%s", action, extra, buf.String())
	}
	return work[0]
}

// ListFiles returns the list of files in a given commit.
func ListFiles(c *Commit) []string {
	if c.Parent == "" {
		return nil
	}
	return nonBlankLines(cmdOutput("git", "diff", "--name-only", c.Parent, c.Hash, "--"))
}

func cmdBranchpoint(args []string) {
	expectZeroArgs(args, "branchpoint")
	b := CurrentBranch()
	b.NeedOriginBranch("branchpoint")
	fmt.Fprintf(stdout(), "%s\n", b.Branchpoint())
}

func cmdRebaseWork(args []string) {
	expectZeroArgs(args, "rebase-work")
	b := CurrentBranch()
	if HasStagedChanges() || HasUnstagedChanges() {
		dief("cannot rebase with uncommitted work")
	}
	if len(b.Pending()) == 0 {
		dief("no pending work")
	}
	run("git", "rebase", "-i", b.Branchpoint())
}
