package main

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

func stateEnum(open bool, merged bool) string {
	if merged {
		return "MERGED"
	}
	if open {
		return "OPEN"
	}
	return "CLOSED"
}

func (e *gExec) fieldValue(gv GVal, f *gField) (interface{}, bool) {
	name := f.name
	args := f.args
	st := e.st
	switch gv.Kind {
	case "__Type", "__Field", "__EnumValue", "__InputValue", "__Schema", "__Directive":
		return e.introspectionField(gv, name)
	case "Query":
		return e.queryField(f)
	case "Mutation":
		return e.mutationField(f)
	case "Connection":
		switch name {
		case "totalCount":
			return gv.Total, true
		case "nodes":
			return gv.Items, true
		case "edges":
			edges := make([]GVal, 0, len(gv.Items))
			start := 0
			if gv.Scalar != nil {
				if s, ok := gv.Scalar["_start"].(int); ok {
					start = s
				}
			}
			for i := range gv.Items {
				item := gv.Items[i]
				edges = append(edges, GVal{Kind: "Edge", Node: &item, Cursor: b64encode(strconv.Itoa(start + i))})
			}
			return edges, true
		case "pageInfo":
			start, end, total := 0, 0, gv.Total
			if gv.Scalar != nil {
				start, _ = gv.Scalar["_start"].(int)
				end, _ = gv.Scalar["_end"].(int)
			}
			sc := map[string]interface{}{
				"hasNextPage": end < total, "hasPreviousPage": start > 0,
			}
			if len(gv.Items) > 0 {
				sc["startCursor"] = b64encode(strconv.Itoa(start))
				sc["endCursor"] = b64encode(strconv.Itoa(end - 1))
			}
			return GVal{Kind: "PageInfo", Scalar: sc}, true
		}
	case "Edge":
		switch name {
		case "node":
			if gv.Node != nil {
				return *gv.Node, true
			}
			return nil, true
		case "cursor":
			return gv.Cursor, true
		}
	case "PageInfo":
		if v, ok := gv.Scalar[name]; ok {
			return v, true
		}
		return nil, true
	case "RateLimit", "PullRequestChangedFile":
		if v, ok := gv.Scalar[name]; ok {
			return v, true
		}
		return nil, true
	case "User":
		u := st.user(gv.UserL)
		switch name {
		case "id":
			return e.entityID(gv), true
		case "login":
			return u.Login, true
		case "name":
			return u.Login, true
		case "avatarUrl":
			return "", true
		case "url":
			return "", true
		case "createdAt":
			return fmtTime(now()), true
		case "updatedAt":
			return fmtTime(now()), true
		case "isSiteAdmin":
			return false, true
		case "repositories":
			var items []GVal
			var repos []*Repo
			for _, rp := range st.Repos {
				if strings.EqualFold(rp.Owner, u.Login) {
					repos = append(repos, rp)
				}
			}
			sort.Slice(repos, func(i, j int) bool { return repos[i].ID < repos[j].ID })
			for _, rp := range repos {
				items = append(items, GVal{Kind: "Repository", Repo: rp})
			}
			return e.conn(items, args), true
		}
	case "Repository":
		return e.repoField(gv, f)
	case "Issue", "PullRequest":
		return e.issueField(gv, f)
	case "IssueComment":
		c := gv.Cmt
		switch name {
		case "id":
			return e.entityID(gv), true
		case "body":
			return c.Body, true
		case "bodyText":
			return c.Body, true
		case "bodyHTML":
			return c.Body, true
		case "author":
			return GVal{Kind: "User", UserL: c.User}, true
		case "createdAt":
			return fmtTime(c.CreatedAt), true
		case "updatedAt":
			return fmtTime(c.UpdatedAt), true
		case "url":
			return "", true
		case "authorAssociation":
			return "OWNER", true
		}
	case "Ref":
		rp := gv.Repo
		sha := rp.Refs[gv.Ref]
		switch name {
		case "id":
			return e.entityID(gv), true
		case "name":
			n := gv.Ref
			n = strings.TrimPrefix(n, "refs/heads/")
			n = strings.TrimPrefix(n, "refs/tags/")
			return n, true
		case "prefix":
			if strings.HasPrefix(gv.Ref, "refs/heads/") {
				return "refs/heads/", true
			}
			return "refs/tags/", true
		case "qualifiedName":
			return gv.Ref, true
		case "target":
			if sha == "" {
				return nil, true
			}
			o := rp.get(sha)
			if o == nil {
				return nil, true
			}
			switch o.Type {
			case "commit":
				return GVal{Kind: "Commit", Repo: rp, SHA: sha}, true
			case "tag":
				return GVal{Kind: "Tag", Repo: rp, SHA: sha}, true
			case "tree":
				return GVal{Kind: "Tree", Repo: rp, SHA: sha}, true
			case "blob":
				return GVal{Kind: "Blob", Repo: rp, SHA: sha}, true
			}
			return nil, true
		case "repository":
			return GVal{Kind: "Repository", Repo: rp}, true
		}
	case "Commit":
		return e.commitField(gv, f)
	case "Tree":
		rp := gv.Repo
		switch name {
		case "id":
			return e.entityID(gv), true
		case "oid":
			return gv.SHA, true
		case "entries":
			o := rp.get(gv.SHA)
			if o == nil {
				return []GVal{}, true
			}
			var items []GVal
			for _, en := range parseTree(o.Data) {
				items = append(items, GVal{Kind: "TreeEntry", Repo: rp, SHA: en.SHA, Scalar: map[string]interface{}{"name": en.Name, "mode": en.Mode, "type": en}})
			}
			return items, true
		case "repository":
			return GVal{Kind: "Repository", Repo: rp}, true
		}
	case "TreeEntry":
		en := gv.Scalar["type"].(TreeEntry)
		switch name {
		case "name":
			return en.Name, true
		case "path":
			return en.Name, true
		case "mode":
			n, _ := strconv.Atoi(en.Mode)
			if en.IsTree() && n < 10000 {
				n = 40000
			}
			return n, true
		case "type":
			if en.IsTree() {
				return "tree", true
			}
			return "blob", true
		case "object":
			if en.IsTree() {
				return GVal{Kind: "Tree", Repo: gv.Repo, SHA: en.SHA}, true
			}
			return GVal{Kind: "Blob", Repo: gv.Repo, SHA: en.SHA}, true
		case "extension":
			return "", true
		}
	case "Blob":
		rp := gv.Repo
		o := rp.get(gv.SHA)
		switch name {
		case "id":
			return e.entityID(gv), true
		case "oid":
			return gv.SHA, true
		case "isBinary":
			if o == nil {
				return true, true
			}
			return isBinary(o.Data), true
		case "byteSize":
			if o == nil {
				return 0, true
			}
			return len(o.Data), true
		case "text":
			if o == nil || isBinary(o.Data) {
				return nil, true
			}
			return string(o.Data), true
		case "repository":
			return GVal{Kind: "Repository", Repo: rp}, true
		}
	case "Tag":
		rp := gv.Repo
		o := rp.get(gv.SHA)
		if o == nil {
			return nil, false
		}
		ti := parseTag(o.Data)
		switch name {
		case "id":
			return gid("Tag", strconv.FormatInt(rp.ID, 10)+":"+gv.SHA), true
		case "oid":
			return gv.SHA, true
		case "name":
			return ti.Tag, true
		case "message":
			return strings.TrimRight(ti.Message, "\n"), true
		case "target":
			tgt := ti.Object
			if to := rp.get(tgt); to != nil && to.Type == "commit" {
				return GVal{Kind: "Commit", Repo: rp, SHA: tgt}, true
			}
			return nil, true
		case "tagger":
			return GVal{Kind: "GitActor", Scalar: map[string]interface{}{"name": ti.Name, "email": ti.Email, "date": fmtTime(time.Unix(ti.Time, 0).UTC())}}, true
		}
	case "GitActor":
		if v, ok := gv.Scalar[name]; ok {
			return v, true
		}
		if name == "user" {
			if n, ok := gv.Scalar["name"].(string); ok {
				if _, exists := e.st.Users[strings.ToLower(n)]; exists {
					return GVal{Kind: "User", UserL: n}, true
				}
			}
			return nil, true
		}
		return nil, true
	case "Label":
		switch name {
		case "id":
			return e.entityID(gv), true
		case "name":
			return gv.Label, true
		case "color":
			c := gv.Repo.Labels[gv.Label]
			if c == "" {
				c = "ededed"
			}
			return c, true
		case "description":
			return nil, true
		case "isDefault":
			return false, true
		case "url":
			return "", true
		}
	case "SearchResult":
		switch name {
		case "issueCount":
			return gv.Scalar["issueCount"], true
		case "repositoryCount":
			return gv.Scalar["repositoryCount"], true
		case "codeCount":
			return 0, true
		case "userCount":
			return 0, true
		case "wikiCount":
			return 0, true
		case "discussionCount":
			return 0, true
		case "edges":
			items := gv.Items
			edges := make([]GVal, 0, len(items))
			for i := range items {
				it := items[i]
				edges = append(edges, GVal{Kind: "Edge", Node: &it, Cursor: b64encode(strconv.Itoa(i))})
			}
			return edges, true
		case "nodes":
			return gv.Items, true
		case "pageInfo":
			return GVal{Kind: "PageInfo", Scalar: map[string]interface{}{"hasNextPage": false, "hasPreviousPage": false}}, true
		}
	}
	// payload kinds: "<something>Payload"
	if strings.HasSuffix(gv.Kind, "Payload") {
		if name == "clientMutationId" {
			if v, ok := gv.Scalar["clientMutationId"]; ok {
				return v, true
			}
			return nil, true
		}
		if gv.Node != nil {
			switch name {
			case "issue", "pullRequest", "repository", "comment", "ref", "labelable", "subject", "label":
				return *gv.Node, true
			case "commentEdge":
				return GVal{Kind: "Edge", Node: gv.Node, Cursor: b64encode("1")}, true
			}
		}
		if v, ok := gv.Scalar[name]; ok {
			return v, true
		}
	}
	return nil, false
}

func (e *gExec) repoField(gv GVal, f *gField) (interface{}, bool) {
	rp := gv.Repo
	args := f.args
	switch f.name {
	case "id":
		return e.entityID(gv), true
	case "name":
		return rp.Name, true
	case "nameWithOwner":
		return rp.Owner + "/" + rp.Name, true
	case "owner":
		return GVal{Kind: "User", UserL: rp.Owner}, true
	case "description":
		if rp.Desc == "" {
			return nil, true
		}
		return rp.Desc, true
	case "databaseId":
		return rp.ID, true
	case "isPrivate":
		return rp.Private, true
	case "visibility":
		if rp.Private {
			return "PRIVATE", true
		}
		return "PUBLIC", true
	case "hasIssuesEnabled", "hasProjectsEnabled", "hasWikiEnabled":
		return true, true
	case "isDisabled", "isTemplate", "isMirror", "isLocked":
		return false, true
	case "mergeCommitAllowed", "squashMergeAllowed", "rebaseMergeAllowed":
		return true, true
	case "resourcePath":
		return "/" + rp.Owner + "/" + rp.Name, true
	case "isEmpty":
		return len(rp.Refs) == 0, true
	case "createdAt":
		return fmtTime(rp.CreatedAt), true
	case "updatedAt":
		return fmtTime(rp.UpdatedAt), true
	case "pushedAt":
		return fmtTime(rp.UpdatedAt), true
	case "url":
		return "", true
	case "sshUrl":
		return "", true
	case "homepageUrl":
		return nil, true
	case "defaultBranchRef":
		ref := "refs/heads/" + rp.HeadBranch
		if rp.Refs[ref] == "" {
			return nil, true
		}
		return GVal{Kind: "Ref", Repo: rp, Ref: ref}, true
	case "isFork":
		return false, true
	case "isArchived":
		return false, true
	case "stargazerCount":
		return 0, true
	case "forkCount":
		return 0, true
	case "diskUsage":
		return 0, true
	case "viewerPermission":
		return "ADMIN", true
	case "primaryLanguage":
		return nil, true
	case "refs":
		prefix := argStr(args, e, "refPrefix")
		if prefix == "" {
			prefix = "refs/"
		}
		var items []GVal
		for _, ref := range sortedRefs(rp) {
			if strings.HasPrefix(ref, prefix) {
				items = append(items, GVal{Kind: "Ref", Repo: rp, Ref: ref})
			}
		}
		return e.conn(items, args), true
	case "ref":
		qn := argStr(args, e, "qualifiedName")
		if qn == "" {
			return nil, true
		}
		if !strings.HasPrefix(qn, "refs/") {
			qn = "refs/heads/" + qn
		}
		if rp.Refs[qn] == "" {
			return nil, true
		}
		return GVal{Kind: "Ref", Repo: rp, Ref: qn}, true
	case "issues":
		return e.issueConn(rp, false, args), true
	case "issue":
		num := argInt(args, e, "number", 0)
		is := rp.findIssue(num)
		if is == nil || is.IsPR {
			return nil, true
		}
		return GVal{Kind: "Issue", Repo: rp, Issue: is}, true
	case "pullRequests":
		return e.issueConn(rp, true, args), true
	case "pullRequest":
		num := argInt(args, e, "number", 0)
		is := rp.findIssue(num)
		if is == nil || !is.IsPR {
			return nil, true
		}
		return GVal{Kind: "PullRequest", Repo: rp, Issue: is}, true
	case "labels":
		var items []GVal
		names := make([]string, 0, len(rp.Labels))
		for n := range rp.Labels {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			items = append(items, GVal{Kind: "Label", Repo: rp, Label: n})
		}
		return e.conn(items, args), true
	case "label":
		name := argStr(args, e, "name")
		if _, ok := rp.Labels[name]; ok {
			return GVal{Kind: "Label", Repo: rp, Label: name}, true
		}
		return nil, true
	case "object":
		expr := argStr(args, e, "expression")
		oid := argStr(args, e, "oid")
		sha := ""
		if oid != "" {
			sha = oid
		} else if expr != "" {
			// forms: "HEAD:path", "branch:path", "sha", "HEAD"
			parts := strings.SplitN(expr, ":", 2)
			refish := parts[0]
			base := rp.resolveRefish(refish)
			if base == "" {
				return nil, true
			}
			if len(parts) == 1 {
				sha = rp.peelToCommit(base)
				if sha == "" {
					sha = base
				}
			} else {
				// walk path
				treeSHA := rp.treeOfCommit(rp.peelToCommit(base))
				pathParts := strings.Split(parts[1], "/")
				cur := treeSHA
				for i, pp := range pathParts {
					o := rp.get(cur)
					if o == nil || o.Type != "tree" {
						return nil, true
					}
					var found *TreeEntry
					for _, en := range parseTree(o.Data) {
						if en.Name == pp {
							ee := en
							found = &ee
							break
						}
					}
					if found == nil {
						return nil, true
					}
					if i == len(pathParts)-1 {
						sha = found.SHA
					} else {
						cur = found.SHA
					}
				}
			}
		}
		if sha == "" {
			return nil, true
		}
		o := rp.get(sha)
		if o == nil {
			return nil, true
		}
		switch o.Type {
		case "commit":
			return GVal{Kind: "Commit", Repo: rp, SHA: sha}, true
		case "tree":
			return GVal{Kind: "Tree", Repo: rp, SHA: sha}, true
		case "blob":
			return GVal{Kind: "Blob", Repo: rp, SHA: sha}, true
		case "tag":
			return GVal{Kind: "Tag", Repo: rp, SHA: sha}, true
		}
		return nil, true
	}
	return nil, false
}

func (e *gExec) issueConn(rp *Repo, prs bool, args map[string]*gValue) GVal {
	states := map[string]bool{}
	if v, ok := args["states"]; ok {
		if list, ok := e.val(v).([]interface{}); ok {
			for _, s := range list {
				if str, ok := s.(string); ok {
					states[strings.ToLower(str)] = true
				}
			}
		}
	}
	if len(states) == 0 {
		states["open"] = true
	}
	var labelFilter []string
	if v, ok := args["labels"]; ok {
		if list, ok := e.val(v).([]interface{}); ok {
			for _, s := range list {
				if str, ok := s.(string); ok {
					labelFilter = append(labelFilter, str)
				}
			}
		}
	}
	var items []GVal
	for _, is := range rp.Issues {
		if is.IsPR != prs {
			continue
		}
		if !states[is.State] {
			continue
		}
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
		kind := "Issue"
		if prs {
			kind = "PullRequest"
		}
		items = append(items, GVal{Kind: kind, Repo: rp, Issue: is})
	}
	// Honor orderBy: {field: CREATED_AT|UPDATED_AT, direction: ASC|DESC}.
	// Without an explicit orderBy, insertion order is kept.
	if v, ok := args["orderBy"]; ok {
		dir := "DESC"
		if obj, ok := e.val(v).(map[string]interface{}); ok {
			if d, ok := obj["direction"].(string); ok && d != "" {
				dir = d
			}
		}
		sort.SliceStable(items, func(i, j int) bool {
			a, b := items[i].Issue, items[j].Issue
			if a == nil || b == nil {
				return false
			}
			if dir == "ASC" {
				if !a.CreatedAt.Equal(b.CreatedAt) {
					return a.CreatedAt.Before(b.CreatedAt)
				}
				return a.Number < b.Number
			}
			if !a.CreatedAt.Equal(b.CreatedAt) {
				return a.CreatedAt.After(b.CreatedAt)
			}
			return a.Number > b.Number
		})
	}
	return e.conn(items, args)
}

func (e *gExec) issueField(gv GVal, f *gField) (interface{}, bool) {
	is := gv.Issue
	rp := gv.Repo
	args := f.args
	switch f.name {
	case "id":
		return e.entityID(gv), true
	case "number":
		return is.Number, true
	case "title":
		return is.Title, true
	case "body":
		return is.Body, true
	case "bodyText":
		return is.Body, true
	case "bodyHTML":
		return is.Body, true
	case "state":
		return stateEnum(is.State == "open", is.Merged), true
	case "author":
		return GVal{Kind: "User", UserL: is.User}, true
	case "createdAt":
		return fmtTime(is.CreatedAt), true
	case "updatedAt":
		return fmtTime(is.UpdatedAt), true
	case "closedAt":
		if is.ClosedAt != nil {
			return fmtTime(*is.ClosedAt), true
		}
		return nil, true
	case "url":
		return "", true
	case "repository":
		return GVal{Kind: "Repository", Repo: rp}, true
	case "labels":
		var items []GVal
		for _, l := range is.Labels {
			items = append(items, GVal{Kind: "Label", Repo: rp, Label: l})
		}
		return e.conn(items, args), true
	case "assignees":
		var items []GVal
		for _, a := range is.Assignees {
			items = append(items, GVal{Kind: "User", UserL: a})
		}
		return e.conn(items, args), true
	case "comments":
		var items []GVal
		for _, c := range is.Comments {
			items = append(items, GVal{Kind: "IssueComment", Repo: rp, Issue: is, Cmt: c})
		}
		return e.conn(items, args), true
	case "milestone":
		return nil, true
	case "authorAssociation":
		return "OWNER", true
	case "locked":
		return false, true
	case "viewerCanUpdate":
		return true, true
	}
	if gv.Kind == "PullRequest" {
		switch f.name {
		case "headRefName":
			return is.HeadRef, true
		case "baseRefName":
			return is.BaseRef, true
		case "headRef":
			ref := "refs/heads/" + is.HeadRef
			if rp.Refs[ref] == "" {
				return nil, true
			}
			return GVal{Kind: "Ref", Repo: rp, Ref: ref}, true
		case "baseRef":
			ref := "refs/heads/" + is.BaseRef
			if rp.Refs[ref] == "" {
				return nil, true
			}
			return GVal{Kind: "Ref", Repo: rp, Ref: ref}, true
		case "headRepository", "baseRepository":
			return GVal{Kind: "Repository", Repo: rp}, true
		case "merged":
			return is.Merged, true
		case "mergedAt":
			if is.MergedAt != nil {
				return fmtTime(*is.MergedAt), true
			}
			return nil, true
		case "mergedBy":
			if is.MergedBy != "" {
				return GVal{Kind: "User", UserL: is.MergedBy}, true
			}
			return nil, true
		case "mergeCommit":
			if is.MergeSHA != "" {
				return GVal{Kind: "Commit", Repo: rp, SHA: is.MergeSHA}, true
			}
			return nil, true
		case "isDraft":
			return false, true
		case "mergeable":
			if is.Merged {
				return "MERGEABLE", true
			}
			baseSHA := rp.resolveRefish(is.BaseRef)
			headSHA := rp.resolveRefish(is.HeadRef)
			ok, conflict := mergeCheck(rp, baseSHA, headSHA)
			if conflict {
				return "CONFLICTING", true
			}
			if ok {
				return "MERGEABLE", true
			}
			return "UNKNOWN", true
		case "mergeStateStatus":
			if is.Merged {
				return "CLEAN", true
			}
			return "CLEAN", true
		case "additions", "deletions", "changedFiles":
			baseSHA := rp.resolveRefish(is.BaseRef)
			headSHA := rp.resolveRefish(is.HeadRef)
			files := diffTrees(rp, rp.treeOfCommit(baseSHA), rp.treeOfCommit(headSHA))
			add, del := 0, 0
			for _, fd := range files {
				add += fd.Additions
				del += fd.Deletions
			}
			switch f.name {
			case "additions":
				return add, true
			case "deletions":
				return del, true
			default:
				return len(files), true
			}
		case "commits":
			baseSHA := rp.resolveRefish(is.BaseRef)
			headSHA := rp.resolveRefish(is.HeadRef)
			commits, _ := rp.commitsBetween(baseSHA, headSHA)
			var items []GVal
			for i := len(commits) - 1; i >= 0; i-- {
				items = append(items, GVal{Kind: "Commit", Repo: rp, SHA: commits[i]})
			}
			return e.conn(items, args), true
		case "files":
			baseSHA := rp.resolveRefish(is.BaseRef)
			headSHA := rp.resolveRefish(is.HeadRef)
			files := diffTrees(rp, rp.treeOfCommit(baseSHA), rp.treeOfCommit(headSHA))
			var items []GVal
			for _, fd := range files {
				changeType := "MODIFIED"
				switch fd.Status {
				case "added":
					changeType = "ADDED"
				case "removed":
					changeType = "DELETED"
				}
				items = append(items, GVal{Kind: "PullRequestChangedFile", Scalar: map[string]interface{}{
					"path": fd.Filename, "additions": fd.Additions, "deletions": fd.Deletions, "changeType": changeType,
				}})
			}
			return e.conn(items, args), true
		case "reviews":
			return e.conn(nil, args), true
		case "latestReviews":
			return e.conn(nil, args), true
		case "reviewRequests":
			return e.conn(nil, args), true
		case "participants":
			return e.conn([]GVal{{Kind: "User", UserL: is.User}}, args), true
		}
	}
	if gv.Kind == "PullRequestChangedFile" {
		if v, ok := gv.Scalar[f.name]; ok {
			return v, true
		}
	}
	return nil, false
}

func (e *gExec) commitField(gv GVal, f *gField) (interface{}, bool) {
	rp := gv.Repo
	o := rp.get(gv.SHA)
	if o == nil || o.Type != "commit" {
		return nil, false
	}
	ci := parseCommit(o.Data)
	args := f.args
	switch f.name {
	case "id":
		return e.entityID(gv), true
	case "oid":
		return gv.SHA, true
	case "message":
		return strings.TrimRight(ci.Message, "\n"), true
	case "messageHeadline":
		msg := strings.TrimRight(ci.Message, "\n")
		if i := strings.Index(msg, "\n"); i >= 0 {
			return msg[:i], true
		}
		return msg, true
	case "messageBody":
		msg := strings.TrimRight(ci.Message, "\n")
		parts := strings.SplitN(msg, "\n\n", 2)
		if len(parts) == 2 {
			return parts[1], true
		}
		return "", true
	case "author":
		return GVal{Kind: "GitActor", Scalar: map[string]interface{}{"name": ci.AuthorName, "email": ci.AuthorEmail, "date": fmtTime(time.Unix(ci.AuthorTime, 0).UTC())}}, true
	case "committer":
		return GVal{Kind: "GitActor", Scalar: map[string]interface{}{"name": ci.CommitterName, "email": ci.CommitterEmail, "date": fmtTime(time.Unix(ci.CommitterTime, 0).UTC())}}, true
	case "authoredDate":
		return fmtTime(time.Unix(ci.AuthorTime, 0).UTC()), true
	case "committedDate":
		return fmtTime(time.Unix(ci.CommitterTime, 0).UTC()), true
	case "tree":
		return GVal{Kind: "Tree", Repo: rp, SHA: ci.Tree}, true
	case "treeResourcePath", "resourcePath", "url":
		return "", true
	case "parents":
		var items []GVal
		for _, p := range ci.Parents {
			items = append(items, GVal{Kind: "Commit", Repo: rp, SHA: p})
		}
		return e.conn(items, args), true
	case "history":
		// commits reachable from this commit (including itself)
		var order []string
		seen := map[string]bool{}
		queue := []string{gv.SHA}
		pathFilter := argStr(args, e, "path")
		var sinceT, untilT int64
		if v := argStr(args, e, "since"); v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				sinceT = t.Unix()
			}
		}
		if v := argStr(args, e, "until"); v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				untilT = t.Unix()
			}
		}
		for len(queue) > 0 {
			c := queue[0]
			queue = queue[1:]
			if c == "" || seen[c] {
				continue
			}
			co := rp.get(c)
			if co == nil || co.Type != "commit" {
				continue
			}
			seen[c] = true
			cci := parseCommit(co.Data)
			include := true
			if sinceT > 0 && cci.CommitterTime < sinceT {
				include = false
			}
			if untilT > 0 && cci.CommitterTime > untilT {
				include = false
			}
			if include && pathFilter != "" {
				var pt string
				if len(cci.Parents) > 0 {
					pt = rp.treeOfCommit(cci.Parents[0])
				}
				found := false
				for _, fd := range diffTrees(rp, pt, cci.Tree) {
					if fd.Filename == pathFilter {
						found = true
						break
					}
				}
				include = found
			}
			if include {
				order = append(order, c)
			}
			queue = append(queue, cci.Parents...)
		}
		var items []GVal
		for _, c := range order {
			items = append(items, GVal{Kind: "Commit", Repo: rp, SHA: c})
		}
		return e.conn(items, args), true
	case "additions", "deletions", "changedFiles":
		var pt string
		if len(ci.Parents) > 0 {
			pt = rp.treeOfCommit(ci.Parents[0])
		}
		files := diffTrees(rp, pt, ci.Tree)
		add, del := 0, 0
		for _, fd := range files {
			add += fd.Additions
			del += fd.Deletions
		}
		switch f.name {
		case "additions":
			return add, true
		case "deletions":
			return del, true
		default:
			return len(files), true
		}
	case "repository":
		return GVal{Kind: "Repository", Repo: rp}, true
	case "associatedPullRequests":
		return e.conn(nil, args), true
	case "status":
		return nil, true
	case "checkSuites":
		return e.conn(nil, args), true
	case "comments":
		return e.conn(nil, args), true
	case "deployments":
		return e.conn(nil, args), true
	}
	return nil, false
}
