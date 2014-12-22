// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// Branch describes a Git branch.
type Branch struct {
	Name            string // branch name
	loadedPending   bool   // following fields are valid
	changeID        string // Change-Id of pending commit ("" if nothing pending)
	subject         string // first line of pending commit ("" if nothing pending)
	message         string // commit message
	commitHash      string // commit hash of pending commit ("" if nothing pending)
	shortCommitHash string // abbreviated commitHash ("" if nothing pending)
	parentHash      string // parent hash of pending commit ("" if nothing pending)
	commitsAhead    int    // number of commits ahead of origin branch
	commitsBehind   int    // number of commits behind origin branch
	originBranch    string // upstream origin branch
}

// CurrentBranch returns the current branch.
func CurrentBranch() *Branch {
	name := getOutput("git", "rev-parse", "--abbrev-ref", "HEAD")
	return &Branch{Name: name}
}

// DetachedHead reports whether branch b corresponds to a detached HEAD
// (does not have a real branch name).
func (b *Branch) DetachedHead() bool {
	return b.Name == "HEAD"
}

// OriginBranch returns the name of the origin branch that branch b tracks.
// The returned name is like "origin/master" or "origin/dev.garbage" or
// "origin/release-branch.go1.4".
func (b *Branch) OriginBranch() string {
	if b.DetachedHead() {
		// Detached head mode.
		// "origin/HEAD" is clearly false, but it should be easy to find when it
		// appears in other commands. Really any caller of OriginBranch
		// should check for detached head mode.
		return "origin/HEAD"
	}

	if b.originBranch != "" {
		return b.originBranch
	}
	argv := []string{"git", "rev-parse", "--abbrev-ref", b.Name + "@{u}"}
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if err == nil && len(out) > 0 {
		b.originBranch = string(bytes.TrimSpace(out))
		return b.originBranch
	}
	if strings.Contains(string(out), "No upstream configured") {
		// Assume branch was created before we set upstream correctly.
		b.originBranch = "origin/master"
		return b.originBranch
	}
	fmt.Fprintf(stderr(), "%v\n%s\n", commandString(argv[0], argv[1:]), out)
	dief("%v", err)
	panic("not reached")
}

func (b *Branch) IsLocalOnly() bool {
	return "origin/"+b.Name != b.OriginBranch()
}

func (b *Branch) HasPendingCommit() bool {
	b.loadPending()
	return b.commitHash != ""
}

func (b *Branch) ChangeID() string {
	b.loadPending()
	return b.changeID
}

func (b *Branch) Subject() string {
	b.loadPending()
	return b.subject
}

func (b *Branch) CommitHash() string {
	b.loadPending()
	return b.commitHash
}

// Branchpoint returns an identifier for the latest revision
// common to both this branch and its upstream branch.
// If this branch has not split from upstream,
// Branchpoint returns "HEAD".
func (b *Branch) Branchpoint() string {
	b.loadPending()
	if b.parentHash == "" {
		return "HEAD"
	}
	return b.parentHash
}

func (b *Branch) loadPending() {
	if b.loadedPending {
		return
	}
	b.loadedPending = true

	if b.DetachedHead() {
		return
	}

	const numField = 5
	all := getOutput("git", "log", "--format=format:%H%x00%h%x00%P%x00%s%x00%B%x00", b.OriginBranch()+".."+b.Name)
	fields := strings.Split(all, "\x00")
	if len(fields) < numField {
		return // nothing pending
	}
	for i := 0; i+numField <= len(fields); i += numField {
		hash := fields[i]
		shortHash := fields[i+1]
		parent := fields[i+2]
		subject := fields[i+3]
		msg := fields[i+4]

		// Overwrite each time through the loop.
		// We want to save the info about the *first* commit
		// after the branch point, and the log is ordered
		// starting at the most recent and working backward.
		b.commitHash = strings.TrimSpace(hash)
		b.shortCommitHash = strings.TrimSpace(shortHash)
		b.parentHash = strings.TrimSpace(parent)
		b.subject = subject
		b.message = msg
		for _, line := range strings.Split(msg, "\n") {
			if strings.HasPrefix(line, "Change-Id: ") {
				b.changeID = line[len("Change-Id: "):]
				break
			}
		}
		b.commitsAhead++
	}
	b.commitsAhead = len(fields) / numField
	b.commitsBehind = len(getOutput("git", "log", "--format=format:x", b.Name+".."+b.OriginBranch()))
}

// Submitted reports whether some form of b's pending commit
// has been cherry picked to origin.
func (b *Branch) Submitted(id string) bool {
	if id == "" {
		return false
	}
	return len(getOutput("git", "log", "--grep", "Change-Id: "+id, b.Name+".."+b.OriginBranch())) > 0
}

var stagedRE = regexp.MustCompile(`^[ACDMR]  `)

func HasStagedChanges() bool {
	for _, s := range getLines("git", "status", "-b", "--porcelain") {
		if stagedRE.MatchString(s) {
			return true
		}
	}
	return false
}

var unstagedRE = regexp.MustCompile(`^.[ACDMR]`)

func HasUnstagedChanges() bool {
	for _, s := range getLines("git", "status", "-b", "--porcelain") {
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
	// NOTE: Cannot use getLines, because it throws away leading spaces.
	for _, s := range strings.Split(getOutput("git", "status", "-b", "--porcelain"), "\n") {
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

func LocalBranches() []*Branch {
	var branches []*Branch
	current := CurrentBranch()
	for _, s := range getLines("git", "branch", "-q") {
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
		branches = append(branches, &Branch{Name: s})
	}
	return branches
}

func OriginBranches() []string {
	var branches []string
	for _, line := range getLines("git", "branch", "-a", "-q") {
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
func (b *Branch) GerritChange(extra ...string) (*GerritChange, error) {
	if !b.HasPendingCommit() {
		return nil, fmt.Errorf("no pending commit")
	}
	id := fullChangeID(b)
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
