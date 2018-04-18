// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Git-codereview manages the code review process for Git changes using a Gerrit
server.

The git-codereview tool manages ``change branches'' in the local git repository.
Each such branch tracks a single commit, or ``pending change'',
that is reviewed using a Gerrit server; the Gerrit remote must be
named ``origin'' in the local git repo.

Modifications to the pending change are applied by amending the commit.
This process implements the ``single-commit feature branch'' model.
Creating multiple-commit feature branches, for example to break a large
change into a reviewable sequence, is also supported; see the discussion below.

Once installed as git-codereview, the tool's commands are available through git
either by running

	git codereview <command>

or, if aliases are installed, as

	git <command>

The review tool's command names do not conflict with any extant git commands.
This document uses the first form for clarity but most users install these
aliases in their .gitconfig file:

	[alias]
		change = codereview change
		gofmt = codereview gofmt
		mail = codereview mail
		pending = codereview pending
		rebase-work = codereview rebase-work
		submit = codereview submit
		sync = codereview sync

Single-Commit Work Branches

For simple, unrelated changes, the typical usage of the git-codereview tool
is to place each pending change in its own Git branch.
In this workflow, the work branch contains
either no pending change beyond origin/master (when there's no local work)
or exactly one pending change beyond origin/master (the change being developed).

When there is no pending change on the work branch,
``git codereview change'' creates one by running ``git commit''.
Otherwise, when there is already a pending change,
``git codereview change'' revises it by running ``git commit --amend''.

The ``git codereview mail'' and ``git codereview submit'' commands
implicitly operate on the lone pending change.

Multiple-Commit Work Branches

Of course, it is not always feasible to put each pending change in a separate branch.
A sequence of changes that build on one another is more easily
managed as multiple commits on a single branch, and the git-codereview tool
supports this workflow as well.
To add a new pending change, invoke ``git commit'' directly,
instead of ``git codereview change''.
The git-codereview tool adjusts its behavior when there are
multiple pending changes.

The ``git codereview change'' command amends the top commit in the stack (HEAD).
To amend a commit further down the stack, use Git's rebase support,
for example by using ``git commit --fixup'' followed by ``git codereview rebase-work''.

The ``git codereview mail'' command requires an explicit revision argument,
but note that since ``git codereview mail'' is implemented as a ``git push'',
any commits earlier in the stack are necessarily also mailed.

The ``git codereview submit'' command also requires an explicit revision argument,
and while earlier commits are necessarily still uploaded and mailed,
only the named revision or revisions are submitted (merged into origin/master).
In a single-commit work branch, a successful ``git codereview submit''
effectively runs ``git codereview sync'' automatically.
In a multiple-commit work branch, it does not, because
the implied ``git rebase'' may conflict with the remaining pending commits.
Instead it is necessary to run ``git codereview sync'' explicitly
(when ready) after ``git codereview submit''.

Reusing Work Branches

Although one common practice is to create a new branch for each pending change,
running ``git codereview submit'' (and possibly ``git codereview sync'')
leaves the current branch ready for reuse with a future change.
Some developers find it helpful to create a single work branch
(``git change work'') and then do all work in that branch,
possibly in the multiple-commit mode, never changing between branches.

Command Details

All commands accept these global flags:

The -v flag prints all commands that make changes. Multiple occurrences
trigger more verbosity in some commands, including sync.

The -n flag prints all commands that would be run, but does not run them.

Descriptions of each command follow.

Branchpoint

The branchpoint command prints the commit hash of the most recent commit
on the current branch that is shared with the Gerrit server.

	git codereview branchpoint

This commit is the point where local work branched from the published tree.
The command is intended mainly for use in scripts. For example,
``git diff $(git codereview branchpoint)'' or
``git log $(git codereview branchpoint)..HEAD''.

Change

The change command creates and moves between Git branches and maintains the
pending changes on work branches.

	git codereview change [-a] [-q] [-m <message>] [branchname]

Given a branch name as an argument, the change command switches to the named
branch, creating it if necessary. If the branch is created and there are staged
changes, it will commit the changes to the branch, creating a new pending
change.

With no argument, the change command creates a new pending change from the
staged changes in the current branch or, if there is already a pending change,
amends that change.

The -q option skips the editing of an extant pending change's commit message.
If -m is present, -q is ignored.

The -a option automatically adds any unstaged edits in tracked files during
commit; it is equivalent to the 'git commit' -a option.

The -m option specifies a commit message and skips the editor prompt. This
option is only useful when creating commits (e.g. if there are unstaged
changes). If a commit already exists, it is overwritten. If -q is also
present, -q will be ignored.

Gofmt

The gofmt command applies the gofmt program to all files modified in the
current work branch, both in the staging area (index) and the working tree
(local directory).

	git codereview gofmt [-l]

The -l option causes the command to list the files that need reformatting but
not reformat them. Otherwise, the gofmt command reformats modified files in
place. That is, files in the staging area are reformatted in the staging area,
and files in the working tree are reformatted in the working tree.

Help

The help command displays basic usage instructions.

	git codereview help

Hooks

The hooks command installs the Git hooks to enforce code review conventions.

	git codereview hooks

The pre-commit hook checks that all Go code is formatted with gofmt and that
the commit is not being made directly to the master branch.

The commit-msg hook adds the Gerrit ``Change-Id'' line to the commit message if
not present. It also checks that the message uses the convention established by
the Go project that the first line has the form, pkg/path: summary.

The hooks command will not overwrite an existing hook.
If it is not installing hooks, use ``git codereview hooks -v'' for details.
This hook installation is also done at startup by all other git codereview
commands, except ``git codereview help''.

Hook-Invoke

The hook-invoke command is an internal command that invokes the named Git hook.

	git codereview hook-invoke <hook> [args]

It is run by the shell scripts installed by the ``git codereview hooks'' command.

Mail

The mail command starts the code review process for the pending change.

	git codereview mail [-f] [-r email] [-cc email] [-trybot] [revision]

It pushes the pending change commit in the current branch to the Gerrit code
review server and prints the URL for the change on the server.
If the change already exists on the server, the mail command updates that
change with a new changeset.

The -r and -cc flags identify the email addresses of people to do the code
review and to be CC'ed about the code review.
Multiple addresses are given as a comma-separated list.

An email address passed to -r or -cc can be shortened from name@domain to name.
The mail command resolves such shortenings by reading the list of past reviewers
from the git repository log to find email addresses of the form name@somedomain
and then, in case of ambiguity, using the reviewer who appears most often.

The -trybot flag runs the trybots on all new or updated changes. It is
equivalent to setting the Run-Trybot+1 label from Gerrit.

The mail command fails if there are staged edits that are not committed.
The -f flag overrides this behavior.

The mail command updates the tag <branchname>.mailed to refer to the
commit that was most recently mailed, so running ``git diff <branchname>.mailed''
shows diffs between what is on the Gerrit server and the current directory.

If there are multiple pending commits, the revision argument is mandatory.
If no revision is specified, the mail command prints a short summary of
the pending commits for use in deciding which to mail.

If any commit that would be pushed to the server contains the text
"DO NOT MAIL" (case insensitive) in its commit message, the mail command
will refuse to send the commit to the server.

Pending

The pending command prints to standard output the status of all pending changes
and staged, unstaged, and untracked files in the local repository.

	git codereview pending [-c] [-l] [-s]

The -c flag causes the command to show pending changes only on the current branch.

The -l flag causes the command to use only locally available information.
By default, it fetches recent commits and code review information from the
Gerrit server.

The -s flag causes the command to print abbreviated (short) output.

Common shorter aliases include ``git p'' for ``git pending''
and ``git pl'' for ``git pending -l'' (notably faster but without Gerrit information).

Rebase-work

The rebase-work command runs git rebase in interactive mode over pending changes.
It is shorthand for ``git rebase -i $(git codereview branchpoint)''.
It differs from plain ``git rebase -i'' in that the latter will try to incorporate
new commits from the origin branch during the rebase;
``git codereview rebase-work'' does not.

In multiple-commit workflows, rebase-work is used so often
that it can be helpful to alias it to ``git rw''.

Submit

The submit command pushes the pending change to the Gerrit server and tells
Gerrit to submit it to the master branch.

	git codereview submit [-i | revision...]

The command fails if there are modified files (staged or unstaged) that are not
part of the pending change.

The -i option causes the submit command to open a list of commits to submit
in the configured text editor, similar to ``git rebase -i''.

If multiple revisions are specified, the submit command submits each one in turn,
stopping at the first failure.

When run in a multiple-commit work branch,
either the -i option or the revision argument is mandatory.
If both are omitted, the submit command prints a short summary of
the pending commits for use in deciding which to submit.

After submitting the pending changes, the submit command tries to synchronize the
current branch to the submitted commit, if it can do so cleanly.
If not, it will prompt the user to run ``git codereview sync'' manually.

After a successful sync, the branch can be used to prepare a new change.

Sync

The sync command updates the local repository.

	git codereview sync

It fetches commits from the remote repository and merges them from the
upstream branch to the current branch, rebasing any pending changes.

Configuration

If a file named codereview.cfg is present in the repository root,
git-codereview will use it for configuration. It should contain lines
of this format:

	key: value

The ``gerrit'' key sets the Gerrit URL for this project. Git-codereview
automatically derives the Gerrit URL from repositories hosted in
*.googlesource.com. If not set or derived, the repository is assumed to
not have Gerrit, and certain features won't work.

The ``issuerepo'' key specifies the GitHub repository to use for issues, if
different from the source repository. If set to ``golang/go'', for example,
lines such as ``Fixes #123'' in a commit message will be rewritten to ``Fixes
golang/go#123''.

*/
package main
