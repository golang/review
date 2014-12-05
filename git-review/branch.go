// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"regexp"
	"strings"
)

// Branch describes a Git branch.
type Branch struct {
	Name     string // branch name
	ChangeID string // Change-Id of pending commit ("" if nothing pending)
	Subject  string // first line of pending commit ("" if nothing pending)
}

// Submitted reports whether some form of b's pending commit
// has been cherry picked to master.
func (b *Branch) Submitted() bool {
	return b.ChangeID != "" && changeSubmitted(b.ChangeID)
}

func CurrentBranch() *Branch {
	b := &Branch{Name: currentBranchName()}
	if hasPendingCommit(b.Name) {
		b.ChangeID = headChangeID(b.Name)
		b.Subject = commitSubject(b.Name)
	}
	return b
}

func hasPendingCommit(branch string) bool {
	head := getOutput("git", "rev-parse", branch)
	base := getOutput("git", "merge-base", "origin/master", branch)
	return base != head
}

func currentBranchName() string {
	return getOutput("git", "rev-parse", "--abbrev-ref", "HEAD")
}

func commitSubject(ref string) string {
	const f = "--format=format:%s"
	return getOutput("git", "log", "-n", "1", f, ref, "--")
}

func changeSubmitted(id string) bool {
	s := "Change-Id: " + id
	return len(getOutput("git", "log", "--grep", s, "origin/master")) > 0
}

func headChangeID(branch string) string {
	const (
		p = "Change-Id: "
		f = "--format=format:%b"
	)
	for _, s := range getLines("git", "log", "-n", "1", f, branch, "--") {
		if strings.HasPrefix(s, p) {
			return strings.TrimSpace(strings.TrimPrefix(s, p))
		}
	}
	dief("no Change-Id line found in HEAD commit on branch %s.", branch)
	panic("unreachable")
}

var stagedRe = regexp.MustCompile(`^[ACDMR]  `)

func hasStagedChanges() bool {
	for _, s := range getLines("git", "status", "-b", "--porcelain") {
		if stagedRe.MatchString(s) {
			return true
		}
	}
	return false
}

func localBranches() (branches []string) {
	for _, s := range getLines("git", "branch", "-l", "-q") {
		branches = append(branches, strings.TrimPrefix(s, "* "))
	}
	return branches
}
