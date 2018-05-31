// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"testing"
)

var authTests = []struct {
	netrc       string
	cookiefile  string
	user        string
	password    string
	cookieName  string
	cookieValue string
	died        bool
}{
	{
		// If we specify the empty string here, git will store an empty
		// value for the local http.cookiefile, and fall back to the global
		// http.cookiefile, which will fail this test on any machine that has
		// a global http.cookiefile configured. If we write a local, invalid
		// value, git will try to load the local cookie file (and then fail
		// later).
		cookiefile: " ",
		died:       true,
	},
	{
		netrc:      "machine go.googlesource.com login u1 password pw\n",
		cookiefile: " ", // prevent global fallback
		user:       "u1",
		password:   "pw",
	},
	{
		cookiefile: "go.googlesource.com	TRUE	/	TRUE	2147483647	o2	git-u2=pw\n",
		cookieName:  "o2",
		cookieValue: "git-u2=pw",
	},
	{
		cookiefile: ".googlesource.com	TRUE	/	TRUE	2147483647	o3	git-u3=pw\n",
		cookieName:  "o3",
		cookieValue: "git-u3=pw",
	},
	{
		cookiefile: ".googlesource.com	TRUE	/	TRUE	2147483647	o4	WRONG\n" +
			"go.googlesource.com	TRUE	/	TRUE	2147483647	o4	git-u4=pw\n",
		cookieName:  "o4",
		cookieValue: "git-u4=pw",
	},
	{
		cookiefile: "go.googlesource.com	TRUE	/	TRUE	2147483647	o5	git-u5=pw\n" +
			".googlesource.com	TRUE	/	TRUE	2147483647	o5	WRONG\n",
		cookieName:  "o5",
		cookieValue: "git-u5=pw",
	},
	{
		netrc:      "machine go.googlesource.com login u6 password pw\n",
		cookiefile: "BOGUS",
		user:       "u6",
		password:   "pw",
	},
	{
		netrc: "BOGUS",
		cookiefile: "go.googlesource.com	TRUE	/	TRUE	2147483647	o7	git-u7=pw\n",
		cookieName:  "o7",
		cookieValue: "git-u7=pw",
	},
	{
		netrc:      "machine go.googlesource.com login u8 password pw\n",
		cookiefile: "MISSING",
		user:       "u8",
		password:   "pw",
	},
}

func TestLoadAuth(t *testing.T) {
	gt := newGitTest(t)
	defer gt.done()
	gt.work(t)

	defer os.Setenv("HOME", os.Getenv("HOME"))
	os.Setenv("HOME", gt.client)

	testHomeDir = gt.client
	netrc := filepath.Join(gt.client, netrcName())
	defer func() {
		testHomeDir = ""
	}()
	trun(t, gt.client, "git", "config", "remote.origin.url", "https://go.googlesource.com/go")

	for i, tt := range authTests {
		t.Logf("#%d", i)
		auth.user = ""
		auth.password = ""
		auth.cookieName = ""
		auth.cookieValue = ""
		trun(t, gt.client, "git", "config", "http.cookiefile", "XXX")
		trun(t, gt.client, "git", "config", "--unset", "http.cookiefile")

		remove(t, netrc)
		remove(t, gt.client+"/.cookies")
		if tt.netrc != "" {
			write(t, netrc, tt.netrc)
		}
		if tt.cookiefile != "" {
			if tt.cookiefile != "MISSING" {
				write(t, gt.client+"/.cookies", tt.cookiefile)
			}
			trun(t, gt.client, "git", "config", "http.cookiefile", "~/.cookies")
		}

		// Run command via testMain to trap stdout, stderr, death.
		if tt.died {
			testMainDied(t, "test-loadAuth")
		} else {
			testMain(t, "test-loadAuth")
		}

		if auth.user != tt.user || auth.password != tt.password {
			t.Errorf("#%d: have user, password = %q, %q, want %q, %q", i, auth.user, auth.password, tt.user, tt.password)
		}
		if auth.cookieName != tt.cookieName || auth.cookieValue != tt.cookieValue {
			t.Errorf("#%d: have cookie name, value = %q, %q, want %q, %q", i, auth.cookieName, auth.cookieValue, tt.cookieName, tt.cookieValue)
		}
	}
}

func TestLoadGerritOrigin(t *testing.T) {
	list := []struct {
		origin, originUrl string

		fail               bool
		host, url, project string
	}{
		{
			// *.googlesource.com
			origin:    "",
			originUrl: "https://go.googlesource.com/crypto",
			host:      "go.googlesource.com",
			url:       "https://go-review.googlesource.com",
			project:   "crypto",
		},
		{
			// Gerrit origin is set.
			// Gerrit is hosted on a sub-domain.
			origin:    "https://gerrit.mysite.com",
			originUrl: "https://gerrit.mysite.com/projectA",
			host:      "gerrit.mysite.com",
			url:       "https://gerrit.mysite.com",
			project:   "projectA",
		},
		{
			// Gerrit origin is set.
			// Gerrit is hosted under sub-path under "/gerrit".
			origin:    "https://mysite.com/gerrit",
			originUrl: "https://mysite.com/gerrit/projectA",
			host:      "mysite.com",
			url:       "https://mysite.com/gerrit",
			project:   "projectA",
		},
		{
			// Gerrit origin is set.
			// Gerrit is hosted under sub-path under "/gerrit" and repo is under
			// sub-folder.
			origin:    "https://mysite.com/gerrit",
			originUrl: "https://mysite.com/gerrit/sub/projectA",
			host:      "mysite.com",
			url:       "https://mysite.com/gerrit",
			project:   "sub/projectA",
		},
	}

	for _, item := range list {
		auth.host = ""
		auth.url = ""
		auth.project = ""
		err := loadGerritOriginInternal(item.origin, item.originUrl)
		if err != nil && !item.fail {
			t.Errorf("unexpected error from item %q %q: %v", item.origin, item.originUrl, err)
			continue
		}
		if auth.host != item.host || auth.url != item.url || auth.project != item.project {
			t.Errorf("want %q %q %q, got %q %q %q", item.host, item.url, item.project, auth.host, auth.url, auth.project)
			continue
		}
		if item.fail {
			continue
		}
		have := haveGerritInternal(item.origin, item.originUrl)
		if !have {
			t.Errorf("for %q %q expect haveGerrit() == true, but got false", item.origin, item.originUrl)
		}
	}
}
