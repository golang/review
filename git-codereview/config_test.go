// Copyright 2015 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"reflect"
	"testing"
)

func TestParseConfig(t *testing.T) {
	cases := []struct {
		raw     string
		want    map[string]string
		wanterr bool
	}{
		{raw: "", want: map[string]string{}},
		{raw: "issuerepo: golang/go", want: map[string]string{"issuerepo": "golang/go"}},
		{raw: "# comment", want: map[string]string{}},
		{raw: "# comment\n  k  :   v   \n# comment 2\n\n k2:v2\n", want: map[string]string{"k": "v", "k2": "v2"}},
	}

	for _, tt := range cases {
		cfg, err := parseConfig(tt.raw)
		if err != nil != tt.wanterr {
			t.Errorf("parse(%q) error: %v", tt.raw, err)
			continue
		}
		if !reflect.DeepEqual(cfg, tt.want) {
			t.Errorf("parse(%q)=%v want %v", tt.raw, cfg, tt.want)
		}
	}
}

func TestHaveGerritInternal(t *testing.T) {
	tests := []struct {
		gerrit string
		origin string
		want   bool
	}{
		{gerrit: "off", want: false},
		{gerrit: "on", want: true},
		{origin: "invalid url", want: false},
		{origin: "https://github.com/golang/go", want: false},
		{origin: "http://github.com/golang/go", want: false},
		{origin: "git@github.com:golang/go", want: false},
		{origin: "git@github.com:golang/go.git", want: false},
		{origin: "git@github.com:/golang/go", want: false},
		{origin: "git@github.com:/golang/go.git", want: false},
		{origin: "ssh://git@github.com/golang/go", want: false},
		{origin: "ssh://git@github.com/golang/go.git", want: false},
		{origin: "git+ssh://git@github.com/golang/go", want: false},
		{origin: "git+ssh://git@github.com/golang/go.git", want: false},
		{origin: "git://github.com/golang/go", want: false},
		{origin: "git://github.com/golang/go.git", want: false},
		{origin: "sso://go/tools", want: true}, // Google-internal
		{origin: "rpc://go/tools", want: true}, // Google-internal
		{origin: "http://go.googlesource.com/sys", want: false},
		{origin: "https://go.googlesource.com/review", want: true},
		{origin: "https://go.googlesource.com/review/", want: true},
	}

	for _, test := range tests {
		if got := haveGerritInternal(test.gerrit, test.origin); got != test.want {
			t.Errorf("haveGerritInternal(%q, %q) = %t, want %t", test.gerrit, test.origin, got, test.want)
		}
	}
}
