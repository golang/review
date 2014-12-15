// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"sort"
	"time"
)

// TODO(rsc): Add -tbr.

func submit(args []string) {
	expectZeroArgs(args, "submit")

	// Must have pending change, no staged changes.
	b := CurrentBranch()
	if !b.HasPendingCommit() {
		dief("cannot submit: no pending commit")
	}
	checkStaged("submit")

	// Also, no unstaged changes, at least for now.
	// This makes sure the sync at the end will work well.
	// We can relax this later if there is a good reason.
	checkUnstaged("submit")

	// Fetch Gerrit information about this change.
	ch, err := b.GerritChange()
	if err != nil {
		dief("%v", err)
	}

	// Check Gerrit change status.
	switch ch.Status {
	default:
		dief("cannot submit: unexpected Gerrit change status %q", ch.Status)

	case "NEW", "SUBMITTED":
		// Not yet "MERGED", so try the submit.
		// "SUBMITTED" is a weird state. It means that Submit has been clicked once,
		// but it hasn't happened yet, usually because of a merge failure.
		// The user may have done git sync and may now have a mergable
		// copy waiting to be uploaded, so continue on as if it were "NEW".

	case "MERGED":
		// Can happen if moving between different clients.
		dief("cannot submit: change already submitted, run 'git sync'")

	case "ABANDONED":
		dief("cannot submit: change abandoned")
	}

	// Check for label approvals (like CodeReview+2).
	// The final submit will check these too, but it is better to fail now.
	var names []string
	for name := range ch.Labels {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		label := ch.Labels[name]
		if label.Optional {
			continue
		}
		if label.Rejected != nil {
			dief("cannot submit: change has %s rejection", name)
		}
		if label.Approved == nil {
			dief("cannot submit: change missing %s approval", name)
		}
	}

	// Upload most recent revision if not already on server.
	if b.CommitHash() != ch.CurrentRevision {
		run("git", "push", "-q", "origin", b.PushSpec())

		// Refetch change information, especially mergeable.
		ch, err = b.GerritChange()
		if err != nil {
			dief("%v", err)
		}
	}

	// Don't bother if the server can't merge the changes.
	if !ch.Mergeable {
		// Server cannot merge; explicit sync is needed.
		dief("cannot submit: conflicting changes submitted, run 'git sync'")
	}

	if *noRun {
		dief("stopped before submit")
	}

	// Otherwise, try the submit. Sends back updated GerritChange,
	// but we need extended information and the reply is in the
	// "SUBMITTED" state anyway, so ignore the GerritChange
	// in the response and fetch a new one below.
	if err := gerritAPI("/a/changes/"+fullChangeID(b)+"/submit", []byte(`{"wait_for_merge": true}`), nil); err != nil {
		dief("cannot submit: %v", err)
	}

	// It is common to get back "SUBMITTED" for a split second after the
	// request is made. That indicates that the change has been queued for submit,
	// but the first merge (the one wait_for_merge waited for)
	// failed, possibly due to a spurious condition. We see this often, and the
	// status usually changes to MERGED shortly thereafter.
	// Wait a little while to see if we can get to a different state.
	const steps = 6
	const max = 2 * time.Second
	for i := 0; i < steps; i++ {
		time.Sleep(max * (1 << uint(i+1)) / (1 << steps))
		ch, err = b.GerritChange()
		if err != nil {
			dief("waiting for merge: %v", err)
		}
		if ch.Status != "SUBMITTED" {
			break
		}
	}

	switch ch.Status {
	default:
		dief("submit error: unexpected post-submit Gerrit change status %q", ch.Status)

	case "MERGED":
		// good

	case "SUBMITTED":
		// see above
		dief("cannot submit: timed out waiting for change to be submitted by Gerrit")
	}

	// Sync client to revision that Gerrit committed, but only if we can do it cleanly.
	// Otherwise require user to run 'git sync' themselves (if they care).
	run("git", "fetch", "-q")
	if err := runErr("git", "checkout", "-q", "-B", b.Name, ch.CurrentRevision, "--"); err != nil {
		dief("submit succeeded, but cannot sync local branch\n"+
			"\trun 'git sync' to sync, or\n"+
			"\trun 'git branch -D %s; git change master; git sync' to discard local branch", b.Name)
	}

	// Done! Change is submitted, branch is up to date, ready for new work.
}

