// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
)

var (
	pendingLocal       bool // -l flag, use only local operations (no network)
	pendingCurrentOnly bool // -c flag, show only current branch
	pendingShort       bool // -s flag, short display
	pendingGerrit      bool // -g flag, Gerrit based short display
)

// A pendingBranch collects information about a single pending branch.
// We overlap the reading of this information for each branch.
type pendingBranch struct {
	*Branch            // standard Branch functionality
	current   bool     // is this the current branch?
	staged    []string // files in staging area, only if current==true
	unstaged  []string // files unstaged in local directory, only if current==true
	untracked []string // files untracked in local directory, only if current==true
}

// load populates b with information about the branch.
func (b *pendingBranch) load() {
	b.loadPending()
	if !b.current && b.commitsAhead == 0 {
		// Won't be displayed, don't bother looking any closer.
		return
	}
	b.OriginBranch() // cache result
	if b.current {
		b.staged, b.unstaged, b.untracked = LocalChanges()
	}
	var changeIDs []string
	var commits []*Commit
	for _, c := range b.Pending() {
		c.committed = ListFiles(c)
		if c.ChangeID == "" {
			c.gerr = fmt.Errorf("missing Change-Id in commit message")
		} else {
			changeIDs = append(changeIDs, fullChangeID(b.Branch, c))
			commits = append(commits, c)
		}
	}
	if !pendingLocal {
		gs, err := b.GerritChanges(changeIDs, "DETAILED_LABELS", "CURRENT_REVISION", "MESSAGES", "DETAILED_ACCOUNTS")
		if len(gs) != len(commits) && err == nil {
			err = fmt.Errorf("invalid response from Gerrit server - %d queries but %d results", len(changeIDs), len(gs))
		}
		if err != nil {
			for _, c := range commits {
				if c.gerr != nil {
					c.gerr = err
				}
			}
		} else {
			for i, c := range commits {
				if len(gs[i]) == 1 {
					c.g = gs[i][0]
				}
			}
		}
	}
	for _, c := range b.Pending() {
		if c.g == nil {
			c.g = new(GerritChange) // easier for formatting code
		}
	}
}

func cmdPending(args []string) {
	// NOTE: New flags should be added to the usage message below as well as doc.go.
	flags.BoolVar(&pendingCurrentOnly, "c", false, "show only current branch")
	flags.BoolVar(&pendingLocal, "l", false, "use only local information - no network operations")
	flags.BoolVar(&pendingShort, "s", false, "show short listing (may not be used with -g)")
	flags.BoolVar(&pendingGerrit, "g", false, "show a different Gerrit-based listing (may not be used with -s)")
	flags.Parse(args)
	if len(flags.Args()) > 0 {
		fmt.Fprintf(stderr(), "Usage: %s pending %s [-c] [-g] [-l] [-s]\n", progName, globalFlags)
		exit(2)
	}

	if pendingShort && pendingGerrit {
		fmt.Fprintf(stderr(), "%s: using -g and -s together is not supported\n", progName)
		exit(2)
	}

	// Fetch info about remote changes, so that we can say which branches need sync.
	doneFetch := make(chan bool, 1)
	if pendingLocal {
		doneFetch <- true
	} else {
		http.DefaultClient.Timeout = 60 * time.Second
		go func() {
			run("git", "fetch", "-q")
			doneFetch <- true
		}()
	}

	// Build list of pendingBranch structs to be filled in.
	// The current branch is always first.
	var branches []*pendingBranch
	branches = []*pendingBranch{{Branch: CurrentBranch(), current: true}}
	if !pendingCurrentOnly {
		current := CurrentBranch().Name
		for _, b := range LocalBranches() {
			if b.Name != current {
				branches = append(branches, &pendingBranch{Branch: b})
			}
		}
	}

	// The various data gathering is a little slow,
	// especially run in serial with a lot of branches.
	// Overlap inspection of multiple branches.
	// Each branch is only accessed by a single worker.

	// Build work queue.
	work := make(chan *pendingBranch, len(branches))
	done := make(chan bool, len(branches))
	for _, b := range branches {
		work <- b
	}
	close(work)

	// Kick off goroutines to do work.
	n := len(branches)
	if n > 10 {
		n = 10
	}
	for i := 0; i < n; i++ {
		go func() {
			for b := range work {
				// This b.load may be using a stale origin/master ref, which is OK.
				b.load()
				done <- true
			}
		}()
	}

	// Wait for goroutines to finish.
	// Note: Counting work items, not goroutines (there may be fewer goroutines).
	for range branches {
		<-done
	}
	<-doneFetch

	if !pendingGerrit {
		printPendingStandard(branches)
	} else {
		printPendingGerrit(branches)
	}
}

// printPendingStandard prints the default output format,
// as modified by pendingShort.
func printPendingStandard(branches []*pendingBranch) {
	// Print output.
	// If there are multiple changes in the current branch, the output splits them out into separate sections,
	// in reverse commit order, to match git log output.
	//
	//	wbshadow 7a524a1..a496c1e (current branch, all mailed, 23 behind, tracking master)
	//	+ uncommitted changes
	//		Files unstaged:
	//			src/runtime/proc1.go
	//
	//	+ a496c1e https://go-review.googlesource.com/2064 (mailed)
	//		runtime: add missing write barriers in append's copy of slice data
	//
	//		Found with GODEBUG=wbshadow=1 mode.
	//		Eventually that will run automatically, but right now
	//		it still detects other missing write barriers.
	//
	//		Change-Id: Ic8624401d7c8225a935f719f96f2675c6f5c0d7c
	//
	//		Code-Review:
	//			+0 Austin Clements, Rick Hudson
	//		Files in this change:
	//			src/runtime/slice.go
	//
	//	+ 95390c7 https://go-review.googlesource.com/2061 (mailed)
	//		runtime: add GODEBUG wbshadow for finding missing write barriers
	//
	//		This is the detection code. It works well enough that I know of
	//		a handful of missing write barriers. However, those are subtle
	//		enough that I'll address them in separate followup CLs.
	//
	//		Change-Id: If863837308e7c50d96b5bdc7d65af4969bf53a6e
	//
	//		Code-Review:
	//			+0 Austin Clements, Rick Hudson
	//		Files in this change:
	//			src/runtime/extern.go
	//			src/runtime/malloc1.go
	//			src/runtime/malloc2.go
	//			src/runtime/mgc.go
	//			src/runtime/mgc0.go
	//			src/runtime/proc1.go
	//			src/runtime/runtime1.go
	//			src/runtime/runtime2.go
	//			src/runtime/stack1.go
	//
	// The first line only gives information that applies to the entire branch:
	// the name, the commit range, whether this is the current branch, whether
	// all the commits are mailed/submitted, how far behind, what remote branch
	// it is tracking.
	// The individual change sections have per-change information: the hash of that
	// commit, the URL on the Gerrit server, whether it is mailed/submitted, the list of
	// files in that commit. The uncommitted file modifications are shown as a separate
	// section, at the beginning, to fit better into the reverse commit order.
	//
	// The short view compresses the listing down to two lines per commit:
	//	wbshadow 7a524a1..a496c1e (current branch, all mailed, 23 behind, tracking master)
	//	+ uncommitted changes
	//		Files unstaged:
	//			src/runtime/proc1.go
	//	+ a496c1e runtime: add missing write barriers in append's copy of slice data (CL 2064, mailed)
	//	+ 95390c7 runtime: add GODEBUG wbshadow for finding missing write barriers (CL 2061, mailed)

	var buf bytes.Buffer
	printFileList := func(name string, list []string) {
		if len(list) == 0 {
			return
		}
		fmt.Fprintf(&buf, "\tFiles %s:\n", name)
		for _, file := range list {
			fmt.Fprintf(&buf, "\t\t%s\n", file)
		}
	}

	for _, b := range branches {
		if !b.current && b.commitsAhead == 0 {
			// Hide branches with no work on them.
			continue
		}

		fmt.Fprintf(&buf, "%s", b.Name)
		work := b.Pending()
		if len(work) > 0 {
			fmt.Fprintf(&buf, " %.7s..%s", b.branchpoint, work[0].ShortHash)
		}
		var tags []string
		if b.DetachedHead() {
			tags = append(tags, "detached")
		} else if b.current {
			tags = append(tags, "current branch")
		}
		if allMailed(work) && len(work) > 0 {
			tags = append(tags, "all mailed")
		}
		if allSubmitted(work) && len(work) > 0 {
			tags = append(tags, "all submitted")
		}
		if n := b.CommitsBehind(); n > 0 {
			tags = append(tags, fmt.Sprintf("%d behind", n))
		}
		if br := b.OriginBranch(); br == "" {
			tags = append(tags, "remote branch unknown")
		} else if br != "origin/master" && br != "origin/main" {
			tags = append(tags, "tracking "+strings.TrimPrefix(b.OriginBranch(), "origin/"))
		}
		if len(tags) > 0 {
			fmt.Fprintf(&buf, " (%s)", strings.Join(tags, ", "))
		}
		fmt.Fprintf(&buf, "\n")
		printed := false

		if b.current && len(b.staged)+len(b.unstaged)+len(b.untracked) > 0 {
			printed = true
			fmt.Fprintf(&buf, "+ uncommitted changes\n")
			printFileList("untracked", b.untracked)
			printFileList("unstaged", b.unstaged)
			printFileList("staged", b.staged)
			if !pendingShort {
				fmt.Fprintf(&buf, "\n")
			}
		}

		for _, c := range work {
			printed = true
			fmt.Fprintf(&buf, "+ ")
			formatCommit(&buf, c, pendingShort)
			if !pendingShort {
				printFileList("in this change", c.committed)
				fmt.Fprintf(&buf, "\n")
			}
		}
		if pendingShort || !printed {
			fmt.Fprintf(&buf, "\n")
		}
	}

	stdout().Write(buf.Bytes())
}

// formatCommit writes detailed information about c to w. c.g must
// have the "CURRENT_REVISION" (or "ALL_REVISIONS") and
// "DETAILED_LABELS" options set.
//
// If short is true, this writes a single line overview.
//
// If short is false, this writes detailed information about the
// commit and its Gerrit state.
func formatCommit(w io.Writer, c *Commit, short bool) {
	g := c.g
	if g == nil {
		g = new(GerritChange)
	}
	msg := strings.TrimRight(c.Message, "\r\n")
	fmt.Fprintf(w, "%s", c.ShortHash)
	var tags []string
	if short {
		if i := strings.Index(msg, "\n"); i >= 0 {
			msg = msg[:i]
		}
		fmt.Fprintf(w, " %s", msg)
		if g.Number != 0 {
			tags = append(tags, fmt.Sprintf("CL %d%s", g.Number, codeReviewScores(g)))
		}
	} else {
		if g.Number != 0 {
			fmt.Fprintf(w, " %s/%d", auth.url, g.Number)
		}
	}
	if g.CurrentRevision == c.Hash {
		tags = append(tags, "mailed")
	}
	switch g.Status {
	case "MERGED":
		tags = append(tags, "submitted")
	case "ABANDONED":
		tags = append(tags, "abandoned")
	}
	if len(c.Parents) > 1 {
		var h []string
		for _, p := range c.Parents[1:] {
			h = append(h, p[:7])
		}
		tags = append(tags, "merge="+strings.Join(h, ","))
	}
	if g.UnresolvedCommentCount > 0 {
		tags = append(tags, fmt.Sprintf("%d unresolved comments", g.UnresolvedCommentCount))
	}
	if len(tags) > 0 {
		fmt.Fprintf(w, " (%s)", strings.Join(tags, ", "))
	}
	fmt.Fprintf(w, "\n")
	if short {
		return
	}

	fmt.Fprintf(w, "\t%s\n", strings.Replace(msg, "\n", "\n\t", -1))
	fmt.Fprintf(w, "\n")

	for _, name := range g.LabelNames() {
		label := g.Labels[name]
		minValue := 10000
		maxValue := -10000
		byScore := map[int][]string{}
		for _, x := range label.All {
			// Hide CL owner unless owner score is nonzero.
			if g.Owner != nil && x.ID == g.Owner.ID && x.Value == 0 {
				continue
			}
			byScore[x.Value] = append(byScore[x.Value], x.Name)
			if minValue > x.Value {
				minValue = x.Value
			}
			if maxValue < x.Value {
				maxValue = x.Value
			}
		}
		// Unless there are scores to report, do not show labels other than Code-Review.
		// This hides Run-TryBot and TryBot-Result.
		if minValue >= 0 && maxValue <= 0 && name != "Code-Review" {
			continue
		}
		fmt.Fprintf(w, "\t%s:\n", name)
		for score := maxValue; score >= minValue; score-- {
			who := byScore[score]
			if len(who) == 0 || score == 0 && name != "Code-Review" {
				continue
			}
			sort.Strings(who)
			fmt.Fprintf(w, "\t\t%+d %s\n", score, strings.Join(who, ", "))
		}
	}
}

// codeReviewScores reports the code review scores as tags for the short output.
//
// g must have the "DETAILED_LABELS" option set.
func codeReviewScores(g *GerritChange) string {
	label := g.Labels["Code-Review"]
	if label == nil {
		return ""
	}
	minValue := 10000
	maxValue := -10000
	for _, x := range label.All {
		if minValue > x.Value {
			minValue = x.Value
		}
		if maxValue < x.Value {
			maxValue = x.Value
		}
	}
	var scores string
	if minValue < 0 {
		scores += fmt.Sprintf(" %d", minValue)
	}
	if maxValue > 0 {
		scores += fmt.Sprintf(" %+d", maxValue)
	}
	return scores
}

// printPendingGerrit prints the Gerrit-based format.
// This prints only CLs that have been fully sent to Gerrit,
// with their CL number and the topic line.
func printPendingGerrit(branches []*pendingBranch) {
	type branchBuf struct {
		name    string
		buf     bytes.Buffer
		updated time.Time
	}
	var branchBufs []*branchBuf

	for _, b := range branches {
		if b.commitsAhead == 0 {
			continue
		}

		work := b.Pending()

		if len(work) == 0 {
			continue
		}

		var updatedStr string
		for _, c := range work {
			if c.g.Updated != "" {
				updatedStr = c.g.Updated
				break
			}
			if c.g.Created != "" {
				updatedStr = c.g.Created
				break
			}
		}

		var updated time.Time
		if updatedStr != "" {
			var err error
			updated, err = time.Parse("2006-01-02 15:04:05.999999999", updatedStr)
			if err != nil {
				fmt.Fprintf(stderr(), "failed to parse gerrit timestamp %q: %v\n", updatedStr, err)
			}
		}

		bb := &branchBuf{
			name:    b.Name,
			updated: updated,
		}
		branchBufs = append(branchBufs, bb)

		if allSubmitted(work) {
			fmt.Fprintf(&bb.buf, "- branch %s submitted\n", b.Name)
			continue
		}
		if allAbandoned(work) {
			fmt.Fprintf(&bb.buf, "- branch %s abandoned\n", b.Name)
			continue
		}

		fmt.Fprintf(&bb.buf, "- branch %s updated %s\n", b.Name, updated)
		for _, c := range work {
			if c.g.Number == 0 {
				continue
			}
			fmt.Fprintf(&bb.buf, "  https://go.dev/cl/%d %s\n", c.g.Number, c.g.Subject)
		}
	}

	slices.SortFunc(branchBufs, func(a, b *branchBuf) int {
		if r := a.updated.Compare(b.updated); r != 0 {
			return r
		}
		return strings.Compare(a.name, b.name)
	})

	for _, bb := range branchBufs {
		stdout().Write(bb.buf.Bytes())
	}
}

// allMailed reports whether all commits in work have been posted to Gerrit.
func allMailed(work []*Commit) bool {
	for _, c := range work {
		if c.Hash != c.g.CurrentRevision {
			return false
		}
	}
	return true
}

// allSubmitted reports whether all commits in work have been submitted to the origin branch.
func allSubmitted(work []*Commit) bool {
	for _, c := range work {
		if c.g.Status != "MERGED" {
			return false
		}
	}
	return true
}

// allAbandoned reports whether all commits in work have been abandoned.
func allAbandoned(work []*Commit) bool {
	for _, c := range work {
		if c.g.Status != "ABANDONED" {
			return false
		}
	}
	return true
}

// suffix returns an empty string if n == 1, s otherwise.
func suffix(n int, s string) string {
	if n == 1 {
		return ""
	}
	return s
}
