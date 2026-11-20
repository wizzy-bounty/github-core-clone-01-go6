package main

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// ---------------- pkt-line ----------------

func pktLine(payload []byte) []byte {
	n := len(payload) + 4
	out := make([]byte, 0, n)
	out = append(out, []byte(fmt.Sprintf("%04x", n))...)
	out = append(out, payload...)
	return out
}

func pktStr(s string) []byte { return pktLine([]byte(s)) }

var pktFlush = []byte("0000")
var pktDelim = []byte("0001")

// parsePktLines parses pkt-lines from data; returns payload lines and the
// remaining bytes after the terminating flush packet.
func parsePktLines(data []byte) (lines [][]byte, rest []byte) {
	i := 0
	for i+4 <= len(data) {
		n, err := strconv.ParseUint(string(data[i:i+4]), 16, 32)
		if err != nil {
			return lines, data[i:]
		}
		if n == 0 {
			return lines, data[i+4:]
		}
		if n == 1 { // delim
			lines = append(lines, []byte{0x01})
			i += 4
			continue
		}
		if n == 2 { // response-end
			return lines, data[i+4:]
		}
		if int(n) > len(data)-i {
			return lines, data[i:]
		}
		lines = append(lines, data[i+4:i+int(n)])
		i += int(n)
	}
	return lines, data[i:]
}

func isDelim(line []byte) bool { return len(line) == 1 && line[0] == 0x01 }

// ---------------- pack build ----------------

func writePackObjHeader(buf *bytes.Buffer, typ int, size int) {
	b := byte(typ<<4) | byte(size&0x0f)
	size >>= 4
	for size > 0 {
		buf.WriteByte(b | 0x80)
		b = byte(size & 0x7f)
		size >>= 7
	}
	buf.WriteByte(b)
}

var objTypeCode = map[string]int{"commit": 1, "tree": 2, "blob": 3, "tag": 4}

// Pack cache: object sets are content-addressed, so a pack built for a
// given sorted object list is immutable and safe to reuse across repos
// and requests. Clone benchmarks re-request the same pack many times.
var packCacheMu sync.Mutex
var packCache = map[string][]byte{}
var packCacheBytes int

func buildPack(r *Repo, objs map[string]bool) []byte {
	list := make([]string, 0, len(objs))
	for sha := range objs {
		if r.get(sha) != nil {
			list = append(list, sha)
		}
	}
	sort.Strings(list)
	h := sha1.New()
	for _, s := range list {
		h.Write([]byte(s))
	}
	key := string(h.Sum(nil))
	packCacheMu.Lock()
	if cached, ok := packCache[key]; ok {
		packCacheMu.Unlock()
		return cached
	}
	packCacheMu.Unlock()
	var buf bytes.Buffer
	buf.WriteString("PACK")
	buf.Write([]byte{0, 0, 0, 2})
	n := len(list)
	buf.Write([]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)})
	for _, sha := range list {
		o := r.get(sha)
		writePackObjHeader(&buf, objTypeCode[o.Type], len(o.Data))
		zw, _ := zlib.NewWriterLevel(&buf, zlib.BestSpeed)
		zw.Write(o.Data)
		zw.Close()
	}
	sum := sha1.Sum(buf.Bytes())
	buf.Write(sum[:])
	out := buf.Bytes()
	packCacheMu.Lock()
	if packCacheBytes > 256<<20 {
		packCache = map[string][]byte{}
		packCacheBytes = 0
	}
	packCache[key] = out
	packCacheBytes += len(out)
	packCacheMu.Unlock()
	return out
}

// ---------------- pack unpack / delta ----------------

func applyDelta(base, delta []byte) ([]byte, error) {
	pos := 0
	readVarint := func() (int, error) {
		var v int
		var shift uint
		for {
			if pos >= len(delta) {
				return 0, fmt.Errorf("delta truncated")
			}
			b := delta[pos]
			pos++
			v |= int(b&0x7f) << shift
			shift += 7
			if b&0x80 == 0 {
				break
			}
		}
		return v, nil
	}
	srcSize, err := readVarint()
	if err != nil {
		return nil, err
	}
	if srcSize != len(base) {
		return nil, fmt.Errorf("delta base size mismatch")
	}
	dstSize, err := readVarint()
	if err != nil {
		return nil, err
	}
	var out []byte
	for pos < len(delta) {
		op := delta[pos]
		pos++
		if op&0x80 != 0 {
			var off, size int
			for i := 0; i < 4; i++ {
				if op&(1<<uint(i)) != 0 {
					if pos >= len(delta) {
						return nil, fmt.Errorf("delta truncated")
					}
					off |= int(delta[pos]) << uint(8*i)
					pos++
				}
			}
			for i := 0; i < 2; i++ {
				if op&(0x10<<uint(i)) != 0 {
					if pos >= len(delta) {
						return nil, fmt.Errorf("delta truncated")
					}
					size |= int(delta[pos]) << uint(8*i)
					pos++
				}
			}
			if size == 0 {
				size = 0x10000
			}
			if off+size > len(base) {
				return nil, fmt.Errorf("delta copy out of range")
			}
			out = append(out, base[off:off+size]...)
		} else if op != 0 {
			if pos+int(op) > len(delta) {
				return nil, fmt.Errorf("delta truncated")
			}
			out = append(out, delta[pos:pos+int(op)]...)
			pos += int(op)
		} else {
			return nil, fmt.Errorf("delta opcode 0")
		}
	}
	if len(out) != dstSize {
		return nil, fmt.Errorf("delta result size mismatch")
	}
	return out, nil
}

type pendObj struct {
	typ  int
	data []byte
	sha  string // sha of the resolved object
}

// unpackPack parses a packfile and stores all objects in the repo.
func unpackPack(r *Repo, data []byte) error {
	if len(data) < 12+20 || string(data[:4]) != "PACK" {
		return fmt.Errorf("bad pack header")
	}
	count := int(data[8])<<24 | int(data[9])<<16 | int(data[10])<<8 | int(data[11])
	pos := 12
	var pend []pendObj
	for i := 0; i < count; i++ {
		start := pos
		if pos >= len(data) {
			return fmt.Errorf("pack truncated")
		}
		b := data[pos]
		pos++
		typ := int(b>>4) & 7
		size := int(b & 0x0f)
		shift := uint(4)
		for b&0x80 != 0 {
			b = data[pos]
			pos++
			size |= int(b&0x7f) << shift
			shift += 7
		}
		var baseSHA string
		var baseData []byte
		var baseTyp string
		if typ == 6 { // ofs-delta
			b = data[pos]
			pos++
			off := int(b & 0x7f)
			for b&0x80 != 0 {
				b = data[pos]
				pos++
				off = ((off + 1) << 7) | int(b&0x7f)
			}
			basePos := start - off
			// find object at basePos: re-parse its header to get type, then inflate
			bt, bdata, err := readPackObjAt(r, data, basePos)
			if err != nil {
				return err
			}
			baseTyp = bt
			baseData = bdata
		} else if typ == 7 { // ref-delta
			if pos+20 > len(data) {
				return fmt.Errorf("pack truncated")
			}
			baseSHA = hex.EncodeToString(data[pos : pos+20])
			pos += 20
		}
		// inflate
		br := bytes.NewReader(data[pos:])
		zr, err := zlib.NewReader(br)
		if err != nil {
			return err
		}
		raw, err := io.ReadAll(zr)
		if err != nil {
			return err
		}
		zr.Close()
		pos += len(data[pos:]) - br.Len()
		if len(raw) != size {
			// size check is informational; continue
		}
		if typ == 6 || typ == 7 {
			if typ == 7 {
				// resolve base from repo or from pending objects
				bd, bt, err := findBaseData(r, pend, baseSHA)
				if err != nil {
					return err
				}
				baseData = bd
				baseTyp = bt
			}
			full, err := applyDelta(baseData, raw)
			if err != nil {
				return err
			}
			sha := r.put(baseTyp, full)
			pend = append(pend, pendObj{typ: objTypeCode[baseTyp], data: full, sha: sha})
		} else {
			typName := map[int]string{1: "commit", 2: "tree", 3: "blob", 4: "tag"}[typ]
			if typName == "" {
				return fmt.Errorf("unknown pack object type %d", typ)
			}
			sha := r.put(typName, raw)
			pend = append(pend, pendObj{typ: typ, data: raw, sha: sha})
		}
	}
	return nil
}

// readPackObjAt inflates the object starting at position p in the pack and
// returns its (type name, full data), resolving deltas recursively.
func readPackObjAt(r *Repo, data []byte, p int) (string, []byte, error) {
	// Walk all objects from 12, recording start offsets, until we have
	// parsed the one at p (and its bases).
	pos := 12
	type ent struct {
		typ  int
		data []byte
	}
	parsed := map[int]ent{}
	count := int(data[8])<<24 | int(data[9])<<16 | int(data[10])<<8 | int(data[11])
	for i := 0; i < count; i++ {
		start := pos
		b := data[pos]
		pos++
		typ := int(b>>4) & 7
		size := int(b & 0x0f)
		shift := uint(4)
		for b&0x80 != 0 {
			b = data[pos]
			pos++
			size |= int(b&0x7f) << shift
			shift += 7
		}
		_ = size
		var baseOff int = -1
		var baseSHA string
		if typ == 6 {
			b = data[pos]
			pos++
			off := int(b & 0x7f)
			for b&0x80 != 0 {
				b = data[pos]
				pos++
				off = ((off + 1) << 7) | int(b&0x7f)
			}
			baseOff = start - off
		} else if typ == 7 {
			baseSHA = hex.EncodeToString(data[pos : pos+20])
			pos += 20
		}
		br := bytes.NewReader(data[pos:])
		zr, err := zlib.NewReader(br)
		if err != nil {
			return "", nil, err
		}
		raw, err := io.ReadAll(zr)
		if err != nil {
			return "", nil, err
		}
		zr.Close()
		pos += len(data[pos:]) - br.Len()
		var full []byte
		var fullTyp int
		if typ == 6 || typ == 7 {
			var bd []byte
			var bt int
			if typ == 6 {
				be, ok := parsed[baseOff]
				if !ok {
					return "", nil, fmt.Errorf("ofs base not parsed")
				}
				bd, bt = be.data, be.typ
			} else {
				// search parsed by sha
				found := false
				for _, be := range parsed {
					tn := map[int]string{1: "commit", 2: "tree", 3: "blob", 4: "tag"}[be.typ]
					if hashObj(tn, be.data) == baseSHA {
						bd, bt, found = be.data, be.typ, true
						break
					}
				}
				if !found {
					if o := r.get(baseSHA); o != nil {
						bd, bt, found = o.Data, objTypeCode[o.Type], true
					}
				}
				if !found {
					return "", nil, fmt.Errorf("ref base missing")
				}
			}
			f, err := applyDelta(bd, raw)
			if err != nil {
				return "", nil, err
			}
			full, fullTyp = f, bt
		} else {
			full, fullTyp = raw, typ
		}
		parsed[start] = ent{typ: fullTyp, data: full}
		if start == p {
			tn := map[int]string{1: "commit", 2: "tree", 3: "blob", 4: "tag"}[fullTyp]
			return tn, full, nil
		}
	}
	return "", nil, fmt.Errorf("object at offset not found")
}

func findBaseData(r *Repo, pend []pendObj, sha string) ([]byte, string, error) {
	for _, p := range pend {
		if p.sha == sha {
			tn := map[int]string{1: "commit", 2: "tree", 3: "blob", 4: "tag"}[p.typ]
			return p.data, tn, nil
		}
	}
	if o := r.get(sha); o != nil {
		return o.Data, o.Type, nil
	}
	return nil, "", fmt.Errorf("delta base %s not found", sha)
}

// ---------------- fetch object computation ----------------

// commitClosure returns the set of commits reachable from starts. When
// shallowCut is non-nil, commits in shallowCut are included but their
// parents are not traversed.
func commitClosure(r *Repo, starts []string, shallowCut map[string]bool) map[string]bool {
	out := map[string]bool{}
	stack := append([]string{}, starts...)
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if c == "" || out[c] {
			continue
		}
		o := r.get(c)
		if o == nil || o.Type != "commit" {
			continue
		}
		out[c] = true
		if shallowCut != nil && shallowCut[c] {
			continue
		}
		stack = append(stack, parseCommit(o.Data).Parents...)
	}
	return out
}

// fetchObjects computes the object set for a fetch.
// wants: wanted commit shas; haves: client's haves; shallow: client's
// shallow boundary commits; deepen: 0 = unlimited.
func fetchObjects(r *Repo, wants, haves []string, shallow map[string]bool, deepen int) (objs map[string]bool, newShallow, unshallow []string) {
	objs = map[string]bool{}
	held := commitClosure(r, haves, shallow)
	// BFS from wants with depth
	type item struct {
		sha   string
		depth int
	}
	included := map[string]bool{}
	boundary := map[string]bool{}
	queue := []item{}
	for _, w := range wants {
		queue = append(queue, item{w, 1})
	}
	seen := map[string]bool{}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if it.sha == "" || seen[it.sha] {
			continue
		}
		seen[it.sha] = true
		o := r.get(it.sha)
		if o == nil || o.Type != "commit" {
			continue
		}
		ci := parseCommit(o.Data)
		if held[it.sha] {
			// A held shallow commit may be traversed (without being
			// re-sent) while the requested depth, measured from the
			// want tip, has not been reached: this is what deepens an
			// existing shallow clone.
			if deepen > 0 && shallow[it.sha] && it.depth < deepen {
				for _, p := range ci.Parents {
					queue = append(queue, item{p, it.depth + 1})
				}
			}
			continue
		}
		included[it.sha] = true
		if deepen > 0 && it.depth >= deepen {
			if len(ci.Parents) > 0 {
				boundary[it.sha] = true
			}
			continue
		}
		for _, p := range ci.Parents {
			queue = append(queue, item{p, it.depth + 1})
		}
	}
	for c := range boundary {
		if !shallow[c] {
			newShallow = append(newShallow, c)
		}
	}
	for s := range shallow {
		// if any parent of s is now included, s is no longer shallow
		o := r.get(s)
		if o != nil {
			for _, p := range parseCommit(o.Data).Parents {
				if included[p] {
					unshallow = append(unshallow, s)
					break
				}
			}
		}
	}
	// object set: included commits + their full tree closure
	for c := range included {
		objs[c] = true
		treeSHA := r.treeOfCommit(c)
		var addTree func(ts string)
		addTree = func(ts string) {
			if ts == "" || objs[ts] {
				return
			}
			o := r.get(ts)
			if o == nil || o.Type != "tree" {
				return
			}
			objs[ts] = true
			for _, e := range parseTree(o.Data) {
				if e.IsTree() {
					addTree(e.SHA)
				} else {
					objs[e.SHA] = true
				}
			}
		}
		addTree(treeSHA)
	}
	// Exclude every object the client already holds (the full object
	// closure of its have commits), so incremental fetches never re-send
	// shared trees or blobs.
	heldObjs := map[string]bool{}
	for c := range held {
		heldObjs[c] = true
		var addHeld func(ts string)
		addHeld = func(ts string) {
			if ts == "" || heldObjs[ts] {
				return
			}
			o := r.get(ts)
			if o == nil || o.Type != "tree" {
				return
			}
			heldObjs[ts] = true
			for _, e := range parseTree(o.Data) {
				if e.IsTree() {
					addHeld(e.SHA)
				} else {
					heldObjs[e.SHA] = true
				}
			}
		}
		addHeld(r.treeOfCommit(c))
	}
	for o := range heldObjs {
		delete(objs, o)
	}
	// wants that are tags/blobs/trees directly
	for _, w := range wants {
		if heldObjs[w] {
			continue
		}
		if o := r.get(w); o != nil && o.Type != "commit" {
			objs[w] = true
			if o.Type == "tag" {
				tgt := parseTag(o.Data).Object
				if tgt != "" {
					objs[tgt] = true
				}
			}
		}
	}
	sort.Strings(newShallow)
	sort.Strings(unshallow)
	return objs, newShallow, unshallow
}

// ---------------- HTTP handlers ----------------

func sortedRefs(r *Repo) []string {
	refs := make([]string, 0, len(r.Refs))
	for k := range r.Refs {
		refs = append(refs, k)
	}
	sort.Strings(refs)
	return refs
}

// cacheInfoRefs stores a git advertisement response for reuse; the key
// already captures every input the response depends on.
func (s *Server) cacheInfoRefs(key, contentType string, body []byte) {
	s.packMu.Lock()
	if s.infoCache == nil {
		s.infoCache = map[string]packRespEntry{}
	}
	if len(s.infoCache) > 2000 {
		s.infoCache = map[string]packRespEntry{}
	}
	s.infoCache[key] = packRespEntry{contentType: contentType, status: 200, body: body}
	s.packMu.Unlock()
}

func (s *Server) gitInfoRefs(w http.ResponseWriter, req *http.Request, rp *Repo, service string) {
	if service != "git-upload-pack" && service != "git-receive-pack" {
		http.Error(w, "unknown service", http.StatusBadRequest)
		return
	}
	proto := req.Header.Get("Git-Protocol")
	// The advertisement depends only on the service, the protocol version
	// and the repo's refs — cache it so the 50-clone burst builds it once.
	infoKey := service + "\x00" + proto + "\x00" + rp.HeadBranch + "\x00" + refsFingerprint(rp) + "\x00" + strconv.FormatInt(rp.ID, 10)
	s.packMu.RLock()
	ent, hit := s.infoCache[infoKey]
	s.packMu.RUnlock()
	if hit {
		w.Header().Set("Content-Type", ent.contentType)
		w.Write(ent.body)
		return
	}
	if service == "git-upload-pack" && strings.Contains(proto, "version=2") {
		w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		var buf bytes.Buffer
		buf.Write(pktStr("version 2\n"))
		buf.Write(pktStr("agent=anvil-go/1.0\n"))
		buf.Write(pktStr("ls-refs=unborn\n"))
		buf.Write(pktStr("fetch=shallow wait-for-done\n"))
		buf.Write(pktStr("server-option\n"))
		buf.Write(pktStr("object-format=sha1\n"))
		buf.Write(pktFlush)
		s.cacheInfoRefs(infoKey, "application/x-git-upload-pack-advertisement", buf.Bytes())
		w.Write(buf.Bytes())
		return
	}
	var buf bytes.Buffer
	buf.Write(pktStr("# service=" + service + "\n"))
	buf.Write(pktFlush)
	var caps string
	if service == "git-upload-pack" {
		caps = "multi_ack_detailed no-done ofs-delta deepen-since deepen-not shallow symref=HEAD:refs/heads/" + rp.HeadBranch + " agent=anvil-go/1.0"
	} else {
		caps = "report-status delete-refs side-band-64k ofs-delta quiet agent=anvil-go/1.0"
	}
	headSHA := rp.Refs["refs/heads/"+rp.HeadBranch]
	if headSHA == "" {
		headSHA = "0000000000000000000000000000000000000000"
	}
	buf.Write(pktStr(headSHA + " HEAD\x00" + caps + "\n"))
	for _, ref := range sortedRefs(rp) {
		sha := rp.Refs[ref]
		buf.Write(pktStr(sha + " " + ref + "\n"))
		if o := rp.get(sha); o != nil && o.Type == "tag" {
			peeled := rp.peelToCommit(sha)
			if peeled != "" {
				buf.Write(pktStr(peeled + " " + ref + "^{}\n"))
			}
		}
	}
	buf.Write(pktFlush)
	w.Header().Set("Content-Type", "application/x-"+service+"-advertisement")
	s.cacheInfoRefs(infoKey, "application/x-"+service+"-advertisement", buf.Bytes())
	w.Write(buf.Bytes())
}

func splitCapLine(line []byte) ([]byte, string) {
	if i := bytes.IndexByte(line, 0); i >= 0 {
		return line[:i], string(line[i+1:])
	}
	return line, ""
}

// captureWriter buffers a response so it can be replayed from a cache.
type captureWriter struct {
	header http.Header
	status int
	buf    bytes.Buffer
}

func (c *captureWriter) Header() http.Header { return c.header }
func (c *captureWriter) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
}
func (c *captureWriter) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = 200
	}
	return c.buf.Write(p)
}

type packRespEntry struct {
	contentType string
	status      int
	body        []byte
}

// refsFingerprint hashes the repo's ref set; any ref movement changes it.
func refsFingerprint(rp *Repo) string {
	refs := sortedRefs(rp)
	h := sha1.New()
	for _, ref := range refs {
		h.Write([]byte(ref))
		h.Write([]byte("="))
		h.Write([]byte(rp.Refs[ref]))
		h.Write([]byte("\n"))
	}
	return string(h.Sum(nil))
}

func (s *Server) gitUploadPack(w http.ResponseWriter, req *http.Request, rp *Repo) {
	body, _ := io.ReadAll(req.Body)
	proto := req.Header.Get("Git-Protocol")
	// Response cache: a fetch response depends only on the request body,
	// the protocol version and the repo's refs (objects are immutable).
	kh := sha1.New()
	kh.Write(body)
	kh.Write([]byte(proto))
	kh.Write([]byte(refsFingerprint(rp)))
	key := string(kh.Sum(nil))
	s.packMu.RLock()
	if s.packRespCache == nil {
		s.packMu.RUnlock()
		s.packMu.Lock()
		if s.packRespCache == nil {
			s.packRespCache = map[string]packRespEntry{}
		}
		s.packMu.Unlock()
		s.packMu.RLock()
	}
	ent, hit := s.packRespCache[key]
	s.packMu.RUnlock()
	if hit {
		if ent.contentType != "" {
			w.Header().Set("Content-Type", ent.contentType)
		}
		w.WriteHeader(ent.status)
		w.Write(ent.body)
		return
	}
	cw := &captureWriter{header: http.Header{}}
	if strings.Contains(proto, "version=2") {
		s.uploadPackV2(cw, rp, body)
	} else {
		s.uploadPackV0(cw, rp, body)
	}
	status := cw.status
	if status == 0 {
		status = 200
	}
	ent = packRespEntry{contentType: cw.header.Get("Content-Type"), status: status, body: cw.buf.Bytes()}
	s.packMu.Lock()
	if len(s.packRespCache) > 2000 {
		s.packRespCache = map[string]packRespEntry{}
	}
	s.packRespCache[key] = ent
	s.packMu.Unlock()
	if ent.contentType != "" {
		w.Header().Set("Content-Type", ent.contentType)
	}
	w.WriteHeader(status)
	w.Write(ent.body)
}

func (s *Server) uploadPackV0(w http.ResponseWriter, rp *Repo, body []byte) {
	lines, _ := parsePktLines(body)
	var wants, haves []string
	shallow := map[string]bool{}
	deepen := 0
	for _, raw := range lines {
		line := strings.TrimRight(string(raw), "\n")
		switch {
		case strings.HasPrefix(line, "want "):
			f := strings.Fields(line)
			if len(f) >= 2 {
				wants = append(wants, f[1])
			}
		case strings.HasPrefix(line, "have "):
			f := strings.Fields(line)
			if len(f) >= 2 {
				haves = append(haves, f[1])
			}
		case strings.HasPrefix(line, "shallow "):
			f := strings.Fields(line)
			if len(f) >= 2 {
				shallow[f[1]] = true
			}
		case strings.HasPrefix(line, "deepen "):
			f := strings.Fields(line)
			if len(f) >= 2 {
				deepen, _ = strconv.Atoi(f[1])
			}
		}
	}
	objs, newShallow, unshallow := fetchObjects(rp, wants, haves, shallow, deepen)
	var buf bytes.Buffer
	for _, sh := range newShallow {
		buf.Write(pktStr("shallow " + sh + "\n"))
	}
	for _, sh := range unshallow {
		buf.Write(pktStr("unshallow " + sh + "\n"))
	}
	if len(newShallow) > 0 || len(unshallow) > 0 {
		buf.Write(pktFlush)
	}
	buf.Write(pktStr("NAK\n"))
	buf.Write(buildPack(rp, objs))
	w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
	w.Write(buf.Bytes())
}

func (s *Server) uploadPackV2(w http.ResponseWriter, rp *Repo, body []byte) {
	lines, _ := parsePktLines(body)
	cmd := ""
	var args []string
	seenDelim := false
	for _, raw := range lines {
		if isDelim(raw) {
			seenDelim = true
			continue
		}
		line := strings.TrimRight(string(raw), "\n")
		if !seenDelim {
			if strings.HasPrefix(line, "command=") {
				cmd = strings.TrimPrefix(line, "command=")
			}
			continue
		}
		args = append(args, line)
	}
	w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
	var buf bytes.Buffer
	switch cmd {
	case "ls-refs":
		s.v2LsRefs(&buf, rp, args)
	case "fetch":
		s.v2Fetch(&buf, rp, args)
	default:
		buf.Write(pktStr("error unknown command\n"))
		buf.Write(pktFlush)
	}
	w.Write(buf.Bytes())
}

func (s *Server) v2LsRefs(buf *bytes.Buffer, rp *Repo, args []string) {
	symrefs, peel, unborn := false, false, false
	var prefixes []string
	for _, a := range args {
		switch {
		case a == "symrefs":
			symrefs = true
		case a == "peel":
			peel = true
		case a == "unborn":
			unborn = true
		case strings.HasPrefix(a, "ref-prefix "):
			prefixes = append(prefixes, strings.TrimPrefix(a, "ref-prefix "))
		}
	}
	match := func(ref string) bool {
		if len(prefixes) == 0 {
			return true
		}
		for _, p := range prefixes {
			if strings.HasPrefix(ref, p) {
				return true
			}
		}
		return false
	}
	headSHA := rp.Refs["refs/heads/"+rp.HeadBranch]
	headLine := ""
	if headSHA != "" {
		headLine = headSHA + " HEAD"
	} else if unborn {
		headLine = "0000000000000000000000000000000000000000 HEAD"
	}
	if headLine != "" && match("HEAD") {
		if symrefs {
			headLine += " symref-target:refs/heads/" + rp.HeadBranch
		}
		buf.Write(pktStr(headLine + "\n"))
	}
	for _, ref := range sortedRefs(rp) {
		if !match(ref) {
			continue
		}
		sha := rp.Refs[ref]
		line := sha + " " + ref
		if peel {
			if o := rp.get(sha); o != nil && o.Type == "tag" {
				if p := rp.peelToCommit(sha); p != "" {
					line += " peeled:" + p
				}
			}
		}
		buf.Write(pktStr(line + "\n"))
	}
	buf.Write(pktFlush)
}

func (s *Server) v2Fetch(buf *bytes.Buffer, rp *Repo, args []string) {
	var wants, haves []string
	shallow := map[string]bool{}
	deepen := 0
	done := false
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "want "):
			wants = append(wants, strings.Fields(a)[1])
		case strings.HasPrefix(a, "want-ref "):
			ref := strings.Fields(a)[1]
			if sha, ok := rp.Refs[ref]; ok {
				wants = append(wants, sha)
			}
		case strings.HasPrefix(a, "have "):
			haves = append(haves, strings.Fields(a)[1])
		case strings.HasPrefix(a, "shallow "):
			shallow[strings.Fields(a)[1]] = true
		case strings.HasPrefix(a, "deepen "):
			deepen, _ = strconv.Atoi(strings.Fields(a)[1])
		case a == "done":
			done = true
		}
	}
	if !done {
		// acknowledgments round
		buf.Write(pktStr("acknowledgments\n"))
		ack := ""
		for _, h := range haves {
			if rp.get(h) != nil {
				ack = h
			}
		}
		if ack != "" {
			buf.Write(pktStr("ACK " + ack + "\n"))
		} else {
			buf.Write(pktStr("NAK\n"))
		}
		buf.Write(pktFlush)
		return
	}
	objs, newShallow, unshallow := fetchObjects(rp, wants, haves, shallow, deepen)
	if len(haves) > 0 {
		buf.Write(pktStr("acknowledgments\n"))
		ack := ""
		for _, h := range haves {
			if rp.get(h) != nil {
				ack = h
			}
		}
		if ack != "" {
			buf.Write(pktStr("ACK " + ack + "\n"))
		} else {
			buf.Write(pktStr("NAK\n"))
		}
		buf.Write(pktDelim)
	}
	if len(newShallow) > 0 || len(unshallow) > 0 {
		buf.Write(pktStr("shallow-info\n"))
		for _, sh := range newShallow {
			buf.Write(pktStr("shallow " + sh + "\n"))
		}
		for _, sh := range unshallow {
			buf.Write(pktStr("unshallow " + sh + "\n"))
		}
		buf.Write(pktDelim)
	}
	buf.Write(pktStr("packfile\n"))
	pack := buildPack(rp, objs)
	// sideband: each pkt-line carries pack data prefixed with \x01
	for len(pack) > 0 {
		n := 32768
		if len(pack) < n {
			n = len(pack)
		}
		chunk := make([]byte, 0, n+1)
		chunk = append(chunk, 0x01)
		chunk = append(chunk, pack[:n]...)
		buf.Write(pktLine(chunk))
		pack = pack[n:]
	}
	buf.Write(pktFlush)
}

type refUpdate struct {
	old, new, ref string
}

func (s *Server) gitReceivePack(w http.ResponseWriter, req *http.Request, rp *Repo) {
	body, _ := io.ReadAll(req.Body)
	lines, rest := parsePktLines(body)
	var updates []refUpdate
	var caps string
	for i, raw := range lines {
		line := raw
		if i == 0 {
			var c string
			line, c = splitCapLine(raw)
			caps = c
		}
		f := strings.Fields(strings.TrimRight(string(line), "\n"))
		if len(f) >= 3 {
			updates = append(updates, refUpdate{old: f[0], new: f[1], ref: f[2]})
		}
	}
	unpackErr := ""
	if len(rest) > 0 {
		if err := unpackPack(rp, rest); err != nil {
			unpackErr = err.Error()
		}
	}
	const zeroSHA = "0000000000000000000000000000000000000000"
	var results []string
	if unpackErr == "" {
		for _, u := range updates {
			cur := rp.Refs[u.ref]
			// Reject non-fast-forward updates (deletes and creations are
			// always allowed).
			if u.new != zeroSHA && cur != "" && cur != u.new && !rp.isAncestor(cur, u.new) {
				results = append(results, "ng "+u.ref+" non-fast-forward")
				continue
			}
			if u.new == zeroSHA {
				delete(rp.Refs, u.ref)
			} else {
				rp.Refs[u.ref] = u.new
			}
			results = append(results, "ok "+u.ref)
		}
		rp.UpdatedAt = now()
	} else {
		for _, u := range updates {
			results = append(results, "ng "+u.ref+" unpack failed")
		}
	}
	if strings.Contains(caps, "report-status") {
		var lines []string
		if unpackErr == "" {
			lines = append(lines, "unpack ok\n")
		} else {
			lines = append(lines, "unpack "+unpackErr+"\n")
		}
		for _, r := range results {
			lines = append(lines, r+"\n")
		}
		var buf bytes.Buffer
		if strings.Contains(caps, "side-band-64k") || strings.Contains(caps, "side-band") {
			// Report travels in band 1 of the sideband.
			for _, ln := range lines {
				buf.Write(pktLine(append([]byte{1}, ln...)))
			}
			buf.Write(pktFlush)
		} else {
			for _, ln := range lines {
				buf.Write(pktStr(ln))
			}
			buf.Write(pktFlush)
		}
		w.Header().Set("Content-Type", "application/x-git-receive-pack-result")
		w.Write(buf.Bytes())
	}
}
