// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TODO(rsc): Tests

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

var pendingLocal bool // -l flag, use only local operations (no network)

// A pendingBranch collects information about a single pending branch.
// We overlap the reading of this information for each branch.
type pendingBranch struct {
	*Branch                 // standard Branch functionality
	g         *GerritChange // state loaded from Gerrit
	gerr      error         // error loading state from Gerrit
	current   bool          // is this the current branch?
	committed []string      // files committed on this branch
	staged    []string      // files in staging area, only if current==true
	unstaged  []string      // files unstaged in local directory, only if current==true
	untracked []string      // files untracked in local directory, only if current==true
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
	b.committed = getLines("git", "diff", "--name-only", b.parentHash, b.commitHash)
	if !pendingLocal {
		b.g, b.gerr = b.GerritChange("DETAILED_LABELS", "CURRENT_REVISION", "MESSAGES", "DETAILED_ACCOUNTS")
	}
}

func pending(args []string) {
	flags.BoolVar(&pendingLocal, "l", false, "use only local information - no network operations")
	flags.Parse(args)
	if len(flags.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "Usage: %s pending %s [-l]\n", os.Args[0], globalFlags)
		os.Exit(2)
	}

	// Fetch info about remote changes, so that we can say which branches need sync.
	if !pendingLocal {
		run("git", "fetch", "-q")
		http.DefaultClient.Timeout = 5 * time.Second
	}

	// Build list of pendingBranch structs to be filled in.
	current := CurrentBranch().Name
	var branches []*pendingBranch
	for _, b := range LocalBranches() {
		branches = append(branches, &pendingBranch{Branch: b, current: b.Name == current})
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
	//	pending d8fcb99 https://go-review.googlesource.com/1620 (current branch, 1 behind)
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
	//		Files untracked:
	//			git-codereview/doc.go
	//			git-codereview/savedmail.go.txt
	//
	var buf bytes.Buffer
	for _, b := range branches {
		if !b.current && b.commitsAhead == 0 {
			// Hide branches with no work on them.
			continue
		}

		fmt.Fprintf(&buf, "%s", b.Name)
		if b.shortCommitHash != "" {
			fmt.Fprintf(&buf, " %s", b.shortCommitHash)
		}
		if b.g != nil && b.g.Number != 0 {
			fmt.Fprintf(&buf, " %s/%d", auth.url, b.g.Number)
		}
		var tags []string
		if b.current {
			tags = append(tags, "current branch")
		}
		if b.g != nil && b.g.CurrentRevision == b.commitHash {
			tags = append(tags, "mailed")
		}
		if b.g != nil && b.g.Status == "MERGED" {
			tags = append(tags, "submitted")
		}
		if b.commitsBehind > 0 {
			tags = append(tags, fmt.Sprintf("%d behind", b.commitsBehind))
		}
		if len(tags) > 0 {
			fmt.Fprintf(&buf, " (%s)", strings.Join(tags, ", "))
		}
		fmt.Fprintf(&buf, "\n")
		if text := b.errors(); text != "" {
			// TODO(rsc): Test
			fmt.Fprintf(&buf, "\tERROR: %s", strings.Replace(text, "\n", "\n\t", -1))
		}
		if b.message != "" {
			msg := strings.TrimRight(b.message, "\r\n")
			fmt.Fprintf(&buf, "\t%s\n", strings.Replace(msg, "\n", "\n\t", -1))
			fmt.Fprintf(&buf, "\n")
		}
		if b.g != nil {
			for _, name := range b.g.LabelNames() {
				label := b.g.Labels[name]
				minValue := 10000
				maxValue := -10000
				byScore := map[int][]string{}
				for _, x := range label.All {
					// Hide CL owner unless owner score is nonzero.
					if b.g.Owner != nil && x.ID == b.g.Owner.ID && x.Value == 0 {
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
		}
		printFileList := func(name string, list []string) {
			if len(list) == 0 {
				return
			}
			fmt.Fprintf(&buf, "\tFiles %s:\n", name)
			for _, file := range list {
				fmt.Fprintf(&buf, "\t\t%s\n", file)
			}
		}
		printFileList("in this change", b.committed)
		printFileList("staged", b.staged)
		printFileList("unstaged", b.unstaged)
		printFileList("untracked", b.untracked)

		fmt.Fprintf(&buf, "\n")
	}

	os.Stdout.Write(buf.Bytes())
}

// errors returns any errors that should be displayed
// about the state of the current branch, diagnosing common mistakes.
func (b *Branch) errors() string {
	b.loadPending()
	var buf bytes.Buffer
	if !b.IsLocalOnly() && b.commitsAhead > 0 {
		fmt.Fprintf(&buf, "Branch contains %d commit%s not on origin/%s.\n", b.commitsAhead, suffix(b.commitsAhead, "s"), b.Name)
		fmt.Fprintf(&buf, "\tDo not commit directly to %s branch.\n", b.Name)
	} else if b.commitsAhead > 1 {
		fmt.Fprintf(&buf, "Branch contains %d commits not on origin/%s.\n", b.commitsAhead, b.OriginBranch())
		fmt.Fprint(&buf, "\tThere should be at most one.\n")
		fmt.Fprint(&buf, "\tUse 'git change', not 'git commit'.\n")
		fmt.Fprintf(&buf, "\tRun 'git log %s..%s' to list commits.\n", b.OriginBranch(), b.Name)
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
