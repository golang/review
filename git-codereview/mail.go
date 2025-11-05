// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

func cmdMail(args []string) {
	// NOTE: New flags should be added to the usage message below as well as doc.go.
	var (
		rList  = new(stringList) // installed below
		ccList = new(stringList) // installed below

		diff        = flags.Bool("diff", false, "show change commit diff and don't upload or mail")
		force       = flags.Bool("f", false, "mail even if there are staged changes")
		hashtagList = new(stringList) // installed below
		noKeyCheck  = flags.Bool("nokeycheck", false, "set 'git push -o nokeycheck', to prevent Gerrit from checking for private keys")
		topic       = flags.String("topic", "", "set Gerrit topic")
		trybot      = flags.Bool("trybot", false, "run trybots on the uploaded CLs")
		wip         = flags.Bool("wip", false, "set the status of a change to Work-in-Progress")
		noverify    = flags.Bool("no-verify", false, "disable presubmits")
		autoSubmit  = flags.Bool("autosubmit", false, "set autosubmit on the uploaded CLs")
	)
	flags.Var(rList, "r", "comma-separated list of reviewers")
	flags.Var(ccList, "cc", "comma-separated list of people to CC:")
	flags.Var(hashtagList, "hashtag", "comma-separated list of tags to set")

	flags.Usage = func() {
		fmt.Fprintf(stderr(),
			"Usage: %s mail %s [-r reviewer,...] [-cc mail,...]\n"+
				"\t[-autosubmit] [-f] [-diff] [-hashtag tag,...]\n"+
				"\t[-nokeycheck] [-topic topic] [-trybot] [-wip]\n"+
				"\t[commit]\n", progName, globalFlags)
		exit(2)
	}
	flags.Parse(args)
	if len(flags.Args()) > 1 {
		flags.Usage()
		exit(2)
	}

	var trybotVotes []string
	switch os.Getenv("GIT_CODEREVIEW_TRYBOT") {
	case "", "luci":
		trybotVotes = []string{"Commit-Queue+1"}
	case "farmer":
		trybotVotes = []string{"Run-TryBot"}
	case "both":
		trybotVotes = []string{"Commit-Queue+1", "Run-TryBot"}
	default:
		fmt.Fprintf(stderr(), "GIT_CODEREVIEW_TRYBOT must be unset, blank, or one of 'luci', 'farmer', or 'both'\n")
		exit(2)
	}

	b := CurrentBranch()

	var c *Commit
	if len(flags.Args()) == 1 {
		c = b.CommitByRev("mail", flags.Arg(0))
	} else {
		c = b.DefaultCommit("mail", "must specify commit on command line; use HEAD to mail all pending changes")
	}

	if *diff {
		run("git", "diff", b.Branchpoint()[:7]+".."+c.ShortHash, "--")
		return
	}

	if len(ListFiles(c)) == 0 && len(c.Parents) == 1 {
		dief("cannot mail: commit %s is empty", c.ShortHash)
	}

	foundCommit := false
	for _, c1 := range b.Pending() {
		if c1 == c {
			foundCommit = true
		}
		if !foundCommit {
			continue
		}
		if strings.Contains(strings.ToLower(c1.Message), "do not mail") {
			dief("%s: CL says DO NOT MAIL", c1.ShortHash)
		}
		if strings.HasPrefix(c1.Message, "fixup!") {
			dief("%s: CL is a fixup! commit", c1.ShortHash)
		}
		if strings.HasPrefix(c1.Message, "squash!") {
			dief("%s: CL is a squash! commit", c1.ShortHash)
		}

		for _, f := range ListFiles(c1) {
			if strings.HasPrefix(f, ".#") || strings.HasSuffix(f, "~") ||
				(strings.HasPrefix(f, "#") && strings.HasSuffix(f, "#")) {
				dief("cannot mail temporary files: %s", f)
			}
		}
	}
	if !foundCommit {
		// b.CommitByRev and b.DefaultCommit both return a commit on b.
		dief("internal error: did not find chosen commit on current branch")
	}

	if !*force && HasStagedChanges() {
		dief("there are staged changes; aborting.\n"+
			"Use '%s change' to include them or '%s mail -f' to force it.", progName, progName)
	}

	if !utf8.ValidString(c.Message) {
		dief("cannot mail message with invalid UTF-8")
	}
	for _, r := range c.Message {
		if !unicode.IsPrint(r) && !unicode.IsSpace(r) {
			dief("cannot mail message with non-printable rune %q", r)
		}
	}

	// for side effect of dying with a good message if origin is GitHub
	loadGerritOrigin()

	refSpec := b.PushSpec(c)
	start := "%"
	if *rList != "" {
		refSpec += mailList(start, "r", string(*rList))
		start = ","
	}
	if *ccList != "" {
		refSpec += mailList(start, "cc", string(*ccList))
		start = ","
	}
	if *hashtagList != "" {
		for _, tag := range strings.Split(string(*hashtagList), ",") {
			if tag == "" {
				dief("hashtag may not contain empty tags")
			}
			refSpec += start + "hashtag=" + tag
			start = ","
		}
	}
	if *topic != "" {
		// There's no way to escape the topic, but the only
		// ambiguous character is ',' (though other characters
		// like ' ' will be rejected outright by git).
		if strings.Contains(*topic, ",") {
			dief("topic may not contain a comma")
		}
		refSpec += start + "topic=" + *topic
		start = ","
	}
	if *trybot {
		for _, v := range trybotVotes {
			refSpec += start + "l=" + v
			start = ","
		}
	}
	if *wip {
		refSpec += start + "wip"
		start = ","
	}
	if *autoSubmit {
		refSpec += start + "l=Auto-Submit"
	}
	args = []string{"push", "-q"}
	if *noKeyCheck {
		args = append(args, "-o", "nokeycheck")
	}
	if *noverify {
		args = append(args, "--no-verify")
	}
	args = append(args, "origin", refSpec)
	run("git", args...)

	// Create local tag for mailed change.
	// If in the 'work' branch, this creates or updates work.mailed.
	// Older mailings are in the reflog, so work.mailed is newest,
	// work.mailed@{1} is the one before that, work.mailed@{2} before that,
	// and so on.
	// Git doesn't actually have a concept of a local tag,
	// but Gerrit won't let people push tags to it, so the tag
	// can't propagate out of the local client into the official repo.
	// There is no conflict with the branch names people are using
	// for work, because git change rejects any name containing a dot.
	// The space of names with dots is ours (the Go team's) to define.
	run("git", "tag", "--no-sign", "-f", b.Name+".mailed", c.ShortHash)
}

// PushSpec returns the spec for a Gerrit push command to publish the change c in b.
// If c is nil, PushSpec returns a spec for pushing all changes in b.
func (b *Branch) PushSpec(c *Commit) string {
	local := "HEAD"
	if c != nil && (len(b.Pending()) == 0 || b.Pending()[0].Hash != c.Hash) {
		local = c.ShortHash
	}
	return local + ":refs/for/" + strings.TrimPrefix(b.OriginBranch(), "origin/")
}

// mailAddressRE matches the mail addresses we admit. It's restrictive but admits
// all the addresses in the Go CONTRIBUTORS file at time of writing (tested separately).
var mailAddressRE = regexp.MustCompile(`^([a-zA-Z0-9][-_.a-zA-Z0-9]*)(@[-_.a-zA-Z0-9]+)?$`)

// mailList turns the list of mail addresses from the flag value into the format
// expected by gerrit. The start argument is a % or , depending on where we
// are in the processing sequence.
func mailList(start, tag string, flagList string) string {
	errors := false
	spec := start
	short := ""
	long := ""
	for i, addr := range strings.Split(flagList, ",") {
		m := mailAddressRE.FindStringSubmatch(addr)
		if m == nil {
			printf("invalid reviewer mail address: %s", addr)
			errors = true
			continue
		}
		if m[2] == "" {
			email := mailLookup(addr)
			if email == "" {
				printf("unknown reviewer: %s", addr)
				errors = true
				continue
			}
			short += "," + addr
			long += "," + email
			addr = email
		}
		if i > 0 {
			spec += ","
		}
		spec += tag + "=" + addr
	}
	if short != "" {
		verbosef("expanded %s to %s", short[1:], long[1:])
	}
	if errors {
		exit(1)
	}
	return spec
}

// reviewers is the list of reviewers for the current repository,
// sorted by how many reviews each has done.
var reviewers []reviewer

type reviewer struct {
	addr  string
	count int
}

// mailLookup translates the short name (like adg) into a full
// email address (like adg@golang.org).
// It returns "" if no translation is found.
// The algorithm for expanding short user names is as follows:
// Look at the git commit log for the current repository,
// extracting all the email addresses in Reviewed-By lines
// and sorting by how many times each address appears.
// For each short user name, walk the list, most common
// address first, and use the first address found that has
// the short user name on the left side of the @.
func mailLookup(short string) string {
	loadReviewers()

	short += "@"
	for _, r := range reviewers {
		if strings.HasPrefix(r.addr, short) && !shortOptOut[r.addr] {
			return r.addr
		}
	}
	return ""
}

// shortOptOut lists email addresses whose owners have opted out
// from consideration for purposes of expanding short user names.
var shortOptOut = map[string]bool{
	"dmitshur@google.com": true, // My @golang.org is primary; @google.com is used for +1 only.
	"matloob@google.com":  true, // My @golang.org is primary; @google.com is used for +1 only.
}

// loadReviewers reads the reviewer list from the current git repo
// and leaves it in the global variable reviewers.
// See the comment on mailLookup for a description of how the
// list is generated and used.
func loadReviewers() {
	if reviewers != nil {
		return
	}
	countByAddr := map[string]int{}
	for _, line := range nonBlankLines(cmdOutput("git", "log", "--format=format:%B", "-n", "1000")) {
		if strings.HasPrefix(line, "Reviewed-by:") {
			f := strings.Fields(line)
			addr := f[len(f)-1]
			if strings.HasPrefix(addr, "<") && strings.Contains(addr, "@") && strings.HasSuffix(addr, ">") {
				countByAddr[addr[1:len(addr)-1]]++
			}
		}
	}

	reviewers = []reviewer{}
	for addr, count := range countByAddr {
		reviewers = append(reviewers, reviewer{addr, count})
	}
	sort.Sort(reviewersByCount(reviewers))
}

type reviewersByCount []reviewer

func (x reviewersByCount) Len() int      { return len(x) }
func (x reviewersByCount) Swap(i, j int) { x[i], x[j] = x[j], x[i] }
func (x reviewersByCount) Less(i, j int) bool {
	if x[i].count != x[j].count {
		return x[i].count > x[j].count
	}
	return x[i].addr < x[j].addr
}

// stringList is a flag.Value that is like flag.String, but if repeated
// keeps appending to the old value, inserting commas as separators.
// This allows people to write -r rsc,adg (like the old hg command)
// but also -r rsc -r adg (like standard git commands).
// This does change the meaning of -r rsc -r adg (it used to mean just adg).
type stringList string

func (x *stringList) String() string {
	return string(*x)
}

func (x *stringList) Set(s string) error {
	if *x != "" && s != "" {
		*x += ","
	}
	*x += stringList(s)
	return nil
}
