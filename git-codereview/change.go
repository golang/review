// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var changeAuto bool
var changeQuick bool

func cmdChange(args []string) {
	flags.BoolVar(&changeAuto, "a", false, "add changes to any tracked files")
	flags.BoolVar(&changeQuick, "q", false, "do not edit pending commit msg")
	flags.Parse(args)
	if len(flags.Args()) > 1 {
		fmt.Fprintf(stderr(), "Usage: %s change %s [branch]\n", os.Args[0], globalFlags)
		os.Exit(2)
	}

	// Checkout or create branch, if specified.
	target := flags.Arg(0)
	if target != "" {
		checkoutOrCreate(target)
		b := CurrentBranch()
		if HasStagedChanges() && b.IsLocalOnly() && !b.HasPendingCommit() {
			commitChanges(false)
		}
		b.check()
		return
	}

	// Create or amend change commit.
	b := CurrentBranch()
	if !b.IsLocalOnly() {
		dief("can't commit to %s branch (use '%s change branchname').", b.Name, os.Args[0])
	}

	amend := b.HasPendingCommit()
	if amend {
		// Dies if there is not exactly one commit.
		b.DefaultCommit("amend change", "")
	}
	commitChanges(amend)
	b.loadedPending = false // force reload after commitChanges
	b.check()
}

func (b *Branch) check() {
	// TODO(rsc): Test
	staged, unstaged, _ := LocalChanges()
	if len(staged) == 0 && len(unstaged) == 0 {
		// No staged changes, no unstaged changes.
		// If the branch is behind upstream, now is a good time to point that out.
		// This applies to both local work branches and tracking branches.
		// TODO(rsc): Test.
		b.loadPending()
		if b.commitsBehind > 0 {
			printf("warning: %d commit%s behind %s; run 'git sync' to update.", b.commitsBehind, suffix(b.commitsBehind, "s"), b.OriginBranch())
		}
	}

	// TODO(rsc): Test
	if text := b.errors(); text != "" {
		printf("error: %s\n", text)
	}
}

var testCommitMsg string

func commitChanges(amend bool) {
	// git commit will run the gofmt hook.
	// Run it now to give a better error (won't show a git commit command failing).
	hookGofmt()

	if HasUnstagedChanges() && !HasStagedChanges() && !changeAuto {
		printf("warning: unstaged changes and no staged changes; use 'git add' or 'git change -a'")
	}
	commit := func(amend bool) {
		args := []string{"commit", "-q", "--allow-empty"}
		if amend {
			args = append(args, "--amend")
			if changeQuick {
				args = append(args, "--no-edit")
			}
		}
		if testCommitMsg != "" {
			args = append(args, "-m", testCommitMsg)
		}
		if changeAuto {
			args = append(args, "-a")
		}
		run("git", args...)
	}
	commit(amend)
	for !commitMessageOK() {
		fmt.Print("re-edit commit message (y/n)? ")
		if !scanYes() {
			break
		}
		commit(true)
	}
	printf("change updated.")
}

func checkoutOrCreate(target string) {
	// If it's a valid Gerrit number, checkout the CL.
	cl, ps, isCL := parseCL(target)
	if isCL {
		if !haveGerrit() {
			dief("cannot change to a CL without gerrit")
		}
		if HasStagedChanges() || HasUnstagedChanges() {
			dief("cannot change to a CL with uncommitted work")
		}
		checkoutCL(cl, ps)
		return
	}

	if strings.ToUpper(target) == "HEAD" {
		// Git gets very upset and confused if you 'git change head'
		// on systems with case-insensitive file names: the branch
		// head conflicts with the usual HEAD.
		dief("invalid branch name %q: ref name HEAD is reserved for git.", target)
	}

	// If local branch exists, check it out.
	for _, b := range LocalBranches() {
		if b.Name == target {
			run("git", "checkout", "-q", target)
			printf("changed to branch %v.", target)
			return
		}
	}

	// If origin branch exists, create local branch tracking it.
	for _, name := range OriginBranches() {
		if name == "origin/"+target {
			run("git", "checkout", "-q", "-t", "-b", target, name)
			printf("created branch %v tracking %s.", target, name)
			return
		}
	}

	// Otherwise, this is a request to create a local work branch.
	// Check for reserved names. We take everything with a dot.
	if strings.Contains(target, ".") {
		dief("invalid branch name %v: branch names with dots are reserved for git-codereview.", target)
	}

	// If the current branch has a pending commit, building
	// on top of it will not help. Don't allow that.
	// Otherwise, inherit HEAD and upstream from the current branch.
	b := CurrentBranch()
	if b.HasPendingCommit() {
		if !b.IsLocalOnly() {
			dief("bad repo state: branch %s is ahead of origin/%s", b.Name, b.Name)
		}
		dief("cannot branch from work branch; change back to %v first.", strings.TrimPrefix(b.OriginBranch(), "origin/"))
	}

	origin := b.OriginBranch()

	// NOTE: This is different from git checkout -q -t -b branch. It does not move HEAD.
	run("git", "checkout", "-q", "-b", target)
	run("git", "branch", "-q", "--set-upstream-to", origin)
	printf("created branch %v tracking %s.", target, origin)
}

// Checkout the patch set of the given CL. When patch set is empty, use the latest.
func checkoutCL(cl, ps string) {
	if ps == "" {
		change, err := readGerritChange(cl + "?o=CURRENT_REVISION")
		if err != nil {
			dief("cannot change to CL %s: %v", cl, err)
		}
		rev, ok := change.Revisions[change.CurrentRevision]
		if !ok {
			dief("cannot change to CL %s: invalid current revision from gerrit", cl)
		}
		ps = strconv.Itoa(rev.Number)
	}

	var group string
	if len(cl) > 1 {
		group = cl[len(cl)-2:]
	} else {
		group = "0" + cl
	}
	ref := fmt.Sprintf("refs/changes/%s/%s/%s", group, cl, ps)

	err := runErr("git", "fetch", "-q", "origin", ref)
	if err != nil {
		dief("cannot change to CL %s/%s: %v", cl, ps, err)
	}
	err = runErr("git", "checkout", "-q", "FETCH_HEAD")
	if err != nil {
		dief("cannot change to CL %s/%s: %v", cl, ps, err)
	}
	subject, err := trimErr(cmdOutputErr("git", "log", "--format=%s", "-1"))
	if err != nil {
		printf("changed to CL %s/%s.", cl, ps)
		dief("cannot read change subject from git: %v", err)
	}
	printf("changed to CL %s/%s.\n\t%s", cl, ps, subject)
}

var parseCLRE = regexp.MustCompile(`^([0-9]+)(?:/([0-9]+))?$`)

// parseCL validates and splits the CL number and patch set (if present).
func parseCL(arg string) (cl, patchset string, ok bool) {
	m := parseCLRE.FindStringSubmatch(arg)
	if len(m) == 0 {
		return "", "", false
	}
	return m[1], m[2], true
}

var messageRE = regexp.MustCompile(`^(\[[a-zA-Z0-9.-]+\] )?[a-zA-Z0-9-/,. ]+: `)

func commitMessageOK() bool {
	body := cmdOutput("git", "log", "--format=format:%B", "-n", "1")
	ok := true
	if !messageRE.MatchString(body) {
		fmt.Print(commitMessageWarning)
		ok = false
	}
	return ok
}

const commitMessageWarning = `
Your CL description appears not to use the standard form.

The first line of your change description is conventionally a one-line summary
of the change, prefixed by the primary affected package, and is used as the
subject for code review mail; the rest of the description elaborates.

Examples:

	encoding/rot13: new package

	math: add IsInf, IsNaN

	net: fix cname in LookupHost

	unicode: update to Unicode 5.0.2

`

const fixesIssueWarning = `
Your CL description contains the string %q, which is
the old Google Code way of linking commits to issues.

You should rewrite it to use the GitHub convention: "Fixes #%v".

`

func scanYes() bool {
	var s string
	fmt.Scan(&s)
	return strings.HasPrefix(strings.ToLower(s), "y")
}
