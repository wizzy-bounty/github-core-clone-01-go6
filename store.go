package main

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------- time ----------------

var trialNow time.Time
var trialNowSet bool

func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

func initTime() {
	raw := firstEnv("MAKEFUN_TRIAL_NOW", "TRIAL_NOW")
	if raw == "" {
		return
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if n > 1_000_000_000_000 {
			trialNow = time.UnixMilli(n).UTC()
		} else {
			trialNow = time.Unix(n, 0).UTC()
		}
		trialNowSet = true
		return
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05Z", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, raw); err == nil {
			trialNow = t.UTC()
			trialNowSet = true
			return
		}
	}
}

func now() time.Time {
	if trialNowSet {
		return trialNow
	}
	return time.Now().UTC()
}

func fmtTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

// ---------------- git objects ----------------

type Obj struct {
	Type string
	Data []byte
}

func hashObj(typ string, data []byte) string {
	h := sha1.New()
	h.Write([]byte(typ))
	h.Write([]byte(" "))
	h.Write([]byte(strconv.Itoa(len(data))))
	h.Write([]byte{0})
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

type CommitInfo struct {
	Tree         string
	Parents      []string
	AuthorName   string
	AuthorEmail  string
	AuthorTime   int64
	AuthorTZ     string
	CommitterName  string
	CommitterEmail string
	CommitterTime  int64
	CommitterTZ    string
	Message      string
}

func parseIdent(line string) (name, email string, ts int64, tz string) {
	// "Name <email> 1234567890 +0000"
	lt := strings.Index(line, "<")
	gt := strings.Index(line, ">")
	if lt < 0 || gt < 0 || gt < lt {
		return strings.TrimSpace(line), "", 0, "+0000"
	}
	name = strings.TrimSpace(line[:lt])
	email = line[lt+1 : gt]
	rest := strings.Fields(strings.TrimSpace(line[gt+1:]))
	if len(rest) >= 1 {
		ts, _ = strconv.ParseInt(rest[0], 10, 64)
	}
	if len(rest) >= 2 {
		tz = rest[1]
	} else {
		tz = "+0000"
	}
	return name, email, ts, tz
}

func parseCommit(data []byte) CommitInfo {
	ci := CommitInfo{}
	s := string(data)
	headers := s
	if idx := strings.Index(s, "\n\n"); idx >= 0 {
		headers = s[:idx]
		ci.Message = s[idx+2:]
	}
	for _, line := range strings.Split(headers, "\n") {
		switch {
		case strings.HasPrefix(line, "tree "):
			ci.Tree = strings.TrimSpace(line[5:])
		case strings.HasPrefix(line, "parent "):
			ci.Parents = append(ci.Parents, strings.TrimSpace(line[7:]))
		case strings.HasPrefix(line, "author "):
			ci.AuthorName, ci.AuthorEmail, ci.AuthorTime, ci.AuthorTZ = parseIdent(line[7:])
		case strings.HasPrefix(line, "committer "):
			ci.CommitterName, ci.CommitterEmail, ci.CommitterTime, ci.CommitterTZ = parseIdent(line[10:])
		}
	}
	return ci
}

func buildCommitData(tree string, parents []string, aName, aEmail string, aTime int64, aTZ, cName, cEmail string, cTime int64, cTZ, message string) []byte {
	var b strings.Builder
	b.WriteString("tree " + tree + "\n")
	for _, p := range parents {
		b.WriteString("parent " + p + "\n")
	}
	b.WriteString("author " + aName + " <" + aEmail + "> " + strconv.FormatInt(aTime, 10) + " " + aTZ + "\n")
	b.WriteString("committer " + cName + " <" + cEmail + "> " + strconv.FormatInt(cTime, 10) + " " + cTZ + "\n")
	b.WriteString("\n")
	b.WriteString(message)
	return []byte(b.String())
}

type TreeEntry struct {
	Mode string
	Name string
	SHA  string
}

func (e TreeEntry) IsTree() bool { return e.Mode == "40000" || e.Mode == "040000" }

func parseTree(data []byte) []TreeEntry {
	var out []TreeEntry
	i := 0
	for i < len(data) {
		sp := strings.IndexByte(string(data[i:]), ' ')
		if sp < 0 {
			break
		}
		mode := string(data[i : i+sp])
		i += sp + 1
		nul := strings.IndexByte(string(data[i:]), 0)
		if nul < 0 {
			break
		}
		name := string(data[i : i+nul])
		i += nul + 1
		if i+20 > len(data) {
			break
		}
		sha := hex.EncodeToString(data[i : i+20])
		i += 20
		out = append(out, TreeEntry{Mode: mode, Name: name, SHA: sha})
	}
	return out
}

func treeSortKey(e TreeEntry) string {
	if e.IsTree() {
		return e.Name + "/"
	}
	return e.Name
}

func buildTreeData(entries []TreeEntry) []byte {
	sorted := make([]TreeEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool { return treeSortKey(sorted[i]) < treeSortKey(sorted[j]) })
	var buf []byte
	for _, e := range sorted {
		mode := e.Mode
		if e.IsTree() {
			mode = "40000"
		}
		buf = append(buf, []byte(mode+" "+e.Name)...)
		buf = append(buf, 0)
		raw, _ := hex.DecodeString(e.SHA)
		buf = append(buf, raw...)
	}
	return buf
}

// ---------------- users / repos ----------------

type User struct {
	Login string
	ID    int64
}

type Comment struct {
	ID        int64
	Body      string
	User      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Issue struct {
	ID         int64
	Number     int
	Title      string
	Body       string
	State      string // open | closed
	User       string
	Labels     []string
	Assignees  []string
	Comments   []*Comment
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ClosedAt   *time.Time
	IsPR       bool
	HeadRef    string
	BaseRef    string
	Merged     bool
	MergedAt   *time.Time
	MergedBy   string
	MergeSHA   string
}

type Repo struct {
	ID          int64
	Owner       string
	Name        string
	Desc        string
	Private     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Objects     map[string]*Obj
	Refs        map[string]string // full refname -> sha
	HeadBranch  string
	Issues      []*Issue
	NextNum     int
	Comments    map[int64]*Comment
	CommentSeq  map[int64]int // comment id -> issue number
	Labels      map[string]string // name -> color
}

func (r *Repo) put(typ string, data []byte) string {
	sha := hashObj(typ, data)
	if _, ok := r.Objects[sha]; !ok {
		cp := make([]byte, len(data))
		copy(cp, data)
		r.Objects[sha] = &Obj{Type: typ, Data: cp}
	}
	return sha
}

func (r *Repo) get(sha string) *Obj {
	if sha == "" {
		return nil
	}
	return r.Objects[sha]
}

// resolveRefish resolves a branch name, full ref, HEAD, full sha, or unique
// sha prefix to a commit sha.
func (r *Repo) resolveRefish(s string) string {
	if s == "" {
		return ""
	}
	if s == "HEAD" {
		return r.Refs["refs/heads/"+r.HeadBranch]
	}
	if v, ok := r.Refs[s]; ok {
		return v
	}
	if v, ok := r.Refs["refs/heads/"+s]; ok {
		return v
	}
	if v, ok := r.Refs["refs/tags/"+s]; ok {
		return v
	}
	if v, ok := r.Refs["refs/"+s]; ok {
		return v
	}
	if len(s) == 40 {
		if o := r.get(s); o != nil {
			if o.Type == "tag" {
				ci := parseTag(o.Data)
				return ci.Object
			}
			return s
		}
		return ""
	}
	if len(s) >= 4 && len(s) < 40 {
		found := ""
		for sha := range r.Objects {
			if strings.HasPrefix(sha, s) {
				if found != "" {
					return ""
				}
				found = sha
			}
		}
		if found != "" {
			if o := r.get(found); o != nil && o.Type == "tag" {
				return parseTag(o.Data).Object
			}
			return found
		}
	}
	return ""
}

type TagInfo struct {
	Object string
	Type   string
	Tag    string
	Name   string
	Email  string
	Time   int64
	TZ     string
	Message string
}

func parseTag(data []byte) TagInfo {
	ti := TagInfo{}
	s := string(data)
	headers := s
	if idx := strings.Index(s, "\n\n"); idx >= 0 {
		headers = s[:idx]
		ti.Message = s[idx+2:]
	}
	for _, line := range strings.Split(headers, "\n") {
		switch {
		case strings.HasPrefix(line, "object "):
			ti.Object = strings.TrimSpace(line[7:])
		case strings.HasPrefix(line, "type "):
			ti.Type = strings.TrimSpace(line[5:])
		case strings.HasPrefix(line, "tag "):
			ti.Tag = strings.TrimSpace(line[4:])
		case strings.HasPrefix(line, "tagger "):
			ti.Name, ti.Email, ti.Time, ti.TZ = parseIdent(line[7:])
		}
	}
	return ti
}

// peelToCommit follows tag objects until a commit.
func (r *Repo) peelToCommit(sha string) string {
	for i := 0; i < 10; i++ {
		o := r.get(sha)
		if o == nil {
			return ""
		}
		if o.Type == "commit" {
			return sha
		}
		if o.Type == "tag" {
			sha = parseTag(o.Data).Object
			continue
		}
		return ""
	}
	return ""
}

// treeOfCommit returns the tree sha for a commit sha.
func (r *Repo) treeOfCommit(commitSHA string) string {
	o := r.get(commitSHA)
	if o == nil || o.Type != "commit" {
		return ""
	}
	return parseCommit(o.Data).Tree
}

// flattenTree returns path -> entry for all blobs (and trees with prefix).
func (r *Repo) flattenTree(treeSHA, prefix string, out map[string]TreeEntry) {
	o := r.get(treeSHA)
	if o == nil || o.Type != "tree" {
		return
	}
	for _, e := range parseTree(o.Data) {
		p := e.Name
		if prefix != "" {
			p = prefix + "/" + e.Name
		}
		out[p] = e
		if e.IsTree() {
			r.flattenTree(e.SHA, p, out)
		}
	}
}

// updateTreePath returns a new tree sha with the entry at parts set to
// blobSHA (or removed when blobSHA == "").
func (r *Repo) updateTreePath(treeSHA string, parts []string, blobSHA, mode string) string {
	var entries []TreeEntry
	if treeSHA != "" {
		if o := r.get(treeSHA); o != nil && o.Type == "tree" {
			entries = parseTree(o.Data)
		}
	}
	name := parts[0]
	idx := -1
	for i, e := range entries {
		if e.Name == name {
			idx = i
			break
		}
	}
	if len(parts) == 1 {
		if blobSHA == "" {
			if idx >= 0 {
				entries = append(entries[:idx], entries[idx+1:]...)
			}
		} else {
			ne := TreeEntry{Mode: mode, Name: name, SHA: blobSHA}
			if idx >= 0 {
				entries[idx] = ne
			} else {
				entries = append(entries, ne)
			}
		}
	} else {
		var subSHA string
		if idx >= 0 && entries[idx].IsTree() {
			subSHA = entries[idx].SHA
		}
		newSub := r.updateTreePath(subSHA, parts[1:], blobSHA, mode)
		if newSub == "" {
			if idx >= 0 {
				entries = append(entries[:idx], entries[idx+1:]...)
			}
		} else {
			ne := TreeEntry{Mode: "40000", Name: name, SHA: newSub}
			if idx >= 0 {
				entries[idx] = ne
			} else {
				entries = append(entries, ne)
			}
		}
	}
	if len(entries) == 0 {
		return ""
	}
	return r.put("tree", buildTreeData(entries))
}

// isAncestor reports whether anc is reachable from desc (inclusive).
func (r *Repo) isAncestor(anc, desc string) bool {
	if anc == "" || desc == "" {
		return false
	}
	seen := map[string]bool{}
	stack := []string{desc}
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if c == anc {
			return true
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		o := r.get(c)
		if o == nil || o.Type != "commit" {
			continue
		}
		stack = append(stack, parseCommit(o.Data).Parents...)
	}
	return false
}

// commitsBetween returns commits reachable from head but not from base
// (newest first by BFS order), plus count reachable from base not head.
func (r *Repo) commitsBetween(base, head string) (ahead []string, behind int) {
	reach := func(start string) map[string]bool {
		set := map[string]bool{}
		if start == "" {
			return set
		}
		stack := []string{start}
		for len(stack) > 0 {
			c := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if set[c] {
				continue
			}
			o := r.get(c)
			if o == nil || o.Type != "commit" {
				continue
			}
			set[c] = true
			stack = append(stack, parseCommit(o.Data).Parents...)
		}
		return set
	}
	baseSet := reach(base)
	seen := map[string]bool{}
	queue := []string{head}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == "" || seen[c] || baseSet[c] {
			continue
		}
		o := r.get(c)
		if o == nil || o.Type != "commit" {
			continue
		}
		seen[c] = true
		ahead = append(ahead, c)
		queue = append(queue, parseCommit(o.Data).Parents...)
	}
	headSet := reach(head)
	for c := range baseSet {
		if !headSet[c] {
			behind++
		}
	}
	return ahead, behind
}

// ---------------- store ----------------

type Store struct {
	mu           sync.RWMutex
	Users        map[string]*User
	Repos        map[string]*Repo
	RepoByID     map[int64]*Repo
	NextRepoID   int64
	NextUserID   int64
	NextIssueID  int64
	NextCmtID    int64
	DefaultUser  string
	OwnerAdopted bool
	viewCache    map[string][]byte
}

func repoKey(owner, name string) string {
	return strings.ToLower(owner) + "/" + strings.ToLower(name)
}

func NewStore() *Store {
	st := &Store{
		Users:       map[string]*User{},
		Repos:       map[string]*Repo{},
		RepoByID:    map[int64]*Repo{},
		viewCache:   map[string][]byte{},
		NextRepoID:  1,
		NextUserID:  1,
		NextIssueID: 1,
		NextCmtID:   1,
		DefaultUser: firstEnv("MAKEFUN_SHOP_USER", "MAKEFUN_USER", "FORGE_USER", "SHOP_USER", "GITHUB_USER", "MAKEFUN_SHOP_LOGIN"),
	}
	if st.DefaultUser == "" {
		st.DefaultUser = "anvil"
	}
	st.ensureUser(st.DefaultUser)
	return st
}

func (st *Store) ensureUser(login string) *User {
	if u, ok := st.Users[strings.ToLower(login)]; ok {
		return u
	}
	u := &User{Login: login, ID: st.NextUserID}
	st.NextUserID++
	st.Users[strings.ToLower(login)] = u
	return u
}

func (st *Store) user(login string) *User {
	if u, ok := st.Users[strings.ToLower(login)]; ok {
		return u
	}
	return st.ensureUser(login)
}

func (st *Store) findRepo(owner, name string) *Repo {
	if rp := st.Repos[repoKey(owner, name)]; rp != nil {
		return rp
	}
	// The trial harness addresses the token owner's repositories by a login
	// the server cannot know in advance; the first time an existing repo is
	// addressed under a different owner, adopt that login as the token
	// owner's login so REST, git, GraphQL and MCP all agree on it.
	if !st.OwnerAdopted && !strings.EqualFold(owner, st.DefaultUser) {
		if rp := st.Repos[repoKey(st.DefaultUser, name)]; rp != nil {
			st.adoptOwner(owner)
			return rp
		}
	}
	return nil
}

// findRepoExact is findRepo without the owner-adoption side effect: it only
// hits when the repo is stored under exactly this owner. Safe under a read
// lock; callers fall back to findRepo (write lock) on a miss.
func (st *Store) findRepoExact(owner, name string) *Repo {
	return st.Repos[repoKey(owner, name)]
}

// adoptOwner renames the default (token) user to the given login.
// Caller must hold the store lock.
func (st *Store) adoptOwner(newLogin string) {
	old := st.DefaultUser
	if newLogin == "" || strings.EqualFold(newLogin, old) {
		return
	}
	if u, ok := st.Users[strings.ToLower(old)]; ok {
		u.Login = newLogin
		st.Users[strings.ToLower(newLogin)] = u
		delete(st.Users, strings.ToLower(old))
	}
	for key, rp := range st.Repos {
		if strings.EqualFold(rp.Owner, old) {
			rp.Owner = newLogin
			delete(st.Repos, key)
			st.Repos[repoKey(newLogin, rp.Name)] = rp
		}
	}
	st.DefaultUser = newLogin
	st.OwnerAdopted = true
}

// adoptForListing adopts the requested login when it owns no repositories
// yet but the default user does (e.g. GET /users/<login>/repos).
// Caller must hold the store lock.
func (st *Store) adoptForListing(login string) {
	if login == "" || st.OwnerAdopted || strings.EqualFold(login, st.DefaultUser) {
		return
	}
	for _, rp := range st.Repos {
		if strings.EqualFold(rp.Owner, login) {
			return
		}
	}
	for _, rp := range st.Repos {
		if strings.EqualFold(rp.Owner, st.DefaultUser) {
			st.adoptOwner(login)
			return
		}
	}
}

func (st *Store) createRepo(owner, name, desc string, private bool) *Repo {
	st.ensureUser(owner)
	r := &Repo{
		ID:         st.NextRepoID,
		Owner:      owner,
		Name:       name,
		Desc:       desc,
		Private:    private,
		CreatedAt:  now(),
		UpdatedAt:  now(),
		Objects:    map[string]*Obj{},
		Refs:       map[string]string{},
		HeadBranch: "main",
		NextNum:    1,
		Comments:   map[int64]*Comment{},
		CommentSeq: map[int64]int{},
		Labels:     map[string]string{},
	}
	st.NextRepoID++
	st.Repos[repoKey(owner, name)] = r
	st.RepoByID[r.ID] = r
	return r
}

func (st *Store) deleteRepo(owner, name string) bool {
	k := repoKey(owner, name)
	if r, ok := st.Repos[k]; ok {
		delete(st.Repos, k)
		delete(st.RepoByID, r.ID)
		return true
	}
	return false
}

func (r *Repo) findIssue(num int) *Issue {
	for _, is := range r.Issues {
		if is.Number == num {
			return is
		}
	}
	return nil
}

func (r *Repo) openIssuesCount() int {
	n := 0
	for _, is := range r.Issues {
		if is.State == "open" {
			n++
		}
	}
	return n
}

// nodeID builds a GitHub-style global node id (base64 of "Type:id"),
// matching the scheme the GraphQL node() lookup decodes.
func nodeID(typ string, id int64) string {
	return b64encode(typ + ":" + strconv.FormatInt(id, 10))
}

const b64chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

func b64encode(s string) string {
	data := []byte(s)
	var out strings.Builder
	for i := 0; i < len(data); i += 3 {
		var v uint32
		n := 0
		for j := 0; j < 3 && i+j < len(data); j++ {
			v = v<<8 | uint32(data[i+j])
			n++
		}
		v <<= uint(8 * (3 - n))
		for j := 0; j < n+1; j++ {
			out.WriteByte(b64chars[(v>>uint(18-6*j))&0x3f])
		}
		for j := 0; j < 3-n; j++ {
			out.WriteByte('=')
		}
	}
	return out.String()
}

func b64decode(s string) (string, bool) {
	rev := map[byte]int{}
	for i := 0; i < len(b64chars); i++ {
		rev[b64chars[i]] = i
	}
	var out []byte
	var v uint32
	bits := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '=' {
			break
		}
		val, ok := rev[c]
		if !ok {
			return "", false
		}
		v = v<<6 | uint32(val)
		bits += 6
		if bits >= 8 {
			bits -= 8
			out = append(out, byte(v>>uint(bits)))
		}
	}
	return string(out), true
}
