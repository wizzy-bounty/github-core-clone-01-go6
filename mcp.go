package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

type mcpTool struct {
	Name string
	Desc string
	Fn   func(s *Server, req *http.Request, args map[string]interface{}) (interface{}, string)
}

func mcpText(v interface{}) map[string]interface{} {
	data, _ := json.Marshal(v)
	return map[string]interface{}{
		"content": []interface{}{map[string]interface{}{"type": "text", "text": string(data)}},
		"isError": false,
	}
}

func mcpErr(msg string) map[string]interface{} {
	return map[string]interface{}{
		"content": []interface{}{map[string]interface{}{"type": "text", "text": msg}},
		"isError": true,
	}
}

func toolSchema(props map[string]string, required []string) map[string]interface{} {
	p := map[string]interface{}{}
	for k, t := range props {
		p[k] = map[string]interface{}{"type": t}
	}
	return map[string]interface{}{
		"type": "object", "properties": p, "required": required,
	}
}

var mcpTools []mcpTool

func initMCP() {
	str := "string"
	num := "number"
	boolean := "boolean"
	_ = num
	mcpTools = []mcpTool{
		{"create_repository", "Create a new repository", func(s *Server, req *http.Request, a map[string]interface{}) (interface{}, string) {
			name := jStr(a, "name")
			if name == "" {
				return nil, "name is required"
			}
			owner := s.store.DefaultUser
			if s.store.findRepo(owner, name) != nil {
				return nil, "name already exists on this account"
			}
			rp := s.store.createRepo(owner, name, jStr(a, "description"), jBool(a, "private"))
			if jBool(a, "auto_init") {
				u := s.store.user(owner)
				blob := rp.put("blob", []byte("# "+name+"\n"))
				tree := rp.put("tree", buildTreeData([]TreeEntry{{Mode: "100644", Name: "README.md", SHA: blob}}))
				t := now()
				c := rp.put("commit", buildCommitData(tree, nil, u.Login, u.Login+"@users.noreply.github.com", t.Unix(), "+0000", u.Login, u.Login+"@users.noreply.github.com", t.Unix(), "+0000", "Initial commit"))
				rp.Refs["refs/heads/"+rp.HeadBranch] = c
			}
			return repoView(s.store, req, rp), ""
		}},
		{"get_repository", "Get a repository", func(s *Server, req *http.Request, a map[string]interface{}) (interface{}, string) {
			rp := s.store.findRepo(jStr(a, "owner"), jStr(a, "repo"))
			if rp == nil {
				return nil, "Not Found"
			}
			return repoView(s.store, req, rp), ""
		}},
		{"search_repositories", "Search repositories", func(s *Server, req *http.Request, a map[string]interface{}) (interface{}, string) {
			q := strings.ToLower(jStr(a, "query"))
			var out []interface{}
			var repos []*Repo
			for _, rp := range s.store.Repos {
				if q == "" || strings.Contains(strings.ToLower(rp.Name), q) || strings.Contains(strings.ToLower(rp.Desc), q) {
					repos = append(repos, rp)
				}
			}
			sort.Slice(repos, func(i, j int) bool { return repos[i].ID < repos[j].ID })
			for _, rp := range repos {
				out = append(out, repoView(s.store, req, rp))
			}
			return map[string]interface{}{"total_count": len(out), "items": out}, ""
		}},
		{"list_branches", "List branches", func(s *Server, req *http.Request, a map[string]interface{}) (interface{}, string) {
			rp := s.store.findRepo(jStr(a, "owner"), jStr(a, "repo"))
			if rp == nil {
				return nil, "Not Found"
			}
			var names []string
			for ref := range rp.Refs {
				if strings.HasPrefix(ref, "refs/heads/") {
					names = append(names, strings.TrimPrefix(ref, "refs/heads/"))
				}
			}
			sort.Strings(names)
			out := []interface{}{}
			for _, n := range names {
				out = append(out, map[string]interface{}{"name": n, "commit": map[string]interface{}{"sha": rp.Refs["refs/heads/"+n]}})
			}
			return out, ""
		}},
		{"create_branch", "Create a branch", func(s *Server, req *http.Request, a map[string]interface{}) (interface{}, string) {
			rp := s.store.findRepo(jStr(a, "owner"), jStr(a, "repo"))
			if rp == nil {
				return nil, "Not Found"
			}
			branch := jStr(a, "branch")
			from := jStr(a, "from_branch")
			if from == "" {
				from = rp.HeadBranch
			}
			sha := rp.resolveRefish(from)
			if sha == "" {
				return nil, "from branch not found"
			}
			rp.Refs["refs/heads/"+branch] = sha
			return map[string]interface{}{"ref": "refs/heads/" + branch, "object": map[string]interface{}{"sha": sha, "type": "commit"}}, ""
		}},
		{"get_file_contents", "Get file or directory contents", func(s *Server, req *http.Request, a map[string]interface{}) (interface{}, string) {
			rp := s.store.findRepo(jStr(a, "owner"), jStr(a, "repo"))
			if rp == nil {
				return nil, "Not Found"
			}
			path := jStr(a, "path")
			ref := jStr(a, "ref")
			if ref == "" {
				ref = jStr(a, "branch")
			}
			if ref == "" {
				ref = rp.HeadBranch
			}
			head := rp.resolveRefish(ref)
			if head == "" {
				return nil, "Not Found"
			}
			treeSHA := rp.treeOfCommit(head)
			if path == "" || path == "/" {
				return dirListing(req, rp, treeSHA, "", ref), ""
			}
			parts := strings.Split(strings.Trim(path, "/"), "/")
			cur := treeSHA
			for i, part := range parts {
				o := rp.get(cur)
				if o == nil || o.Type != "tree" {
					return nil, "Not Found"
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
					return nil, "Not Found"
				}
				if i == len(parts)-1 {
					if found.IsTree() {
						return dirListing(req, rp, found.SHA, path, ref), ""
					}
					return fileView(req, rp, *found, path, ref), ""
				}
				cur = found.SHA
			}
			return nil, "Not Found"
		}},
		{"create_or_update_file", "Create or update a file", func(s *Server, req *http.Request, a map[string]interface{}) (interface{}, string) {
			rp := s.store.findRepo(jStr(a, "owner"), jStr(a, "repo"))
			if rp == nil {
				return nil, "Not Found"
			}
			path := strings.Trim(jStr(a, "path"), "/")
			content := jStr(a, "content")
			msg := jStr(a, "message")
			branch := jStr(a, "branch")
			if branch == "" {
				branch = rp.HeadBranch
			}
			baseSHA := rp.resolveRefish(branch)
			if baseSHA == "" {
				return nil, "branch not found"
			}
			blobSHA := rp.put("blob", []byte(content))
			parts := strings.Split(path, "/")
			newTree := rp.updateTreePath(rp.treeOfCommit(baseSHA), parts, blobSHA, "100644")
			u := s.store.user(s.store.DefaultUser)
			t := now()
			if msg == "" {
				msg = "Update " + path
			}
			commitSHA := rp.put("commit", buildCommitData(newTree, []string{baseSHA}, u.Login, u.Login+"@users.noreply.github.com", t.Unix(), "+0000", u.Login, u.Login+"@users.noreply.github.com", t.Unix(), "+0000", msg))
			rp.Refs["refs/heads/"+branch] = commitSHA
			rp.UpdatedAt = t
			return map[string]interface{}{
				"content": fileView(req, rp, TreeEntry{Mode: "100644", Name: parts[len(parts)-1], SHA: blobSHA}, path, branch),
				"commit": map[string]interface{}{"sha": commitSHA, "message": msg},
			}, ""
		}},
		{"list_commits", "List commits", func(s *Server, req *http.Request, a map[string]interface{}) (interface{}, string) {
			rp := s.store.findRepo(jStr(a, "owner"), jStr(a, "repo"))
			if rp == nil {
				return nil, "Not Found"
			}
			ref := jStr(a, "sha")
			if ref == "" {
				ref = rp.HeadBranch
			}
			head := rp.resolveRefish(ref)
			out := []interface{}{}
			seen := map[string]bool{}
			queue := []string{head}
			for len(queue) > 0 && len(out) < 100 {
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
				out = append(out, commitView(s.store, req, rp, c))
				queue = append(queue, parseCommit(o.Data).Parents...)
			}
			return out, ""
		}},
		{"get_commit", "Get a commit", func(s *Server, req *http.Request, a map[string]interface{}) (interface{}, string) {
			rp := s.store.findRepo(jStr(a, "owner"), jStr(a, "repo"))
			if rp == nil {
				return nil, "Not Found"
			}
			sha := rp.resolveRefish(jStr(a, "sha"))
			if sha == "" {
				return nil, "Not Found"
			}
			return commitView(s.store, req, rp, sha), ""
		}},
		{"create_issue", "Create an issue", func(s *Server, req *http.Request, a map[string]interface{}) (interface{}, string) {
			rp := s.store.findRepo(jStr(a, "owner"), jStr(a, "repo"))
			if rp == nil {
				return nil, "Not Found"
			}
			is := &Issue{
				ID: s.store.NextIssueID, Number: rp.NextNum, Title: jStr(a, "title"),
				Body: jStr(a, "body"), State: "open", User: s.store.DefaultUser,
				Labels: parseLabels(a), Assignees: parseAssignees(a),
				CreatedAt: now(), UpdatedAt: now(),
			}
			s.store.NextIssueID++
			rp.NextNum++
			for _, l := range is.Labels {
				if _, ok := rp.Labels[l]; !ok {
					rp.Labels[l] = "ededed"
				}
			}
			rp.Issues = append(rp.Issues, is)
			return issueView(s.store, req, rp, is), ""
		}},
		{"list_issues", "List issues", func(s *Server, req *http.Request, a map[string]interface{}) (interface{}, string) {
			rp := s.store.findRepo(jStr(a, "owner"), jStr(a, "repo"))
			if rp == nil {
				return nil, "Not Found"
			}
			state := jStr(a, "state")
			if state == "" {
				state = "open"
			}
			out := []interface{}{}
			for _, is := range rp.Issues {
				if is.IsPR {
					continue
				}
				if state != "all" && is.State != state {
					continue
				}
				out = append(out, issueView(s.store, req, rp, is))
			}
			return out, ""
		}},
		{"get_issue", "Get an issue", func(s *Server, req *http.Request, a map[string]interface{}) (interface{}, string) {
			rp := s.store.findRepo(jStr(a, "owner"), jStr(a, "repo"))
			if rp == nil {
				return nil, "Not Found"
			}
			num := argNum(a, "issue_number")
			is := rp.findIssue(num)
			if is == nil {
				return nil, "Not Found"
			}
			return issueView(s.store, req, rp, is), ""
		}},
		{"update_issue", "Update an issue", func(s *Server, req *http.Request, a map[string]interface{}) (interface{}, string) {
			rp := s.store.findRepo(jStr(a, "owner"), jStr(a, "repo"))
			if rp == nil {
				return nil, "Not Found"
			}
			is := rp.findIssue(argNum(a, "issue_number"))
			if is == nil {
				return nil, "Not Found"
			}
			if a["title"] != nil {
				is.Title = jStr(a, "title")
			}
			if a["body"] != nil {
				is.Body = jStr(a, "body")
			}
			if v := jStr(a, "state"); v != "" {
				is.State = v
				if v == "closed" && is.ClosedAt == nil {
					t := now()
					is.ClosedAt = &t
				}
				if v == "open" {
					is.ClosedAt = nil
				}
			}
			if a["labels"] != nil {
				is.Labels = parseLabels(a)
			}
			is.UpdatedAt = now()
			if is.IsPR {
				return pullView(s.store, req, rp, is), ""
			}
			return issueView(s.store, req, rp, is), ""
		}},
		{"add_issue_comment", "Add a comment to an issue", func(s *Server, req *http.Request, a map[string]interface{}) (interface{}, string) {
			rp := s.store.findRepo(jStr(a, "owner"), jStr(a, "repo"))
			if rp == nil {
				return nil, "Not Found"
			}
			num := argNum(a, "issue_number")
			is := rp.findIssue(num)
			if is == nil {
				return nil, "Not Found"
			}
			c := &Comment{ID: s.store.NextCmtID, Body: jStr(a, "body"), User: s.store.DefaultUser, CreatedAt: now(), UpdatedAt: now()}
			s.store.NextCmtID++
			is.Comments = append(is.Comments, c)
			is.UpdatedAt = now()
			rp.Comments[c.ID] = c
			rp.CommentSeq[c.ID] = num
			return commentView(s.store, req, rp, num, c), ""
		}},
		{"list_issue_comments", "List issue comments", func(s *Server, req *http.Request, a map[string]interface{}) (interface{}, string) {
			rp := s.store.findRepo(jStr(a, "owner"), jStr(a, "repo"))
			if rp == nil {
				return nil, "Not Found"
			}
			num := argNum(a, "issue_number")
			is := rp.findIssue(num)
			if is == nil {
				return nil, "Not Found"
			}
			out := []interface{}{}
			for _, c := range is.Comments {
				out = append(out, commentView(s.store, req, rp, num, c))
			}
			return out, ""
		}},
		{"create_pull_request", "Create a pull request", func(s *Server, req *http.Request, a map[string]interface{}) (interface{}, string) {
			rp := s.store.findRepo(jStr(a, "owner"), jStr(a, "repo"))
			if rp == nil {
				return nil, "Not Found"
			}
			head := jStr(a, "head")
			base := jStr(a, "base")
			if i := strings.Index(head, ":"); i >= 0 {
				head = head[i+1:]
			}
			if rp.resolveRefish(head) == "" || rp.resolveRefish(base) == "" {
				return nil, "Invalid head or base"
			}
			is := &Issue{
				ID: s.store.NextIssueID, Number: rp.NextNum, Title: jStr(a, "title"),
				Body: jStr(a, "body"), State: "open", User: s.store.DefaultUser,
				CreatedAt: now(), UpdatedAt: now(), IsPR: true, HeadRef: head, BaseRef: base,
			}
			s.store.NextIssueID++
			rp.NextNum++
			rp.Issues = append(rp.Issues, is)
			return pullView(s.store, req, rp, is), ""
		}},
		{"list_pull_requests", "List pull requests", func(s *Server, req *http.Request, a map[string]interface{}) (interface{}, string) {
			rp := s.store.findRepo(jStr(a, "owner"), jStr(a, "repo"))
			if rp == nil {
				return nil, "Not Found"
			}
			state := jStr(a, "state")
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
				out = append(out, pullView(s.store, req, rp, is))
			}
			return out, ""
		}},
		{"get_pull_request", "Get a pull request", func(s *Server, req *http.Request, a map[string]interface{}) (interface{}, string) {
			rp := s.store.findRepo(jStr(a, "owner"), jStr(a, "repo"))
			if rp == nil {
				return nil, "Not Found"
			}
			is := rp.findIssue(argNum(a, "pull_number"))
			if is == nil || !is.IsPR {
				return nil, "Not Found"
			}
			return pullView(s.store, req, rp, is), ""
		}},
		{"merge_pull_request", "Merge a pull request", func(s *Server, req *http.Request, a map[string]interface{}) (interface{}, string) {
			rp := s.store.findRepo(jStr(a, "owner"), jStr(a, "repo"))
			if rp == nil {
				return nil, "Not Found"
			}
			is := rp.findIssue(argNum(a, "pull_number"))
			if is == nil || !is.IsPR {
				return nil, "Not Found"
			}
			if is.Merged {
				return nil, "Pull Request is already merged"
			}
			baseSHA := rp.resolveRefish(is.BaseRef)
			headSHA := rp.resolveRefish(is.HeadRef)
			if baseSHA == "" || headSHA == "" {
				return nil, "Pull Request is not mergeable"
			}
			u := s.store.user(s.store.DefaultUser)
			t := now()
			method := jStr(a, "merge_method")
			var mergeSHA string
			switch method {
			case "squash":
				tree := rp.treeOfCommit(headSHA)
				msg := jStr(a, "commit_message")
				if msg == "" {
					msg = is.Title
				}
				mergeSHA = rp.put("commit", buildCommitData(tree, []string{baseSHA}, u.Login, u.Login+"@users.noreply.github.com", t.Unix(), "+0000", u.Login, u.Login+"@users.noreply.github.com", t.Unix(), "+0000", msg))
			case "rebase":
				if rp.isAncestor(baseSHA, headSHA) {
					mergeSHA = headSHA
				} else {
					tree := rp.treeOfCommit(headSHA)
					mergeSHA = rp.put("commit", buildCommitData(tree, []string{baseSHA}, u.Login, u.Login+"@users.noreply.github.com", t.Unix(), "+0000", u.Login, u.Login+"@users.noreply.github.com", t.Unix(), "+0000", is.Title))
				}
			default:
				var tree string
				if rp.isAncestor(baseSHA, headSHA) {
					tree = rp.treeOfCommit(headSHA)
				} else {
					mb := mergeBase(rp, baseSHA, headSHA)
					merged, conflict := threeWayTree(rp, rp.treeOfCommit(mb), rp.treeOfCommit(baseSHA), rp.treeOfCommit(headSHA))
					if conflict {
						return nil, "Pull Request is not mergeable"
					}
					tree = buildTreeFromFlat(rp, merged)
				}
				mergeSHA = rp.put("commit", buildCommitData(tree, []string{baseSHA, headSHA}, u.Login, u.Login+"@users.noreply.github.com", t.Unix(), "+0000", u.Login, u.Login+"@users.noreply.github.com", t.Unix(), "+0000", "Merge pull request #"+strconv.Itoa(is.Number)+" from "+rp.Owner+"/"+is.HeadRef))
			}
			rp.Refs["refs/heads/"+is.BaseRef] = mergeSHA
			is.Merged = true
			is.State = "closed"
			is.MergedAt = &t
			is.ClosedAt = &t
			is.MergedBy = s.store.DefaultUser
			is.MergeSHA = mergeSHA
			is.UpdatedAt = t
			return map[string]interface{}{"sha": mergeSHA, "merged": true, "message": "Pull Request successfully merged"}, ""
		}},
		{"get_pull_request_files", "List pull request files", func(s *Server, req *http.Request, a map[string]interface{}) (interface{}, string) {
			rp := s.store.findRepo(jStr(a, "owner"), jStr(a, "repo"))
			if rp == nil {
				return nil, "Not Found"
			}
			is := rp.findIssue(argNum(a, "pull_number"))
			if is == nil || !is.IsPR {
				return nil, "Not Found"
			}
			files := diffTrees(rp, rp.treeOfCommit(rp.resolveRefish(is.BaseRef)), rp.treeOfCommit(rp.resolveRefish(is.HeadRef)))
			out := []interface{}{}
			for _, f := range files {
				out = append(out, fileDiffView(req, rp, f))
			}
			return out, ""
		}},
		{"push_files", "Push multiple files in one commit", func(s *Server, req *http.Request, a map[string]interface{}) (interface{}, string) {
			rp := s.store.findRepo(jStr(a, "owner"), jStr(a, "repo"))
			if rp == nil {
				return nil, "Not Found"
			}
			branch := jStr(a, "branch")
			if branch == "" {
				branch = rp.HeadBranch
			}
			baseSHA := rp.resolveRefish(branch)
			if baseSHA == "" {
				return nil, "branch not found"
			}
			treeSHA := rp.treeOfCommit(baseSHA)
			files, _ := a["files"].([]interface{})
			for _, fv := range files {
				fm, ok := fv.(map[string]interface{})
				if !ok {
					continue
				}
				path := strings.Trim(jStr(fm, "path"), "/")
				blobSHA := rp.put("blob", []byte(jStr(fm, "content")))
				treeSHA = rp.updateTreePath(treeSHA, strings.Split(path, "/"), blobSHA, "100644")
			}
			u := s.store.user(s.store.DefaultUser)
			t := now()
			msg := jStr(a, "message")
			if msg == "" {
				msg = "Push files"
			}
			commitSHA := rp.put("commit", buildCommitData(treeSHA, []string{baseSHA}, u.Login, u.Login+"@users.noreply.github.com", t.Unix(), "+0000", u.Login, u.Login+"@users.noreply.github.com", t.Unix(), "+0000", msg))
			rp.Refs["refs/heads/"+branch] = commitSHA
			rp.UpdatedAt = t
			return map[string]interface{}{"commit": map[string]interface{}{"sha": commitSHA, "message": msg}}, ""
		}},
	}
	_ = str
	_ = boolean
	_ = base64.StdEncoding
}

func argNum(a map[string]interface{}, k string) int {
	switch v := a[k].(type) {
	case float64:
		return int(v)
	case int64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

func mcpToolList() []interface{} {
	schemas := map[string]map[string]interface{}{}
	str, num, boolT, arr := "string", "number", "boolean", "array"
	_ = arr
	repoProps := map[string]string{"owner": str, "repo": str}
	schemas["create_repository"] = toolSchema(map[string]string{"name": str, "description": str, "private": boolT, "auto_init": boolT}, []string{"name"})
	schemas["get_repository"] = toolSchema(repoProps, []string{"owner", "repo"})
	schemas["search_repositories"] = toolSchema(map[string]string{"query": str}, []string{"query"})
	schemas["list_branches"] = toolSchema(repoProps, []string{"owner", "repo"})
	schemas["create_branch"] = toolSchema(map[string]string{"owner": str, "repo": str, "branch": str, "from_branch": str}, []string{"owner", "repo", "branch"})
	schemas["get_file_contents"] = toolSchema(map[string]string{"owner": str, "repo": str, "path": str, "ref": str, "branch": str}, []string{"owner", "repo", "path"})
	schemas["create_or_update_file"] = toolSchema(map[string]string{"owner": str, "repo": str, "path": str, "content": str, "message": str, "branch": str, "sha": str}, []string{"owner", "repo", "path", "content", "message"})
	schemas["list_commits"] = toolSchema(map[string]string{"owner": str, "repo": str, "sha": str, "branch": str}, []string{"owner", "repo"})
	schemas["get_commit"] = toolSchema(map[string]string{"owner": str, "repo": str, "sha": str}, []string{"owner", "repo", "sha"})
	schemas["create_issue"] = toolSchema(map[string]string{"owner": str, "repo": str, "title": str, "body": str}, []string{"owner", "repo", "title"})
	schemas["list_issues"] = toolSchema(map[string]string{"owner": str, "repo": str, "state": str}, []string{"owner", "repo"})
	schemas["get_issue"] = toolSchema(map[string]string{"owner": str, "repo": str, "issue_number": num}, []string{"owner", "repo", "issue_number"})
	schemas["update_issue"] = toolSchema(map[string]string{"owner": str, "repo": str, "issue_number": num, "title": str, "body": str, "state": str}, []string{"owner", "repo", "issue_number"})
	schemas["add_issue_comment"] = toolSchema(map[string]string{"owner": str, "repo": str, "issue_number": num, "body": str}, []string{"owner", "repo", "issue_number", "body"})
	schemas["list_issue_comments"] = toolSchema(map[string]string{"owner": str, "repo": str, "issue_number": num}, []string{"owner", "repo", "issue_number"})
	schemas["create_pull_request"] = toolSchema(map[string]string{"owner": str, "repo": str, "title": str, "body": str, "head": str, "base": str}, []string{"owner", "repo", "title", "head", "base"})
	schemas["list_pull_requests"] = toolSchema(map[string]string{"owner": str, "repo": str, "state": str, "head": str, "base": str}, []string{"owner", "repo"})
	schemas["get_pull_request"] = toolSchema(map[string]string{"owner": str, "repo": str, "pull_number": num}, []string{"owner", "repo", "pull_number"})
	schemas["merge_pull_request"] = toolSchema(map[string]string{"owner": str, "repo": str, "pull_number": num, "merge_method": str, "commit_message": str}, []string{"owner", "repo", "pull_number"})
	schemas["get_pull_request_files"] = toolSchema(map[string]string{"owner": str, "repo": str, "pull_number": num}, []string{"owner", "repo", "pull_number"})
	schemas["push_files"] = map[string]interface{}{"type": "object", "properties": map[string]interface{}{
		"owner": map[string]interface{}{"type": str}, "repo": map[string]interface{}{"type": str},
		"branch": map[string]interface{}{"type": str}, "message": map[string]interface{}{"type": str},
		"files": map[string]interface{}{"type": arr, "items": map[string]interface{}{"type": "object"}},
	}, "required": []string{"owner", "repo", "branch", "files", "message"}}
	out := []interface{}{}
	for _, t := range mcpTools {
		out = append(out, map[string]interface{}{
			"name": t.Name, "description": t.Desc,
			"inputSchema": schemas[t.Name],
		})
	}
	return out
}

func (s *Server) handleMCP(w http.ResponseWriter, req *http.Request) {
	if req.Method == "GET" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	raw, _ := io.ReadAll(req.Body)
	body := map[string]interface{}{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			writeJSON(w, 200, map[string]interface{}{
				"jsonrpc": "2.0", "id": nil,
				"error": map[string]interface{}{"code": -32700, "message": "Parse error"},
			})
			return
		}
	}
	id := body["id"]
	method, _ := body["method"].(string)
	if id == nil {
		// notification
		w.WriteHeader(202)
		return
	}
	var result interface{}
	var rpcErr map[string]interface{}
	switch method {
	case "initialize":
		params := jObj(body, "params")
		proto := jStr(params, "protocolVersion")
		if proto == "" {
			proto = "2024-11-05"
		}
		result = map[string]interface{}{
			"protocolVersion": proto,
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{"listChanged": false},
			},
			"serverInfo": map[string]interface{}{"name": "anvil-go", "version": "1.0.0"},
		}
	case "ping":
		result = map[string]interface{}{}
	case "tools/list":
		result = map[string]interface{}{"tools": mcpToolList()}
	case "tools/call":
		params := jObj(body, "params")
		name := jStr(params, "name")
		args := jObj(params, "arguments")
		found := false
		for _, t := range mcpTools {
			if t.Name == name {
				found = true
				s.store.mu.Lock()
				v, errStr := t.Fn(s, req, args)
				s.store.mu.Unlock()
				if errStr != "" {
					result = mcpErr(errStr)
				} else {
					result = mcpText(v)
				}
				break
			}
		}
		if !found {
			rpcErr = map[string]interface{}{"code": -32601, "message": "Tool not found: " + name}
		}
	case "resources/list":
		result = map[string]interface{}{"resources": []interface{}{}}
	case "prompts/list":
		result = map[string]interface{}{"prompts": []interface{}{}}
	default:
		rpcErr = map[string]interface{}{"code": -32601, "message": "Method not found: " + method}
	}
	resp := map[string]interface{}{"jsonrpc": "2.0", "id": id}
	if rpcErr != nil {
		resp["error"] = rpcErr
	} else {
		resp["result"] = result
	}
	writeJSON(w, 200, resp)
}
