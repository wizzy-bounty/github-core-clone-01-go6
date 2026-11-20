package main

import "sort"

// Minimal GraphQL introspection support: a static type registry covering
// the types this forge serves, so __type / __schema queries resolve.

type gqlFieldDef struct {
	name string
	typ  string
}

type gqlTypeDef struct {
	kind   string // OBJECT, SCALAR, ENUM, INTERFACE, INPUT_OBJECT
	fields []gqlFieldDef
	enums  []string
}

func objFields(pairs ...string) []gqlFieldDef {
	out := make([]gqlFieldDef, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, gqlFieldDef{name: pairs[i], typ: pairs[i+1]})
	}
	return out
}

var connFields = objFields(
	"totalCount", "Int",
	"nodes", "String",
	"edges", "String",
	"pageInfo", "PageInfo",
)

var edgeFields = objFields(
	"node", "String",
	"cursor", "String",
)

var payloadFields = objFields(
	"clientMutationId", "String",
)

func withPayload(extra ...string) []gqlFieldDef {
	out := append([]gqlFieldDef{}, payloadFields...)
	for i := 0; i+1 < len(extra); i += 2 {
		out = append(out, gqlFieldDef{name: extra[i], typ: extra[i+1]})
	}
	return out
}

var gqlTypes = map[string]gqlTypeDef{
	"Query": {kind: "OBJECT", fields: objFields(
		"repository", "Repository",
		"repositoryOwner", "RepositoryOwner",
		"user", "User",
		"organization", "Organization",
		"viewer", "User",
		"node", "Node",
		"nodes", "Node",
		"search", "SearchResultItemConnection",
		"rateLimit", "RateLimit",
	)},
	"Mutation": {kind: "OBJECT", fields: objFields(
		"createIssue", "CreateIssuePayload",
		"updateIssue", "UpdateIssuePayload",
		"closeIssue", "CloseIssuePayload",
		"reopenIssue", "ReopenIssuePayload",
		"addComment", "AddCommentPayload",
		"updateIssueComment", "UpdateIssueCommentPayload",
		"deleteIssueComment", "DeleteIssueCommentPayload",
		"createPullRequest", "CreatePullRequestPayload",
		"mergePullRequest", "MergePullRequestPayload",
		"createRepository", "CreateRepositoryPayload",
		"deleteRepository", "DeleteRepositoryPayload",
		"createRef", "CreateRefPayload",
		"addLabelsToLabelable", "AddLabelsToLabelablePayload",
		"removeLabelsFromLabelable", "RemoveLabelsFromLabelablePayload",
		"createLabel", "CreateLabelPayload",
	)},
	"User": {kind: "OBJECT", fields: objFields(
		"id", "ID", "databaseId", "Int", "login", "String", "name", "String",
		"email", "String", "bio", "String", "company", "String", "location", "String",
		"url", "URI", "avatarUrl", "URI", "websiteUrl", "URI", "twitterUsername", "String",
		"createdAt", "DateTime", "updatedAt", "DateTime", "isViewer", "Boolean",
		"repositories", "RepositoryConnection", "issues", "IssueConnection",
		"pullRequests", "PullRequestConnection", "followers", "FollowerConnection",
		"following", "FollowingConnection", "organizations", "OrganizationConnection",
		"viewerCanFollow", "Boolean", "viewerIsFollowing", "Boolean",
	)},
	"Organization": {kind: "OBJECT", fields: objFields(
		"id", "ID", "databaseId", "Int", "login", "String", "name", "String",
		"email", "String", "description", "String", "url", "URI", "avatarUrl", "URI",
		"createdAt", "DateTime", "updatedAt", "DateTime",
		"repositories", "RepositoryConnection", "membersWithRole", "OrganizationMemberConnection",
	)},
	"RepositoryOwner": {kind: "INTERFACE", fields: objFields(
		"id", "ID", "login", "String", "url", "URI", "avatarUrl", "URI",
		"repositories", "RepositoryConnection",
	)},
	"Repository": {kind: "OBJECT", fields: objFields(
		"id", "ID", "databaseId", "Int", "name", "String", "nameWithOwner", "String",
		"owner", "RepositoryOwner", "description", "String", "url", "URI",
		"homepageUrl", "String", "isPrivate", "Boolean", "visibility", "RepositoryVisibility",
		"createdAt", "DateTime", "updatedAt", "DateTime", "pushedAt", "DateTime",
		"defaultBranchRef", "Ref", "primaryLanguage", "Language", "licenseInfo", "License",
		"stargazerCount", "Int", "forkCount", "Int", "isFork", "Boolean",
		"isArchived", "Boolean", "isDisabled", "Boolean", "isEmpty", "Boolean",
		"diskUsage", "Int", "parent", "Repository",
		"issues", "IssueConnection", "pullRequests", "PullRequestConnection",
		"issue", "Issue", "pullRequest", "PullRequest",
		"labels", "LabelConnection", "refs", "RefConnection", "ref", "Ref",
		"object", "GitObject", "branchProtectionRules", "BranchProtectionRuleConnection",
		"watchers", "UserConnection", "stargazers", "StargazerConnection",
		"languages", "LanguageConnection", "repositoryTopics", "RepositoryTopicConnection",
		"milestones", "MilestoneConnection", "releases", "ReleaseConnection",
		"mentionableUsers", "UserConnection", "assignableUsers", "UserConnection",
		"viewerPermission", "RepositoryPermission", "sshUrl", "String",
		"isTemplate", "Boolean", "isMirror", "Boolean", "isLocked", "Boolean",
		"hasIssuesEnabled", "Boolean", "hasProjectsEnabled", "Boolean",
		"hasWikiEnabled", "Boolean", "mergeCommitAllowed", "Boolean",
		"squashMergeAllowed", "Boolean", "rebaseMergeAllowed", "Boolean",
	)},
	"Issue": {kind: "OBJECT", fields: objFields(
		"id", "ID", "databaseId", "Int", "number", "Int", "title", "String",
		"body", "String", "bodyHTML", "HTML", "bodyText", "String",
		"state", "IssueState", "stateReason", "IssueClosedStateReason",
		"url", "URI", "resourcePath", "URI", "author", "Actor", "editor", "Actor",
		"labels", "LabelConnection", "assignees", "UserConnection",
		"comments", "IssueCommentConnection", "createdAt", "DateTime",
		"updatedAt", "DateTime", "closedAt", "DateTime", "closed", "Boolean",
		"locked", "Boolean", "milestone", "Milestone", "repository", "Repository",
		"participants", "UserConnection", "reactions", "ReactionConnection",
		"timelineItems", "IssueTimelineItemsConnection",
		"viewerCanUpdate", "Boolean", "viewerCanDelete", "Boolean",
		"viewerCanReact", "Boolean", "viewerCanSubscribe", "Boolean",
		"viewerSubscription", "SubscriptionState", "authorAssociation", "CommentAuthorAssociation",
		"activeLockReason", "LockReason", "duplicateOf", "Issue", "isPinned", "Boolean",
		"linkedBranches", "LinkedBranchConnection", "subIssues", "IssueConnection",
		"parent", "Issue", "issueType", "IssueType",
	)},
	"PullRequest": {kind: "OBJECT", fields: objFields(
		"id", "ID", "databaseId", "Int", "number", "Int", "title", "String",
		"body", "String", "bodyHTML", "HTML", "bodyText", "String",
		"state", "PullRequestState", "url", "URI", "resourcePath", "URI",
		"author", "Actor", "editor", "Actor",
		"labels", "LabelConnection", "assignees", "UserConnection",
		"comments", "IssueCommentConnection", "createdAt", "DateTime",
		"updatedAt", "DateTime", "closedAt", "DateTime", "closed", "Boolean",
		"locked", "Boolean", "milestone", "Milestone", "repository", "Repository",
		"participants", "UserConnection", "reactions", "ReactionConnection",
		"baseRef", "Ref", "baseRefName", "String", "baseRefOid", "GitObjectID",
		"baseRepository", "Repository",
		"headRef", "Ref", "headRefName", "String", "headRefOid", "GitObjectID",
		"headRepository", "Repository", "isCrossRepository", "Boolean",
		"mergeable", "MergeableState", "merged", "Boolean", "mergedAt", "DateTime",
		"mergedBy", "Actor", "mergeCommit", "Commit", "mergeStateStatus", "MergeStateStatus",
		"additions", "Int", "deletions", "Int", "changedFiles", "Int",
		"files", "PullRequestChangedFileConnection", "commits", "PullRequestCommitConnection",
		"reviews", "PullRequestReviewConnection", "reviewRequests", "ReviewRequestConnection",
		"latestReviews", "PullRequestReviewConnection", "isDraft", "Boolean",
		"maintainerCanModify", "Boolean", "viewerCanUpdate", "Boolean",
		"viewerCanDelete", "Boolean", "viewerCanReact", "Boolean",
		"viewerCanSubscribe", "Boolean", "viewerCanMerge", "Boolean",
		"viewerCanEnableAutoMerge", "Boolean", "viewerCanDisableAutoMerge", "Boolean",
		"authorAssociation", "CommentAuthorAssociation", "activeLockReason", "LockReason",
		"suggestedReviewers", "SuggestedReviewer", "timelineItems", "PullRequestTimelineItemsConnection",
		"permalink", "URI", "potentialMergeCommit", "Commit", "revertUrl", "URI",
		"willCloseTarget", "Boolean", "autoMergeRequest", "AutoMergeRequest",
	)},
	"PullRequestChangedFile": {kind: "OBJECT", fields: objFields(
		"path", "String", "additions", "Int", "deletions", "Int",
		"changeType", "PatchStatus", "viewerViewedState", "FileViewedState",
	)},
	"IssueComment": {kind: "OBJECT", fields: objFields(
		"id", "ID", "databaseId", "Int", "body", "String", "bodyHTML", "HTML",
		"bodyText", "String", "url", "URI", "resourcePath", "URI",
		"author", "Actor", "editor", "Actor", "createdAt", "DateTime",
		"updatedAt", "DateTime", "issue", "Issue", "pullRequest", "PullRequest",
		"repository", "Repository", "viewerCanUpdate", "Boolean",
		"viewerCanDelete", "Boolean", "viewerCanReact", "Boolean",
		"reactions", "ReactionConnection", "authorAssociation", "CommentAuthorAssociation",
		"isMinimized", "Boolean", "minimizedReason", "String",
		"viewerDidAuthor", "Boolean", "viaApp", "App", "createdViaEmail", "Boolean",
		"lastEditedAt", "DateTime",
	)},
	"Label": {kind: "OBJECT", fields: objFields(
		"id", "ID", "name", "String", "color", "String", "description", "String",
		"url", "URI", "isDefault", "Boolean", "createdAt", "DateTime",
		"updatedAt", "DateTime", "issues", "IssueConnection",
		"pullRequests", "PullRequestConnection", "repository", "Repository",
	)},
	"Ref": {kind: "OBJECT", fields: objFields(
		"id", "ID", "name", "String", "prefix", "String", "repository", "Repository",
		"target", "GitObject", "associatedPullRequests", "PullRequestConnection",
	)},
	"Commit": {kind: "OBJECT", fields: objFields(
		"id", "ID", "oid", "GitObjectID", "abbreviatedOid", "String",
		"message", "String", "messageHeadline", "String", "messageBody", "String",
		"url", "URI", "resourcePath", "URI", "commitUrl", "URI",
		"author", "GitActor", "committer", "GitActor",
		"authoredDate", "DateTime", "committedDate", "DateTime",
		"parents", "CommitConnection", "tree", "Tree", "treeUrl", "URI",
		"history", "CommitHistoryConnection", "repository", "Repository",
		"associatedPullRequests", "PullRequestConnection",
		"additions", "Int", "deletions", "Int", "changedFiles", "Int",
		"changedFilesIfAvailable", "Int", "signature", "GitSignature",
		"status", "Status", "statusCheckRollup", "StatusCheckRollup",
		"authoredByCommitter", "Boolean", "committerResourcePath", "URI",
		"authorResourcePath", "URI", "pushedDate", "DateTime",
		"tarballUrl", "URI", "zipballUrl", "URI", "onBehalfOf", "Organization",
		"comments", "CommitCommentConnection", "deployments", "DeploymentConnection",
	)},
	"Tree": {kind: "OBJECT", fields: objFields(
		"id", "ID", "oid", "GitObjectID", "abbreviatedOid", "String",
		"entries", "TreeEntry", "repository", "Repository", "url", "URI",
	)},
	"TreeEntry": {kind: "OBJECT", fields: objFields(
		"name", "String", "path", "String", "mode", "Int", "type", "String",
		"oid", "GitObjectID", "object", "GitObject", "extension", "String",
		"lineCount", "Int", "size", "Int", "isGenerated", "Boolean",
		"language", "Language",
	)},
	"Blob": {kind: "OBJECT", fields: objFields(
		"id", "ID", "oid", "GitObjectID", "abbreviatedOid", "String",
		"byteSize", "Int", "text", "String", "isBinary", "Boolean",
		"isTruncated", "Boolean", "commitResourcePath", "URI",
		"commitUrl", "URI", "repository", "Repository", "url", "URI",
		"resourcePath", "URI",
	)},
	"Tag": {kind: "OBJECT", fields: objFields(
		"id", "ID", "name", "String", "message", "String",
		"oid", "GitObjectID", "abbreviatedOid", "String",
		"target", "GitObject", "repository", "Repository", "tagger", "GitActor",
	)},
	"GitActor": {kind: "OBJECT", fields: objFields(
		"name", "String", "email", "String", "date", "GitTimestamp",
		"avatarUrl", "URI", "user", "User",
	)},
	"GitObject": {kind: "INTERFACE", fields: objFields(
		"id", "ID", "oid", "GitObjectID", "abbreviatedOid", "String",
		"commitResourcePath", "URI", "commitUrl", "URI", "repository", "Repository",
		"url", "URI", "resourcePath", "URI",
	)},
	"Actor": {kind: "INTERFACE", fields: objFields(
		"login", "String", "url", "URI", "avatarUrl", "URI", "resourcePath", "URI",
	)},
	"Node": {kind: "INTERFACE", fields: objFields(
		"id", "ID",
	)},
	"PageInfo": {kind: "OBJECT", fields: objFields(
		"hasNextPage", "Boolean", "hasPreviousPage", "Boolean",
		"startCursor", "String", "endCursor", "String",
	)},
	"RateLimit": {kind: "OBJECT", fields: objFields(
		"limit", "Int", "cost", "Int", "remaining", "Int",
		"resetAt", "DateTime", "used", "Int", "nodeCount", "Int",
	)},
	"Language": {kind: "OBJECT", fields: objFields(
		"id", "ID", "name", "String", "color", "String",
	)},
	"SearchResultItemConnection": {kind: "OBJECT", fields: objFields(
		"codeCount", "Int", "issueCount", "Int", "repositoryCount", "Int",
		"userCount", "Int", "wikiCount", "Int", "discussionCount", "Int",
		"nodes", "SearchResultItem", "edges", "SearchResultItemEdge",
		"pageInfo", "PageInfo",
	)},
	"IssueConnection":            {kind: "OBJECT", fields: connFields},
	"PullRequestConnection":      {kind: "OBJECT", fields: connFields},
	"IssueCommentConnection":     {kind: "OBJECT", fields: connFields},
	"LabelConnection":            {kind: "OBJECT", fields: connFields},
	"RefConnection":              {kind: "OBJECT", fields: connFields},
	"RepositoryConnection":       {kind: "OBJECT", fields: connFields},
	"CommitConnection":           {kind: "OBJECT", fields: connFields},
	"CommitHistoryConnection":    {kind: "OBJECT", fields: connFields},
	"PullRequestCommitConnection": {kind: "OBJECT", fields: objFields(
		"totalCount", "Int", "nodes", "PullRequestCommit", "edges", "String", "pageInfo", "PageInfo",
	)},
	"PullRequestChangedFileConnection": {kind: "OBJECT", fields: connFields},
	"UserConnection":                   {kind: "OBJECT", fields: connFields},
	"OrganizationConnection":           {kind: "OBJECT", fields: connFields},
	"FollowerConnection":               {kind: "OBJECT", fields: connFields},
	"FollowingConnection":              {kind: "OBJECT", fields: connFields},
	"LanguageConnection":               {kind: "OBJECT", fields: connFields},
	"MilestoneConnection":              {kind: "OBJECT", fields: connFields},
	"ReleaseConnection":                {kind: "OBJECT", fields: connFields},
	"ReactionConnection":               {kind: "OBJECT", fields: connFields},
	"BranchProtectionRuleConnection":   {kind: "OBJECT", fields: connFields},
	"StargazerConnection":              {kind: "OBJECT", fields: connFields},
	"RepositoryTopicConnection":        {kind: "OBJECT", fields: connFields},
	"OrganizationMemberConnection":     {kind: "OBJECT", fields: connFields},
	"IssueTimelineItemsConnection":     {kind: "OBJECT", fields: connFields},
	"PullRequestTimelineItemsConnection": {kind: "OBJECT", fields: connFields},
	"PullRequestReviewConnection":      {kind: "OBJECT", fields: connFields},
	"ReviewRequestConnection":          {kind: "OBJECT", fields: connFields},
	"CommitCommentConnection":          {kind: "OBJECT", fields: connFields},
	"DeploymentConnection":             {kind: "OBJECT", fields: connFields},
	"LinkedBranchConnection":           {kind: "OBJECT", fields: connFields},
	"IssueEdge":                        {kind: "OBJECT", fields: edgeFields},
	"PullRequestEdge":                  {kind: "OBJECT", fields: edgeFields},
	"IssueCommentEdge":                 {kind: "OBJECT", fields: edgeFields},
	"LabelEdge":                        {kind: "OBJECT", fields: edgeFields},
	"RefEdge":                          {kind: "OBJECT", fields: edgeFields},
	"RepositoryEdge":                   {kind: "OBJECT", fields: edgeFields},
	"CommitEdge":                       {kind: "OBJECT", fields: edgeFields},
	"SearchResultItemEdge":             {kind: "OBJECT", fields: edgeFields},
	"PullRequestCommit": {kind: "OBJECT", fields: objFields(
		"id", "ID", "commit", "Commit", "resourcePath", "URI", "url", "URI",
	)},
	"CreateIssuePayload":            {kind: "OBJECT", fields: withPayload("issue", "Issue")},
	"UpdateIssuePayload":            {kind: "OBJECT", fields: withPayload("issue", "Issue")},
	"CloseIssuePayload":             {kind: "OBJECT", fields: withPayload("issue", "Issue")},
	"ReopenIssuePayload":            {kind: "OBJECT", fields: withPayload("issue", "Issue")},
	"AddCommentPayload":             {kind: "OBJECT", fields: withPayload("commentEdge", "IssueCommentEdge", "subject", "Issue")},
	"UpdateIssueCommentPayload":     {kind: "OBJECT", fields: withPayload("issueComment", "IssueComment")},
	"DeleteIssueCommentPayload":     {kind: "OBJECT", fields: withPayload()},
	"CreatePullRequestPayload":      {kind: "OBJECT", fields: withPayload("pullRequest", "PullRequest")},
	"MergePullRequestPayload":       {kind: "OBJECT", fields: withPayload("pullRequest", "PullRequest")},
	"CreateRepositoryPayload":       {kind: "OBJECT", fields: withPayload("repository", "Repository")},
	"DeleteRepositoryPayload":       {kind: "OBJECT", fields: withPayload()},
	"CreateRefPayload":              {kind: "OBJECT", fields: withPayload("ref", "Ref")},
	"AddLabelsToLabelablePayload":   {kind: "OBJECT", fields: withPayload("labelable", "Issue")},
	"RemoveLabelsFromLabelablePayload": {kind: "OBJECT", fields: withPayload("labelable", "Issue")},
	"CreateLabelPayload":            {kind: "OBJECT", fields: withPayload("label", "Label")},
	"__Type": {kind: "OBJECT", fields: objFields(
		"name", "String", "kind", "String", "description", "String",
		"fields", "__Field", "inputFields", "__InputValue",
		"interfaces", "__Type", "enumValues", "__EnumValue",
		"possibleTypes", "__Type", "specifiedByUrl", "String", "ofType", "__Type",
	)},
	"__Field": {kind: "OBJECT", fields: objFields(
		"name", "String", "description", "String", "args", "__InputValue",
		"type", "__Type", "isDeprecated", "Boolean", "deprecationReason", "String",
	)},
	"__InputValue": {kind: "OBJECT", fields: objFields(
		"name", "String", "description", "String", "type", "__Type",
		"defaultValue", "String", "isDeprecated", "Boolean", "deprecationReason", "String",
	)},
	"__EnumValue": {kind: "OBJECT", fields: objFields(
		"name", "String", "description", "String",
		"isDeprecated", "Boolean", "deprecationReason", "String",
	)},
	"__Schema": {kind: "OBJECT", fields: objFields(
		"description", "String", "types", "__Type", "queryType", "__Type",
		"mutationType", "__Type", "subscriptionType", "__Type",
		"directives", "__Directive",
	)},
	"__Directive": {kind: "OBJECT", fields: objFields(
		"name", "String", "description", "String", "locations", "String",
		"args", "__InputValue", "isRepeatable", "Boolean",
	)},
	// Scalars
	"String":      {kind: "SCALAR"},
	"Int":         {kind: "SCALAR"},
	"Float":       {kind: "SCALAR"},
	"Boolean":     {kind: "SCALAR"},
	"ID":          {kind: "SCALAR"},
	"DateTime":    {kind: "SCALAR"},
	"URI":         {kind: "SCALAR"},
	"GitObjectID": {kind: "SCALAR"},
	"Base64String": {kind: "SCALAR"},
	"HTML":        {kind: "SCALAR"},
	"GitTimestamp": {kind: "SCALAR"},
	"GitRefname":  {kind: "SCALAR"},
	"PreciseDateTime": {kind: "SCALAR"},
	"BigInt":      {kind: "SCALAR"},
	"JSON":        {kind: "SCALAR"},
	// Enums
	"IssueState":            {kind: "ENUM", enums: []string{"OPEN", "CLOSED"}},
	"PullRequestState":      {kind: "ENUM", enums: []string{"OPEN", "CLOSED", "MERGED"}},
	"MergeableState":        {kind: "ENUM", enums: []string{"MERGEABLE", "CONFLICTING", "UNKNOWN"}},
	"MergeStateStatus":      {kind: "ENUM", enums: []string{"BEHIND", "BLOCKED", "CLEAN", "DIRTY", "DRAFT", "HAS_HOOKS", "UNKNOWN", "UNSTABLE"}},
	"SearchType":            {kind: "ENUM", enums: []string{"ISSUE", "REPOSITORY", "USER", "DISCUSSION"}},
	"OrderDirection":        {kind: "ENUM", enums: []string{"ASC", "DESC"}},
	"MergeMethod":           {kind: "ENUM", enums: []string{"MERGE", "SQUASH", "REBASE"}},
	"RepositoryVisibility":  {kind: "ENUM", enums: []string{"PUBLIC", "PRIVATE", "INTERNAL"}},
	"RepositoryPermission":  {kind: "ENUM", enums: []string{"ADMIN", "MAINTAIN", "WRITE", "TRIAGE", "READ"}},
	"SubscriptionState":     {kind: "ENUM", enums: []string{"IGNORED", "SUBSCRIBED", "UNSUBSCRIBED"}},
	"CommentAuthorAssociation": {kind: "ENUM", enums: []string{"COLLABORATOR", "CONTRIBUTOR", "FIRST_TIMER", "FIRST_TIME_CONTRIBUTOR", "MANNEQUIN", "MEMBER", "NONE", "OWNER"}},
	"IssueClosedStateReason":   {kind: "ENUM", enums: []string{"COMPLETED", "NOT_PLANNED", "DUPLICATE"}},
	"PatchStatus":              {kind: "ENUM", enums: []string{"ADDED", "CHANGED", "COPIED", "DELETED", "MODIFIED", "REMOVED", "RENAMED"}},
	"FileViewedState":          {kind: "ENUM", enums: []string{"DISMISSED", "VIEWED", "UNVIEWED"}},
	"LockReason":               {kind: "ENUM", enums: []string{"OFF_TOPIC", "RESOLVED", "SPAM", "TOO_HEATED"}},
	"__TypeKind":               {kind: "ENUM", enums: []string{"SCALAR", "OBJECT", "INTERFACE", "UNION", "ENUM", "INPUT_OBJECT", "LIST", "NON_NULL"}},
	"__DirectiveLocation":      {kind: "ENUM", enums: []string{"QUERY", "MUTATION", "SUBSCRIPTION", "FIELD", "FRAGMENT_DEFINITION", "FRAGMENT_SPREAD", "INLINE_FRAGMENT", "VARIABLE_DEFINITION", "SCHEMA", "SCALAR", "OBJECT", "FIELD_DEFINITION", "ARGUMENT_DEFINITION", "INTERFACE", "UNION", "ENUM", "ENUM_VALUE", "INPUT_OBJECT", "INPUT_FIELD_DEFINITION"}},
}

func gqlKindOf(name string) string {
	if def, ok := gqlTypes[name]; ok {
		return def.kind
	}
	return "OBJECT"
}

// typeGVal builds a __Type value for the named type.
func typeGVal(name string) GVal {
	return GVal{Kind: "__Type", Scalar: map[string]interface{}{
		"name": name, "kind": gqlKindOf(name),
	}}
}

// introspectionField resolves fields on __Type / __Field / __Schema /
// __EnumValue / __InputValue / __Directive values.
func (e *gExec) introspectionField(gv GVal, name string) (interface{}, bool) {
	switch gv.Kind {
	case "__Type":
		typeName, _ := gv.Scalar["name"].(string)
		def, known := gqlTypes[typeName]
		switch name {
		case "name":
			if !known {
				return nil, true
			}
			return typeName, true
		case "kind":
			return gqlKindOf(typeName), true
		case "description", "specifiedByURL", "specifiedByUrl":
			return nil, true
		case "fields":
			if !known || (def.kind != "OBJECT" && def.kind != "INTERFACE") {
				return nil, true
			}
			out := make([]GVal, 0, len(def.fields))
			for _, fd := range def.fields {
				out = append(out, GVal{Kind: "__Field", Scalar: map[string]interface{}{
					"name": fd.name, "typeName": fd.typ,
				}})
			}
			return out, true
		case "inputFields":
			return nil, true
		case "interfaces":
			return []GVal{}, true
		case "possibleTypes":
			return nil, true
		case "enumValues":
			if !known || def.kind != "ENUM" {
				return nil, true
			}
			out := make([]GVal, 0, len(def.enums))
			for _, en := range def.enums {
				out = append(out, GVal{Kind: "__EnumValue", Scalar: map[string]interface{}{"name": en}})
			}
			return out, true
		case "ofType":
			return nil, true
		}
		return nil, true
	case "__Field":
		switch name {
		case "name":
			return gv.Scalar["name"], true
		case "description", "deprecationReason":
			return nil, true
		case "args":
			return []GVal{}, true
		case "type":
			tn, _ := gv.Scalar["typeName"].(string)
			return typeGVal(tn), true
		case "isDeprecated":
			return false, true
		}
		return nil, true
	case "__EnumValue":
		switch name {
		case "name":
			return gv.Scalar["name"], true
		case "description", "deprecationReason":
			return nil, true
		case "isDeprecated":
			return false, true
		}
		return nil, true
	case "__InputValue":
		switch name {
		case "name":
			return gv.Scalar["name"], true
		case "description", "deprecationReason", "defaultValue":
			return nil, true
		case "type":
			tn, _ := gv.Scalar["typeName"].(string)
			return typeGVal(tn), true
		case "isDeprecated":
			return false, true
		}
		return nil, true
	case "__Schema":
		switch name {
		case "description":
			return nil, true
		case "queryType":
			return typeGVal("Query"), true
		case "mutationType":
			return typeGVal("Mutation"), true
		case "subscriptionType":
			return nil, true
		case "types":
			names := make([]string, 0, len(gqlTypes))
			for n := range gqlTypes {
				names = append(names, n)
			}
			sort.Strings(names)
			out := make([]GVal, 0, len(names))
			for _, n := range names {
				out = append(out, typeGVal(n))
			}
			return out, true
		case "directives":
			return []GVal{}, true
		}
		return nil, true
	case "__Directive":
		switch name {
		case "name":
			return gv.Scalar["name"], true
		case "description":
			return nil, true
		case "locations":
			return []interface{}{}, true
		case "args":
			return []GVal{}, true
		case "isRepeatable":
			return false, true
		}
		return nil, true
	}
	return nil, false
}
