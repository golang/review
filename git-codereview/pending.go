// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

var (
	pendingLocal   bool // -l flag, use only local operations (no network)
	pendingCurrent bool // -c flag, show only current branch
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
	for _, c := range b.Pending() {
		c.committed = nonBlankLines(cmdOutput("git", "diff", "--name-only", c.Parent, c.Hash, "--"))
		if !pendingLocal {
			c.g, c.gerr = b.GerritChange(c, "DETAILED_LABELS", "CURRENT_REVISION", "MESSAGES", "DETAILED_ACCOUNTS")
		}
		if c.g == nil {
			c.g = new(GerritChange) // easier for formatting code
		}
	}
}

func pending(args []string) {
	flags.BoolVar(&pendingCurrent, "c", false, "show only current branch")
	flags.BoolVar(&pendingLocal, "l", false, "use only local information - no network operations")
	flags.Parse(args)
	if len(flags.Args()) > 0 {
		fmt.Fprintf(stderr(), "Usage: %s pending %s [-l]\n", os.Args[0], globalFlags)
		os.Exit(2)
	}

	// Fetch info about remote changes, so that we can say which branches need sync.
	if !pendingLocal {
		run("git", "fetch", "-q")
		http.DefaultClient.Timeout = 5 * time.Second
	}

	// Build list of pendingBranch structs to be filled in.
	var branches []*pendingBranch
	if pendingCurrent {
		branches = []*pendingBranch{{Branch: CurrentBranch(), current: true}}
	} else {
		current := CurrentBranch().Name
		for _, b := range LocalBranches() {
			branches = append(branches, &pendingBranch{Branch: b, current: b.Name == current})
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

	// Print output, like:
	//	pending 2378abf..d8fcb99 https://go-review.googlesource.com/1620 (current branch, mailed, submitted, 1 behind)
	//		git-codereview: expand pending output
	//
	//		for pending:
	//		- show full commit message
	//		- show information about being behind upstream
	//		- show list of modified files
	//		- for current branch, show staged, unstaged, untracked files
	//		- warn about being ahead of upstream on master
	//		- warn about being multiple commits ahead of upstream
	//
	//		- add same warnings to change
	//		- add change -a (mostly unrelated, but prompted by this work)
	//
	//		Change-Id: Ie480ba5b66cc07faffca421ee6c9623d35204696
	//
	//		Code-Review:
	//			+2 Andrew Gerrand, Rob Pike
	//		Files in this change:
	//			git-codereview/api.go
	//			git-codereview/branch.go
	//			git-codereview/change.go
	//			git-codereview/pending.go
	//			git-codereview/review.go
	//			git-codereview/submit.go
	//			git-codereview/sync.go
	//		Files staged:
	//			git-codereview/sync.go
	//		Files unstaged:
	//			git-codereview/submit.go
	//		Files untracked:
	//			git-codereview/doc.go
	//			git-codereview/savedmail.go.txt
	//
	// If there are multiple changes in the current branch, the output splits them out into separate sections,
	// in reverse commit order, to match git log output.
	//
	//	wbshadow 7a524a1..a496c1e (current branch, all mailed, 23 behind)
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
	// In multichange mode, the first line only gives information that applies to the entire
	// branch: the name, the commit range, whether this is the current branch, whether
	// all the commits are mailed/submitted, how far behind.
	// The individual change sections have per-change information: the hash of that
	// commit, the URL on the Gerrit server, whether it is mailed/submitted, the list of
	// files in that commit. The uncommitted file modifications are shown as a separate
	// section, at the beginning, to fit better into the reverse commit order.

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
		if len(work) == 1 && work[0].g.Number != 0 {
			fmt.Fprintf(&buf, " %s/%d", auth.url, work[0].g.Number)
		}
		var tags []string
		if b.current {
			tags = append(tags, "current branch")
		}
		if allMailed(work) {
			if len(work) == 1 {
				tags = append(tags, "mailed")
			} else if len(work) > 1 {
				tags = append(tags, "all mailed")
			}
		}
		if allSubmitted(work) {
			if len(work) == 1 {
				tags = append(tags, "submitted")
			} else if len(work) > 1 {
				tags = append(tags, "all submitted")
			}
		}
		if b.commitsBehind > 0 {
			tags = append(tags, fmt.Sprintf("%d behind", b.commitsBehind))
		}
		if len(tags) > 0 {
			fmt.Fprintf(&buf, " (%s)", strings.Join(tags, ", "))
		}
		fmt.Fprintf(&buf, "\n")
		if text := b.errors(); text != "" {
			fmt.Fprintf(&buf, "\tERROR: %s\n\n", strings.Replace(strings.TrimSpace(text), "\n", "\n\t", -1))
		}

		if b.current && len(work) > 1 && len(b.staged)+len(b.unstaged)+len(b.untracked) > 0 {
			fmt.Fprintf(&buf, "+ uncommitted changes\n")
			printFileList("staged", b.staged)
			printFileList("unstaged", b.unstaged)
			printFileList("untracked", b.untracked)
			fmt.Fprintf(&buf, "\n")
		}

		for _, c := range work {
			g := c.g
			if len(work) > 1 {
				fmt.Fprintf(&buf, "+ %s", c.ShortHash)
				if g.Number != 0 {
					fmt.Fprintf(&buf, " %s/%d", auth.url, g.Number)
				}
				var tags []string
				if g.CurrentRevision == c.Hash {
					tags = append(tags, "mailed")
				}
				if g.Status == "MERGED" {
					tags = append(tags, "submitted")
				}
				if len(tags) > 0 {
					fmt.Fprintf(&buf, " (%s)", strings.Join(tags, ", "))
				}
				fmt.Fprintf(&buf, "\n")
			}
			msg := strings.TrimRight(c.Message, "\r\n")
			fmt.Fprintf(&buf, "\t%s\n", strings.Replace(msg, "\n", "\n\t", -1))
			fmt.Fprintf(&buf, "\n")

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
				fmt.Fprintf(&buf, "\t%s:\n", name)
				for score := maxValue; score >= minValue; score-- {
					who := byScore[score]
					if len(who) == 0 {
						continue
					}
					sort.Strings(who)
					fmt.Fprintf(&buf, "\t\t%+d %s\n", score, strings.Join(who, ", "))
				}
			}

			printFileList("in this change", c.committed)
			if b.current && len(work) == 1 {
				// staged file list will be printed next
			} else {
				fmt.Fprintf(&buf, "\n")
			}
		}
		if b.current && len(work) <= 1 {
			printFileList("staged", b.staged)
			printFileList("unstaged", b.unstaged)
			printFileList("untracked", b.untracked)
			fmt.Fprintf(&buf, "\n")
		}
	}

	stdout().Write(buf.Bytes())
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

// errors returns any errors that should be displayed
// about the state of the current branch, diagnosing common mistakes.
func (b *Branch) errors() string {
	b.loadPending()
	var buf bytes.Buffer
	if !b.IsLocalOnly() && b.commitsAhead > 0 {
		fmt.Fprintf(&buf, "Branch contains %d commit%s not on origin/%s.\n", b.commitsAhead, suffix(b.commitsAhead, "s"), b.Name)
		fmt.Fprintf(&buf, "\tDo not commit directly to %s branch.\n", b.Name)
	}
	return buf.String()
}

// suffix returns an empty string if n == 1, s otherwise.
func suffix(n int, s string) string {
	if n == 1 {
		return ""
	}
	return s
}
