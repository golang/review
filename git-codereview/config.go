// Copyright 2015 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"
)

var (
	configPath   string
	cachedConfig map[string]string
)

// Config returns the code review config.
// Configs consist of lines of the form "key: value".
// Lines beginning with # are comments.
// If there is no config, it returns an empty map.
// If the config is malformed, it dies.
func config() map[string]string {
	if cachedConfig != nil {
		return cachedConfig
	}
	configPath = filepath.Join(repoRoot(), "codereview.cfg")
	b, err := ioutil.ReadFile(configPath)
	raw := string(b)
	if err != nil {
		verbosef("%sfailed to load config from %q: %v", raw, configPath, err)
		cachedConfig = make(map[string]string)
		return cachedConfig
	}
	cachedConfig, err = parseConfig(raw)
	if err != nil {
		dief("%v", err)
	}
	return cachedConfig
}

// haveGerrit returns true if gerrit should be used.
// To enable gerrit, codereview.cfg must be present with "gerrit" property set to
// the gerrit https URL or the git origin must be to
// "https://<project>.googlesource.com/<repo>".
func haveGerrit() bool {
	gerrit := config()["gerrit"]
	origin := trim(cmdOutput("git", "config", "remote.origin.url"))
	return haveGerritInternal(gerrit, origin)
}

// haveGerritInternal is the same as haveGerrit but factored out
// for testing.
func haveGerritInternal(gerrit, origin string) bool {
	if gerrit == "off" {
		return false
	}
	if gerrit != "" {
		return true
	}
	if strings.Contains(origin, "github.com") {
		return false
	}
	if strings.HasPrefix(origin, "sso://") || strings.HasPrefix(origin, "rpc://") {
		return true
	}
	if !strings.Contains(origin, "https://") {
		return false
	}
	if strings.Count(origin, "/") != 3 {
		return false
	}
	host := origin[:strings.LastIndex(origin, "/")]
	return strings.HasSuffix(host, ".googlesource.com")
}

func parseConfig(raw string) (map[string]string, error) {
	cfg := make(map[string]string)
	for _, line := range nonBlankLines(raw) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			// comment line
			continue
		}
		fields := strings.SplitN(line, ":", 2)
		if len(fields) != 2 {
			return nil, fmt.Errorf("bad config line, expected 'key: value': %q", line)
		}
		cfg[strings.TrimSpace(fields[0])] = strings.TrimSpace(fields[1])
	}
	return cfg, nil
}
