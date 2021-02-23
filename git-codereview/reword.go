// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

func cmdReword(args []string) {
	flags.Usage = func() {
		fmt.Fprintf(stderr(), "Usage: %s reword %s [commit...]\n",
			progName, globalFlags)
		exit(2)
	}
	flags.Parse(args)
	args = flags.Args()

	// Check that we understand the structure
	// before we let the user spend time editing messages.
	b := CurrentBranch()
	pending := b.Pending()
	if len(pending) == 0 {
		dief("reword: no commits pending")
	}
	if b.Name == "HEAD" {
		dief("reword: no current branch")
	}
	var last *Commit
	for i := len(pending) - 1; i >= 0; i-- {
		c := pending[i]
		if last != nil && !c.HasParent(last.Hash) {
			dief("internal error: confused about pending commit graph: parent %.7s vs %.7s", last.Hash, c.Parents)
		}
		last = c
	}

	headState := func() (head, branch string) {
		head = trim(cmdOutput("git", "rev-parse", "HEAD"))
		for _, line := range nonBlankLines(cmdOutput("git", "branch", "-l")) {
			if strings.HasPrefix(line, "* ") {
				branch = trim(line[1:])
				return head, branch
			}
		}
		dief("internal error: cannot find current branch")
		panic("unreachable")
	}

	head, branch := headState()
	if head != last.Hash {
		dief("internal error: confused about pending commit graph: HEAD vs parent: %.7s vs %.7s", head, last.Hash)
	}
	if branch != b.Name {
		dief("internal error: confused about pending commit graph: branch name %s vs %s", branch, b.Name)
	}

	// Build list of commits to be reworded.
	// Do first, in case there are typos on the command line.
	var cs []*Commit
	newMsg := make(map[*Commit]string)
	if len(args) == 0 {
		for _, c := range pending {
			cs = append(cs, c)
		}
	} else {
		for _, arg := range args {
			c := b.CommitByRev("reword", arg)
			cs = append(cs, c)
		}
	}
	for _, c := range cs {
		newMsg[c] = ""
	}

	// Invoke editor to reword all the messages message.
	// Save the edits to REWORD_MSGS immediately after editor exit
	// in case we for some reason cannot apply the changes - don't want
	// to throw away the user's writing.
	// But we don't use REWORD_MSGS as the actual editor file,
	// because if there are multiple git rewords happening
	// (perhaps the user has forgotten about one in another window),
	// we don't want them to step on each other during editing.
	var buf bytes.Buffer
	saveFile := filepath.Join(gitPathDir(), "REWORD_MSGS")
	saveBuf := func() {
		if err := ioutil.WriteFile(saveFile, buf.Bytes(), 0666); err != nil {
			dief("cannot save messages: %v", err)
		}
	}
	saveBuf() // make sure it works before we let the user edit anything
	printf("editing messages (new texts logged in %s in case of failure)", saveFile)
	note := "edited messages saved in " + saveFile

	if len(cs) == 1 {
		c := cs[0]
		edited := editor(c.Message)
		if edited == "" {
			dief("edited message is empty")
		}
		newMsg[c] = string(fixCommitMessage([]byte(edited)))
		fmt.Fprintf(&buf, "# %s\n\n%s\n\n", c.Subject, edited)
		saveBuf()
	} else {
		// Edit all at once.
		var ed bytes.Buffer
		ed.WriteString(rewordProlog)
		byHash := make(map[string]*Commit)
		for _, c := range cs {
			if strings.HasPrefix(c.Message, "# ") || strings.Contains(c.Message, "\n# ") {
				// Will break our framing.
				// Should be pretty rare since 'git commit' and 'git commit --amend'
				// delete lines beginning with # after editing sessions.
				dief("commit %.7s has a message line beginning with # - cannot reword with other commits", c.Hash)
			}
			hash := c.Hash[:7]
			byHash[hash] = c
			// Two blank lines before #, one after.
			// Lots of space to make it easier to see the boundaries
			// between commit messages.
			fmt.Fprintf(&ed, "\n\n# %s %s\n\n%s\n", hash, c.Subject, c.Message)
		}
		edited := editor(ed.String())
		if edited == "" {
			dief("edited text is empty")
		}

		// Save buffer for user before going further.
		buf.WriteString(edited)
		saveBuf()

		for i, text := range strings.Split("\n"+edited, "\n# ") {
			if i == 0 {
				continue
			}
			text = "# " + text // restore split separator

			// Pull out # hash header line and body.
			hdr, body, _ := cut(text, "\n")

			// Cut blank lines at start and end of body but keep newline-terminated.
			for body != "" {
				line, rest, _ := cut(body, "\n")
				if line != "" {
					break
				}
				body = rest
			}
			body = strings.TrimRight(body, " \t\n")
			if body != "" {
				body += "\n"
			}

			// Look up hash.
			f := strings.Fields(hdr)
			if len(f) < 2 {
				dief("edited text has # line with no commit hash\n%s", note)
			}
			c := byHash[f[1]]
			if c == nil {
				dief("cannot find commit for header: %s\n%s", strings.TrimSpace(hdr), note)
			}
			newMsg[c] = string(fixCommitMessage([]byte(body)))
		}
	}

	// Rebuild the commits the way git would,
	// but without doing any git checkout that
	// would affect the files in the working directory.
	var newHash string
	last = nil
	for i := len(pending) - 1; i >= 0; i-- {
		c := pending[i]
		if (newMsg[c] == "" || newMsg[c] == c.Message) && newHash == "" {
			// Have not started making changes yet. Leave exactly as is.
			last = c
			continue
		}
		// Rebuilding.
		msg := newMsg[c]
		if msg == "" {
			msg = c.Message
		}
		if last != nil && newHash != "" && !c.HasParent(last.Hash) {
			dief("internal error: confused about pending commit graph")
		}
		gitArgs := []string{"commit-tree", "-p"}
		for _, p := range c.Parents {
			if last != nil && newHash != "" && p == last.Hash {
				p = newHash
			}
			gitArgs = append(gitArgs, p)
		}
		gitArgs = append(gitArgs, "-m", msg, c.Tree)
		os.Setenv("GIT_AUTHOR_NAME", c.AuthorName)
		os.Setenv("GIT_AUTHOR_EMAIL", c.AuthorEmail)
		os.Setenv("GIT_AUTHOR_DATE", c.AuthorDate)
		newHash = trim(cmdOutput("git", gitArgs...))
		last = c
	}
	if newHash == "" {
		// No messages changed.
		return
	}

	// Attempt swap of HEAD but leave index and working copy alone.
	// No obvious way to make it atomic, but check for races.
	head, branch = headState()
	if head != pending[0].Hash {
		dief("cannot reword: commits changed underfoot\n%s", note)
	}
	if branch != b.Name {
		dief("cannot reword: branch changed underfoot\n%s", note)
	}
	run("git", "reset", "--soft", newHash)
}

func cut(s, sep string) (before, after string, ok bool) {
	i := strings.Index(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

var rewordProlog = `Rewording multiple commit messages.
The # lines separate the different commits and must be left unchanged.
`
