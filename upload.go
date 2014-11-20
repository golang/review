// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
)

var uploadFlags struct {
	flag.FlagSet
	diff   bool
	force  bool
	rList  string
	ccList string
}

func init() {
	f := &uploadFlags
	f.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [-n] [-v] upload [-r reviewer,...] [-cc mail,...]\n", os.Args[0])
	}
	f.BoolVar(&f.diff, "diff", false, "show change commit diff and don't upload")
	f.BoolVar(&f.force, "f", false, "upload even if there are staged changes")
	f.StringVar(&f.rList, "r", "", "comma-separated list of reviewers")
	f.StringVar(&f.ccList, "cc", "", "comma-separated list of people to CC:")

}

func upload(args []string) {
	f := &uploadFlags
	if f.Parse(args) != nil || len(f.Args()) != 0 {
		f.Usage()
		os.Exit(2)
	}

	branch := CurrentBranch()
	if branch.Name == "master" {
		dief("on master branch; can't upload.")
	}
	if branch.ChangeID == "" {
		dief("no pending change; can't upload.")
	}

	if f.diff {
		run("git", "diff", "master..HEAD")
		return
	}

	if !f.force && hasStagedChanges() {
		dief("there are staged changes; aborting.\n" +
			"Use 'review change' to include them or 'review upload -f' to force upload.")
	}

	refSpec := "HEAD:refs/for/master"
	start := "%"
	if f.rList != "" {
		refSpec += mailList(start, "r", f.rList)
		start = ","
	}
	if f.ccList != "" {
		refSpec += mailList(start, "cc", f.ccList)
	}
	run("git", "push", "-q", "origin", refSpec)
}

// mailAddressRE matches the mail addresses we admit. It's restrictive but admits
// all the addresses in the Go CONTRIBUTORS file at time of writing (tested separately).
var mailAddressRE = regexp.MustCompile(`^[a-zA-Z0-9][-_.a-zA-Z0-9]*@[-_.a-zA-Z0-9]+$`)

// mailList turns the list of mail addresses from the flag value into the format
// expected by gerrit. The start argument is a % or , depending on where we
// are in the processing sequence.
func mailList(start, tag string, flagList string) string {
	spec := start
	for i, addr := range strings.Split(flagList, ",") {
		if !mailAddressRE.MatchString(addr) {
			dief("%q is not a valid reviewer mail address", addr)
		}
		if i > 0 {
			spec += ","
		}
		spec += tag + "=" + addr
	}
	return spec
}
