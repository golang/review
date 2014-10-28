/*
Copyright 2014 The Camlistore Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

//go:generate go run bake.go commit-msg.githook

import (
	"bufio"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
)

var hookFile = filepath.FromSlash(".git/hooks/commit-msg")

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: go-review\n")
		os.Exit(2)
	}
	flag.Parse()
	if len(flag.Args()) > 0 {
		flag.Usage()
	}
	goToRepoRoot()
	checkHook()
	gitPush()
}

func goToRepoRoot() {
	prevDir, err := os.Getwd()
	if err != nil {
		dief("could not get current directory: %v\n", err)
	}
	for {
		if _, err := os.Stat(".git"); err == nil {
			return
		}
		if err := os.Chdir(".."); err != nil {
			dief("could not chdir: %v\n", err)
		}
		currentDir, err := os.Getwd()
		if err != nil {
			dief("could not get current directory: %v\n", err)
		}
		if currentDir == prevDir {
			dief("Git root not found. Run from within the Git tree please.\n")
		}
		prevDir = currentDir
	}
}

func checkHook() {
	_, err := os.Stat(hookFile)
	if err == nil {
		return
	}
	if !os.IsNotExist(err) {
		dief("checking for hook file: %v\n", err)
	}
	fmt.Printf("Presubmit hook to add Change-Id to commit messages is missing.\nNow automatically creating it at %v.\n\n", hookFile)
	hookContent := []byte(baked["commit-msg.githook"])
	if err := ioutil.WriteFile(hookFile, hookContent, 0700); err != nil {
		dief("writing hook file: %v\n", err)
	}
	fmt.Printf("Amending last commit to add Change-Id.\nPlease re-save description without making changes.\n\n")
	fmt.Printf("Press Enter to continue.\n")
	if _, _, err := bufio.NewReader(os.Stdin).ReadLine(); err != nil {
		dief("waiting for user input: %v\n", err)
	}

	cmd := exec.Command("git", []string{"commit", "--amend"}...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		dief("amending commit: %v\n", err)
	}
}

func gitPush() {
	cmd := exec.Command("git",
		[]string{"push", "https://camlistore.googlesource.com/camlistore", "HEAD:refs/for/master"}...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		dief("Could not git push: %v\n", err)
	}
}

func dief(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format, args...)
	os.Exit(1)
}
