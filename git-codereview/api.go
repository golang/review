// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// auth holds cached data about authentication to Gerrit.
var auth struct {
	initialized bool

	host    string // "go.googlesource.com"
	url     string // "https://go-review.googlesource.com"
	project string // "go", "tools", "crypto", etc

	// Authentication information.
	// Either cookie name + value from git cookie file
	// or username and password from .netrc.
	cookieName  string
	cookieValue string
	user        string
	password    string
}

// loadGerritOriginMutex is used to control access when initializing auth
// in loadGerritOrigin, which can be called in parallel by "pending".
// We use a mutex rather than a sync.Once because the tests clear auth.
var loadGerritOriginMutex sync.Mutex

// loadGerritOrigin loads the Gerrit host name from the origin remote.
// This sets auth.{initialized,host,url,project}.
// If the origin remote does not appear to be a Gerrit server
// (is missing, is GitHub, is not https, has too many path elements),
// loadGerritOrigin dies.
func loadGerritOrigin() {
	loadGerritOriginMutex.Lock()
	defer loadGerritOriginMutex.Unlock()

	if auth.initialized {
		return
	}

	// Gerrit must be set, either explicitly via the code review config or
	// implicitly as Git's origin remote.
	origin := config()["gerrit"]
	originUrl := trim(cmdOutput("git", "config", "remote.origin.url"))

	err := loadGerritOriginInternal(origin, originUrl)
	if err != nil {
		dief("failed to load Gerrit origin: %v", err)
	}

	auth.initialized = true
}

// loadGerritOriginInternal does the work of loadGerritOrigin, just extracted out
// for easier testing.
func loadGerritOriginInternal(origin, remoteOrigin string) error {
	originUrl, err := url.Parse(remoteOrigin)
	if err != nil {
		return fmt.Errorf("failed to parse git's remote.origin.url %q as a URL: %v", remoteOrigin, err)
	} else {
		originUrl.User = nil
		remoteOrigin = originUrl.String()
	}
	hasGerritConfig := true
	if origin == "" {
		hasGerritConfig = false
		origin = remoteOrigin
	}
	if strings.Contains(origin, "github.com") {
		return fmt.Errorf("git origin must be a Gerrit host, not GitHub: %s", origin)
	}

	if googlesourceIndex := strings.Index(origin, ".googlesource.com"); googlesourceIndex >= 0 {
		if !strings.HasPrefix(origin, "https://") {
			return fmt.Errorf("git origin must be an https:// URL: %s", origin)
		}
		// https:// prefix and then one slash between host and top-level name
		if strings.Count(origin, "/") != 3 {
			return fmt.Errorf("git origin is malformed: %s", origin)
		}
		host := origin[len("https://"):strings.LastIndex(origin, "/")]

		// In the case of Google's Gerrit, host is go.googlesource.com
		// and apiURL uses go-review.googlesource.com, but the Gerrit
		// setup instructions do not write down a cookie explicitly for
		// go-review.googlesource.com, so we look for the non-review
		// host name instead.
		url := origin
		i := googlesourceIndex
		url = url[:i] + "-review" + url[i:]

		i = strings.LastIndex(url, "/")
		url, project := url[:i], url[i+1:]

		auth.host = host
		auth.url = url
		auth.project = project
		return nil
	}

	// Origin is not *.googlesource.com.
	//
	// If the Gerrit origin is set from the codereview.cfg file than we handle it
	// differently to allow for sub-path hosted Gerrit.
	auth.host = originUrl.Host
	if hasGerritConfig {
		if !strings.HasPrefix(remoteOrigin, origin) {
			return fmt.Errorf("Gerrit origin %q from %q different than git origin url %q", origin, configPath, originUrl)
		}

		auth.project = strings.Trim(strings.TrimPrefix(remoteOrigin, origin), "/")
		auth.url = origin
	} else {
		auth.project = strings.Trim(originUrl.Path, "/")
		auth.url = strings.TrimSuffix(remoteOrigin, originUrl.Path)
	}

	return nil
}

// testHomeDir is empty for normal use. During tests it may be set and used
// in place of the actual home directory. Tests may still need to
// set the HOME var for sub-processes such as git.
var testHomeDir = ""

func netrcName() string {
	// Git on Windows will look in $HOME\_netrc.
	if runtime.GOOS == "windows" {
		return "_netrc"
	}
	return ".netrc"
}

// loadAuth loads the authentication tokens for making API calls to
// the Gerrit origin host.
func loadAuth() {
	if auth.user != "" || auth.cookieName != "" {
		return
	}

	loadGerritOrigin()

	// First look in Git's http.cookiefile, which is where Gerrit
	// now tells users to store this information.
	if cookieFile, _ := trimErr(cmdOutputErr("git", "config", "--path", "--get-urlmatch", "http.cookiefile", auth.url)); cookieFile != "" {
		data, _ := ioutil.ReadFile(cookieFile)
		maxMatch := -1
		for _, line := range lines(string(data)) {
			f := strings.Split(line, "\t")
			if len(f) >= 7 && (f[0] == auth.host || strings.HasPrefix(f[0], ".") && strings.HasSuffix(auth.host, f[0])) {
				if len(f[0]) > maxMatch {
					auth.cookieName = f[5]
					auth.cookieValue = f[6]
					maxMatch = len(f[0])
				}
			}
		}
		if maxMatch > 0 {
			return
		}
	}

	// If not there, then look in $HOME/.netrc, which is where Gerrit
	// used to tell users to store the information, until the passwords
	// got so long that old versions of curl couldn't handle them.
	netrc := netrcName()
	homeDir := testHomeDir
	if homeDir == "" {
		usr, err := user.Current()
		if err != nil {
			dief("failed to get current user home directory to look for %q: %v", netrc, err)
		}
		homeDir = usr.HomeDir
	}
	data, _ := ioutil.ReadFile(filepath.Join(homeDir, netrc))
	for _, line := range lines(string(data)) {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		f := strings.Fields(line)
		if len(f) >= 6 && f[0] == "machine" && f[1] == auth.host && f[2] == "login" && f[4] == "password" {
			auth.user = f[3]
			auth.password = f[5]
			return
		}
	}

	dief("cannot find authentication info for %s", auth.host)
}

// gerritError is an HTTP error response served by Gerrit.
type gerritError struct {
	url        string
	statusCode int
	status     string
	body       string
}

func (e *gerritError) Error() string {
	if e.statusCode == http.StatusNotFound {
		return "change not found on Gerrit server"
	}

	extra := strings.TrimSpace(e.body)
	if extra != "" {
		extra = ": " + extra
	}
	return fmt.Sprintf("%s%s", e.status, extra)
}

// gerritAPI executes a GET or POST request to a Gerrit API endpoint.
// It uses GET when requestBody is nil, otherwise POST. If target != nil,
// gerritAPI expects to get a 200 response with a body consisting of an
// anti-xss line (]})' or some such) followed by JSON.
// If requestBody != nil, gerritAPI sets the Content-Type to application/json.
func gerritAPI(path string, requestBody []byte, target interface{}) (err error) {
	var respBodyBytes []byte
	defer func() {
		if err != nil {
			// os.Stderr, not stderr(), because the latter is not safe for
			// use from multiple goroutines.
			fmt.Fprintf(os.Stderr, "git-codereview: fetch %s: %v\n", path, err)
			if len(respBodyBytes) > 0 {
				fmt.Fprintf(os.Stderr, "Gerrit response:\n%s\n", respBodyBytes)
			}
		}
	}()

	// Strictly speaking, we might be able to use unauthenticated
	// access, by removing the /a/ from the URL, but that assumes
	// that all the information we care about is publicly visible.
	// Using authentication makes it possible for this to work with
	// non-public CLs or Gerrit hosts too.
	loadAuth()

	if !strings.HasPrefix(path, "/") {
		dief("internal error: gerritAPI called with malformed path")
	}

	url := auth.url + path
	method := "GET"
	var reader io.Reader
	if requestBody != nil {
		method = "POST"
		reader = bytes.NewReader(requestBody)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return err
	}
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth.cookieName != "" {
		req.AddCookie(&http.Cookie{
			Name:  auth.cookieName,
			Value: auth.cookieValue,
		})
	} else {
		req.SetBasicAuth(auth.user, auth.password)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	body, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()

	respBodyBytes = body

	if err != nil {
		return fmt.Errorf("reading response body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return &gerritError{url, resp.StatusCode, resp.Status, string(body)}
	}

	if target != nil {
		i := bytes.IndexByte(body, '\n')
		if i < 0 {
			return fmt.Errorf("%s: malformed json response - bad header", url)
		}
		body = body[i:]
		if err := json.Unmarshal(body, target); err != nil {
			return fmt.Errorf("%s: malformed json response", url)
		}
	}
	return nil
}

// fullChangeID returns the unambigous Gerrit change ID for the commit c on branch b.
// The returned ID has the form project~originbranch~Ihexhexhexhexhex.
// See https://gerrit-review.googlesource.com/Documentation/rest-api-changes.html#change-id for details.
func fullChangeID(b *Branch, c *Commit) string {
	loadGerritOrigin()
	return auth.project + "~" + strings.TrimPrefix(b.OriginBranch(), "origin/") + "~" + c.ChangeID
}

// readGerritChange reads the metadata about a change from the Gerrit server.
// The changeID should use the syntax project~originbranch~Ihexhexhexhexhex returned
// by fullChangeID. Using only Ihexhexhexhexhex will work provided it uniquely identifies
// a single change on the server.
// The changeID can have additional query parameters appended to it, as in "normalid?o=LABELS".
// See https://gerrit-review.googlesource.com/Documentation/rest-api-changes.html#change-id for details.
func readGerritChange(changeID string) (*GerritChange, error) {
	var c GerritChange
	err := gerritAPI("/a/changes/"+changeID, nil, &c)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// readGerritChanges is like readGerritChange but expects changeID
// to be a query parameter list like q=change:XXX&q=change:YYY&o=OPTIONS,
// and it expects to receive a JSON array of GerritChanges, not just one.
func readGerritChanges(query string) ([][]*GerritChange, error) {
	// The Gerrit server imposes a limit of at most 10 q= parameters.
	v, err := url.ParseQuery(query)
	if err != nil {
		return nil, err
	}
	var results []chan gerritChangeResult
	for len(v["q"]) > 0 {
		n := len(v["q"])
		if n > 10 {
			n = 10
		}
		all := v["q"]
		v["q"] = all[:n]
		query := v.Encode()
		v["q"] = all[n:]
		ch := make(chan gerritChangeResult, 1)
		go readGerritChangesBatch(query, n, ch)
		results = append(results, ch)
	}

	var c [][]*GerritChange
	for _, ch := range results {
		res := <-ch
		if res.err != nil {
			return nil, res.err
		}
		c = append(c, res.c...)
	}
	return c, nil
}

type gerritChangeResult struct {
	c   [][]*GerritChange
	err error
}

func readGerritChangesBatch(query string, n int, ch chan gerritChangeResult) {
	var c [][]*GerritChange
	// If there are multiple q=, the server sends back an array of arrays of results.
	// If there is a single q=, it only sends back an array of results; in that case
	// we need to do the wrapping ourselves.
	var arg interface{} = &c
	if n == 1 {
		c = append(c, nil)
		arg = &c[0]
	}
	err := gerritAPI("/a/changes/?"+query, nil, arg)
	if len(c) != n && err == nil {
		err = fmt.Errorf("gerrit result count mismatch")
	}
	ch <- gerritChangeResult{c, err}
}

// GerritChange is the JSON struct for a Gerrit ChangeInfo, returned by a Gerrit CL query.
type GerritChange struct {
	ID                     string
	Project                string
	Branch                 string
	ChangeId               string `json:"change_id"`
	Subject                string
	Status                 string
	Created                string
	Updated                string
	Insertions             int
	Deletions              int
	Number                 int `json:"_number"`
	Owner                  *GerritAccount
	Labels                 map[string]*GerritLabel
	CurrentRevision        string `json:"current_revision"`
	Revisions              map[string]*GerritRevision
	Messages               []*GerritMessage
	TotalCommentCount      int `json:"total_comment_count"`
	UnresolvedCommentCount int `json:"unresolved_comment_count"`
}

// LabelNames returns the label names for the change, in lexicographic order.
func (g *GerritChange) LabelNames() []string {
	var names []string
	for name := range g.Labels {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// GerritMessage is the JSON struct for a Gerrit MessageInfo.
type GerritMessage struct {
	Author struct {
		Name string
	}
	Message string
}

// GerritLabel is the JSON struct for a Gerrit LabelInfo.
type GerritLabel struct {
	Optional bool
	Blocking bool
	Approved *GerritAccount
	Rejected *GerritAccount
	All      []*GerritApproval
}

// GerritAccount is the JSON struct for a Gerrit AccountInfo.
type GerritAccount struct {
	ID       int `json:"_account_id"`
	Name     string
	Email    string
	Username string
}

// GerritApproval is the JSON struct for a Gerrit ApprovalInfo.
type GerritApproval struct {
	GerritAccount
	Value int
	Date  string
}

// GerritRevision is the JSON struct for a Gerrit RevisionInfo.
type GerritRevision struct {
	Number int `json:"_number"`
	Ref    string
	Fetch  map[string]*GerritFetch
}

// GerritFetch is the JSON struct for a Gerrit FetchInfo.
type GerritFetch struct {
	URL string
	Ref string
}

// GerritComment is the JSON struct for a Gerrit CommentInfo.
type GerritComment struct {
	PatchSet        string `json:"patch_set"`
	ID              string
	Path            string
	Side            string
	Parent          string
	Line            string
	Range           *GerritCommentRange
	InReplyTo       string
	Message         string
	Updated         string
	Author          *GerritAccount
	Tag             string
	Unresolved      bool
	ChangeMessageID string `json:"change_message_id"`
	CommitID        string `json:"commit_id"` // SHA1 hex
}

// GerritCommentRange is the JSON struct for a Gerrit CommentRange.
type GerritCommentRange struct {
	StartLine      int `json:"start_line"`      // 1-based
	StartCharacter int `json:"start_character"` // 0-based
	EndLine        int `json:"end_line"`        // 1-based
	EndCharacter   int `json:"end_character"`   // 0-based
}

// GerritContextLine is the JSON struct for a Gerrit ContextLine.
type GerritContextLine struct {
	LineNumber  int    `json:"line_number"` // 1-based
	ContextLine string `json:"context_line"`
}

// GerritCommentInput is the JSON struct for a Gerrit CommentInput.
type GerritCommentInput struct {
	ID         string              `json:"id,omitempty"`   // ID of a draft comment to update
	Path       string              `json:"path,omitempty"` // file to attach comment to
	Side       string              `json:"side,omitempty"` // REVISION (default) or PARENT
	Line       int                 `json:"line,omitempty"` // 0 to use range (or else file comment)
	Range      *GerritCommentRange `json:"range,omitempty"`
	InReplyTo  string              `json:"in_reply_to,omitempty"` // ID of comment being replied to
	Message    string              `json:"message,omitempty"`
	Unresolved *bool               `json:"unresolved,omitempty"` // defaults to parent setting or else false
}
