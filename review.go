// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TODO(adg): accept -a flag on 'create' and 'commit' (like git commit -a)
// TODO(adg): accept -r flag on 'upload' to nominate reviewer
// TODO(adg): support 'create' from non-master branches
// TODO(adg): check style of commit message
// TODO(adg): write doc comment
// TOOD(adg): print gerrit votes on 'pending'
// TODO(adg): add gofmt commit hook
// TODO(adg): 'upload' warn about uncommitted changes (maybe commit/create too?)

package main // import "golang.org/x/review"

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

var (
	hookFile = filepath.FromSlash(".git/hooks/commit-msg")
	verbose  = flag.Bool("v", false, "verbose output")
	noRun    = flag.Bool("n", false, "print but do not run commands")
)

const usage = `Usage: %s [-n] [-v] <command>
Type "%s help" for more information.
`

const help = `Usage: %s [-n] [-v] <command>

The review command is a wrapper for the git command that provides a simple
interface to the "single-commit feature branch" development model.

Available comands:

	create <name>
		Create a local branch with the provided name
		and commit the staged changes to it.

	commit
		Amend local branch HEAD commit with the staged changes.

	diff
		View differences between remote branch HEAD and
		the local branch HEAD.
		(The differences introduced by this change.)

	upload
		Upload HEAD commit to the code review server.

	sync
		Fetch changes from the remote repository and merge them to the
		current branch, rebasing the HEAD commit (if any) on top of
		them. If the HEAD commit has been submitted, switch back to the
		master branch and delete the feature branch.

	pending
		Show local branches and their head commits.

`

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, usage, os.Args[0], os.Args[0])
		os.Exit(2)
	}
	flag.Parse()

	goToRepoRoot()
	installHook()

	command, args := flag.Arg(0), flag.Args()[1:]

	switch command {
	case "help":
		fmt.Fprintf(os.Stdout, help, os.Args[0])
	case "create", "cr":
		create(args)
	case "commit", "co":
		commit(args)
	case "diff", "d":
		diff(args)
	case "upload", "u":
		upload(args)
	case "sync", "s":
		doSync(args)
	case "pending", "p":
		pending(args)
	default:
		flag.Usage()
	}
}

func create(args []string) {
	if len(args) != 1 || args[0] == "" {
		flag.Usage()
	}
	name := args[0]
	if !hasStagedChanges() {
		dief("no staged changes.\nDid you forget to 'git add'?")
	}
	if b := currentBranch(); b != "master" {
		dief("must run 'create' from the master branch (now on %q).\n"+
			"(Try 'review sync' or 'git checkout master' first.)",
			b)
	}
	run("git", "checkout", "-q", "-b", name)
	if err := runErr("git", "commit", "-q"); err != nil {
		verbosef("Commit failed: %v\nSwitching back to master.\n", err)
		run("git", "checkout", "-q", "master")
		run("git", "branch", "-q", "-d", name)
	}
}

func commit(args []string) {
	if len(args) != 0 {
		flag.Usage()
	}
	if !hasStagedChanges() {
		dief("no staged changes. Did you forget to 'git add'?")
	}
	if currentBranch() == "master" {
		dief("can't commit to master branch.")
	}
	run("git", "commit", "-q", "--amend", "-C", "HEAD")
}

func diff(args []string) {
	if len(args) != 0 {
		flag.Usage()
	}
	run("git", "diff", "HEAD^", "HEAD")
}

func upload(args []string) {
	if len(args) != 0 {
		flag.Usage()
	}
	if currentBranch() == "master" {
		dief("can't upload from master branch.")
	}
	run("git", "push", "-q", "origin", "HEAD:refs/for/master")
}

func doSync(args []string) {
	if len(args) != 0 {
		flag.Usage()
	}
	run("git", "fetch", "-q")

	// If we're on master, just fast-forward.
	branch := currentBranch()
	if branch == "master" {
		run("git", "merge", "-q", "--ff-only", "origin/master")
		return
	}

	// Check that exactly this commit was submitted to master. If so,
	// switch back to master, fast-forward, delete the feature branch.
	if branchContains("origin/master", "HEAD") {
		run("git", "checkout", "-q", "master")
		run("git", "merge", "-q", "--ff-only", "origin/master")
		run("git", "branch", "-q", "-d", branch)
		fmt.Printf("Change on %q submitted; branch deleted.\n"+
			"On master branch.\n", branch)
		return
	}

	// Check whether a rebased version of this commit was submitted to
	// master. If so, switch back to master, fast-forward, and
	// provide instructions for deleting the feature branch.
	// (We're not 100% sure that the feature branch HEAD was submitted,
	// so be cautious.)
	if headSubmitted(branch) {
		run("git", "checkout", "-q", "master")
		run("git", "merge", "-q", "--ff-only", "origin/master")
		fmt.Fprintf(os.Stderr,
			"I think the change on %q has been submitted.\n"+
				"If you agree, and no longer need branch %q, "+
				"run:\n\tgit branch -D %v\n"+
				"On master branch.\n",
			branch, branch, branch)
		return
	}

	// Bump master HEAD to that of origin/master, just in case the user
	// switches back to master with "git checkout master" later.
	// TODO(adg): maybe we shouldn't do this at all?
	if !branchContains("origin/master", "master") {
		run("git", "branch", "-f", "master", "origin/master")
	}

	// We have un-submitted changes on this feature branch; rebase.
	run("git", "rebase", "-q", "origin/master")
}

func pending(args []string) {
	if len(args) != 0 {
		flag.Usage()
	}
	var (
		wg      sync.WaitGroup
		origin  = originURL()
		current = currentBranch()
	)
	if current == "master" {
		fmt.Println("On master branch.")
	}
	for _, branch := range localBranches() {
		if branch == "master" {
			continue
		}
		wg.Add(1)
		go func(branch string) {
			defer wg.Done()
			p := ""
			if branch == current {
				p = "* "
			}
			id := headChangeId(branch)
			c, err := getChange(origin, id)
			switch err {
			case notFound:
				// TODO(adg): read local commit msg
				var msg string
				fmt.Printf("%v%v:\n\t%v\n\t(not uploaded)\n",
					p, branch, msg)
			case nil:
				status := ""
				switch c.Status {
				case "MERGED":
					status += " [submitted]"
				case "ABANDONED":
					status += " [abandoned]"
				}
				fmt.Printf("%v%v%v:\n\t%v\n\t%v\n",
					p, branch, status, c.Subject, c.URL)
			default:
				fmt.Fprintf(os.Stderr, "fetching change for %q: %v\n", branch, err)
			}
		}(branch)
	}
	wg.Wait()
}

func originURL() string {
	out, err := exec.Command("git", "config", "remote.origin.url").CombinedOutput()
	if err != nil {
		dief("could not find URL for 'origin' remote.\n"+
			"Did you check out from the right place?\n"+
			"git config remote.origin.url: %v\n"+
			"%s", err, out)
	}
	return string(out)
}

func localBranches() (branches []string) {
	for _, s := range getLines("git", "branch", "-l", "-q") {
		branches = append(branches, strings.TrimPrefix(s, "* "))
	}
	return branches
}

func branchContains(branch, rev string) bool {
	for _, s := range getLines("git", "branch", "-r", "--contains", rev) {
		if s == branch {
			return true
		}
	}
	return false
}

var stagedRe = regexp.MustCompile(`^[ACDMR]  `)

func hasStagedChanges() bool {
	for _, s := range getLines("git", "status", "-b", "--porcelain") {
		if stagedRe.MatchString(s) {
			return true
		}
	}
	return false
}

func currentBranch() string {
	return strings.TrimSpace(getOutput("git", "rev-parse", "--abbrev-ref", "HEAD"))
}

func headSubmitted(branch string) bool {
	s := "Change-Id: " + headChangeId(branch)
	return len(getOutput("git", "log", "--grep", s, "origin/master")) > 0
}

func headChangeId(branch string) string {
	const (
		p = "Change-Id: "
		f = "--format=format:%b"
	)
	for _, s := range getLines("git", "log", "-n", "1", f, branch, "--") {
		if strings.HasPrefix(s, p) {
			return strings.TrimSpace(strings.TrimPrefix(s, p))
		}
	}
	dief("no Change-Id line found in HEAD commit on branch %s.", branch)
	panic("unreachable")
}

func goToRepoRoot() {
	prevDir, err := os.Getwd()
	if err != nil {
		dief("could not get current directory: %v", err)
	}
	for {
		if _, err := os.Stat(".git"); err == nil {
			return
		}
		if err := os.Chdir(".."); err != nil {
			dief("could not chdir: %v", err)
		}
		currentDir, err := os.Getwd()
		if err != nil {
			dief("could not get current directory: %v", err)
		}
		if currentDir == prevDir {
			dief("git root not found.\n" +
				"Run from within the Git tree please.")
		}
		prevDir = currentDir
	}
}

func installHook() {
	_, err := os.Stat(hookFile)
	if err == nil {
		return
	}
	if !os.IsNotExist(err) {
		dief("error checking for hook file: %v", err)
	}
	verbosef("Presubmit hook to add Change-Id to commit messages is missing.\n"+
		"Automatically creating it at %v.\n", hookFile)
	hookContent := []byte(commitMsgHook)
	if err := ioutil.WriteFile(hookFile, hookContent, 0700); err != nil {
		dief("error writing hook file: %v", err)
	}
}

func run(command string, args ...string) {
	if err := runErr(command, args...); err != nil {
		if !*verbose {
			// If we're not in verbose mode, print the command
			// before dying to give context to the failure.
			fmt.Fprintln(os.Stderr, commandString(command, args))
		}
		dief("%v", err)
	}
}

func runErr(command string, args ...string) error {
	if *verbose || *noRun {
		fmt.Fprintln(os.Stderr, commandString(command, args))
	}
	if *noRun {
		return nil
	}
	cmd := exec.Command(command, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// getOutput runs the specified command and returns its combined standard
// output and standard error outputs.
// NOTE: It should only be used to run commands that return information,
// **not** commands that make any actual changes.
func getOutput(command string, args ...string) string {
	b, err := exec.Command(command, args...).CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n%s\n", commandString(command, args), b)
		dief("%v", err)
	}
	return string(b)
}

// getLines is like getOutput but it returns non-empty output lines.
// NOTE: It should only be used to run commands that return information,
// **not** commands that make any actual changes.
func getLines(command string, args ...string) []string {
	var s []string
	for _, l := range strings.Split(getOutput(command, args...), "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			s = append(s, l)
		}
	}
	return s
}

func commandString(command string, args []string) string {
	return strings.Join(append([]string{command}, args...), " ")
}

func dief(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "review: "+format+"\n", args...)
	os.Exit(1)
}

func verbosef(format string, args ...interface{}) {
	if *verbose {
		fmt.Fprintf(os.Stderr, format, args...)
	}
}
