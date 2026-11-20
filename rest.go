package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// ---------------- JSON helpers ----------------

var reqCounter uint64

func newRequestID() string {
	n := atomic.AddUint64(&reqCounter, 1)
	return fmt.Sprintf("%04X:%04X:%04X:%04X", n&0xffff, (n>>16)&0xffff, (n>>32)&0xffff, (n*2654435761)>>16&0xffff)
}

func apiHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("X-OAuth-Scopes", "")
	h.Set("X-Accepted-OAuth-Scopes", "")
	h.Set("X-RateLimit-Limit", "5000")
	h.Set("X-RateLimit-Remaining", "4999")
	h.Set("X-RateLimit-Used", "1")
	h.Set("X-RateLimit-Reset", strconv.FormatInt(now().Unix()+3600, 10))
	h.Set("X-RateLimit-Resource", "core")
	h.Set("X-GitHub-Media-Type", "github.v3")
	rid := newRequestID()
	h.Set("X-Request-Id", rid)
	h.Set("X-GitHub-Request-Id", rid)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	apiHeaders(w)
	w.WriteHeader(status)
	if v != nil {
		data, _ := json.Marshal(v)
		w.Write(data)
	}
}

func errJSON(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]interface{}{"message": msg})
}

func writeRawJSON(w http.ResponseWriter, status int, data []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	apiHeaders(w)
	w.WriteHeader(status)
	w.Write(data)
}

// issueViewKey fingerprints every Issue field that issueView renders, so a
// cached serialization is only reused while the view is unchanged.
func issueViewKey(req *http.Request, rp *Repo, is *Issue) string {
	return issueViewKeyBase(baseURL(req), rp, is)
}

func issueViewKeyBase(base string, rp *Repo, is *Issue) string {
	var sb strings.Builder
	sb.WriteString(base)
	sb.WriteString("|")
	sb.WriteString(strconv.FormatInt(rp.ID, 10))
	sb.WriteString("|")
	sb.WriteString(strconv.Itoa(is.Number))
	sb.WriteString("|")
	sb.WriteString(strconv.FormatInt(is.UpdatedAt.UnixNano(), 10))
	sb.WriteString("|")
	sb.WriteString(is.State)
	sb.WriteString("|")
	sb.WriteString(is.Title)
	sb.WriteString("|")
	sb.WriteString(is.Body)
	sb.WriteString("|")
	sb.WriteString(strconv.Itoa(len(is.Comments)))
	sb.WriteString("|")
	sb.WriteString(strconv.Itoa(len(is.Labels)))
	for _, l := range is.Labels {
		sb.WriteString(",")
		sb.WriteString(l)
	}
	sb.WriteString("|")
	sb.WriteString(strconv.Itoa(len(is.Assignees)))
	if is.Merged {
		sb.WriteString("|merged:")
		sb.WriteString(is.MergeSHA)
	}
	if is.ClosedAt != nil {
		sb.WriteString("|closed")
	}
	return sb.String()
}

// readBodyJSONStrict parses the request body as a JSON object; on malformed
// JSON it writes a 400 (like GitHub's "Problems parsing JSON") and returns
// ok=false.
func readBodyJSONStrict(w http.ResponseWriter, req *http.Request) (map[string]interface{}, bool) {
	out := map[string]interface{}{}
	if req.Body == nil {
		return out, true
	}
	data, err := io.ReadAll(req.Body)
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"message": "Problems parsing JSON"})
		return nil, false
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return out, true
	}
	if err := json.Unmarshal(data, &out); err != nil {
		writeJSON(w, 400, map[string]interface{}{"message": "Problems parsing JSON"})
		return nil, false
	}
	return out, true
}

func readBodyJSON(req *http.Request) map[string]interface{} {
	out := map[string]interface{}{}
	if req.Body == nil {
		return out
	}
	data := make([]byte, 0, 4096)
	buf := make([]byte, 8192)
	for {
		n, err := req.Body.Read(buf)
		data = append(data, buf[:n]...)
		if err != nil {
			break
		}
	}
	if len(data) > 0 {
		json.Unmarshal(data, &out)
	}
	return out
}

func jStr(m map[string]interface{}, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func jBool(m map[string]interface{}, k string) bool {
	if v, ok := m[k].(bool); ok {
		return v
	}
	return false
}

func jArr(m map[string]interface{}, k string) []interface{} {
	if v, ok := m[k].([]interface{}); ok {
		return v
	}
	return nil
}

func jObj(m map[string]interface{}, k string) map[string]interface{} {
	if v, ok := m[k].(map[string]interface{}); ok {
		return v
	}
	return nil
}

func baseURL(req *http.Request) string {
	scheme := "http"
	if req.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + req.Host
}

// ---------------- views ----------------

func userView(st *Store, login string) map[string]interface{} {
	u := st.user(login)
	return map[string]interface{}{
		"login": u.Login, "id": u.ID, "node_id": nodeID("User", u.ID),
		"type": "User", "site_admin": false,
		"avatar_url": "", "html_url": "", "url": "",
	}
}

func repoView(st *Store, req *http.Request, rp *Repo) map[string]interface{} {
	b := baseURL(req)
	api := b + "/api/v3/repos/" + rp.Owner + "/" + rp.Name
	return map[string]interface{}{
		"id": rp.ID, "node_id": nodeID("Repository", rp.ID),
		"name": rp.Name, "full_name": rp.Owner + "/" + rp.Name,
		"owner": userView(st, rp.Owner), "private": rp.Private,
		"html_url": b + "/" + rp.Owner + "/" + rp.Name,
		"description": rp.Desc, "fork": false, "url": api,
		"created_at": fmtTime(rp.CreatedAt), "updated_at": fmtTime(rp.UpdatedAt),
		"pushed_at": fmtTime(rp.UpdatedAt), "homepage": nil,
		"size": 0, "stargazers_count": 0, "watchers_count": 0, "watchers": 0,
		"language": nil, "forks_count": 0, "forks": 0, "open_issues_count": rp.openIssuesCount(),
		"open_issues": rp.openIssuesCount(), "default_branch": rp.HeadBranch,
		"visibility": map[bool]string{true: "private", false: "public"}[rp.Private],
		"topics": []string{}, "archived": false, "disabled": false,
		"license": nil, "allow_forking": true,
		"git_url": "git://" + req.Host + "/" + rp.Owner + "/" + rp.Name + ".git",
		"ssh_url": "git@" + req.Host + ":" + rp.Owner + "/" + rp.Name + ".git",
		"clone_url": b + "/" + rp.Owner + "/" + rp.Name + ".git",
		"svn_url": b + "/" + rp.Owner + "/" + rp.Name,
	}
}

func identView(name, email string, ts int64) map[string]interface{} {
	return map[string]interface{}{
		"name": name, "email": email,
		"date": fmtTime(time.Unix(ts, 0).UTC()),
	}
}

func commitView(st *Store, req *http.Request, rp *Repo, sha string) map[string]interface{} {
	b := baseURL(req)
	api := b + "/api/v3/repos/" + rp.Owner + "/" + rp.Name
	o := rp.get(sha)
	if o == nil || o.Type != "commit" {
		return nil
	}
	ci := parseCommit(o.Data)
	parents := []interface{}{}
	for _, p := range ci.Parents {
		parents = append(parents, map[string]interface{}{
			"sha": p, "url": api + "/commits/" + p, "html_url": b + "/" + rp.Owner + "/" + rp.Name + "/commit/" + p,
		})
	}
	var authorUser, committerUser interface{}
	if u, ok := st.Users[strings.ToLower(ci.AuthorName)]; ok {
		authorUser = userView(st, u.Login)
	}
	if u, ok := st.Users[strings.ToLower(ci.CommitterName)]; ok {
		committerUser = userView(st, u.Login)
	}
	return map[string]interface{}{
		"sha": sha, "node_id": gid("Commit", strconv.FormatInt(rp.ID, 10)+":"+sha),
		"url": api + "/commits/" + sha, "html_url": b + "/" + rp.Owner + "/" + rp.Name + "/commit/" + sha,
		"commit": map[string]interface{}{
			"author": identView(ci.AuthorName, ci.AuthorEmail, ci.AuthorTime),
			"committer": identView(ci.CommitterName, ci.CommitterEmail, ci.CommitterTime),
			"message": strings.TrimRight(ci.Message, "\n"),
			"tree": map[string]interface{}{"sha": ci.Tree, "url": api + "/git/trees/" + ci.Tree},
			"comment_count": 0,
			"verification": map[string]interface{}{"verified": false, "reason": "unsigned", "signature": nil, "payload": nil},
		},
		"author": authorUser, "committer": committerUser, "parents": parents,
	}
}

func gitCommitView(req *http.Request, rp *Repo, sha string) map[string]interface{} {
	b := baseURL(req)
	api := b + "/api/v3/repos/" + rp.Owner + "/" + rp.Name
	o := rp.get(sha)
	if o == nil || o.Type != "commit" {
		return nil
	}
	ci := parseCommit(o.Data)
	parents := []interface{}{}
	for _, p := range ci.Parents {
		parents = append(parents, map[string]interface{}{"sha": p, "url": api + "/git/commits/" + p})
	}
	return map[string]interface{}{
		"sha": sha, "node_id": gid("Commit", strconv.FormatInt(rp.ID, 10)+":"+sha),
		"url": api + "/git/commits/" + sha,
		"author": identView(ci.AuthorName, ci.AuthorEmail, ci.AuthorTime),
		"committer": identView(ci.CommitterName, ci.CommitterEmail, ci.CommitterTime),
		"message": strings.TrimRight(ci.Message, "\n"),
		"tree": map[string]interface{}{"sha": ci.Tree, "url": api + "/git/trees/" + ci.Tree},
		"parents": parents,
		"verification": map[string]interface{}{"verified": false, "reason": "unsigned", "signature": nil, "payload": nil},
	}
}

func labelView(rp *Repo, name string) map[string]interface{} {
	color := rp.Labels[name]
	if color == "" {
		color = "ededed"
	}
	return map[string]interface{}{
		"id": int64(len(name)) + rp.ID, "node_id": gid("Label", strconv.FormatInt(rp.ID, 10)+":"+name),
		"name": name, "color": color, "default": false, "description": nil,
	}
}

func issueView(st *Store, req *http.Request, rp *Repo, is *Issue) map[string]interface{} {
	b := baseURL(req)
	api := b + "/api/v3/repos/" + rp.Owner + "/" + rp.Name
	labels := []interface{}{}
	for _, l := range is.Labels {
		labels = append(labels, labelView(rp, l))
	}
	assignees := []interface{}{}
	for _, a := range is.Assignees {
		assignees = append(assignees, userView(st, a))
	}
	var closedAt interface{}
	if is.ClosedAt != nil {
		closedAt = fmtTime(*is.ClosedAt)
	}
	v := map[string]interface{}{
		"id": is.ID, "node_id": nodeID(map[bool]string{true: "PullRequest", false: "Issue"}[is.IsPR], is.ID),
		"url": api + "/issues/" + strconv.Itoa(is.Number),
		"repository_url": api, "number": is.Number,
		"state": is.State, "title": is.Title, "body": is.Body,
		"user": userView(st, is.User), "labels": labels,
		"assignee": nil, "assignees": assignees, "milestone": nil,
		"comments": len(is.Comments), "locked": false,
		"created_at": fmtTime(is.CreatedAt), "updated_at": fmtTime(is.UpdatedAt),
		"closed_at": closedAt,
		"html_url": b + "/" + rp.Owner + "/" + rp.Name + "/issues/" + strconv.Itoa(is.Number),
		"author_association": "OWNER",
	}
	if len(is.Assignees) > 0 {
		v["assignee"] = userView(st, is.Assignees[0])
	}
	if is.IsPR {
		v["pull_request"] = map[string]interface{}{
			"url": api + "/pulls/" + strconv.Itoa(is.Number),
			"html_url": b + "/" + rp.Owner + "/" + rp.Name + "/pull/" + strconv.Itoa(is.Number),
			"diff_url": b + "/" + rp.Owner + "/" + rp.Name + "/pull/" + strconv.Itoa(is.Number) + ".diff",
			"patch_url": b + "/" + rp.Owner + "/" + rp.Name + "/pull/" + strconv.Itoa(is.Number) + ".patch",
		}
	}
	return v
}

func commentView(st *Store, req *http.Request, rp *Repo, issueNum int, c *Comment) map[string]interface{} {
	b := baseURL(req)
	api := b + "/api/v3/repos/" + rp.Owner + "/" + rp.Name
	return map[string]interface{}{
		"id": c.ID, "node_id": nodeID("IssueComment", c.ID),
		"url": api + "/issues/comments/" + strconv.FormatInt(c.ID, 10),
		"html_url": b + "/" + rp.Owner + "/" + rp.Name + "/issues/" + strconv.Itoa(issueNum) + "#issuecomment-" + strconv.FormatInt(c.ID, 10),
		"issue_url": api + "/issues/" + strconv.Itoa(issueNum),
		"user": userView(st, c.User), "body": c.Body,
		"created_at": fmtTime(c.CreatedAt), "updated_at": fmtTime(c.UpdatedAt),
		"author_association": "OWNER",
	}
}

func refSideView(st *Store, req *http.Request, rp *Repo, label, refName string) map[string]interface{} {
	sha := rp.resolveRefish(refName)
	repoV := interface{}(nil)
	if sha != "" {
		repoV = repoView(st, req, rp)
	}
	owner := rp.Owner
	if i := strings.Index(label, ":"); i >= 0 {
		owner = label[:i]
	}
	return map[string]interface{}{
		"label": label, "ref": refName, "sha": sha,
		"user": userView(st, owner), "repo": repoV,
	}
}

func pullView(st *Store, req *http.Request, rp *Repo, is *Issue) map[string]interface{} {
	v := issueView(st, req, rp, is)
	b := baseURL(req)
	api := b + "/api/v3/repos/" + rp.Owner + "/" + rp.Name
	headSHA := rp.resolveRefish(is.HeadRef)
	baseSHA := rp.resolveRefish(is.BaseRef)
	v["url"] = api + "/pulls/" + strconv.Itoa(is.Number)
	v["html_url"] = b + "/" + rp.Owner + "/" + rp.Name + "/pull/" + strconv.Itoa(is.Number)
	v["diff_url"] = b + "/" + rp.Owner + "/" + rp.Name + "/pull/" + strconv.Itoa(is.Number) + ".diff"
	v["patch_url"] = b + "/" + rp.Owner + "/" + rp.Name + "/pull/" + strconv.Itoa(is.Number) + ".patch"
	v["head"] = refSideView(st, req, rp, rp.Owner+":"+is.HeadRef, is.HeadRef)
	v["base"] = refSideView(st, req, rp, rp.Owner+":"+is.BaseRef, is.BaseRef)
	v["merged"] = is.Merged
	v["merged_at"] = nil
	if is.MergedAt != nil {
		v["merged_at"] = fmtTime(*is.MergedAt)
	}
	v["merged_by"] = nil
	if is.MergedBy != "" {
		v["merged_by"] = userView(st, is.MergedBy)
	}
	v["merge_commit_sha"] = nil
	if is.MergeSHA != "" {
		v["merge_commit_sha"] = is.MergeSHA
	}
	mergeable := false
	mergeState := "unknown"
	if is.Merged {
		mergeable = true
		mergeState = "clean"
	} else if headSHA != "" && baseSHA != "" {
		if ok, conflict := mergeCheck(rp, baseSHA, headSHA); ok {
			mergeable = true
			mergeState = "clean"
		} else if conflict {
			mergeState = "dirty"
		} else {
			mergeable = true
			mergeState = "clean"
		}
	}
	v["mergeable"] = mergeable
	v["mergeable_state"] = mergeState
	v["rebaseable"] = mergeable
	commits, _ := rp.commitsBetween(baseSHA, headSHA)
	v["commits"] = len(commits)
	files := diffTrees(rp, rp.treeOfCommit(baseSHA), rp.treeOfCommit(headSHA))
	add, del := 0, 0
	for _, f := range files {
		add += f.Additions
		del += f.Deletions
	}
	v["additions"] = add
	v["deletions"] = del
	v["changed_files"] = len(files)
	v["comments"] = len(is.Comments)
	v["review_comments"] = 0
	v["draft"] = false
	v["author_association"] = "OWNER"
	v["_links"] = map[string]interface{}{}
	return v
}

// ---------------- diff ----------------

type fileDiff struct {
	Filename  string
	Status    string
	Additions int
	Deletions int
	Changes   int
	Patch     string
	SHA       string
	PrevName  string
}

func splitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	s := string(data)
	lines := strings.SplitAfter(s, "\n")
	out := lines[:0]
	for _, l := range lines {
		out = append(out, strings.TrimRight(l, "\n"))
	}
	if len(out) > 0 && out[len(out)-1] == "" && !strings.HasSuffix(s, "\n\n") {
		// keep semantics simple: drop trailing empty from final newline
		if strings.HasSuffix(s, "\n") {
			out = out[:len(out)-1]
		}
	}
	return out
}

func isBinary(data []byte) bool {
	for _, c := range data {
		if c == 0 {
			return true
		}
	}
	return false
}

// lcsOps returns edit ops: 0=equal, -1=delete, +1=insert.
func lcsOps(a, b []string) [][2]int {
	n, m := len(a), len(b)
	if n*m > 4_000_000 {
		// too big: treat as full replace
		ops := [][2]int{}
		for i := 0; i < n; i++ {
			ops = append(ops, [2]int{-1, i})
		}
		for j := 0; j < m; j++ {
			ops = append(ops, [2]int{1, j})
		}
		return ops
	}
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var ops [][2]int
	i, j := 0, 0
	for i < n && j < m {
		if a[i] == b[j] {
			ops = append(ops, [2]int{0, i})
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			ops = append(ops, [2]int{-1, i})
			i++
		} else {
			ops = append(ops, [2]int{1, j})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, [2]int{-1, i})
	}
	for ; j < m; j++ {
		ops = append(ops, [2]int{1, j})
	}
	return ops
}

func makePatch(path string, a, b []string) (string, int, int) {
	ops := lcsOps(a, b)
	add, del := 0, 0
	for _, op := range ops {
		if op[0] == 1 {
			add++
		} else if op[0] == -1 {
			del++
		}
	}
	if add == 0 && del == 0 {
		return "", 0, 0
	}
	var sb strings.Builder
	sb.WriteString("--- a/" + path + "\n+++ b/" + path + "\n")
	sb.WriteString(fmt.Sprintf("@@ -1,%d +1,%d @@\n", len(a), len(b)))
	for _, op := range ops {
		switch op[0] {
		case 0:
			sb.WriteString(" " + a[op[1]] + "\n")
		case -1:
			sb.WriteString("-" + a[op[1]] + "\n")
		case 1:
			sb.WriteString("+" + b[op[1]] + "\n")
		}
	}
	return sb.String(), add, del
}

func diffTrees(rp *Repo, baseTree, headTree string) []fileDiff {
	baseFlat := map[string]TreeEntry{}
	headFlat := map[string]TreeEntry{}
	if baseTree != "" {
		rp.flattenTree(baseTree, "", baseFlat)
	}
	if headTree != "" {
		rp.flattenTree(headTree, "", headFlat)
	}
	paths := map[string]bool{}
	for p, e := range baseFlat {
		if !e.IsTree() {
			paths[p] = true
		}
	}
	for p, e := range headFlat {
		if !e.IsTree() {
			paths[p] = true
		}
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	var out []fileDiff
	for _, p := range sorted {
		be, inBase := baseFlat[p]
		he, inHead := headFlat[p]
		fd := fileDiff{Filename: p}
		switch {
		case inBase && !inHead:
			fd.Status = "removed"
			fd.SHA = be.SHA
			data := blobData(rp, be.SHA)
			if !isBinary(data) {
				_, a, d := makePatch(p, splitLines(data), nil)
				fd.Additions, fd.Deletions = a, d
				fd.Patch, _, _ = makePatch(p, splitLines(data), nil)
			}
		case !inBase && inHead:
			fd.Status = "added"
			fd.SHA = he.SHA
			data := blobData(rp, he.SHA)
			if !isBinary(data) {
				fd.Patch, fd.Additions, fd.Deletions = makePatch(p, nil, splitLines(data))
			}
		case be.SHA != he.SHA || be.Mode != he.Mode:
			fd.Status = "modified"
			fd.SHA = he.SHA
			bd, hd := blobData(rp, be.SHA), blobData(rp, he.SHA)
			if !isBinary(bd) && !isBinary(hd) {
				fd.Patch, fd.Additions, fd.Deletions = makePatch(p, splitLines(bd), splitLines(hd))
			}
		default:
			continue
		}
		fd.Changes = fd.Additions + fd.Deletions
		out = append(out, fd)
	}
	return out
}

func blobData(rp *Repo, sha string) []byte {
	if o := rp.get(sha); o != nil {
		return o.Data
	}
	return nil
}

func fileDiffView(req *http.Request, rp *Repo, fd fileDiff) map[string]interface{} {
	b := baseURL(req)
	v := map[string]interface{}{
		"sha": fd.SHA, "filename": fd.Filename, "status": fd.Status,
		"additions": fd.Additions, "deletions": fd.Deletions, "changes": fd.Changes,
		"blob_url": b + "/api/v3/repos/" + rp.Owner + "/" + rp.Name + "/git/blobs/" + fd.SHA,
		"raw_url": b + "/" + rp.Owner + "/" + rp.Name + "/raw/" + fd.SHA,
		"contents_url": b + "/api/v3/repos/" + rp.Owner + "/" + rp.Name + "/contents/" + fd.Filename,
	}
	if fd.Patch != "" {
		v["patch"] = fd.Patch
	}
	return v
}

// mergeCheck: returns (mergeable, conflict). Uses 3-way tree merge against
// the merge base; when base is an ancestor of head it is always mergeable.
func mergeCheck(rp *Repo, baseSHA, headSHA string) (bool, bool) {
	if baseSHA == "" || headSHA == "" {
		return false, false
	}
	if rp.isAncestor(baseSHA, headSHA) || rp.isAncestor(headSHA, baseSHA) {
		return true, false
	}
	mb := mergeBase(rp, baseSHA, headSHA)
	_, conflict := threeWayTree(rp, rp.treeOfCommit(mb), rp.treeOfCommit(baseSHA), rp.treeOfCommit(headSHA))
	return !conflict, conflict
}

func mergeBase(rp *Repo, a, b string) string {
	ancA := map[string]bool{}
	stack := []string{a}
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if ancA[c] {
			continue
		}
		ancA[c] = true
		if o := rp.get(c); o != nil && o.Type == "commit" {
			stack = append(stack, parseCommit(o.Data).Parents...)
		}
	}
	// BFS from b, first commit in ancA wins
	seen := map[string]bool{}
	queue := []string{b}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if seen[c] {
			continue
		}
		seen[c] = true
		if ancA[c] {
			return c
		}
		if o := rp.get(c); o != nil && o.Type == "commit" {
			queue = append(queue, parseCommit(o.Data).Parents...)
		}
	}
	return ""
}

// threeWayTree merges ours (base side) and theirs (head side) relative to
// ancestor tree. Returns merged flat map and conflict flag.
func threeWayTree(rp *Repo, ancTree, oursTree, theirsTree string) (map[string]TreeEntry, bool) {
	anc := map[string]TreeEntry{}
	ours := map[string]TreeEntry{}
	theirs := map[string]TreeEntry{}
	if ancTree != "" {
		rp.flattenTree(ancTree, "", anc)
	}
	if oursTree != "" {
		rp.flattenTree(oursTree, "", ours)
	}
	if theirsTree != "" {
		rp.flattenTree(theirsTree, "", theirs)
	}
	paths := map[string]bool{}
	for p := range anc {
		paths[p] = true
	}
	for p := range ours {
		paths[p] = true
	}
	for p := range theirs {
		paths[p] = true
	}
	out := map[string]TreeEntry{}
	conflict := false
	for p := range paths {
		ae, aok := anc[p]
		oe, ook := ours[p]
		te, tok := theirs[p]
		if ae.IsTree() || oe.IsTree() || te.IsTree() {
			continue // directories handled implicitly by file paths
		}
		switch {
		case ook && tok && oe.SHA == te.SHA:
			out[p] = oe
		case aok && ook && ae.SHA == oe.SHA && !tok:
			// deleted by theirs, unchanged in ours -> deleted
		case aok && tok && ae.SHA == te.SHA && !ook:
			// deleted by ours, unchanged in theirs -> deleted
		case aok && ook && tok && ae.SHA == oe.SHA:
			out[p] = te // only theirs changed
		case aok && ook && tok && ae.SHA == te.SHA:
			out[p] = oe // only ours changed
		case aok && ook && tok:
			conflict = true // both sides changed the same path differently
			out[p] = oe
		case !aok && ook && !tok:
			out[p] = oe
		case !aok && !ook && tok:
			out[p] = te
		case !aok && ook && tok && oe.SHA != te.SHA:
			conflict = true
			out[p] = te
		case aok && !ook && !tok:
			// deleted both sides
		case aok && ook && !tok && ae.SHA != oe.SHA:
			conflict = true // modified in ours, deleted in theirs
		case aok && !ook && tok && ae.SHA != te.SHA:
			conflict = true
		default:
			if ook {
				out[p] = oe
			} else if tok {
				out[p] = te
			}
		}
	}
	return out, conflict
}

// buildTreeFromFlat creates nested trees from a flat path->entry map.
func buildTreeFromFlat(rp *Repo, flat map[string]TreeEntry) string {
	root := map[string]TreeEntry{}
	dirs := map[string]map[string]TreeEntry{}
	for p, e := range flat {
		if e.IsTree() {
			continue
		}
		parts := strings.Split(p, "/")
		if len(parts) == 1 {
			root[p] = e
		} else {
			d := parts[0]
			if dirs[d] == nil {
				dirs[d] = map[string]TreeEntry{}
			}
			dirs[d][strings.Join(parts[1:], "/")] = e
		}
	}
	entries := []TreeEntry{}
	for name, e := range root {
		entries = append(entries, e)
		_ = name
	}
	for d, sub := range dirs {
		sha := buildTreeFromFlat(rp, sub)
		entries = append(entries, TreeEntry{Mode: "40000", Name: d, SHA: sha})
	}
	if len(entries) == 0 {
		return rp.put("tree", []byte{})
	}
	return rp.put("tree", buildTreeData(entries))
}
