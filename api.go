package main

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

func paginate(req *http.Request, n int) (int, int) {
	q := req.URL.Query()
	pp, _ := strconv.Atoi(q.Get("per_page"))
	if pp <= 0 {
		pp = 30
	}
	if pp > 100 {
		pp = 100
	}
	page, _ := strconv.Atoi(q.Get("page"))
	if page <= 0 {
		page = 1
	}
	start := (page - 1) * pp
	if start > n {
		start = n
	}
	end := start + pp
	if end > n {
		end = n
	}
	return start, end
}

func pageSlice[T any](req *http.Request, items []T) []T {
	s, e := paginate(req, len(items))
	return items[s:e]
}

// paginationLinks emits GitHub-style Link headers (rel next/last/first/prev)
// for a list endpoint, based on the request's page/per_page parameters.
func paginationLinks(w http.ResponseWriter, req *http.Request, total int) {
	q := req.URL.Query()
	pp, _ := strconv.Atoi(q.Get("per_page"))
	if pp <= 0 {
		pp = 30
	}
	if pp > 100 {
		pp = 100
	}
	page, _ := strconv.Atoi(q.Get("page"))
	if page <= 0 {
		page = 1
	}
	lastPage := (total + pp - 1) / pp
	if lastPage < 1 {
		lastPage = 1
	}
	mk := func(p int) string {
		return "<" + baseURL(req) + req.URL.Path + "?page=" + strconv.Itoa(p) + "&per_page=" + strconv.Itoa(pp) + ">; rel="
	}
	var links []string
	if page < lastPage {
		links = append(links, mk(page+1)+`"next"`, mk(lastPage)+`"last"`)
	}
	if page > 1 {
		links = append(links, mk(1)+`"first"`, mk(page-1)+`"prev"`)
	}
	if len(links) > 0 {
		w.Header().Set("Link", strings.Join(links, ", "))
	}
}

// handleREST routes REST API calls. seg is the path split into segments
// (API prefix already stripped).
func (s *Server) handleREST(w http.ResponseWriter, req *http.Request, seg []string) {
	st := s.store
	st.mu.Lock()
	defer st.mu.Unlock()
	m := req.Method
	if len(seg) == 0 {
		writeJSON(w, 200, map[string]interface{}{"current_user_url": baseURL(req) + "/api/v3/user"})
		return
	}
	switch seg[0] {
	case "meta":
		writeJSON(w, 200, map[string]interface{}{
			"installed_version":                    "3.13.0",
			"verifiable_password_authentication": false,
			"ssh_key_fingerprints": map[string]interface{}{
				"SHA256_RSA":     "uNiVztksCsDhcc0u9e8BujQXVUpKZIDTMczCvj3tD2s",
				"SHA256_ECDSA":   "p2QAMXNIC1TJYWeIOttrVc98/R1BUFWu3/LiyKgUfQM",
				"SHA256_ED25519": "+DiY3wvvV6TuJJhbpZisF/zLDA0zPMSvHdkr4UvCOqU",
			},
			"ssh_keys": []interface{}{},
			"hooks":    []interface{}{},
			"web":      []interface{}{},
			"api":      []interface{}{},
			"git":      []interface{}{},
			"packages": []interface{}{},
			"pages":    []interface{}{},
			"importer": []interface{}{},
			"actions":  []interface{}{},
			"dependabot": []interface{}{},
		})
		return
	case "rate_limit":
		writeJSON(w, 200, map[string]interface{}{
			"resources": map[string]interface{}{
				"core":    map[string]interface{}{"limit": 5000, "remaining": 4999, "reset": now().Unix() + 3600, "used": 1},
				"search":  map[string]interface{}{"limit": 30, "remaining": 30, "reset": now().Unix() + 60, "used": 0},
				"graphql": map[string]interface{}{"limit": 5000, "remaining": 5000, "reset": now().Unix() + 3600, "used": 0},
			},
			"rate": map[string]interface{}{"limit": 5000, "remaining": 4999, "reset": now().Unix() + 3600, "used": 1},
		})
		return
	case "user":
		if len(seg) == 1 {
			writeJSON(w, 200, userView(st, st.DefaultUser))
			return
		}
		if seg[1] == "repos" {
			if m == "GET" {
				s.listRepos(w, req, st.DefaultUser)
				return
			}
			if m == "POST" {
				s.createRepo(w, req, st.DefaultUser)
				return
			}
		}
	case "users":
		if len(seg) == 3 && seg[2] == "repos" && m == "GET" {
			s.listRepos(w, req, seg[1])
			return
		}
		if len(seg) == 2 && m == "GET" {
			writeJSON(w, 200, userView(st, seg[1]))
			return
		}
	case "orgs":
		if len(seg) == 3 && seg[2] == "repos" {
			if m == "GET" {
				s.listRepos(w, req, seg[1])
				return
			}
			if m == "POST" {
				s.createRepo(w, req, seg[1])
				return
			}
		}
	case "search":
		if len(seg) == 2 {
			s.handleSearch(w, req, seg[1])
			return
		}
	case "repos":
		if len(seg) >= 3 {
			s.handleRepo(w, req, seg[1], seg[2], seg[3:])
			return
		}
	}
	errJSON(w, 404, "Not Found")
}

func (s *Server) listRepos(w http.ResponseWriter, req *http.Request, owner string) {
	st := s.store
	st.adoptForListing(owner)
	var out []map[string]interface{}
	var repos []*Repo
	for _, rp := range st.Repos {
		if strings.EqualFold(rp.Owner, owner) {
			repos = append(repos, rp)
		}
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].ID < repos[j].ID })
	for _, rp := range pageSlice(req, repos) {
		out = append(out, repoView(st, req, rp))
	}
	if out == nil {
		out = []map[string]interface{}{}
	}
	paginationLinks(w, req, len(repos))
	writeJSON(w, 200, out)
}

func (s *Server) createRepo(w http.ResponseWriter, req *http.Request, owner string) {
	st := s.store
	body, ok := readBodyJSONStrict(w, req)
	if !ok {
		return
	}
	name := jStr(body, "name")
	if name == "" {
		writeJSON(w, 422, map[string]interface{}{"message": "Repository creation failed.", "errors": []interface{}{map[string]interface{}{"resource": "Repository", "code": "custom", "field": "name", "message": "name is required"}}})
		return
	}
	if st.findRepo(owner, name) != nil {
		writeJSON(w, 422, map[string]interface{}{"message": "Repository creation failed.", "errors": []interface{}{map[string]interface{}{"resource": "Repository", "code": "custom", "field": "name", "message": "name already exists on this account"}}})
		return
	}
	rp := st.createRepo(owner, name, jStr(body, "description"), jBool(body, "private"))
	if db := jStr(body, "default_branch"); db != "" {
		rp.HeadBranch = db
	}
	if jBool(body, "auto_init") {
		u := st.user(owner)
		blob := rp.put("blob", []byte("# "+name+"\n"))
		tree := rp.put("tree", buildTreeData([]TreeEntry{{Mode: "100644", Name: "README.md", SHA: blob}}))
		t := now()
		c := rp.put("commit", buildCommitData(tree, nil, u.Login, u.Login+"@users.noreply.github.com", t.Unix(), "+0000", u.Login, u.Login+"@users.noreply.github.com", t.Unix(), "+0000", "Initial commit"))
		rp.Refs["refs/heads/"+rp.HeadBranch] = c
	}
	writeJSON(w, 201, repoView(st, req, rp))
}

func (s *Server) handleRepo(w http.ResponseWriter, req *http.Request, owner, name string, rest []string) {
	st := s.store
	rp := st.findRepo(owner, name)
	m := req.Method
	if len(rest) == 0 {
		if rp == nil {
			errJSON(w, 404, "Not Found")
			return
		}
		switch m {
		case "GET":
			writeJSON(w, 200, repoView(st, req, rp))
		case "PATCH":
			body := readBodyJSON(req)
			if v := jStr(body, "description"); body["description"] != nil {
				rp.Desc = v
			}
			if body["private"] != nil {
				rp.Private = jBool(body, "private")
			}
			if v := jStr(body, "default_branch"); v != "" {
				rp.HeadBranch = v
			}
			if v := jStr(body, "name"); v != "" && v != rp.Name {
				delete(st.Repos, repoKey(rp.Owner, rp.Name))
				rp.Name = v
				st.Repos[repoKey(rp.Owner, rp.Name)] = rp
			}
			rp.UpdatedAt = now()
			writeJSON(w, 200, repoView(st, req, rp))
		case "DELETE":
			st.deleteRepo(owner, name)
			w.WriteHeader(204)
		default:
			errJSON(w, 404, "Not Found")
		}
		return
	}
	if rp == nil {
		errJSON(w, 404, "Not Found")
		return
	}
	switch rest[0] {
	case "branches":
		s.repoBranches(w, req, rp, rest[1:])
	case "tags":
		s.repoTags(w, req, rp)
	case "commits":
		s.repoCommits(w, req, rp, rest[1:])
	case "compare":
		if len(rest) >= 2 {
			s.repoCompare(w, req, rp, strings.Join(rest[1:], "/"))
			return
		}
		errJSON(w, 404, "Not Found")
	case "git":
		s.repoGit(w, req, rp, rest[1:])
	case "contents":
		s.repoContents(w, req, rp, rest[1:])
	case "issues":
		s.repoIssues(w, req, rp, rest[1:])
	case "labels":
		s.repoLabels(w, req, rp, rest[1:])
	case "pulls":
		s.repoPulls(w, req, rp, rest[1:])
	default:
		errJSON(w, 404, "Not Found")
	}
}

func (s *Server) repoBranches(w http.ResponseWriter, req *http.Request, rp *Repo, rest []string) {
	st := s.store
	if len(rest) > 0 {
		branch := strings.Join(rest, "/")
		sha := rp.Refs["refs/heads/"+branch]
		if sha == "" {
			errJSON(w, 404, "Not Found")
			return
		}
		cv := commitView(st, req, rp, sha)
		writeJSON(w, 200, map[string]interface{}{
			"name": branch, "commit": cv, "protected": false,
			"_links": map[string]interface{}{},
		})
		return
	}
	var names []string
	for ref := range rp.Refs {
		if strings.HasPrefix(ref, "refs/heads/") {
			names = append(names, strings.TrimPrefix(ref, "refs/heads/"))
		}
	}
	sort.Strings(names)
	b := baseURL(req)
	out := []interface{}{}
	for _, n := range pageSlice(req, names) {
		sha := rp.Refs["refs/heads/"+n]
		out = append(out, map[string]interface{}{
			"name": n, "protected": false,
			"commit": map[string]interface{}{"sha": sha, "url": b + "/api/v3/repos/" + rp.Owner + "/" + rp.Name + "/commits/" + sha},
		})
	}
	writeJSON(w, 200, out)
}

func (s *Server) repoTags(w http.ResponseWriter, req *http.Request, rp *Repo) {
	var names []string
	for ref := range rp.Refs {
		if strings.HasPrefix(ref, "refs/tags/") {
			names = append(names, strings.TrimPrefix(ref, "refs/tags/"))
		}
	}
	sort.Strings(names)
	b := baseURL(req)
	out := []interface{}{}
	for _, n := range pageSlice(req, names) {
		sha := rp.Refs["refs/tags/"+n]
		out = append(out, map[string]interface{}{
			"name": n,
			"commit": map[string]interface{}{"sha": rp.peelToCommit(sha), "url": b + "/api/v3/repos/" + rp.Owner + "/" + rp.Name + "/commits/" + rp.peelToCommit(sha)},
			"zipball_url": b + "/api/v3/repos/" + rp.Owner + "/" + rp.Name + "/zipball/" + n,
			"tarball_url": b + "/api/v3/repos/" + rp.Owner + "/" + rp.Name + "/tarball/" + n,
			"node_id": nodeID("Tag", rp.ID),
		})
	}
	writeJSON(w, 200, out)
}

func (s *Server) repoCommits(w http.ResponseWriter, req *http.Request, rp *Repo, rest []string) {
	st := s.store
	if len(rest) > 0 {
		sha := rp.resolveRefish(rest[0])
		if sha == "" {
			errJSON(w, 404, "Not Found")
			return
		}
		cv := commitView(st, req, rp, sha)
		if cv == nil {
			errJSON(w, 404, "Not Found")
			return
		}
		// include files changed vs first parent
		ci := parseCommit(rp.get(sha).Data)
		var baseTree string
		if len(ci.Parents) > 0 {
			baseTree = rp.treeOfCommit(ci.Parents[0])
		}
		files := diffTrees(rp, baseTree, ci.Tree)
		fv := []interface{}{}
		for _, f := range files {
			fv = append(fv, fileDiffView(req, rp, f))
		}
		cv["files"] = fv
		writeJSON(w, 200, cv)
		return
	}
	q := req.URL.Query()
	refish := q.Get("sha")
	if refish == "" {
		refish = rp.HeadBranch
	}
	head := rp.resolveRefish(refish)
	if head == "" {
		writeJSON(w, 200, []interface{}{})
		return
	}
	pathFilter := q.Get("path")
	var since, until int64
	if v := q.Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			since = t.Unix()
		}
	}
	if v := q.Get("until"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			until = t.Unix()
		}
	}
	authorFilter := strings.ToLower(q.Get("author"))
	// BFS newest-first
	var order []string
	seen := map[string]bool{}
	queue := []string{head}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == "" || seen[c] {
			continue
		}
		o := rp.get(c)
		if o == nil || o.Type != "commit" {
			continue
		}
		seen[c] = true
		order = append(order, c)
		queue = append(queue, parseCommit(o.Data).Parents...)
	}
	out := []interface{}{}
	for _, sha := range order {
		ci := parseCommit(rp.get(sha).Data)
		if since > 0 && ci.CommitterTime < since {
			continue
		}
		if until > 0 && ci.CommitterTime > until {
			continue
		}
		if authorFilter != "" && !strings.Contains(strings.ToLower(ci.AuthorName), authorFilter) && !strings.Contains(strings.ToLower(ci.AuthorEmail), authorFilter) {
			continue
		}
		if pathFilter != "" {
			var parentTree string
			if len(ci.Parents) > 0 {
				parentTree = rp.treeOfCommit(ci.Parents[0])
			}
			files := diffTrees(rp, parentTree, ci.Tree)
			found := false
			for _, f := range files {
				if f.Filename == pathFilter {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		out = append(out, commitView(st, req, rp, sha))
	}
	start, end := paginate(req, len(out))
	writeJSON(w, 200, out[start:end])
}

func (s *Server) repoCompare(w http.ResponseWriter, req *http.Request, rp *Repo, spec string) {
	st := s.store
	parts := strings.SplitN(spec, "...", 2)
	if len(parts) != 2 {
		errJSON(w, 404, "Not Found")
		return
	}
	baseSHA := rp.resolveRefish(parts[0])
	headSHA := rp.resolveRefish(parts[1])
	if baseSHA == "" || headSHA == "" {
		errJSON(w, 404, "Not Found")
		return
	}
	ahead, behind := rp.commitsBetween(baseSHA, headSHA)
	status := "identical"
	switch {
	case len(ahead) > 0 && behind > 0:
		status = "diverged"
	case len(ahead) > 0:
		status = "ahead"
	case behind > 0:
		status = "behind"
	}
	commits := []interface{}{}
	// ahead list is BFS order; reverse for chronological
	for i := len(ahead) - 1; i >= 0; i-- {
		commits = append(commits, commitView(st, req, rp, ahead[i]))
	}
	files := diffTrees(rp, rp.treeOfCommit(baseSHA), rp.treeOfCommit(headSHA))
	fv := []interface{}{}
	for _, f := range files {
		fv = append(fv, fileDiffView(req, rp, f))
	}
	b := baseURL(req)
	writeJSON(w, 200, map[string]interface{}{
		"url": b + "/api/v3/repos/" + rp.Owner + "/" + rp.Name + "/compare/" + spec,
		"html_url": b + "/" + rp.Owner + "/" + rp.Name + "/compare/" + spec,
		"permalink_url": b + "/" + rp.Owner + "/" + rp.Name + "/compare/" + baseSHA + "..." + headSHA,
		"diff_url": b + "/" + rp.Owner + "/" + rp.Name + "/compare/" + spec + ".diff",
		"patch_url": b + "/" + rp.Owner + "/" + rp.Name + "/compare/" + spec + ".patch",
		"base_commit": commitView(st, req, rp, baseSHA),
		"merge_base_commit": commitView(st, req, rp, mergeBase(rp, baseSHA, headSHA)),
		"status": status, "ahead_by": len(ahead), "behind_by": behind,
		"total_commits": len(ahead), "commits": commits, "files": fv,
	})
}
