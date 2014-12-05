// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "fmt"

func pending(args []string) {
	expectZeroArgs(args, "pending")
	// TODO(adg): implement -r

	current := currentBranchName()
	for _, branch := range localBranches() {
		p := "  "
		if branch == current {
			p = "* "
		}
		pending := hasPendingCommit(branch)
		if branch == current || pending {
			sub := "(no pending change)"
			if pending {
				sub = commitSubject(branch)
			}
			fmt.Printf("%v%v: %v\n", p, branch, sub)
		}
	}
}
