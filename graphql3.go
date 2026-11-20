package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

func (e *gExec) findByNodeID(idStr string) (GVal, bool) {
	dec, ok := b64decode(idStr)
	if !ok {
		return GVal{}, false
	}
	parts := strings.SplitN(dec, ":", 2)
	if len(parts) != 2 {
		return GVal{}, false
	}
	typ, key := parts[0], parts[1]
	st := e.st
	switch typ {
	case "Repository":
		id, _ := strconv.ParseInt(key, 10, 64)
		if rp := st.RepoByID[id]; rp != nil {
			return GVal{Kind: "Repository", Repo: rp}, true
		}
	case "Issue", "PullRequest":
		id, _ := strconv.ParseInt(key, 10, 64)
		for _, rp := range st.Repos {
			for _, is := range rp.Issues {
				if is.ID == id {
					kind := "Issue"
					if is.IsPR {
						kind = "PullRequest"
					}
					return GVal{Kind: kind, Repo: rp, Issue: is}, true
				}
			}
		}
	case "IssueComment":
		id, _ := strconv.ParseInt(key, 10, 64)
		for _, rp := range st.Repos {
			if c := rp.Comments[id]; c != nil {
				num := rp.CommentSeq[id]
				return GVal{Kind: "IssueComment", Repo: rp, Issue: rp.findIssue(num), Cmt: c}, true
			}
		}
	case "User":
		id, _ := strconv.ParseInt(key, 10, 64)
		for _, u := range st.Users {
			if u.ID == id {
				return GVal{Kind: "User", UserL: u.Login}, true
			}
		}
	case "Ref", "Commit", "Tree", "Blob", "Tag", "Label":
		sub := strings.SplitN(key, ":", 2)
		if len(sub) == 2 {
			rid, _ := strconv.ParseInt(sub[0], 10, 64)
			rp := st.RepoByID[rid]
			if rp == nil {
				return GVal{}, false
			}
			switch typ {
			case "Ref":
				if rp.Refs[sub[1]] != "" {
					return GVal{Kind: "Ref", Repo: rp, Ref: sub[1]}, true
				}
			case "Commit", "Tree", "Blob", "Tag":
				if rp.get(sub[1]) != nil {
					return GVal{Kind: typ, Repo: rp, SHA: sub[1]}, true
				}
			case "Label":
				if _, ok := rp.Labels[sub[1]]; ok {
					return GVal{Kind: "Label", Repo: rp, Label: sub[1]}, true
				}
			}
		}
	}
	return GVal{}, false
}

func (e *gExec) queryField(f *gField) (interface{}, bool) {
	st := e.st
	args := f.args
	switch f.name {
	case "__type":
		name := argStr(args, e, "name")
		if _, ok := gqlTypes[name]; ok {
			return typeGVal(name), true
		}
		return nil, true
	case "__schema":
		return GVal{Kind: "__Schema"}, true
	case "repository":
		owner := argStr(args, e, "owner")
		name := argStr(args, e, "name")
		rp := st.findRepo(owner, name)
		if rp == nil {
			return nil, true
		}
		return GVal{Kind: "Repository", Repo: rp}, true
	case "repositoryOwner":
		login := argStr(args, e, "login")
		return GVal{Kind: "User", UserL: login}, true
	case "user":
		login := argStr(args, e, "login")
		if _, ok := st.Users[strings.ToLower(login)]; !ok {
			return nil, true
		}
		return GVal{Kind: "User", UserL: login}, true
	case "organization":
		login := argStr(args, e, "login")
		return GVal{Kind: "User", UserL: login}, true
	case "viewer":
		return GVal{Kind: "User", UserL: st.DefaultUser}, true
	case "node":
		id := argStr(args, e, "id")
		if gv, ok := e.findByNodeID(id); ok {
			return gv, true
		}
		return nil, true
	case "nodes":
		var out []GVal
		if v, ok := args["ids"]; ok {
			if list, ok := e.val(v).([]interface{}); ok {
				for _, item := range list {
					if s, ok := item.(string); ok {
						if gv, ok2 := e.findByNodeID(s); ok2 {
							out = append(out, gv)
						}
					}
				}
			}
		}
		return out, true
	case "search":
		q := argStr(args, e, "query")
		typ := argStr(args, e, "type")
		return e.searchResult(q, typ, args), true
	case "rateLimit":
		return GVal{Kind: "RateLimit", Scalar: map[string]interface{}{
			"limit": 5000, "cost": 1, "remaining": 4999, "used": 1,
			"resetAt": fmtTime(now()), "nodeCount": 0,
		}}, true
	case "__typename":
		return "Query", true
	}
	return nil, false
}

func (e *gExec) searchResult(q, typ string, args map[string]*gValue) GVal {
	st := e.st
	needle := ""
	repoFilter := ""
	for _, t := range strings.Fields(q) {
		if strings.HasPrefix(t, "repo:") {
			repoFilter = strings.TrimPrefix(t, "repo:")
		} else if strings.HasPrefix(t, "type:") || strings.HasPrefix(t, "state:") || strings.HasPrefix(t, "is:") || strings.HasPrefix(t, "label:") {
			continue
		} else if !strings.Contains(t, ":") {
			if needle != "" {
				needle += " "
			}
			needle += t
		}
	}
	needle = strings.ToLower(needle)
	var items []GVal
	issueCount, repoCount := 0, 0
	switch strings.ToUpper(typ) {
	case "REPOSITORY":
		for _, rp := range st.Repos {
			if needle == "" || strings.Contains(strings.ToLower(rp.Name), needle) || strings.Contains(strings.ToLower(rp.Desc), needle) {
				items = append(items, GVal{Kind: "Repository", Repo: rp})
				repoCount++
			}
		}
	default: // ISSUE
		for _, rp := range st.Repos {
			if repoFilter != "" && !strings.EqualFold(rp.Owner+"/"+rp.Name, repoFilter) {
				continue
			}
			for _, is := range rp.Issues {
				if needle == "" || strings.Contains(strings.ToLower(is.Title), needle) || strings.Contains(strings.ToLower(is.Body), needle) {
					kind := "Issue"
					if is.IsPR {
						kind = "PullRequest"
					}
					items = append(items, GVal{Kind: kind, Repo: rp, Issue: is})
					issueCount++
				}
			}
		}
	}
	conn := e.conn(items, args)
	return GVal{Kind: "SearchResult", Items: conn.Items, Total: conn.Total, Scalar: map[string]interface{}{
		"issueCount": issueCount, "repositoryCount": repoCount,
	}}
}

func inputObj(args map[string]*gValue, e *gExec) map[string]interface{} {
	if v, ok := args["input"]; ok {
		if m, ok := e.val(v).(map[string]interface{}); ok {
			return m
		}
	}
	return map[string]interface{}{}
}

func mStr(m map[string]interface{}, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func (e *gExec) mutationField(f *gField) (interface{}, bool) {
	st := e.st
	in := inputObj(f.args, e)
	cmid := mStr(in, "clientMutationId")
	payload := func(kind string, node *GVal) GVal {
		sc := map[string]interface{}{}
		if cmid != "" {
			sc["clientMutationId"] = cmid
		}
		return GVal{Kind: kind, Node: node, Scalar: sc}
	}
	switch f.name {
	case "createIssue":
		rid := mStr(in, "repositoryId")
		gv, ok := e.findByNodeID(rid)
		if !ok || gv.Kind != "Repository" {
			return nil, true
		}
		rp := gv.Repo
		is := &Issue{
			ID: st.NextIssueID, Number: rp.NextNum, Title: mStr(in, "title"),
			Body: mStr(in, "body"), State: "open", User: st.DefaultUser,
			CreatedAt: now(), UpdatedAt: now(),
		}
		st.NextIssueID++
		rp.NextNum++
		if labels, ok := in["labelIds"].([]interface{}); ok {
			for _, lid := range labels {
				if s, ok := lid.(string); ok {
					if lgv, ok2 := e.findByNodeID(s); ok2 && lgv.Kind == "Label" {
						is.Labels = append(is.Labels, lgv.Label)
					}
				}
			}
		}
		if assignees, ok := in["assigneeIds"].([]interface{}); ok {
			for _, aid := range assignees {
				if s, ok := aid.(string); ok {
					if ugv, ok2 := e.findByNodeID(s); ok2 && ugv.Kind == "User" {
						is.Assignees = append(is.Assignees, ugv.UserL)
					}
				}
			}
		}
		for _, l := range is.Labels {
			if _, ok := rp.Labels[l]; !ok {
				rp.Labels[l] = "ededed"
			}
		}
		rp.Issues = append(rp.Issues, is)
		rp.UpdatedAt = now()
		node := GVal{Kind: "Issue", Repo: rp, Issue: is}
		return payload("CreateIssuePayload", &node), true
	case "updateIssue", "closeIssue", "reopenIssue":
		id := mStr(in, "id")
		if id == "" {
			id = mStr(in, "issueId")
		}
		gv, ok := e.findByNodeID(id)
		if !ok || gv.Issue == nil {
			return nil, true
		}
		is := gv.Issue
		if f.name == "closeIssue" {
			is.State = "closed"
			t := now()
			is.ClosedAt = &t
		} else if f.name == "reopenIssue" {
			is.State = "open"
			is.ClosedAt = nil
		} else {
			if v := mStr(in, "title"); v != "" {
				is.Title = v
			}
			if _, has := in["body"]; has {
				is.Body = mStr(in, "body")
			}
			if v := mStr(in, "state"); v != "" {
				is.State = strings.ToLower(v)
				if is.State == "closed" {
					t := now()
					is.ClosedAt = &t
				} else {
					is.ClosedAt = nil
				}
			}
		}
		is.UpdatedAt = now()
		kind := "Issue"
		if is.IsPR {
			kind = "PullRequest"
		}
		node := GVal{Kind: kind, Repo: gv.Repo, Issue: is}
		pname := "UpdateIssuePayload"
		if f.name == "closeIssue" {
			pname = "CloseIssuePayload"
		} else if f.name == "reopenIssue" {
			pname = "ReopenIssuePayload"
		}
		return payload(pname, &node), true
	case "addComment":
		sid := mStr(in, "subjectId")
		gv, ok := e.findByNodeID(sid)
		if !ok || gv.Issue == nil {
			return nil, true
		}
		rp := gv.Repo
		is := gv.Issue
		c := &Comment{ID: st.NextCmtID, Body: mStr(in, "body"), User: st.DefaultUser, CreatedAt: now(), UpdatedAt: now()}
		st.NextCmtID++
		is.Comments = append(is.Comments, c)
		is.UpdatedAt = now()
		rp.Comments[c.ID] = c
		rp.CommentSeq[c.ID] = is.Number
		node := GVal{Kind: "IssueComment", Repo: rp, Issue: is, Cmt: c}
		return payload("AddCommentPayload", &node), true
	case "updateIssueComment":
		id := mStr(in, "id")
		gv, ok := e.findByNodeID(id)
		if !ok || gv.Cmt == nil {
			return nil, true
		}
		gv.Cmt.Body = mStr(in, "body")
		gv.Cmt.UpdatedAt = now()
		node := GVal{Kind: "IssueComment", Repo: gv.Repo, Issue: gv.Issue, Cmt: gv.Cmt}
		return payload("UpdateIssueCommentPayload", &node), true
	case "deleteIssueComment":
		id := mStr(in, "id")
		gv, ok := e.findByNodeID(id)
		if ok && gv.Cmt != nil {
			rp := gv.Repo
			is := gv.Issue
			if is != nil {
				for i, cc := range is.Comments {
					if cc.ID == gv.Cmt.ID {
						is.Comments = append(is.Comments[:i], is.Comments[i+1:]...)
						break
					}
				}
			}
			delete(rp.Comments, gv.Cmt.ID)
		}
		return payload("DeleteIssueCommentPayload", nil), true
	case "createPullRequest":
		rid := mStr(in, "repositoryId")
		gv, ok := e.findByNodeID(rid)
		if !ok || gv.Kind != "Repository" {
			return nil, true
		}
		rp := gv.Repo
		head := mStr(in, "headRefName")
		base := mStr(in, "baseRefName")
		if rp.resolveRefish(head) == "" || rp.resolveRefish(base) == "" {
			e.errors = append(e.errors, map[string]interface{}{"message": "Invalid head or base ref", "type": "Invalid"})
			return nil, true
		}
		is := &Issue{
			ID: st.NextIssueID, Number: rp.NextNum, Title: mStr(in, "title"),
			Body: mStr(in, "body"), State: "open", User: st.DefaultUser,
			CreatedAt: now(), UpdatedAt: now(), IsPR: true, HeadRef: head, BaseRef: base,
		}
		st.NextIssueID++
		rp.NextNum++
		rp.Issues = append(rp.Issues, is)
		rp.UpdatedAt = now()
		node := GVal{Kind: "PullRequest", Repo: rp, Issue: is}
		return payload("CreatePullRequestPayload", &node), true
	case "mergePullRequest":
		id := mStr(in, "pullRequestId")
		gv, ok := e.findByNodeID(id)
		if !ok || gv.Issue == nil || !gv.Issue.IsPR {
			return nil, true
		}
		rp := gv.Repo
		is := gv.Issue
		// perform merge like REST
		baseSHA := rp.resolveRefish(is.BaseRef)
		headSHA := rp.resolveRefish(is.HeadRef)
		if baseSHA != "" && headSHA != "" && !is.Merged {
			u := st.user(st.DefaultUser)
			t := now()
			method := strings.ToUpper(mStr(in, "mergeMethod"))
			var mergeSHA string
			switch method {
			case "SQUASH":
				tree := rp.treeOfCommit(headSHA)
				mergeSHA = rp.put("commit", buildCommitData(tree, []string{baseSHA}, u.Login, u.Login+"@users.noreply.github.com", t.Unix(), "+0000", u.Login, u.Login+"@users.noreply.github.com", t.Unix(), "+0000", is.Title+" (#"+strconv.Itoa(is.Number)+")"))
			case "REBASE":
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
					if !conflict {
						tree = buildTreeFromFlat(rp, merged)
					}
				}
				if tree != "" {
					mergeSHA = rp.put("commit", buildCommitData(tree, []string{baseSHA, headSHA}, u.Login, u.Login+"@users.noreply.github.com", t.Unix(), "+0000", u.Login, u.Login+"@users.noreply.github.com", t.Unix(), "+0000", "Merge pull request #"+strconv.Itoa(is.Number)+" from "+rp.Owner+"/"+is.HeadRef))
				}
			}
			if mergeSHA != "" {
				rp.Refs["refs/heads/"+is.BaseRef] = mergeSHA
				is.Merged = true
				is.State = "closed"
				is.MergedAt = &t
				is.ClosedAt = &t
				is.MergedBy = st.DefaultUser
				is.MergeSHA = mergeSHA
				is.UpdatedAt = t
				rp.UpdatedAt = t
			}
		}
		node := GVal{Kind: "PullRequest", Repo: rp, Issue: is}
		return payload("MergePullRequestPayload", &node), true
	case "createRepository":
		name := mStr(in, "name")
		if name == "" {
			return nil, true
		}
		owner := st.DefaultUser
		if oid := mStr(in, "ownerId"); oid != "" {
			if gv, ok := e.findByNodeID(oid); ok && gv.Kind == "User" {
				owner = gv.UserL
			}
		}
		if st.findRepo(owner, name) != nil {
			e.errors = append(e.errors, map[string]interface{}{"message": "Name already exists on this account", "type": "Invalid"})
			return nil, true
		}
		vis := strings.ToUpper(mStr(in, "visibility"))
		rp := st.createRepo(owner, name, mStr(in, "description"), vis == "PRIVATE")
		node := GVal{Kind: "Repository", Repo: rp}
		return payload("CreateRepositoryPayload", &node), true
	case "deleteRepository":
		rid := mStr(in, "repositoryId")
		gv, ok := e.findByNodeID(rid)
		if ok && gv.Kind == "Repository" {
			st.deleteRepo(gv.Repo.Owner, gv.Repo.Name)
		}
		return payload("DeleteRepositoryPayload", nil), true
	case "createRef":
		rid := mStr(in, "repositoryId")
		gv, ok := e.findByNodeID(rid)
		if !ok || gv.Kind != "Repository" {
			return nil, true
		}
		rp := gv.Repo
		name := mStr(in, "name")
		oid := mStr(in, "oid")
		if name != "" && oid != "" && rp.get(oid) != nil {
			rp.Refs[name] = oid
			rp.UpdatedAt = now()
			node := GVal{Kind: "Ref", Repo: rp, Ref: name}
			return payload("CreateRefPayload", &node), true
		}
		return nil, true
	case "addLabelsToLabelable", "removeLabelsFromLabelable":
		lid := mStr(in, "labelableId")
		gv, ok := e.findByNodeID(lid)
		if !ok || gv.Issue == nil {
			return nil, true
		}
		is := gv.Issue
		rp := gv.Repo
		if labelIds, ok := in["labelIds"].([]interface{}); ok {
			for _, x := range labelIds {
				if s, ok := x.(string); ok {
					if lgv, ok2 := e.findByNodeID(s); ok2 && lgv.Kind == "Label" {
						if f.name == "addLabelsToLabelable" {
							found := false
							for _, l := range is.Labels {
								if l == lgv.Label {
									found = true
								}
							}
							if !found {
								is.Labels = append(is.Labels, lgv.Label)
							}
							if _, ok3 := rp.Labels[lgv.Label]; !ok3 {
								rp.Labels[lgv.Label] = "ededed"
							}
						} else {
							var kept []string
							for _, l := range is.Labels {
								if l != lgv.Label {
									kept = append(kept, l)
								}
							}
							is.Labels = kept
						}
					}
				}
			}
		}
		is.UpdatedAt = now()
		kind := "Issue"
		if is.IsPR {
			kind = "PullRequest"
		}
		node := GVal{Kind: kind, Repo: rp, Issue: is}
		pname := "AddLabelsToLabelablePayload"
		if f.name == "removeLabelsFromLabelable" {
			pname = "RemoveLabelsFromLabelablePayload"
		}
		return payload(pname, &node), true
	case "createLabel":
		rid := mStr(in, "repositoryId")
		gv, ok := e.findByNodeID(rid)
		if !ok || gv.Kind != "Repository" {
			return nil, true
		}
		rp := gv.Repo
		name := mStr(in, "name")
		color := mStr(in, "color")
		if color == "" {
			color = "ededed"
		}
		rp.Labels[name] = strings.TrimPrefix(color, "#")
		node := GVal{Kind: "Label", Repo: rp, Label: name}
		sc := map[string]interface{}{}
		if cmid != "" {
			sc["clientMutationId"] = cmid
		}
		return GVal{Kind: "CreateLabelPayload", Node: &node, Scalar: sc}, true
	}
	return nil, false
}

// ---------------- HTTP ----------------

func (s *Server) handleGraphQL(w http.ResponseWriter, req *http.Request) {
	st := s.store
	st.mu.Lock()
	defer st.mu.Unlock()
	if req.Method == "GET" {
		q := req.URL.Query()
		body := map[string]interface{}{"query": q.Get("query"), "operationName": q.Get("operationName")}
		status, resp := s.executeGraphQL(req, body)
		writeJSON(w, status, resp)
		return
	}
	raw, _ := io.ReadAll(req.Body)
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var batch []map[string]interface{}
		if err := json.Unmarshal(trimmed, &batch); err != nil {
			writeJSON(w, 400, map[string]interface{}{"errors": []interface{}{map[string]interface{}{"message": "Problems parsing JSON"}}})
			return
		}
		out := make([]interface{}, 0, len(batch))
		for _, item := range batch {
			_, resp := s.executeGraphQL(req, item)
			out = append(out, resp)
		}
		writeJSON(w, 200, out)
		return
	}
	body := map[string]interface{}{}
	if len(trimmed) > 0 {
		if err := json.Unmarshal(trimmed, &body); err != nil {
			writeJSON(w, 400, map[string]interface{}{"errors": []interface{}{map[string]interface{}{"message": "Problems parsing JSON"}}})
			return
		}
	}
	status, resp := s.executeGraphQL(req, body)
	writeJSON(w, status, resp)
}

// executeGraphQL runs one GraphQL operation object and returns the HTTP
// status and response body. Caller must hold the store lock.
func (s *Server) executeGraphQL(req *http.Request, body map[string]interface{}) (int, map[string]interface{}) {
	st := s.store
	var vars map[string]interface{}
	query := jStr(body, "query")
	opName := jStr(body, "operationName")
	if vm, ok := body["variables"].(map[string]interface{}); ok {
		vars = vm
	}
	if query == "" {
		return 400, map[string]interface{}{"errors": []interface{}{map[string]interface{}{"message": "No query provided"}}}
	}
	ops, frags, err := parseDocument(query)
	if err != nil {
		return 400, map[string]interface{}{"errors": []interface{}{map[string]interface{}{"message": err.Error()}}}
	}
	var op *gOp
	for _, o := range ops {
		if opName == "" || o.name == opName {
			op = o
			break
		}
	}
	if op == nil {
		return 400, map[string]interface{}{"errors": []interface{}{map[string]interface{}{"message": "Operation not found"}}}
	}
	ex := &gExec{st: st, req: req, vars: map[string]interface{}{}, frags: frags}
	for k, v := range vars {
		ex.vars[k] = v
	}
	for k, dv := range op.defaults {
		if _, has := ex.vars[k]; !has {
			ex.vars[k] = ex.val(dv)
		}
	}
	rootKind := "Query"
	if op.typ == "mutation" {
		rootKind = "Mutation"
	}
	data := ex.execSel(GVal{Kind: rootKind}, op.sel)
	resp := map[string]interface{}{"data": data}
	if len(ex.errors) > 0 {
		errs := make([]interface{}, 0, len(ex.errors))
		for _, e2 := range ex.errors {
			errs = append(errs, e2)
		}
		resp["errors"] = errs
	}
	return 200, resp
}
