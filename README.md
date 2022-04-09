# git-codereview

The git-codereview tool is a command-line tool for working with Gerrit.

## Download/Install

The easiest way to install is to run `go install golang.org/x/review/git-codereview@latest`. You can
also manually git clone the repository to `$GOPATH/src/golang.org/x/review`.

Run `git codereview hooks` to install Gerrit hooks for your git repository.

## Report Issues / Send Patches

This repository uses Gerrit for code changes. To learn how to submit changes to
this repository, see https://golang.org/doc/contribute.html.

The main issue tracker for the review repository is located at
https://github.com/golang/go/issues. Prefix your issue with "x/review:" in the
subject line, so it is easy to find.
