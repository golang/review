// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"testing"
)

const (
	goodGo      = "package good\n"
	badGo       = " package bad1 "
	badGoFixed  = "package bad1\n"
	bad2Go      = " package bad2 "
	bad2GoFixed = "package bad2\n"
	brokenGo    = "package B R O K E N"
)

func TestGofmt(t *testing.T) {
	// Test of basic operations.
	gt := newGitTest(t)
	defer gt.done()

	gt.work(t)

	if err := os.MkdirAll(gt.client+"/test/bench", 0755); err != nil {
		t.Fatal(err)
	}
	write(t, gt.client+"/bad.go", badGo)
	write(t, gt.client+"/good.go", goodGo)
	write(t, gt.client+"/test/bad.go", badGo)
	write(t, gt.client+"/test/good.go", goodGo)
	write(t, gt.client+"/test/bench/bad.go", badGo)
	write(t, gt.client+"/test/bench/good.go", goodGo)
	trun(t, gt.client, "git", "add", ".") // make files tracked

	testMain(t, "gofmt", "-l")
	testPrintedStdout(t, "bad.go\n", "!good.go", "!test/bad", "test/bench/bad.go")

	testMain(t, "gofmt", "-l")
	testPrintedStdout(t, "bad.go\n", "!good.go", "!test/bad", "test/bench/bad.go")

	testMain(t, "gofmt")
	testPrintedStdout(t, "!.go") // no output

	testMain(t, "gofmt", "-l")
	testPrintedStdout(t, "!.go") // no output

	write(t, gt.client+"/bad.go", badGo)
	write(t, gt.client+"/broken.go", brokenGo)
	trun(t, gt.client, "git", "add", ".")
	testMainDied(t, "gofmt", "-l")
	testPrintedStdout(t, "bad.go")
	testPrintedStderr(t, "gofmt reported errors", "broken.go")
}

func TestGofmtUnstaged(t *testing.T) {
	// Test when unstaged files are different from staged ones.
	// See TestHookPreCommitUnstaged for an explanation.
	// In this test we use two different kinds of bad files, so that
	// we can test having a bad file in the index and a different
	// bad file in the working directory.

	gt := newGitTest(t)
	defer gt.done()
	gt.work(t)

	name := []string{"good", "bad", "bad2", "broken"}
	orig := []string{goodGo, badGo, bad2Go, brokenGo}
	fixed := []string{goodGo, badGoFixed, bad2GoFixed, brokenGo}
	const N = 4

	var allFiles, wantOut, wantErr []string
	writeFiles := func(n int) {
		allFiles = nil
		wantOut = nil
		wantErr = nil
		for i := 0; i < N*N*N; i++ {
			// determine n'th digit of 3-digit base-N value i
			j := i
			for k := 0; k < (3 - 1 - n); k++ {
				j /= N
			}
			text := orig[j%N]
			file := fmt.Sprintf("%s-%s-%s.go", name[i/N/N], name[(i/N)%N], name[i%N])
			allFiles = append(allFiles, file)
			write(t, gt.client+"/"+file, text)

			if (i/N)%N != i%N {
				staged := file + " (staged)"
				switch {
				case strings.Contains(file, "-bad-"), strings.Contains(file, "-bad2-"):
					wantOut = append(wantOut, staged)
					wantErr = append(wantErr, "!"+staged)
				case strings.Contains(file, "-broken-"):
					wantOut = append(wantOut, "!"+staged)
					wantErr = append(wantErr, staged)
				default:
					wantOut = append(wantOut, "!"+staged)
					wantErr = append(wantErr, "!"+staged)
				}
			}
			switch {
			case strings.Contains(file, "-bad.go"), strings.Contains(file, "-bad2.go"):
				if (i/N)%N != i%N {
					file += " (unstaged)"
				}
				wantOut = append(wantOut, file+"\n")
				wantErr = append(wantErr, "!"+file+":", "!"+file+" (unstaged)")
			case strings.Contains(file, "-broken.go"):
				wantOut = append(wantOut, "!"+file+"\n", "!"+file+" (unstaged)")
				wantErr = append(wantErr, file+":")
			default:
				wantOut = append(wantOut, "!"+file+"\n", "!"+file+":", "!"+file+" (unstaged)")
				wantErr = append(wantErr, "!"+file+"\n", "!"+file+":", "!"+file+" (unstaged)")
			}
		}
	}

	// committed files
	writeFiles(0)
	trun(t, gt.client, "git", "add", ".")
	trun(t, gt.client, "git", "commit", "-m", "msg")

	// staged files
	writeFiles(1)
	trun(t, gt.client, "git", "add", ".")

	// unstaged files
	writeFiles(2)

	// Check that gofmt -l shows the right output and errors.
	testMainDied(t, "gofmt", "-l")
	testPrintedStdout(t, wantOut...)
	testPrintedStderr(t, wantErr...)

	// Again (last command should not have written anything).
	testMainDied(t, "gofmt", "-l")
	testPrintedStdout(t, wantOut...)
	testPrintedStderr(t, wantErr...)

	// Reformat in place.
	testMainDied(t, "gofmt")
	testPrintedStdout(t, "!.go")
	testPrintedStderr(t, wantErr...)

	// Read files to make sure unstaged did not bleed into staged.
	for i, file := range allFiles {
		if data, err := ioutil.ReadFile(gt.client + "/" + file); err != nil {
			t.Errorf("%v", err)
		} else if want := fixed[i%N]; string(data) != want {
			t.Errorf("%s: working tree = %q, want %q", file, string(data), want)
		}
		if data, want := trun(t, gt.client, "git", "show", ":"+file), fixed[i/N%N]; data != want {
			t.Errorf("%s: index = %q, want %q", file, data, want)
		}
		if data, want := trun(t, gt.client, "git", "show", "HEAD:"+file), orig[i/N/N]; data != want {
			t.Errorf("%s: commit = %q, want %q", file, data, want)
		}
	}

	// Check that gofmt -l still shows the errors.
	testMainDied(t, "gofmt", "-l")
	testPrintedStdout(t, "!.go")
	testPrintedStderr(t, wantErr...)
}
