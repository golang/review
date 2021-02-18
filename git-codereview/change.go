// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var commitMsg string
var changeAuto bool
var changeQuick bool

func cmdChange(args []string) {
	// NOTE: New flags should be added to the usage message below as well as doc.go.
	flags.StringVar(&commitMsg, "m", "", "specify a commit message")
	flags.BoolVar(&changeAuto, "a", false, "add changes to any tracked files")
	flags.BoolVar(&changeQuick, "q", false, "do not edit pending commit msg")
	flags.Parse(args)
	if len(flags.Args()) > 1 {
		fmt.Fprintf(stderr(), "Usage: %s change %s [-a] [-m msg] [-q] [branch]\n", progName, globalFlags)
		exit(2)
	}

	if _, err := cmdOutputErr("git", "rev-parse", "--abbrev-ref", "MERGE_HEAD"); err == nil {
		diePendingMerge("change")
	}
	// Note: A rebase with a conflict + rebase --continue sometimes leaves behind REBASE_HEAD.
	// So check for the rebase-merge directory instead, which it does a better job cleaning up.
	if _, err := os.Stat(filepath.Join(gitPathDir(), "rebase-merge")); err == nil {
		dief("cannot change: found pending rebase or sync")
	}

	// Checkout or create branch, if specified.
	target := flags.Arg(0)
	if target != "" {
		checkoutOrCreate(target)
		b := CurrentBranch()
		if HasStagedChanges() && !b.HasPendingCommit() {
			commitChanges(false)
		}
		b.check()
		return
	}

	// Create or amend change commit.
	b := CurrentBranch()
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
	staged, unstaged, _ := LocalChanges()
	if len(staged) == 0 && len(unstaged) == 0 {
		// No staged changes, no unstaged changes.
		// If the branch is behind upstream, now is a good time to point that out.
		// This applies to both local work branches and tracking branches.
		b.loadPending()
		if n := b.CommitsBehind(); n > 0 {
			printf("warning: %d commit%s behind %s; run 'git codereview sync' to update.", n, suffix(n, "s"), b.OriginBranch())
		}
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
		if commitMsg != "" {
			args = append(args, "-m", commitMsg)
		} else if testCommitMsg != "" {
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
	// If it's a valid Gerrit number CL or CL/PS or GitHub pull request number PR,
	// checkout the CL or PR.
	cl, ps, isCL := parseCL(target)
	if isCL {
		what := "CL"
		if !haveGerrit() && haveGitHub() {
			what = "PR"
			if ps != "" {
				dief("change PR syntax is NNN not NNN.PP")
			}
		}
		if what == "CL" && !haveGerrit() {
			dief("cannot change to a CL without gerrit")
		}
		if HasStagedChanges() || HasUnstagedChanges() {
			dief("cannot change to a %s with uncommitted work", what)
		}
		checkoutCL(what, cl, ps)
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
	// Otherwise, inherit branchpoint and upstream from the current branch.
	b := CurrentBranch()
	branchpoint := "HEAD"
	if b.HasPendingCommit() {
		fmt.Fprintf(stderr(), "warning: pending changes on %s are not copied to new branch %s\n", b.Name, target)
		branchpoint = b.Branchpoint()
	}

	origin := b.OriginBranch()

	// NOTE: This is different from git checkout -q -t -b origin,
	// because the -t wold use the origin directly, and that may be
	// ahead of the current directory. The goal of this command is
	// to create a new branch for work on the current directory,
	// not to incorporate new commits at the same time (use 'git sync' for that).
	// The ideal is that HEAD doesn't change at all.
	// In the absence of pending commits, that ideal is achieved.
	// But if there are pending commits, it'd be too confusing to have them
	// on two different work branches, so we drop them and use the
	// branchpoint they started from (after warning above), moving HEAD
	// as little as possible.
	run("git", "checkout", "-q", "-b", target, branchpoint)
	run("git", "branch", "-q", "--set-upstream-to", origin)
	printf("created branch %v tracking %s.", target, origin)
}

// Checkout the patch set of the given CL. When patch set is empty, use the latest.
func checkoutCL(what, cl, ps string) {
	if what == "CL" && ps == "" {
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

	var ref string
	if what == "CL" {
		var group string
		if len(cl) > 1 {
			group = cl[len(cl)-2:]
		} else {
			group = "0" + cl
		}
		cl = fmt.Sprintf("%s/%s", cl, ps)
		ref = fmt.Sprintf("refs/changes/%s/%s", group, cl)
	} else {
		ref = fmt.Sprintf("pull/%s/head", cl)
	}
	err := runErr("git", "fetch", "-q", "origin", ref)
	if err != nil {
		dief("cannot change to %v %s: %v", what, cl, err)
	}
	err = runErr("git", "checkout", "-q", "FETCH_HEAD")
	if err != nil {
		dief("cannot change to %s %s: %v", what, cl, err)
	}
	if *noRun {
		return
	}
	subject, err := trimErr(cmdOutputErr("git", "log", "--format=%s", "-1"))
	if err != nil {
		printf("changed to %s %s.", what, cl)
		dief("cannot read change subject from git: %v", err)
	}
	printf("changed to %s %s.\n\t%s", what, cl, subject)
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

func scanYes() bool {
	var s string
	fmt.Scan(&s)
	return strings.HasPrefix(strings.ToLower(s), "y")
}
