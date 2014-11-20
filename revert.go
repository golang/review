// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"
)

func revert(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: %s [-n] [-v] revert files...\n", os.Args[0])
		os.Exit(2)
	}
	branch := CurrentBranch()
	if branch.Name == "master" {
		dief("on master branch; can't revert.")
	}
	if branch.ChangeID == "" {
		dief("no pending change; can't revert.")
	}
	// TODO(adg): make this work correctly before hooking it up
	run("git", append([]string{"checkout", "HEAD^"}, args...)...)
	run("git", append([]string{"add"}, args...)...)
}
