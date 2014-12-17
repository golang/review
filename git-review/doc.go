// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Git-review manages the code review process for Git changes using a Gerrit
server.

The git-review tool manages "change branches" in the local git repository.
Each such branch tracks a single commit, or "pending change",
that is reviewed using a Gerrit server.
Modifications to the pending change are applied by amending the commit.
This process implements the "single-commit feature branch" model.

Once installed as git-review, the tool's commands are available through git
either by running

	git review <command>

or, if aliases are installed, as

	git <command>

The review tool's command names do not conflict with any extant git commands.
This document uses the first form for clarity but most users install these
aliases in their .gitconfig file:

	[alias]
		change = review change
		gofmt = review gofmt
		mail = review mail
		pending = review pending
		submit = review submit
		sync = review sync

The commands are:

change

The change command creates and moves between Git branches and maintains the
pending commits on work branches.

	git review change [branchname]

Given a branch name as an argument, the change command switches to the named
branch, creating it if necessary. If the branch is created and there are staged
changes, it will commit the changes to the branch, creating a new pending
change.

With no argument, the change command creates a new pending change from the
staged changes in the current branch or, if there is already a pending change,
amends that change.

TODO: document -q, -a

gofmt

The gofmt command applies the gofmt program to all files modified on the
current work branch (TODO: is this right?).

	git review gofmt [-a] [-c]

By default, the gofmt command leaves any formatting changes unstaged.

The -a option (TODO).

The -c option (TODO).

help

The help command displays basic usage instructions.

	git review help

hooks

The hooks command installs the Git hooks to enforce code review conventions.

	git review hooks

The pre-commit hook checks that all Go code is formatted with gofmt and that
the commit is not being made directly to the master branch.

The commit-msg hook adds the Gerrit "Change-Id" line to the commit message if
not present. It also checks that the message uses the convention established by
the Go project that the first line has the form, pkg/path: summary.

hook-invoke

The hook-invoke command is an internal command that invokes the named Git hook.

	git review hook-invoke <hook>

It is run by the shell scripts installed by the "git review hooks" command.

mail

The mail command starts the code review process for the pending change.

	git mail [-f] [-r email] [-cc email]

It pushes the pending change commit in the current branch to the Gerrit code
review server and prints the URL for the change on the server.
If the change already exists on the server, the mail command updates that
change with a new changeset.

The -r and -cc flags identify the email addresses of people to do the code
review and to be CC'ed about the code review.
Multiple addresses are given as a comma-separated list.

The mail command fails if there are staged changes that are not committed. The
-f flag overrides this behavior.

pending

The pending command prints to standard output the status of all pending changes
and unstaged files in the local repository.

	git review [-l] pending

The -l flag causes the command to use only locally available information.
By default, it contacts the Gerrit server for code review information.

submit

The submit command pushes the pending change to the Gerrit server and tells
Gerrit to submit it to the master branch.

	git review submit

The command fails if there are modified files (staged or unstaged) that are not
part of the pending change.

After submitting the change, the change command tries to synchronize the
current branch to the submitted commit, if it can do so cleanly.
If not, it will prompt the user to run 'git review sync' manually.

sync

The sync command updates the local repository.

	git review sync

It fetches changes from the remote repository and merges changes from the
upstream branch to the current branch, rebasing the pending change, if any,
onto those changes.

*/
package main
