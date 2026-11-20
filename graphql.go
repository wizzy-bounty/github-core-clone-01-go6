package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// ---------------- tokenizer ----------------

type gTok struct {
	kind string // name,int,float,string,punct,eof
	val  string
}

func gTokenize(src string) ([]gTok, error) {
	var toks []gTok
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == 0xef && i+2 < len(src) && src[i+1] == 0xbb && src[i+2] == 0xbf:
			i += 3
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',':
			i++
		case c == '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '.' && i+2 < len(src) && src[i+1] == '.' && src[i+2] == '.':
			toks = append(toks, gTok{"punct", "..."})
			i += 3
		case strings.ContainsRune("!$():=@[]{}|", rune(c)):
			toks = append(toks, gTok{"punct", string(c)})
			i++
		case c == '"':
			if i+2 < len(src) && src[i+1] == '"' && src[i+2] == '"' {
				end := strings.Index(src[i+3:], `"""`)
				if end < 0 {
					return nil, fmt.Errorf("unterminated block string")
				}
				toks = append(toks, gTok{"string", src[i+3 : i+3+end]})
				i += 3 + end + 3
			} else {
				var sb strings.Builder
				i++
				for i < len(src) && src[i] != '"' {
					if src[i] == '\\' && i+1 < len(src) {
						i++
						switch src[i] {
						case 'n':
							sb.WriteByte('\n')
						case 't':
							sb.WriteByte('\t')
						case 'r':
							sb.WriteByte('\r')
						case '/':
							sb.WriteByte('/')
						case 'u':
							if i+4 < len(src) {
								n, _ := strconv.ParseUint(src[i+1:i+5], 16, 32)
								sb.WriteRune(rune(n))
								i += 4
							}
						default:
							sb.WriteByte(src[i])
						}
						i++
					} else {
						sb.WriteByte(src[i])
						i++
					}
				}
				i++
				toks = append(toks, gTok{"string", sb.String()})
			}
		case c == '-' || (c >= '0' && c <= '9'):
			j := i
			isFloat := false
			if src[j] == '-' {
				j++
			}
			for j < len(src) && ((src[j] >= '0' && src[j] <= '9') || src[j] == '.' || src[j] == 'e' || src[j] == 'E' || src[j] == '+' || src[j] == '-') {
				if src[j] == '.' || src[j] == 'e' || src[j] == 'E' {
					isFloat = true
				}
				j++
			}
			kind := "int"
			if isFloat {
				kind = "float"
			}
			toks = append(toks, gTok{kind, src[i:j]})
			i = j
		case c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z'):
			j := i
			for j < len(src) && (src[j] == '_' || (src[j] >= '0' && src[j] <= '9') || (src[j] >= 'A' && src[j] <= 'Z') || (src[j] >= 'a' && src[j] <= 'z')) {
				j++
			}
			toks = append(toks, gTok{"name", src[i:j]})
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q", c)
		}
	}
	toks = append(toks, gTok{"eof", ""})
	return toks, nil
}

// ---------------- AST / parser ----------------

type gValue struct {
	kind string // var,int,float,string,bool,enum,null,list,object
	s    string
	list []*gValue
	obj  map[string]*gValue
}

type gDirective struct {
	name string
	args map[string]*gValue
}

type gField struct {
	name  string
	alias string
	args  map[string]*gValue
	dirs  []gDirective
	sel   []*gSel
}

type gSel struct {
	field  *gField
	spread string
	dirs   []gDirective
	onType string
	sub    []*gSel
}

type gFrag struct {
	onType string
	sel    []*gSel
}

type gOp struct {
	typ      string
	name     string
	defaults map[string]*gValue
	sel      []*gSel
}

type gParser struct {
	toks []gTok
	pos  int
}

func (p *gParser) peek() gTok  { return p.toks[p.pos] }
func (p *gParser) next() gTok  { t := p.toks[p.pos]; p.pos++; return t }
func (p *gParser) isPunct(v string) bool {
	t := p.peek()
	return t.kind == "punct" && t.val == v
}
func (p *gParser) expectPunct(v string) error {
	if !p.isPunct(v) {
		return fmt.Errorf("expected %s got %q", v, p.peek().val)
	}
	p.pos++
	return nil
}

func (p *gParser) parseValue() (*gValue, error) {
	t := p.peek()
	switch t.kind {
	case "int", "float":
		p.next()
		return &gValue{kind: t.kind, s: t.val}, nil
	case "string":
		p.next()
		return &gValue{kind: "string", s: t.val}, nil
	case "name":
		p.next()
		switch t.val {
		case "true", "false":
			return &gValue{kind: "bool", s: t.val}, nil
		case "null":
			return &gValue{kind: "null"}, nil
		default:
			return &gValue{kind: "enum", s: t.val}, nil
		}
	case "punct":
		switch t.val {
		case "$":
			p.next()
			n := p.next()
			return &gValue{kind: "var", s: n.val}, nil
		case "[":
			p.next()
			v := &gValue{kind: "list"}
			for !p.isPunct("]") {
				item, err := p.parseValue()
				if err != nil {
					return nil, err
				}
				v.list = append(v.list, item)
			}
			p.next()
			return v, nil
		case "{":
			p.next()
			v := &gValue{kind: "object", obj: map[string]*gValue{}}
			for !p.isPunct("}") {
				n := p.next()
				if err := p.expectPunct(":"); err != nil {
					return nil, err
				}
				item, err := p.parseValue()
				if err != nil {
					return nil, err
				}
				v.obj[n.val] = item
			}
			p.next()
			return v, nil
		}
	}
	return nil, fmt.Errorf("unexpected token %q", t.val)
}

func (p *gParser) parseArgs() (map[string]*gValue, error) {
	args := map[string]*gValue{}
	if !p.isPunct("(") {
		return args, nil
	}
	p.next()
	for !p.isPunct(")") {
		n := p.next()
		if n.kind == "eof" {
			return nil, fmt.Errorf("unterminated args")
		}
		if err := p.expectPunct(":"); err != nil {
			return nil, err
		}
		v, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		args[n.val] = v
	}
	p.next()
	return args, nil
}

func (p *gParser) parseDirectives() ([]gDirective, error) {
	var dirs []gDirective
	for p.isPunct("@") {
		p.next()
		n := p.next()
		args, err := p.parseArgs()
		if err != nil {
			return nil, err
		}
		dirs = append(dirs, gDirective{name: n.val, args: args})
	}
	return dirs, nil
}

func (p *gParser) parseSelectionSet() ([]*gSel, error) {
	if err := p.expectPunct("{"); err != nil {
		return nil, err
	}
	var sels []*gSel
	for !p.isPunct("}") {
		if p.peek().kind == "eof" {
			return nil, fmt.Errorf("unterminated selection set")
		}
		if p.isPunct("...") {
			p.next()
			if p.peek().kind == "name" && p.peek().val == "on" {
				p.next()
				on := p.next()
				dirs, err := p.parseDirectives()
				if err != nil {
					return nil, err
				}
				sub, err := p.parseSelectionSet()
				if err != nil {
					return nil, err
				}
				sels = append(sels, &gSel{onType: on.val, dirs: dirs, sub: sub})
			} else {
				n := p.next()
				dirs, err := p.parseDirectives()
				if err != nil {
					return nil, err
				}
				sels = append(sels, &gSel{spread: n.val, dirs: dirs})
			}
			continue
		}
		n := p.next()
		if n.kind != "name" {
			return nil, fmt.Errorf("expected field name, got %q", n.val)
		}
		f := &gField{name: n.val}
		if p.isPunct(":") {
			p.next()
			f.alias = f.name
			n2 := p.next()
			f.name = n2.val
		}
		args, err := p.parseArgs()
		if err != nil {
			return nil, err
		}
		f.args = args
		dirs, err := p.parseDirectives()
		if err != nil {
			return nil, err
		}
		f.dirs = dirs
		if p.isPunct("{") {
			sub, err := p.parseSelectionSet()
			if err != nil {
				return nil, err
			}
			f.sel = sub
		}
		sels = append(sels, &gSel{field: f})
	}
	p.next()
	return sels, nil
}

func (p *gParser) parseTypeRef() error {
	if p.isPunct("[") {
		p.next()
		if err := p.parseTypeRef(); err != nil {
			return err
		}
		if err := p.expectPunct("]"); err != nil {
			return err
		}
	} else {
		p.next()
	}
	if p.isPunct("!") {
		p.next()
	}
	return nil
}

func parseDocument(src string) (ops []*gOp, frags map[string]*gFrag, err error) {
	toks, err := gTokenize(src)
	if err != nil {
		return nil, nil, err
	}
	p := &gParser{toks: toks}
	frags = map[string]*gFrag{}
	for p.peek().kind != "eof" {
		t := p.peek()
		if t.kind == "punct" && t.val == "{" {
			sel, err := p.parseSelectionSet()
			if err != nil {
				return nil, nil, err
			}
			ops = append(ops, &gOp{typ: "query", sel: sel, defaults: map[string]*gValue{}})
			continue
		}
		if t.kind != "name" {
			return nil, nil, fmt.Errorf("unexpected token %q", t.val)
		}
		switch t.val {
		case "query", "mutation", "subscription":
			p.next()
			op := &gOp{typ: t.val, defaults: map[string]*gValue{}}
			if p.peek().kind == "name" {
				op.name = p.next().val
			}
			if p.isPunct("(") {
				p.next()
				for !p.isPunct(")") {
					if err := p.expectPunct("$"); err != nil {
						return nil, nil, err
					}
					vn := p.next()
					if err := p.expectPunct(":"); err != nil {
						return nil, nil, err
					}
					if err := p.parseTypeRef(); err != nil {
						return nil, nil, err
					}
					if p.isPunct("=") {
						p.next()
						dv, err := p.parseValue()
						if err != nil {
							return nil, nil, err
						}
						op.defaults[vn.val] = dv
					}
					// directives on var defs: skip
					if _, err := p.parseDirectives(); err != nil {
						return nil, nil, err
					}
				}
				p.next()
			}
			if _, err := p.parseDirectives(); err != nil {
				return nil, nil, err
			}
			sel, err := p.parseSelectionSet()
			if err != nil {
				return nil, nil, err
			}
			op.sel = sel
			ops = append(ops, op)
		case "fragment":
			p.next()
			name := p.next().val
			if p.peek().kind == "name" && p.peek().val == "on" {
				p.next()
			}
			on := p.next().val
			if _, err := p.parseDirectives(); err != nil {
				return nil, nil, err
			}
			sel, err := p.parseSelectionSet()
			if err != nil {
				return nil, nil, err
			}
			frags[name] = &gFrag{onType: on, sel: sel}
		default:
			return nil, nil, fmt.Errorf("unexpected definition %q", t.val)
		}
	}
	return ops, frags, nil
}

// ---------------- execution ----------------

type GVal struct {
	Kind   string
	Repo   *Repo
	Issue  *Issue
	Cmt    *Comment
	UserL  string
	SHA    string
	Ref    string
	Label  string
	Items  []GVal
	Total  int
	Node   *GVal
	Cursor string
	Scalar map[string]interface{}
}

type gExec struct {
	st     *Store
	req    *http.Request
	vars   map[string]interface{}
	frags  map[string]*gFrag
	errors []map[string]interface{}
}

func (e *gExec) val(v *gValue) interface{} {
	if v == nil {
		return nil
	}
	switch v.kind {
	case "var":
		return e.vars[v.s]
	case "int":
		n, _ := strconv.ParseInt(v.s, 10, 64)
		return n
	case "float":
		f, _ := strconv.ParseFloat(v.s, 64)
		return f
	case "string", "enum":
		return v.s
	case "bool":
		return v.s == "true"
	case "null":
		return nil
	case "list":
		out := make([]interface{}, 0, len(v.list))
		for _, item := range v.list {
			out = append(out, e.val(item))
		}
		return out
	case "object":
		out := map[string]interface{}{}
		for k, item := range v.obj {
			out[k] = e.val(item)
		}
		return out
	}
	return nil
}

func argStr(args map[string]*gValue, e *gExec, name string) string {
	if v, ok := args[name]; ok {
		if s, ok := e.val(v).(string); ok {
			return s
		}
	}
	return ""
}

func argInt(args map[string]*gValue, e *gExec, name string, def int) int {
	if v, ok := args[name]; ok {
		switch n := e.val(v).(type) {
		case int64:
			return int(n)
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return def
}

func argBool(args map[string]*gValue, e *gExec, name string) bool {
	if v, ok := args[name]; ok {
		if b, ok := e.val(v).(bool); ok {
			return b
		}
	}
	return false
}

func (e *gExec) dirsOK(dirs []gDirective) bool {
	for _, d := range dirs {
		switch d.name {
		case "include":
			if !argBool(d.args, e, "if") {
				return false
			}
		case "skip":
			if argBool(d.args, e, "if") {
				return false
			}
		}
	}
	return true
}

func gid(typ, key string) string { return b64encode(typ + ":" + key) }

func (e *gExec) entityID(gv GVal) string {
	switch gv.Kind {
	case "Repository":
		return gid("Repository", strconv.FormatInt(gv.Repo.ID, 10))
	case "Issue":
		return gid("Issue", strconv.FormatInt(gv.Issue.ID, 10))
	case "PullRequest":
		return gid("PullRequest", strconv.FormatInt(gv.Issue.ID, 10))
	case "IssueComment":
		return gid("IssueComment", strconv.FormatInt(gv.Cmt.ID, 10))
	case "User":
		u := e.st.user(gv.UserL)
		return gid("User", strconv.FormatInt(u.ID, 10))
	case "Ref":
		return gid("Ref", strconv.FormatInt(gv.Repo.ID, 10)+":"+gv.Ref)
	case "Commit":
		return gid("Commit", strconv.FormatInt(gv.Repo.ID, 10)+":"+gv.SHA)
	case "Tree":
		return gid("Tree", strconv.FormatInt(gv.Repo.ID, 10)+":"+gv.SHA)
	case "Blob":
		return gid("Blob", strconv.FormatInt(gv.Repo.ID, 10)+":"+gv.SHA)
	case "Label":
		return gid("Label", strconv.FormatInt(gv.Repo.ID, 10)+":"+gv.Label)
	}
	return gid(gv.Kind, "0")
}

func (e *gExec) conn(items []GVal, args map[string]*gValue) GVal {
	total := len(items)
	first := argInt(args, e, "first", -1)
	after := argStr(args, e, "after")
	start := 0
	if after != "" {
		if dec, ok := b64decode(after); ok {
			if n, err := strconv.Atoi(dec); err == nil {
				start = n + 1
			}
		}
	}
	if start > len(items) {
		start = len(items)
	}
	end := len(items)
	if first >= 0 && start+first < end {
		end = start + first
	}
	sliced := items[start:end]
	return GVal{Kind: "Connection", Items: sliced, Total: total, Scalar: map[string]interface{}{
		"_start": start, "_end": end, "_total": total,
	}}
}

func (e *gExec) execSel(gv GVal, sels []*gSel) map[string]interface{} {
	out := map[string]interface{}{}
	for _, sel := range sels {
		if !e.dirsOK(sel.dirs) {
			continue
		}
		if sel.spread != "" {
			fr := e.frags[sel.spread]
			if fr == nil {
				continue
			}
			if fr.onType != "" && fr.onType != gv.Kind && gv.Kind != "Query" && gv.Kind != "Mutation" {
				continue
			}
			for k, v := range e.execSel(gv, fr.sel) {
				out[k] = v
			}
			continue
		}
		if sel.onType != "" || sel.sub != nil {
			if sel.onType == gv.Kind || sel.onType == "" {
				for k, v := range e.execSel(gv, sel.sub) {
					out[k] = v
				}
			}
			continue
		}
		f := sel.field
		if !e.dirsOK(f.dirs) {
			continue
		}
		key := f.name
		if f.alias != "" {
			key = f.alias
		}
		out[key] = e.resolveField(gv, f)
	}
	return out
}

func (e *gExec) resolveField(gv GVal, f *gField) interface{} {
	name := f.name
	if name == "__typename" {
		return gv.Kind
	}
	val, handled := e.fieldValue(gv, f)
	if !handled {
		e.errors = append(e.errors, map[string]interface{}{
			"message": fmt.Sprintf("Field '%s' doesn't exist on type '%s'", name, gv.Kind),
			"type":    "FieldUndefined",
		})
		return nil
	}
	switch v := val.(type) {
	case GVal:
		if len(f.sel) == 0 {
			return map[string]interface{}{}
		}
		return e.execSel(v, f.sel)
	case []GVal:
		out := make([]interface{}, 0, len(v))
		for _, item := range v {
			if len(f.sel) == 0 {
				out = append(out, map[string]interface{}{})
			} else {
				out = append(out, e.execSel(item, f.sel))
			}
		}
		return out
	default:
		return val
	}
}
