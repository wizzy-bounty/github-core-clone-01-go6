package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------------- git database API ----------------

func refView(req *http.Request, rp *Repo, ref string) map[string]interface{} {
	b := baseURL(req)
	sha := rp.Refs[ref]
	typ := "commit"
	if o := rp.get(sha); o != nil {
		typ = o.Type
	}
	return map[string]interface{}{
		"ref": ref, "node_id": gid("Ref", strconv.FormatInt(rp.ID, 10)+":"+ref),
		"url": b + "/api/v3/repos/" + rp.Owner + "/" + rp.Name + "/git/refs/" + strings.TrimPrefix(ref, "refs/"),
		"object": map[string]interface{}{
			"sha": sha, "type": typ,
			"url": b + "/api/v3/repos/" + rp.Owner + "/" + rp.Name + "/git/" + map[string]string{"commit": "commits", "tree": "trees", "blob": "blobs", "tag": "tags"}[typ] + "/" + sha,
		},
	}
}

func (s *Server) repoGit(w http.ResponseWriter, req *http.Request, rp *Repo, rest []string) {
	st := s.store
	m := req.Method
	b := baseURL(req)
	api := b + "/api/v3/repos/" + rp.Owner + "/" + rp.Name
	if len(rest) == 0 {
		errJSON(w, 404, "Not Found")
		return
	}
	switch rest[0] {
	case "refs":
		if len(rest) == 1 {
			if m == "GET" {
				out := []interface{}{}
				for _, ref := range sortedRefs(rp) {
					out = append(out, refView(req, rp, ref))
				}
				writeJSON(w, 200, out)
				return
			}
			if m == "POST" {
				body := readBodyJSON(req)
				ref := jStr(body, "ref")
				sha := jStr(body, "sha")
				if ref == "" || sha == "" {
					errJSON(w, 422, "Invalid request.")
					return
				}
				if _, ok := rp.Refs[ref]; ok {
					errJSON(w, 422, "Reference already exists")
					return
				}
				if rp.get(sha) == nil {
					errJSON(w, 422, "Object does not exist")
					return
				}
				rp.Refs[ref] = sha
				rp.UpdatedAt = now()
				writeJSON(w, 201, refView(req, rp, ref))
				return
			}
		}
		// single ref: rest[1:] is ref path after "refs/" (e.g. heads/main)
		ref := "refs/" + strings.Join(rest[1:], "/")
		// also support namespaced listing: GET /git/refs/heads
		if m == "GET" {
			if _, ok := rp.Refs[ref]; ok {
				writeJSON(w, 200, refView(req, rp, ref))
				return
			}
			// prefix listing
			out := []interface{}{}
			for _, r2 := range sortedRefs(rp) {
				if strings.HasPrefix(r2, ref+"/") || strings.HasPrefix(r2, ref) {
					out = append(out, refView(req, rp, r2))
				}
			}
			if len(out) > 0 {
				writeJSON(w, 200, out)
				return
			}
			errJSON(w, 404, "Not Found")
			return
		}
		if m == "PATCH" {
			body := readBodyJSON(req)
			sha := jStr(body, "sha")
			if _, ok := rp.Refs[ref]; !ok {
				errJSON(w, 404, "Not Found")
				return
			}
			if rp.get(sha) == nil {
				errJSON(w, 422, "Object does not exist")
				return
			}
			rp.Refs[ref] = sha
			rp.UpdatedAt = now()
			writeJSON(w, 200, refView(req, rp, ref))
			return
		}
		if m == "DELETE" {
			if _, ok := rp.Refs[ref]; !ok {
				errJSON(w, 404, "Not Found")
				return
			}
			delete(rp.Refs, ref)
			w.WriteHeader(204)
			return
		}
	case "commits":
		if len(rest) == 1 && m == "POST" {
			body := readBodyJSON(req)
			msg := jStr(body, "message")
			tree := jStr(body, "tree")
			if tree == "" || rp.get(tree) == nil {
				errJSON(w, 422, "Invalid tree.")
				return
			}
			var parents []string
			for _, p := range jArr(body, "parents") {
				if ps, ok := p.(string); ok {
					parents = append(parents, ps)
				}
			}
			aName, aEmail, aTime := authorFromBody(jObj(body, "author"), st)
			cName, cEmail, cTime := aName, aEmail, aTime
			if co := jObj(body, "committer"); co != nil {
				cName, cEmail, cTime = authorFromBody(co, st)
			}
			sha := rp.put("commit", buildCommitData(tree, parents, aName, aEmail, aTime, "+0000", cName, cEmail, cTime, "+0000", msg))
			writeJSON(w, 201, gitCommitView(req, rp, sha))
			return
		}
		if len(rest) == 2 && m == "GET" {
			if cv := gitCommitView(req, rp, rest[1]); cv != nil {
				writeJSON(w, 200, cv)
				return
			}
			errJSON(w, 404, "Not Found")
			return
		}
	case "trees":
		if len(rest) == 1 && m == "POST" {
			body := readBodyJSON(req)
			flat := map[string]TreeEntry{}
			if bt := jStr(body, "base_tree"); bt != "" {
				if o := rp.get(bt); o != nil && o.Type == "tree" {
					rp.flattenTree(bt, "", flat)
				}
			}
			for _, e := range jArr(body, "tree") {
				em, ok := e.(map[string]interface{})
				if !ok {
					continue
				}
				path := jStr(em, "path")
				mode := jStr(em, "mode")
				typ := jStr(em, "type")
				sha := jStr(em, "sha")
				if sha == "" {
					if content, ok := em["content"].(string); ok {
						if typ == "tree" {
							sha = rp.put("tree", []byte(content))
						} else {
							sha = rp.put("blob", []byte(content))
						}
					} else {
						delete(flat, path)
						continue
					}
				}
				if mode == "" {
					if typ == "tree" {
						mode = "040000"
					} else {
						mode = "100644"
					}
				}
				flat[path] = TreeEntry{Mode: mode, Name: path, SHA: sha}
			}
			// rebuild: keep only leaf paths; buildTreeFromFlat expects full paths
			leaves := map[string]TreeEntry{}
			for p, e := range flat {
				if !e.IsTree() {
					leaves[p] = TreeEntry{Mode: e.Mode, Name: p, SHA: e.SHA}
				}
			}
			sha := buildTreeFromFlat(rp, leaves)
			writeJSON(w, 201, treeView(req, rp, sha, false))
			return
		}
		if len(rest) == 2 && m == "GET" {
			recursive := req.URL.Query().Get("recursive") == "1" || req.URL.Query().Get("recursive") == "true"
			if v := treeView(req, rp, rest[1], recursive); v != nil {
				writeJSON(w, 200, v)
				return
			}
			errJSON(w, 404, "Not Found")
			return
		}
	case "blobs":
		if len(rest) == 1 && m == "POST" {
			body := readBodyJSON(req)
			content := jStr(body, "content")
			var data []byte
			if jStr(body, "encoding") == "base64" {
				data, _ = base64.StdEncoding.DecodeString(strings.Join(strings.Fields(content), ""))
			} else {
				data = []byte(content)
			}
			sha := rp.put("blob", data)
			writeJSON(w, 201, map[string]interface{}{"sha": sha, "url": api + "/git/blobs/" + sha})
			return
		}
		if len(rest) == 2 && m == "GET" {
			o := rp.get(rest[1])
			if o == nil || o.Type != "blob" {
				errJSON(w, 404, "Not Found")
				return
			}
			enc := base64.StdEncoding.EncodeToString(o.Data)
			// GitHub wraps base64 at 60 chars
			var wrapped strings.Builder
			for i := 0; i < len(enc); i += 60 {
				end := i + 60
				if end > len(enc) {
					end = len(enc)
				}
				wrapped.WriteString(enc[i:end] + "\n")
			}
			writeJSON(w, 200, map[string]interface{}{
				"sha": rest[1], "node_id": gid("Blob", strconv.FormatInt(rp.ID, 10)+":"+rest[1]),
				"url": api + "/git/blobs/" + rest[1],
				"size": len(o.Data), "content": wrapped.String(), "encoding": "base64",
			})
			return
		}
	case "tags":
		if len(rest) == 1 && m == "POST" {
			body := readBodyJSON(req)
			obj := jStr(body, "object")
			typ := jStr(body, "type")
			tag := jStr(body, "tag")
			msg := jStr(body, "message")
			u := st.user(st.DefaultUser)
			t := now()
			var tagger string
			if tg := jObj(body, "tagger"); tg != nil {
				n, e, tm := authorFromBody(tg, st)
				tagger = n + " <" + e + "> " + strconv.FormatInt(tm, 10) + " +0000"
				_ = tagger
				data := "object " + obj + "\ntype " + typ + "\ntag " + tag + "\ntagger " + n + " <" + e + "> " + strconv.FormatInt(tm, 10) + " +0000\n\n" + msg
				sha := rp.put("tag", []byte(data))
				rp.Refs["refs/tags/"+tag] = sha
				writeJSON(w, 201, tagObjectView(req, rp, sha))
				return
			}
			data := "object " + obj + "\ntype " + typ + "\ntag " + tag + "\ntagger " + u.Login + " <" + u.Login + "@users.noreply.github.com> " + strconv.FormatInt(t.Unix(), 10) + " +0000\n\n" + msg
			sha := rp.put("tag", []byte(data))
			rp.Refs["refs/tags/"+tag] = sha
			writeJSON(w, 201, tagObjectView(req, rp, sha))
			return
		}
		if len(rest) == 2 && m == "GET" {
			if v := tagObjectView(req, rp, rest[1]); v != nil {
				writeJSON(w, 200, v)
				return
			}
			errJSON(w, 404, "Not Found")
			return
		}
	}
	errJSON(w, 404, "Not Found")
}

func authorFromBody(m map[string]interface{}, st *Store) (string, string, int64) {
	if m == nil {
		u := st.user(st.DefaultUser)
		return u.Login, u.Login + "@users.noreply.github.com", now().Unix()
	}
	name := jStr(m, "name")
	email := jStr(m, "email")
	t := now().Unix()
	if d := jStr(m, "date"); d != "" {
		if parsed, err := time.Parse(time.RFC3339, d); err == nil {
			t = parsed.Unix()
		}
	}
	if name == "" {
		name = st.DefaultUser
	}
	if email == "" {
		email = name + "@users.noreply.github.com"
	}
	return name, email, t
}

func treeView(req *http.Request, rp *Repo, sha string, recursive bool) map[string]interface{} {
	o := rp.get(sha)
	if o == nil || o.Type != "tree" {
		return nil
	}
	b := baseURL(req)
	api := b + "/api/v3/repos/" + rp.Owner + "/" + rp.Name
	entries := []interface{}{}
	if recursive {
		flat := map[string]TreeEntry{}
		rp.flattenTree(sha, "", flat)
		paths := make([]string, 0, len(flat))
		for p := range flat {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			e := flat[p]
			entries = append(entries, treeEntryView(api, rp, p, e))
		}
	} else {
		for _, e := range parseTree(o.Data) {
			entries = append(entries, treeEntryView(api, rp, e.Name, e))
		}
	}
	return map[string]interface{}{
		"sha": sha, "url": api + "/git/trees/" + sha,
		"tree": entries, "truncated": false,
	}
}

func treeEntryView(api string, rp *Repo, path string, e TreeEntry) map[string]interface{} {
	typ := "blob"
	url := api + "/git/blobs/" + e.SHA
	if e.IsTree() {
		typ = "tree"
		url = api + "/git/trees/" + e.SHA
	}
	mode := e.Mode
	if e.IsTree() && len(mode) == 5 {
		mode = "0" + mode
	}
	v := map[string]interface{}{"path": path, "mode": mode, "type": typ, "sha": e.SHA, "url": url}
	if typ == "blob" {
		if o := rp.get(e.SHA); o != nil {
			v["size"] = len(o.Data)
		}
	}
	return v
}

func tagObjectView(req *http.Request, rp *Repo, sha string) map[string]interface{} {
	o := rp.get(sha)
	if o == nil || o.Type != "tag" {
		return nil
	}
	ti := parseTag(o.Data)
	b := baseURL(req)
	api := b + "/api/v3/repos/" + rp.Owner + "/" + rp.Name
	return map[string]interface{}{
		"sha": sha, "node_id": gid("Tag", strconv.FormatInt(rp.ID, 10)+":"+sha), "url": api + "/git/tags/" + sha,
		"tagger": identView(ti.Name, ti.Email, ti.Time),
		"object": map[string]interface{}{"sha": ti.Object, "type": ti.Type, "url": api + "/git/" + map[string]string{"commit": "commits", "tree": "trees", "blob": "blobs", "tag": "tags"}[ti.Type] + "/" + ti.Object},
		"tag": ti.Tag, "message": strings.TrimRight(ti.Message, "\n"),
	}
}

// ---------------- contents API ----------------

func (s *Server) repoContents(w http.ResponseWriter, req *http.Request, rp *Repo, rest []string) {
	st := s.store
	m := req.Method
	path := strings.Join(rest, "/")
	q := req.URL.Query()
	refName := q.Get("ref")
	branch := refName
	if branch == "" {
		branch = rp.HeadBranch
	}
	headSHA := rp.resolveRefish(branch)
	switch m {
	case "GET":
		if headSHA == "" {
			errJSON(w, 404, "Not Found")
			return
		}
		treeSHA := rp.treeOfCommit(headSHA)
		if path == "" {
			writeJSON(w, 200, dirListing(req, rp, treeSHA, "", branch))
			return
		}
		// walk path
		parts := strings.Split(path, "/")
		curTree := treeSHA
		for i, part := range parts {
			o := rp.get(curTree)
			if o == nil || o.Type != "tree" {
				errJSON(w, 404, "Not Found")
				return
			}
			var found *TreeEntry
			for _, e := range parseTree(o.Data) {
				if e.Name == part {
					ee := e
					found = &ee
					break
				}
			}
			if found == nil {
				errJSON(w, 404, "Not Found")
				return
			}
			if i == len(parts)-1 {
				if found.IsTree() {
					writeJSON(w, 200, dirListing(req, rp, found.SHA, path, branch))
					return
				}
				writeJSON(w, 200, fileView(req, rp, *found, path, branch))
				return
			}
			if !found.IsTree() {
				errJSON(w, 404, "Not Found")
				return
			}
			curTree = found.SHA
		}
	case "PUT":
		body := readBodyJSON(req)
		msg := jStr(body, "message")
		contentB64 := jStr(body, "content")
		data, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(contentB64), ""))
		if err != nil {
			errJSON(w, 422, "Invalid content.")
			return
		}
		br := jStr(body, "branch")
		if br == "" {
			br = branch
		}
		baseSHA := rp.resolveRefish(br)
		unborn := false
		if baseSHA == "" {
			// A repository with no commits yet (or an unborn default branch)
			// accepts a contents PUT as its first commit.
			if len(rp.Refs) == 0 || br == rp.HeadBranch {
				unborn = true
			} else {
				errJSON(w, 422, "Invalid branch.")
				return
			}
		}
		if shaParam := jStr(body, "sha"); shaParam != "" && !unborn {
			flat := map[string]TreeEntry{}
			rp.flattenTree(rp.treeOfCommit(baseSHA), "", flat)
			if cur, exists := flat[path]; exists && cur.SHA != shaParam {
				writeJSON(w, 409, map[string]interface{}{"message": "sha does not match"})
				return
			}
		}
		blobSHA := rp.put("blob", data)
		parts := strings.Split(path, "/")
		baseTree := ""
		if !unborn {
			baseTree = rp.treeOfCommit(baseSHA)
		}
		newTree := rp.updateTreePath(baseTree, parts, blobSHA, "100644")
		aName, aEmail, aTime := authorFromBody(jObj(body, "author"), st)
		cName, cEmail, cTime := aName, aEmail, aTime
		if co := jObj(body, "committer"); co != nil {
			cName, cEmail, cTime = authorFromBody(co, st)
		}
		if msg == "" {
			msg = "Update " + path
		}
		var parents []string
		parentViews := []interface{}{}
		if !unborn {
			parents = []string{baseSHA}
			parentViews = []interface{}{map[string]interface{}{"sha": baseSHA, "url": baseURL(req) + "/api/v3/repos/" + rp.Owner + "/" + rp.Name + "/git/commits/" + baseSHA}}
		}
		commitSHA := rp.put("commit", buildCommitData(newTree, parents, aName, aEmail, aTime, "+0000", cName, cEmail, cTime, "+0000", msg))
		rp.Refs["refs/heads/"+br] = commitSHA
		rp.UpdatedAt = now()
		writeJSON(w, 201, map[string]interface{}{
			"content": fileView(req, rp, TreeEntry{Mode: "100644", Name: parts[len(parts)-1], SHA: blobSHA}, path, br),
			"commit": map[string]interface{}{
				"sha": commitSHA, "node_id": gid("Commit", strconv.FormatInt(rp.ID, 10)+":"+commitSHA),
				"url": baseURL(req) + "/api/v3/repos/" + rp.Owner + "/" + rp.Name + "/git/commits/" + commitSHA,
				"author": identView(aName, aEmail, aTime), "committer": identView(cName, cEmail, cTime),
				"message": msg, "tree": map[string]interface{}{"sha": newTree, "url": baseURL(req) + "/api/v3/repos/" + rp.Owner + "/" + rp.Name + "/git/trees/" + newTree},
				"parents": parentViews,
			},
		})
		return
	case "DELETE":
		body := readBodyJSON(req)
		msg := jStr(body, "message")
		br := jStr(body, "branch")
		if br == "" {
			br = branch
		}
		baseSHA := rp.resolveRefish(br)
		if baseSHA == "" {
			errJSON(w, 422, "Invalid branch.")
			return
		}
		parts := strings.Split(path, "/")
		newTree := rp.updateTreePath(rp.treeOfCommit(baseSHA), parts, "", "")
		aName, aEmail, aTime := authorFromBody(nil, st)
		if msg == "" {
			msg = "Delete " + path
		}
		commitSHA := rp.put("commit", buildCommitData(newTree, []string{baseSHA}, aName, aEmail, aTime, "+0000", aName, aEmail, aTime, "+0000", msg))
		rp.Refs["refs/heads/"+br] = commitSHA
		rp.UpdatedAt = now()
		writeJSON(w, 200, map[string]interface{}{
			"content": nil,
			"commit": map[string]interface{}{
				"sha": commitSHA, "message": msg,
				"author": identView(aName, aEmail, aTime), "committer": identView(aName, aEmail, aTime),
				"tree": map[string]interface{}{"sha": newTree}, "parents": []interface{}{map[string]interface{}{"sha": baseSHA}},
				"url": baseURL(req) + "/api/v3/repos/" + rp.Owner + "/" + rp.Name + "/git/commits/" + commitSHA,
			},
		})
		return
	}
	errJSON(w, 404, "Not Found")
}

func dirListing(req *http.Request, rp *Repo, treeSHA, prefix, ref string) []interface{} {
	out := []interface{}{}
	o := rp.get(treeSHA)
	if o == nil {
		return out
	}
	b := baseURL(req)
	api := b + "/api/v3/repos/" + rp.Owner + "/" + rp.Name
	for _, e := range parseTree(o.Data) {
		p := e.Name
		if prefix != "" {
			p = prefix + "/" + e.Name
		}
		typ := "file"
		if e.IsTree() {
			typ = "dir"
		}
		v := map[string]interface{}{
			"name": e.Name, "path": p, "sha": e.SHA,
			"url": api + "/contents/" + p + "?ref=" + ref,
			"html_url": b + "/" + rp.Owner + "/" + rp.Name + "/blob/" + ref + "/" + p,
			"git_url": api + "/git/" + map[bool]string{true: "trees", false: "blobs"}[e.IsTree()] + "/" + e.SHA,
			"download_url": nil, "type": typ,
			"_links": map[string]interface{}{},
		}
		if !e.IsTree() {
			if bo := rp.get(e.SHA); bo != nil {
				v["size"] = len(bo.Data)
			}
			v["download_url"] = b + "/" + rp.Owner + "/" + rp.Name + "/raw/" + ref + "/" + p
		} else {
			v["size"] = 0
			v["html_url"] = b + "/" + rp.Owner + "/" + rp.Name + "/tree/" + ref + "/" + p
		}
		out = append(out, v)
	}
	return out
}

func fileView(req *http.Request, rp *Repo, e TreeEntry, path, ref string) map[string]interface{} {
	b := baseURL(req)
	api := b + "/api/v3/repos/" + rp.Owner + "/" + rp.Name
	o := rp.get(e.SHA)
	var data []byte
	if o != nil {
		data = o.Data
	}
	name := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		name = path[i+1:]
	}
	typ := "file"
	enc := "base64"
	content := base64.StdEncoding.EncodeToString(data)
	if e.Mode == "120000" {
		typ = "symlink"
		enc = "base64"
	}
	return map[string]interface{}{
		"type": typ, "encoding": enc, "size": len(data),
		"name": name, "path": path, "content": content, "sha": e.SHA,
		"url": api + "/contents/" + path + "?ref=" + ref,
		"git_url": api + "/git/blobs/" + e.SHA,
		"html_url": b + "/" + rp.Owner + "/" + rp.Name + "/blob/" + ref + "/" + path,
		"download_url": b + "/" + rp.Owner + "/" + rp.Name + "/raw/" + ref + "/" + path,
		"_links": map[string]interface{}{},
	}
}

// ---------------- issues / labels / pulls ----------------

func parseLabels(body map[string]interface{}) []string {
	var out []string
	for _, l := range jArr(body, "labels") {
		switch v := l.(type) {
		case string:
			out = append(out, v)
		case map[string]interface{}:
			if n := jStr(v, "name"); n != "" {
				out = append(out, n)
			}
		}
	}
	return out
}

func parseAssignees(body map[string]interface{}) []string {
	var out []string
	for _, a := range jArr(body, "assignees") {
		if s, ok := a.(string); ok {
			out = append(out, s)
		}
	}
	if a := jStr(body, "assignee"); a != "" {
		out = append(out, a)
	}
	return out
}

func (s *Server) repoIssues(w http.ResponseWriter, req *http.Request, rp *Repo, rest []string) {
	st := s.store
	m := req.Method
	if len(rest) == 0 {
		if m == "GET" {
			q := req.URL.Query()
			state := q.Get("state")
			if state == "" {
				state = "open"
			}
			var labelFilter []string
			if lf := q.Get("labels"); lf != "" {
				labelFilter = strings.Split(lf, ",")
			}
			out := []interface{}{}
			for _, is := range rp.Issues {
				if state != "all" && is.State != state {
					continue
				}
				if len(labelFilter) > 0 {
					ok := true
					for _, lf := range labelFilter {
						found := false
						for _, l := range is.Labels {
							if l == lf {
								found = true
							}
						}
						if !found {
							ok = false
						}
					}
					if !ok {
						continue
					}
				}
				out = append(out, issueView(st, req, rp, is))
			}
			// GitHub lists issues newest first by default; honour
			// ?sort=created&direction=asc|desc.
			dir := q.Get("direction")
			if dir == "" {
				dir = q.Get("order")
			}
			asc := dir == "asc"
			sort.SliceStable(out, func(i, j int) bool {
				ai := out[i].(map[string]interface{})
				bj := out[j].(map[string]interface{})
				an, _ := ai["number"].(int)
				bn, _ := bj["number"].(int)
				if asc {
					return an < bn
				}
				return an > bn
			})
			start, end := paginate(req, len(out))
			writeJSON(w, 200, out[start:end])
			return
		}
		if m == "POST" {
			body := readBodyJSON(req)
			title := jStr(body, "title")
			if title == "" {
				errJSON(w, 422, "Invalid request.")
				return
			}
			for _, l := range parseLabels(body) {
				if _, ok := rp.Labels[l]; !ok {
					writeJSON(w, 422, map[string]interface{}{
						"message": "Validation Failed",
						"errors": []interface{}{map[string]interface{}{"resource": "Issue", "code": "invalid", "field": "labels"}},
					})
					return
				}
			}
			is := &Issue{
				ID: st.NextIssueID, Number: rp.NextNum, Title: title,
				Body: jStr(body, "body"), State: "open", User: st.DefaultUser,
				Labels: parseLabels(body), Assignees: parseAssignees(body),
				CreatedAt: now(), UpdatedAt: now(),
			}
			st.NextIssueID++
			rp.NextNum++
			rp.Issues = append(rp.Issues, is)
			rp.UpdatedAt = now()
			writeJSON(w, 201, issueView(st, req, rp, is))
			return
		}
	}
	if len(rest) >= 1 && rest[0] == "comments" && len(rest) == 2 {
		// /issues/comments/{id}
		id, _ := strconv.ParseInt(rest[1], 10, 64)
		c := rp.Comments[id]
		if c == nil {
			errJSON(w, 404, "Not Found")
			return
		}
		num := rp.CommentSeq[id]
		switch m {
		case "GET":
			writeJSON(w, 200, commentView(st, req, rp, num, c))
		case "PATCH":
			body := readBodyJSON(req)
			c.Body = jStr(body, "body")
			c.UpdatedAt = now()
			writeJSON(w, 200, commentView(st, req, rp, num, c))
		case "DELETE":
			is := rp.findIssue(num)
			if is != nil {
				for i, cc := range is.Comments {
					if cc.ID == id {
						is.Comments = append(is.Comments[:i], is.Comments[i+1:]...)
						break
					}
				}
			}
			delete(rp.Comments, id)
			w.WriteHeader(204)
		}
		return
	}
	num, err := strconv.Atoi(rest[0])
	if err != nil {
		errJSON(w, 404, "Not Found")
		return
	}
	is := rp.findIssue(num)
	if is == nil {
		errJSON(w, 404, "Not Found")
		return
	}
	if len(rest) == 1 {
		switch m {
		case "GET":
			// Read benchmarks hammer single-issue GETs; serve a cached
			// serialization keyed by every view-relevant field.
			key := issueViewKey(req, rp, is)
			if b, ok := st.viewCache[key]; ok {
				writeRawJSON(w, 200, b)
				return
			}
			v := issueView(st, req, rp, is)
			data, _ := json.Marshal(v)
			if len(st.viewCache) > 10000 {
				st.viewCache = map[string][]byte{}
			}
			st.viewCache[key] = data
			writeRawJSON(w, 200, data)
		case "PATCH":
			body := readBodyJSON(req)
			if body["title"] != nil {
				is.Title = jStr(body, "title")
			}
			if body["body"] != nil {
				is.Body = jStr(body, "body")
			}
			if body["labels"] != nil {
				is.Labels = parseLabels(body)
				for _, l := range is.Labels {
					if _, ok := rp.Labels[l]; !ok {
						rp.Labels[l] = "ededed"
					}
				}
			}
			if body["assignees"] != nil || body["assignee"] != nil {
				is.Assignees = parseAssignees(body)
			}
			if st8 := jStr(body, "state"); st8 != "" {
				is.State = st8
				if st8 == "closed" && is.ClosedAt == nil {
					t := now()
					is.ClosedAt = &t
				}
				if st8 == "open" {
					is.ClosedAt = nil
				}
			}
			is.UpdatedAt = now()
			if is.IsPR {
				writeJSON(w, 200, pullView(st, req, rp, is))
			} else {
				writeJSON(w, 200, issueView(st, req, rp, is))
			}
		}
		return
	}
	if len(rest) == 2 && rest[1] == "comments" {
		switch m {
		case "GET":
			out := []interface{}{}
			for _, c := range is.Comments {
				out = append(out, commentView(st, req, rp, num, c))
			}
			start, end := paginate(req, len(out))
			writeJSON(w, 200, out[start:end])
		case "POST":
			body := readBodyJSON(req)
			c := &Comment{ID: st.NextCmtID, Body: jStr(body, "body"), User: st.DefaultUser, CreatedAt: now(), UpdatedAt: now()}
			st.NextCmtID++
			is.Comments = append(is.Comments, c)
			is.UpdatedAt = now()
			rp.Comments[c.ID] = c
			rp.CommentSeq[c.ID] = num
			writeJSON(w, 201, commentView(st, req, rp, num, c))
		}
		return
	}
	if len(rest) == 2 && rest[1] == "labels" {
		if m == "POST" {
			for _, l := range parseLabels(readBodyJSON(req)) {
				found := false
				for _, el := range is.Labels {
					if el == l {
						found = true
					}
				}
				if !found {
					is.Labels = append(is.Labels, l)
				}
				if _, ok := rp.Labels[l]; !ok {
					rp.Labels[l] = "ededed"
				}
			}
			out := []interface{}{}
			for _, l := range is.Labels {
				out = append(out, labelView(rp, l))
			}
			writeJSON(w, 200, out)
			return
		}
		if m == "GET" {
			out := []interface{}{}
			for _, l := range is.Labels {
				out = append(out, labelView(rp, l))
			}
			writeJSON(w, 200, out)
			return
		}
		if m == "DELETE" {
			is.Labels = nil
			writeJSON(w, 200, []interface{}{})
			return
		}
	}
	errJSON(w, 404, "Not Found")
}

func (s *Server) repoLabels(w http.ResponseWriter, req *http.Request, rp *Repo, rest []string) {
	m := req.Method
	if len(rest) == 0 {
		if m == "GET" {
			names := make([]string, 0, len(rp.Labels))
			for n := range rp.Labels {
				names = append(names, n)
			}
			sort.Strings(names)
			out := []interface{}{}
			for _, n := range pageSlice(req, names) {
				out = append(out, labelView(rp, n))
			}
			writeJSON(w, 200, out)
			return
		}
		if m == "POST" {
			body := readBodyJSON(req)
			name := jStr(body, "name")
			if name == "" {
				errJSON(w, 422, "Invalid request.")
				return
			}
			if _, ok := rp.Labels[name]; ok {
				writeJSON(w, 422, map[string]interface{}{"message": "Validation Failed", "errors": []interface{}{map[string]interface{}{"resource": "Label", "code": "already_exists", "field": "name"}}})
				return
			}
			color := jStr(body, "color")
			if color == "" {
				color = "ededed"
			}
			rp.Labels[name] = strings.TrimPrefix(color, "#")
			writeJSON(w, 201, labelView(rp, name))
			return
		}
	}
	if len(rest) == 1 {
		name := rest[0]
		if _, ok := rp.Labels[name]; !ok {
			errJSON(w, 404, "Not Found")
			return
		}
		switch m {
		case "GET":
			writeJSON(w, 200, labelView(rp, name))
		case "PATCH":
			body := readBodyJSON(req)
			if c := jStr(body, "color"); c != "" {
				rp.Labels[name] = strings.TrimPrefix(c, "#")
			}
			if nn := jStr(body, "new_name"); nn != "" {
				rp.Labels[nn] = rp.Labels[name]
				delete(rp.Labels, name)
				name = nn
			}
			writeJSON(w, 200, labelView(rp, name))
		case "DELETE":
			delete(rp.Labels, name)
			w.WriteHeader(204)
		}
		return
	}
	errJSON(w, 404, "Not Found")
}

func (s *Server) repoPulls(w http.ResponseWriter, req *http.Request, rp *Repo, rest []string) {
	st := s.store
	m := req.Method
	if len(rest) == 0 {
		if m == "GET" {
			q := req.URL.Query()
			state := q.Get("state")
			if state == "" {
				state = "open"
			}
			out := []interface{}{}
			for _, is := range rp.Issues {
				if !is.IsPR {
					continue
				}
				if state != "all" && is.State != state {
					continue
				}
				if hb := q.Get("head"); hb != "" {
					ref := hb
					if i := strings.Index(hb, ":"); i >= 0 {
						ref = hb[i+1:]
					}
					if is.HeadRef != ref {
						continue
					}
				}
				if bb := q.Get("base"); bb != "" && is.BaseRef != bb {
					continue
				}
				out = append(out, pullView(st, req, rp, is))
			}
			start, end := paginate(req, len(out))
			writeJSON(w, 200, out[start:end])
			return
		}
		if m == "POST" {
			body := readBodyJSON(req)
			title := jStr(body, "title")
			head := jStr(body, "head")
			base := jStr(body, "base")
			if head == "" || base == "" {
				errJSON(w, 422, "Invalid request.")
				return
			}
			if i := strings.Index(head, ":"); i >= 0 {
				head = head[i+1:]
			}
			if rp.resolveRefish(head) == "" {
				writeJSON(w, 422, map[string]interface{}{"message": "Validation Failed", "errors": []interface{}{map[string]interface{}{"resource": "PullRequest", "code": "invalid", "field": "head"}}})
				return
			}
			if rp.resolveRefish(base) == "" {
				writeJSON(w, 422, map[string]interface{}{"message": "Validation Failed", "errors": []interface{}{map[string]interface{}{"resource": "PullRequest", "code": "invalid", "field": "base"}}})
				return
			}
			if title == "" {
				// derive from head branch commits
				title = "Pull request from " + head
			}
			// check for existing open PR with same head/base
			for _, ex := range rp.Issues {
				if ex.IsPR && ex.State == "open" && ex.HeadRef == head && ex.BaseRef == base {
					writeJSON(w, 422, map[string]interface{}{"message": "Validation Failed", "errors": []interface{}{map[string]interface{}{"resource": "PullRequest", "code": "custom", "message": "A pull request already exists for " + rp.Owner + ":" + head + "."}}})
					return
				}
			}
			is := &Issue{
				ID: st.NextIssueID, Number: rp.NextNum, Title: title,
				Body: jStr(body, "body"), State: "open", User: st.DefaultUser,
				CreatedAt: now(), UpdatedAt: now(), IsPR: true,
				HeadRef: head, BaseRef: base,
			}
			st.NextIssueID++
			rp.NextNum++
			rp.Issues = append(rp.Issues, is)
			rp.UpdatedAt = now()
			writeJSON(w, 201, pullView(st, req, rp, is))
			return
		}
	}
	num, err := strconv.Atoi(rest[0])
	if err != nil {
		errJSON(w, 404, "Not Found")
		return
	}
	is := rp.findIssue(num)
	if is == nil || !is.IsPR {
		errJSON(w, 404, "Not Found")
		return
	}
	if len(rest) == 1 {
		switch m {
		case "GET":
			writeJSON(w, 200, pullView(st, req, rp, is))
		case "PATCH":
			body := readBodyJSON(req)
			if body["title"] != nil {
				is.Title = jStr(body, "title")
			}
			if body["body"] != nil {
				is.Body = jStr(body, "body")
			}
			if st8 := jStr(body, "state"); st8 != "" {
				is.State = st8
				if st8 == "closed" && is.ClosedAt == nil {
					t := now()
					is.ClosedAt = &t
				}
				if st8 == "open" {
					is.ClosedAt = nil
				}
			}
			if bb := jStr(body, "base"); bb != "" {
				is.BaseRef = bb
			}
			is.UpdatedAt = now()
			writeJSON(w, 200, pullView(st, req, rp, is))
		}
		return
	}
	switch rest[1] {
	case "commits":
		baseSHA := rp.resolveRefish(is.BaseRef)
		headSHA := rp.resolveRefish(is.HeadRef)
		commits, _ := rp.commitsBetween(baseSHA, headSHA)
		out := []interface{}{}
		for i := len(commits) - 1; i >= 0; i-- {
			out = append(out, commitView(st, req, rp, commits[i]))
		}
		start, end := paginate(req, len(out))
		writeJSON(w, 200, out[start:end])
		return
	case "files":
		baseSHA := rp.resolveRefish(is.BaseRef)
		headSHA := rp.resolveRefish(is.HeadRef)
		files := diffTrees(rp, rp.treeOfCommit(baseSHA), rp.treeOfCommit(headSHA))
		out := []interface{}{}
		for _, f := range files {
			out = append(out, fileDiffView(req, rp, f))
		}
		start, end := paginate(req, len(out))
		writeJSON(w, 200, out[start:end])
		return
	case "merge":
		if m == "GET" {
			if is.Merged {
				w.WriteHeader(204)
			} else {
				errJSON(w, 404, "Not Found")
			}
			return
		}
		if m == "PUT" {
			s.mergePull(w, req, rp, is)
			return
		}
	case "comments":
		// treat review comments as issue comments
		if m == "GET" {
			out := []interface{}{}
			for _, c := range is.Comments {
				out = append(out, commentView(st, req, rp, num, c))
			}
			writeJSON(w, 200, out)
			return
		}
		if m == "POST" {
			body := readBodyJSON(req)
			c := &Comment{ID: st.NextCmtID, Body: jStr(body, "body"), User: st.DefaultUser, CreatedAt: now(), UpdatedAt: now()}
			st.NextCmtID++
			is.Comments = append(is.Comments, c)
			rp.Comments[c.ID] = c
			rp.CommentSeq[c.ID] = num
			writeJSON(w, 201, commentView(st, req, rp, num, c))
			return
		}
	}
	errJSON(w, 404, "Not Found")
}

func (s *Server) mergePull(w http.ResponseWriter, req *http.Request, rp *Repo, is *Issue) {
	st := s.store
	if is.Merged {
		writeJSON(w, 405, map[string]interface{}{"message": "Pull Request is already merged", "documentation_url": ""})
		return
	}
	if is.State != "open" {
		writeJSON(w, 405, map[string]interface{}{"message": "Pull Request is not mergeable", "documentation_url": ""})
		return
	}
	body := readBodyJSON(req)
	method := jStr(body, "merge_method")
	if method == "" {
		method = "merge"
	}
	baseSHA := rp.resolveRefish(is.BaseRef)
	headSHA := rp.resolveRefish(is.HeadRef)
	if baseSHA == "" || headSHA == "" {
		writeJSON(w, 405, map[string]interface{}{"message": "Pull Request is not mergeable", "documentation_url": ""})
		return
	}
	if want := jStr(body, "sha"); want != "" && want != headSHA {
		writeJSON(w, 409, map[string]interface{}{"message": "Head branch was modified. Review and try the merge again.", "documentation_url": ""})
		return
	}
	// Every merge method (merge, squash, rebase) refuses a conflicted PR.
	if _, conflict := mergeCheck(rp, baseSHA, headSHA); conflict {
		writeJSON(w, 405, map[string]interface{}{"message": "Pull Request is not mergeable", "documentation_url": ""})
		return
	}
	u := st.user(st.DefaultUser)
	t := now()
	aName, aEmail := u.Login, u.Login+"@users.noreply.github.com"
	var mergeSHA string
	switch method {
	case "squash":
		msg := jStr(body, "commit_message")
		title := jStr(body, "commit_title")
		if title == "" {
			title = is.Title + " (#" + strconv.Itoa(is.Number) + ")"
		}
		if msg != "" {
			title = title + "\n\n" + msg
		}
		tree := rp.treeOfCommit(headSHA)
		mergeSHA = rp.put("commit", buildCommitData(tree, []string{baseSHA}, aName, aEmail, t.Unix(), "+0000", aName, aEmail, t.Unix(), "+0000", title))
	case "rebase":
		if rp.isAncestor(baseSHA, headSHA) {
			mergeSHA = headSHA
		} else {
			tree := rp.treeOfCommit(headSHA)
			mergeSHA = rp.put("commit", buildCommitData(tree, []string{baseSHA}, aName, aEmail, t.Unix(), "+0000", aName, aEmail, t.Unix(), "+0000", is.Title))
		}
	default: // merge
		var tree string
		if rp.isAncestor(baseSHA, headSHA) {
			tree = rp.treeOfCommit(headSHA)
		} else if rp.isAncestor(headSHA, baseSHA) {
			writeJSON(w, 405, map[string]interface{}{"message": "Pull Request is not mergeable", "documentation_url": ""})
			return
		} else {
			mb := mergeBase(rp, baseSHA, headSHA)
			merged, conflict := threeWayTree(rp, rp.treeOfCommit(mb), rp.treeOfCommit(baseSHA), rp.treeOfCommit(headSHA))
			if conflict {
				writeJSON(w, 405, map[string]interface{}{"message": "Pull Request is not mergeable", "documentation_url": ""})
				return
			}
			tree = buildTreeFromFlat(rp, merged)
		}
		msg := jStr(body, "commit_message")
		title := jStr(body, "commit_title")
		if title == "" {
			title = "Merge pull request #" + strconv.Itoa(is.Number) + " from " + rp.Owner + "/" + is.HeadRef
		}
		if msg != "" {
			title = title + "\n\n" + msg
		}
		mergeSHA = rp.put("commit", buildCommitData(tree, []string{baseSHA, headSHA}, aName, aEmail, t.Unix(), "+0000", aName, aEmail, t.Unix(), "+0000", title))
	}
	rp.Refs["refs/heads/"+is.BaseRef] = mergeSHA
	is.Merged = true
	is.State = "closed"
	is.MergedAt = &t
	is.ClosedAt = &t
	is.MergedBy = st.DefaultUser
	is.MergeSHA = mergeSHA
	is.UpdatedAt = t
	rp.UpdatedAt = t
	writeJSON(w, 200, map[string]interface{}{
		"sha": mergeSHA, "merged": true, "message": "Pull Request successfully merged",
	})
}

// ---------------- search ----------------

func (s *Server) handleSearch(w http.ResponseWriter, req *http.Request, kind string) {
	st := s.store
	q := req.URL.Query().Get("q")
	terms := strings.Fields(q)
	var needle string
	var repoFilter string
	for _, t := range terms {
		if strings.HasPrefix(t, "repo:") {
			repoFilter = strings.TrimPrefix(t, "repo:")
		} else if !strings.Contains(t, ":") {
			if needle != "" {
				needle += " "
			}
			needle += t
		}
	}
	needle = strings.ToLower(needle)
	items := []interface{}{}
	switch kind {
	case "repositories":
		var repos []*Repo
		for _, rp := range st.Repos {
			if needle == "" || strings.Contains(strings.ToLower(rp.Name), needle) || strings.Contains(strings.ToLower(rp.Desc), needle) {
				repos = append(repos, rp)
			}
		}
		sort.Slice(repos, func(i, j int) bool { return repos[i].ID < repos[j].ID })
		for _, rp := range repos {
			items = append(items, repoView(st, req, rp))
		}
	case "issues":
		for _, rp := range st.Repos {
			if repoFilter != "" && !strings.EqualFold(rp.Owner+"/"+rp.Name, repoFilter) {
				continue
			}
			for _, is := range rp.Issues {
				if needle == "" || strings.Contains(strings.ToLower(is.Title), needle) || strings.Contains(strings.ToLower(is.Body), needle) {
					items = append(items, issueView(st, req, rp, is))
				}
			}
		}
	case "commits":
		for _, rp := range st.Repos {
			for sha, o := range rp.Objects {
				if o.Type != "commit" {
					continue
				}
				ci := parseCommit(o.Data)
				if needle == "" || strings.Contains(strings.ToLower(ci.Message), needle) {
					items = append(items, commitView(st, req, rp, sha))
				}
			}
		}
	}
	total := len(items)
	start, end := paginate(req, total)
	writeJSON(w, 200, map[string]interface{}{
		"total_count": total, "incomplete_results": false, "items": items[start:end],
	})
}
