package lex

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SubEdit is one exact-text replacement in a built file: Old must occur exactly Want times.
type SubEdit struct {
	Old, New string
	Want     int
	Origin   string // "arguments" or "block 2 (line 7)", for the answer
}

// SubResult names what Sub wrote: codex files (fragments and manifests) and the other outputs rebuilt from a shared fragment.
type SubResult struct {
	Changed []string
	Shared  []string
}

// SubRefusal is a refused edit: nothing is written, and the edit can go the fallback way (sub on the file, then absorb).
type SubRefusal struct{ Reason string }

func (e *SubRefusal) Error() string { return e.Reason }

func subRefuse(format string, args ...any) error {
	return &SubRefusal{Reason: fmt.Sprintf(format, args...)}
}

// Kinds of the pieces a built file is glued from.
const (
	subFixed   = iota // text the build generates: the lex header, the "  Зачем: " marker, a line end
	subBody           // bytes of a fragment body
	subPurpose        // the value of the fragment field цель
)

type subSegment struct {
	start, end int
	kind       int
	label      string // what fixed text this is, for a refusal
	path       string // fragment file of an editable piece
	raw        int    // offset in the fragment file of the piece start
}

type subLayout struct {
	out  []byte
	segs []subSegment
}

type subRawEdit struct {
	path       string
	start, end int
	text       []byte
}

// Sub replaces text in a built file by editing the fragments the file is built from and rebuilding it.
// Each match is traced to its fragment by the way the build glued the file; the file after the rebuild must equal
// the file with the replacement applied directly, else nothing is written.
func Sub(codex, file string, edits []SubEdit) (SubResult, error) {
	codex, err := canonicalCodex(codex, false)
	if err != nil {
		return SubResult{}, err
	}
	catalog, err := listManifestCatalog(codex)
	if err != nil {
		return SubResult{}, err
	}
	target, err := filepath.Abs(file)
	if err != nil {
		return SubResult{}, fmt.Errorf("resolve output path: %w", err)
	}
	if info, e := os.Lstat(target); e != nil {
		return SubResult{}, fmt.Errorf("%s: %w", file, e)
	} else if info.Mode()&os.ModeSymlink != 0 {
		return SubResult{}, subRefuse("%s: refusing to write through symlink", file)
	}
	actual, err := os.ReadFile(target)
	if err != nil {
		return SubResult{}, fmt.Errorf("%s: %w", file, err)
	}
	if err := rejectForeignOutput(codex, target, actual); err != nil {
		return SubResult{}, err
	}
	selected, m, err := catalog.findOutputManifest(target)
	if err != nil {
		return SubResult{}, err
	}
	enabled, err := readPurposeSetting(codex, os.ReadFile)
	if err != nil {
		return SubResult{}, err
	}
	orig := map[string][]byte{}
	raws := map[string][]byte{}
	read := func(path string) ([]byte, error) {
		if data, ok := raws[path]; ok {
			return data, nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		orig[path] = data
		raws[path] = data
		return data, nil
	}
	layout, err := subBuildLayout(codex, selected, m, enabled, read)
	if err != nil {
		return SubResult{}, err
	}
	if !bytes.Equal(actual, layout.out) {
		line, fileLine, builtLine := firstDiff(actual, layout.out)
		return SubResult{}, subRefuse("%s: file differs from its build at line %d: file %q, built %q; carry that edit over first (lex absorb) or drop it (lex build --force)", file, line, trunc(fileLine), trunc(builtLine))
	}
	cur := actual
	for _, edit := range edits {
		next, err := subApplyEdit(layout, cur, edit, raws, file)
		if err != nil {
			return SubResult{}, err
		}
		layout, err = subBuildLayout(codex, selected, m, enabled, read)
		if err != nil {
			return SubResult{}, subRefuse("%s: %s: the edited fragments do not build: %v", file, edit.Origin, err)
		}
		if !bytes.Equal(layout.out, next) {
			line, wantLine, builtLine := firstDiff(next, layout.out)
			return SubResult{}, subRefuse("%s: %s: the edited fragments would not rebuild the edited file: first differing line %d: wanted %q, built %q", file, edit.Origin, line, trunc(wantLine), trunc(builtLine))
		}
		cur = next
	}
	if err := subCheckStructure(file, orig, raws); err != nil {
		return SubResult{}, err
	}
	return subWrite(codex, target, selected, cur, orig, raws, catalog)
}

// subFragmentText is what an edited fragment holds: its body and its purpose.
type subFragmentText struct {
	path        string
	old, edited map[string]int // words of the body and the field цель
}

// subCheckStructure refuses edits that change what a fragment is rather than its wording: lex sub keeps every
// fragment's id, основание and снять_когда, so a new item, paragraph or heading glued into a neighbour, or text moved
// from one fragment into another, would carry the wrong fields. absorb makes new fragments and moves for these.
func subCheckStructure(file string, orig, raws map[string][]byte) error {
	var changed []subFragmentText
	for _, path := range slices.Sorted(maps.Keys(raws)) {
		data := raws[path]
		if bytes.Equal(orig[path], data) || filepath.Base(path) == "purpose.json" {
			continue
		}
		oldBody, err := fragmentBody(orig[path])
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		newBody, err := fragmentBody(data)
		if err != nil {
			return subRefuse("%s: the edit breaks fragment %s: %v", file, path, err)
		}
		if a, b := len(split(oldBody)), len(split(newBody)); a != b {
			return subRefuse("%s: the edit changes the structure of fragment %s (%d units by the import split, now %d): a new item, paragraph or heading needs its own fragment with its own fields", file, path, a, b)
		}
		if a, b := subItemStarts(oldBody), subItemStarts(newBody); a != b {
			return subRefuse("%s: the edit changes the structure of fragment %s (%d lines start an item or heading, now %d): a new item or heading needs its own fragment with its own fields", file, path, a, b)
		}
		changed = append(changed, subFragmentText{path: path,
			old: subWords(oldBody, purposeValue(orig[path])), edited: subWords(newBody, purposeValue(data))})
	}
	return subRefuseMovedText(file, changed)
}

// subRefuseMovedText refuses when a fragment gains more than half of the words another edited fragment held before:
// that is text moved between fragments, as when two items swap places.
func subRefuseMovedText(file string, changed []subFragmentText) error {
	holders := map[string][]int{} // word → indexes of the fragments that held it before the edit
	totals := make([]int, len(changed))
	for i, from := range changed {
		for word, n := range from.old {
			holders[word] = append(holders[word], i)
			totals[i] += n
		}
	}
	for k, to := range changed {
		got := map[int]int{}
		for word, n := range to.edited {
			if gained := n - to.old[word]; gained > 0 {
				for _, i := range holders[word] {
					got[i] += min(gained, changed[i].old[word])
				}
			}
		}
		for _, i := range slices.Sorted(maps.Keys(got)) {
			if i != k && 2*got[i] > totals[i] {
				return subRefuse("%s: fragment %s would get %d of the %d words of fragment %s: moved text keeps the id, основание and снять_когда of the fragment it lands in", file, to.path, got[i], totals[i], changed[i].path)
			}
		}
	}
	return nil
}

// subItemStarts counts the lines that start a top-level list item or a heading; nested sub-items stay in their item,
// and the import split already catches a new top-level unit.
func subItemStarts(body []byte) int {
	count := 0
	for line := range strings.SplitSeq(string(body), "\n") {
		text := strings.TrimSuffix(line, "\r")
		if itemRE.MatchString(text) || heading(text) {
			count++
		}
	}
	return count
}

// subWords is the multiset of words (runs of letters and digits) of a fragment body and its purpose.
func subWords(body []byte, purpose string) map[string]int {
	words := map[string]int{}
	for _, word := range strings.FieldsFunc(string(body)+"\n"+purpose, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		words[word]++
	}
	return words
}

// subApplyEdit finds edit in cur, traces each match to fragment bytes and changes them in raws; it returns cur with the
// replacement applied directly.
func subApplyEdit(layout subLayout, cur []byte, edit SubEdit, raws map[string][]byte, file string) ([]byte, error) {
	old, repl := []byte(edit.Old), []byte(edit.New)
	if len(old) == 0 {
		return nil, fmt.Errorf("%s: empty OLD text", edit.Origin)
	}
	var starts []int
	for at := 0; ; {
		i := bytes.Index(cur[at:], old)
		if i < 0 {
			break
		}
		starts = append(starts, at+i)
		at += i + len(old)
	}
	if len(starts) != edit.Want {
		return nil, &SubCountError{Origin: edit.Origin, File: file, Want: edit.Want, Found: len(starts), Lines: subLines(cur, starts)}
	}
	p, q := subCommonEnds(old, repl)
	var rawEdits []subRawEdit
	for _, s := range starts {
		got, err := layout.mapChange(s+p, s+len(old)-q, repl[p:len(repl)-q])
		if err != nil {
			return nil, subRefuse("%s: %s, match at line %d: %v", file, edit.Origin, bytes.Count(cur[:s], []byte("\n"))+1, err)
		}
		rawEdits = append(rawEdits, got...)
	}
	if err := subApplyRawEdits(raws, rawEdits); err != nil {
		return nil, subRefuse("%s: %s: %v", file, edit.Origin, err)
	}
	return bytes.ReplaceAll(cur, old, repl), nil
}

// SubCountError is a match count other than the expected one; nothing is written.
type SubCountError struct {
	Origin, File string
	Want, Found  int
	Lines        []int
}

func (e *SubCountError) Error() string {
	where := ""
	if len(e.Lines) > 0 {
		parts := make([]string, len(e.Lines))
		for i, line := range e.Lines {
			parts[i] = strconv.Itoa(line)
		}
		where = " (lines " + strings.Join(parts, ", ") + ")"
	}
	return fmt.Sprintf("%s: %s: expected %d match(es), found %d%s; no file changed", e.File, e.Origin, e.Want, e.Found, where)
}

func subLines(data []byte, starts []int) []int {
	lines := make([]int, 0, min(len(starts), 8))
	for _, s := range starts {
		if len(lines) == 8 {
			break
		}
		lines = append(lines, bytes.Count(data[:s], []byte("\n"))+1)
	}
	return lines
}

// subCommonEnds returns the lengths of the common prefix and suffix of a and b, on rune bounds and not overlapping.
func subCommonEnds(a, b []byte) (int, int) {
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	for p > 0 && p < len(a) && !utf8.RuneStart(a[p]) {
		p--
	}
	q := 0
	for q < len(a)-p && q < len(b)-p && a[len(a)-1-q] == b[len(b)-1-q] {
		q++
	}
	for q > 0 && len(a)-q < len(a) && !utf8.RuneStart(a[len(a)-q]) {
		q--
	}
	return p, q
}

// subBuildLayout builds the manifest output as the build does and records where each piece of it comes from.
func subBuildLayout(codex, manifestPath string, m manifest, enabled bool, read func(string) ([]byte, error)) (subLayout, error) {
	var l subLayout
	add := func(text []byte, kind int, label, path string, raw int) {
		if len(text) == 0 {
			return
		}
		l.segs = append(l.segs, subSegment{start: len(l.out), end: len(l.out) + len(text), kind: kind, label: label, path: path, raw: raw})
		l.out = append(l.out, text...)
	}
	for i, id := range m.parts {
		p := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
		raw, err := read(p)
		if err != nil {
			return subLayout{}, fmt.Errorf("%s: %w", p, err)
		}
		if fragmentIsRecord(raw) {
			return subLayout{}, fmt.Errorf("%s: record must not be in a manifest", p)
		}
		body, err := fragmentBody(raw)
		if err != nil {
			return subLayout{}, fmt.Errorf("%s: %w", p, err)
		}
		if i < len(m.parts)-1 && len(body) > 0 && body[len(body)-1] != '\n' {
			return subLayout{}, fmt.Errorf("%s: fragment before last has no final newline", p)
		}
		bodyAt := len(raw) - len(body)
		rendered, _, err := renderPurpose(p, raw, body, enabled)
		if err != nil {
			return subLayout{}, err
		}
		if bytes.Equal(rendered, body) {
			add(body, subBody, "", p, bodyAt)
			continue
		}
		value, valueAt, ok := subPurposeField(raw)
		if !ok {
			return subLayout{}, fmt.Errorf("%s: purpose field not found", p)
		}
		lineEnd := subPurposeLineEnd(body)
		add(body[:lineEnd], subBody, "", p, bodyAt)
		if lineEnd == 0 || body[lineEnd-1] != '\n' {
			add([]byte("\n"), subFixed, "a line end the build adds", "", 0)
		}
		add([]byte(purposeMarker), subFixed, "the «Зачем:» marker the build prints before the field цель", "", 0)
		add(value, subPurpose, "", p, valueAt)
		add([]byte("\n"), subFixed, "the line end after the «Зачем:» line", "", 0)
		add(body[lineEnd:], subBody, "", p, bodyAt+lineEnd)
	}
	if m.header {
		l.insertFixed(frontMatterOffset(l.out), generatedMarker(manifestPath), "the lex header line")
	}
	want, err := manifestBodyWithRead(codex, m, read)
	if err != nil {
		return subLayout{}, err
	}
	if m.header {
		want = generatedHeader(manifestPath, want)
	}
	if !bytes.Equal(want, l.out) {
		return subLayout{}, errors.New("internal: traced layout differs from the build")
	}
	return l, nil
}

// subPurposeField returns the printed value of the field цель and its offset in the fragment file.
func subPurposeField(raw []byte) ([]byte, int, bool) {
	header := fragmentHeader(raw)
	found, at, offset := false, 0, 0
	var value []byte
	for line := range bytes.SplitAfterSeq(header, []byte("\n")) {
		key, rest, ok := bytes.Cut(line, []byte(":"))
		if ok && string(key) == "цель" {
			trimmed := bytes.TrimSpace(rest)
			lead := len(rest) - len(bytes.TrimLeft(rest, " \t\r\n\v\f"))
			found, at, value = true, offset+len(key)+1+lead, trimmed
		}
		offset += len(line)
	}
	if !found || string(value) != purposeValue(raw) {
		return nil, 0, false
	}
	return value, at, true
}

// subPurposeLineEnd is where insertPurposeLine puts the «Зачем:» line: after the last line with text.
func subPurposeLineEnd(body []byte) int {
	end := len(body)
	for end > 0 && (body[end-1] == '\n' || body[end-1] == '\r' || body[end-1] == ' ' || body[end-1] == '\t') {
		end--
	}
	for end < len(body) && body[end] != '\n' {
		end++
	}
	if end < len(body) {
		end++
	}
	return end
}

func (l *subLayout) insertFixed(at int, text []byte, label string) {
	var segs []subSegment
	inserted := false
	put := func() {
		segs = append(segs, subSegment{start: at, end: at + len(text), kind: subFixed, label: label})
		inserted = true
	}
	for _, seg := range l.segs {
		switch {
		case seg.end <= at:
			segs = append(segs, seg)
		case seg.start >= at:
			if !inserted {
				put()
			}
			seg.start += len(text)
			seg.end += len(text)
			segs = append(segs, seg)
		default:
			left, right := seg, seg
			left.end = at
			right.start = at + len(text)
			right.end += len(text)
			right.raw += at - seg.start
			segs = append(segs, left)
			put()
			segs = append(segs, right)
		}
	}
	if !inserted {
		put()
	}
	l.out = slices.Concat(l.out[:at:at], text, l.out[at:])
	l.segs = segs
}

func (l subLayout) segmentRaw(seg subSegment, from, to int, text []byte) subRawEdit {
	return subRawEdit{path: seg.path, start: seg.raw + from - seg.start, end: seg.raw + to - seg.start, text: text}
}

func fixedRefusal(seg subSegment) error {
	return fmt.Errorf("the edit changes %s, which no fragment holds", seg.label)
}

// mapChange traces the change of out[a:b] to repl into fragment edits.
func (l subLayout) mapChange(a, b int, repl []byte) ([]subRawEdit, error) {
	if a == b {
		return l.mapInsertion(a, repl)
	}
	var idx []int
	for i, seg := range l.segs {
		if seg.start < b && seg.end > a {
			idx = append(idx, i)
		}
	}
	if len(idx) == 1 {
		seg := l.segs[idx[0]]
		if seg.kind == subFixed {
			return nil, fixedRefusal(seg)
		}
		if seg.kind == subPurpose && bytes.ContainsAny(repl, "\r\n") {
			return nil, errors.New("the field цель is one line: the edit puts a line break into the «Зачем:» line")
		}
		return []subRawEdit{l.segmentRaw(seg, a, b, repl)}, nil
	}
	bounds := make([]int, 0, len(idx)+1)
	bounds = append(bounds, a)
	for _, i := range idx[1:] {
		bounds = append(bounds, l.segs[i].start)
	}
	bounds = append(bounds, b)
	rightFixed := make([]bool, len(bounds))
	for k := 1; k < len(bounds)-1; k++ {
		rightFixed[k] = l.segs[idx[k]].kind == subFixed
	}
	mapped, ok := subAlign(l.out[a:b], repl, bounds, rightFixed)
	if !ok {
		return nil, errors.New("the change crosses fragment pieces and is too long to split between them")
	}
	var edits []subRawEdit
	for k, i := range idx {
		seg := l.segs[i]
		from, to := bounds[k], bounds[k+1]
		piece := repl[mapped[k]:mapped[k+1]]
		switch seg.kind {
		case subFixed:
			if !bytes.Equal(piece, l.out[from:to]) {
				return nil, fixedRefusal(seg)
			}
		case subPurpose:
			if bytes.ContainsAny(piece, "\r\n") {
				return nil, errors.New("the field цель is one line: the edit puts a line break into the «Зачем:» line")
			}
			fallthrough
		default:
			edits = append(edits, l.segmentRaw(seg, from, to, append([]byte(nil), piece...)))
		}
	}
	return edits, nil
}

func (l subLayout) mapInsertion(at int, text []byte) ([]subRawEdit, error) {
	var left, right *subSegment
	for i := range l.segs {
		seg := &l.segs[i]
		if seg.start < at && at < seg.end {
			if seg.kind == subFixed {
				return nil, fixedRefusal(*seg)
			}
			if seg.kind == subPurpose && bytes.ContainsAny(text, "\r\n") {
				return nil, errors.New("the field цель is one line: the edit puts a line break into the «Зачем:» line")
			}
			return []subRawEdit{l.segmentRaw(*seg, at, at, text)}, nil
		}
		if seg.end == at {
			left = seg
		}
		if seg.start == at {
			right = seg
		}
	}
	// The bound between pieces is a line start: the text goes to the piece that starts there, unless the build
	// generates that piece (the «Зачем:» line or the header), then to the piece that ends there.
	for _, seg := range []*subSegment{right, left} {
		if seg == nil || seg.kind == subFixed || seg.kind == subPurpose && bytes.ContainsAny(text, "\r\n") {
			continue
		}
		return []subRawEdit{l.segmentRaw(*seg, at, at, text)}, nil
	}
	if left != nil && left.kind == subFixed {
		return nil, fixedRefusal(*left)
	}
	if right != nil {
		return nil, fixedRefusal(*right)
	}
	return nil, errors.New("no fragment holds the place of the insertion")
}

// subAlign maps each byte bound of old (bounds[0] = start of old in absolute offsets, last = end) to a byte offset in
// repl by a rune-level longest common subsequence. Text inserted at a bound goes to the piece on its right, unless that
// piece is fixed, as in mapInsertion.
func subAlign(old, repl []byte, bounds []int, rightFixed []bool) ([]int, bool) {
	or, nr := []rune(string(old)), []rune(string(repl))
	if (len(or)+1)*(len(nr)+1) > 4_000_000 {
		return nil, false
	}
	oOff, nOff := runeOffsets(or), runeOffsets(nr)
	if oOff[len(or)] != len(old) || nOff[len(nr)] != len(repl) {
		return nil, false // invalid UTF-8: rune offsets would not be byte offsets
	}
	first, last := subAlignmentRows(or, nr)
	mapped := make([]int, len(bounds))
	mapped[len(bounds)-1] = len(repl)
	for k := 1; k < len(bounds)-1; k++ {
		byteAt := bounds[k] - bounds[0]
		ri := sort.SearchInts(oOff, byteAt)
		if ri > len(or) || oOff[ri] != byteAt {
			return nil, false
		}
		if rightFixed[k] {
			mapped[k] = nOff[last[ri]]
		} else {
			mapped[k] = nOff[first[ri]]
		}
		if mapped[k] < mapped[k-1] {
			return nil, false
		}
	}
	return mapped, true
}

// runeOffsets returns the byte offset of every rune index of runes, and the total length last.
func runeOffsets(runes []rune) []int {
	offsets := make([]int, len(runes)+1)
	for i, r := range runes {
		offsets[i+1] = offsets[i] + utf8.RuneLen(r)
	}
	return offsets
}

// subAlignmentRows walks a longest common subsequence of a and b; for every index i of a it returns the first and the
// last index of b the walk stands at while at i: b[first[i]:last[i]] is inserted just before a[i].
func subAlignmentRows(a, b []rune) ([]int, []int) {
	n, m := len(a), len(b)
	w := m + 1
	dp := make([]int32, (n+1)*w)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i*w+j] = dp[(i+1)*w+j+1] + 1
			} else {
				dp[i*w+j] = max(dp[(i+1)*w+j], dp[i*w+j+1])
			}
		}
	}
	first, last := make([]int, n+1), make([]int, n+1)
	i, j := 0, 0
	for i < n || j < m {
		last[i] = j
		switch {
		case i < n && j < m && a[i] == b[j] && dp[i*w+j] == dp[(i+1)*w+j+1]+1:
			i, j = i+1, j+1
		case i < n && (j == m || dp[(i+1)*w+j] >= dp[i*w+j+1]):
			i++
		default:
			j++
			continue
		}
		first[i] = j
	}
	last[n] = m
	return first, last
}

func subApplyRawEdits(raws map[string][]byte, edits []subRawEdit) error {
	byPath := map[string][]subRawEdit{}
	for _, e := range edits {
		byPath[e.path] = append(byPath[e.path], e)
	}
	for path, list := range byPath {
		sort.Slice(list, func(i, j int) bool {
			if list[i].start != list[j].start {
				return list[i].start > list[j].start
			}
			return list[i].end > list[j].end
		})
		var kept []subRawEdit
		for _, e := range list {
			if len(kept) > 0 {
				prev := kept[len(kept)-1]
				if prev.start == e.start && prev.end == e.end && bytes.Equal(prev.text, e.text) {
					continue // the same fragment piece printed twice in the file
				}
				if e.end > prev.start || e.start == prev.start {
					return fmt.Errorf("%s: two matches edit the same fragment text differently", path)
				}
			}
			kept = append(kept, e)
		}
		data := append([]byte(nil), raws[path]...)
		for _, e := range kept {
			data = slices.Concat(data[:e.start:e.start], e.text, data[e.end:])
		}
		raws[path] = data
	}
	return nil
}

type subWriteItem struct {
	path    string
	data    []byte
	old     []byte
	existed bool
}

func subWrite(codex, target, selected string, want []byte, orig, raws map[string][]byte, catalog *manifestCatalog) (SubResult, error) {
	var fragPaths []string
	for path, data := range raws {
		if !bytes.Equal(orig[path], data) {
			fragPaths = append(fragPaths, path)
		}
	}
	sort.Strings(fragPaths)
	if len(fragPaths) == 0 {
		return SubResult{}, nil
	}
	changed := map[string]string{} // fragment id → path
	var items []subWriteItem
	for _, path := range fragPaths {
		rel, err := filepath.Rel(filepath.Join(codex, "frag"), path)
		if err != nil {
			return SubResult{}, fmt.Errorf("%s: place fragment in the codex: %w", path, err)
		}
		changed[strings.TrimSuffix(filepath.ToSlash(rel), ".md")] = path
		items = append(items, subWriteItem{path: path, data: raws[path], old: orig[path], existed: true})
	}
	plan := subRebuildPlan{codex: codex, selected: selected, want: want, orig: orig, raws: raws, changed: changed}
	var result SubResult
	var outputs []subWriteItem
	for _, mp := range catalog.paths {
		m, err := readManifest(mp)
		if err != nil {
			return SubResult{}, err
		}
		if !slices.ContainsFunc(m.parts, func(id string) bool { return changed[id] != "" }) {
			continue
		}
		manifestItem, output, err := plan.rebuild(mp, m)
		if err != nil {
			return SubResult{}, err
		}
		items = append(items, manifestItem)
		outputs = append(outputs, output)
		if mp != selected {
			result.Shared = append(result.Shared, output.path)
		}
		result.Changed = append(result.Changed, mp)
	}
	result.Changed = append(result.Changed, fragPaths...)
	sort.Strings(result.Changed)
	sort.Strings(result.Shared)
	if err := subCommit(append(items, outputs...), target, want); err != nil {
		return SubResult{}, err
	}
	return result, nil
}

// subRebuildPlan rebuilds the outputs of manifests that use an edited fragment.
type subRebuildPlan struct {
	codex, selected string
	want            []byte
	orig, raws      map[string][]byte
	changed         map[string]string
}

func (p subRebuildPlan) read(edited bool) func(string) ([]byte, error) {
	files := p.orig
	if edited {
		files = p.raws
	}
	return func(path string) ([]byte, error) {
		if data, ok := files[path]; ok {
			return data, nil
		}
		return os.ReadFile(path)
	}
}

func (p subRebuildPlan) built(mp string, m manifest, edited bool) ([]byte, error) {
	body, err := manifestBodyWithRead(p.codex, m, p.read(edited))
	if err != nil {
		return nil, err
	}
	if m.header {
		body = generatedHeader(mp, body)
	}
	return body, nil
}

// rebuild returns the new manifest and output of mp, a manifest that uses an edited fragment.
func (p subRebuildPlan) rebuild(mp string, m manifest) (subWriteItem, subWriteItem, error) {
	oldBuilt, err := p.built(mp, m, false)
	if err != nil {
		return subWriteItem{}, subWriteItem{}, err
	}
	newBuilt, err := p.built(mp, m, true)
	if err != nil {
		return subWriteItem{}, subWriteItem{}, subRefuse("%s: the edited shared fragment does not build this output: %v", m.out, err)
	}
	if mp == p.selected && !bytes.Equal(newBuilt, p.want) {
		return subWriteItem{}, subWriteItem{}, subRefuse("%s: internal: the rebuilt file differs from the edited file", m.out)
	}
	current, exists, err := p.currentOutput(mp, m, oldBuilt)
	if err != nil {
		return subWriteItem{}, subWriteItem{}, err
	}
	for id, path := range p.changed {
		if slices.Contains(m.parts, id) {
			m.fragmentHashes[id] = sha(p.raws[path])
		}
	}
	m.builtHash = sha(newBuilt)
	manifestOld, err := os.ReadFile(mp)
	if err != nil {
		return subWriteItem{}, subWriteItem{}, fmt.Errorf("%s: %w", mp, err)
	}
	return subWriteItem{path: mp, data: manifestContent(m, m.links), old: manifestOld, existed: true},
		subWriteItem{path: m.out, data: newBuilt, old: current, existed: exists}, nil
}

// currentOutput reads the output of mp; another output of a shared fragment must hold no edits the build would drop.
func (p subRebuildPlan) currentOutput(mp string, m manifest, oldBuilt []byte) ([]byte, bool, error) {
	if info, err := os.Lstat(m.out); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, false, subRefuse("%s: output built from the edited fragment is a symlink; refusing to write through it", m.out)
	}
	current, err := os.ReadFile(m.out)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", m.out, err)
	}
	if mp == p.selected {
		return current, true, nil
	}
	if err := rejectForeignOutput(p.codex, m.out, current); err != nil {
		return nil, false, err
	}
	if !bytes.Equal(current, oldBuilt) && !outputMatchesGenerated(current, oldBuilt, m.header, mp) {
		return nil, false, subRefuse("%s: this output also uses the edited fragment and has edits that are not in the fragments; let them be carried over first (lex absorb) or drop them (lex build --force)", m.out)
	}
	return current, true, nil
}

// subCommit writes items in order; on a failed write, or when target does not end up equal to want, it restores every
// written file.
func subCommit(items []subWriteItem, target string, want []byte) error {
	var written []subWriteItem
	rollback := func(cause error) error {
		problems := []error{cause}
		for i := len(written) - 1; i >= 0; i-- {
			item := written[i]
			var err error
			if item.existed {
				err = os.WriteFile(item.path, item.old, fileMode)
			} else {
				err = os.Remove(item.path)
			}
			if err != nil {
				problems = append(problems, fmt.Errorf("%s: restore: %w", item.path, err))
			}
		}
		return errors.Join(problems...)
	}
	for _, item := range items {
		if err := os.MkdirAll(filepath.Dir(item.path), directoryMode); err != nil {
			return rollback(fmt.Errorf("%s: create directory: %w", filepath.Dir(item.path), err))
		}
		if err := os.WriteFile(item.path, item.data, fileMode); err != nil {
			return rollback(fmt.Errorf("%s: %w", item.path, err))
		}
		written = append(written, item)
	}
	if after, err := os.ReadFile(target); err != nil || !bytes.Equal(after, want) {
		return rollback(subRefuse("%s: after the rebuild the file differs from the edited file; every write was undone", target))
	}
	return nil
}

// SubSpec markers, as the sub tool reads them.
const (
	subMarkSearch  = "<<<<<<< SEARCH"
	subMarkDivider = "======="
	subMarkReplace = ">>>>>>> REPLACE"
)

// ParseSubSpec reads SEARCH/REPLACE blocks that all edit file, in the format of the sub tool: a path line, if
// present, must name file; "<<<<<<< SEARCH N" expects N matches.
func ParseSubSpec(r io.Reader, file string) ([]SubEdit, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read spec: %w", err)
	}
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	p := &subSpecParser{file: file}
	for sc.Scan() {
		p.lineNo++
		if err := p.line(sc.Text()); err != nil {
			return nil, err
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read spec: %w", err)
	}
	if p.section != subOutside {
		return nil, fmt.Errorf("%s: not closed with %q", p.cur.Origin, subMarkReplace)
	}
	if len(p.edits) == 0 {
		return nil, errors.New("spec has no SEARCH/REPLACE blocks")
	}
	return p.edits, nil
}

// Where the spec reader is: outside a block, in its SEARCH part or in its REPLACE part.
const (
	subOutside = iota
	subInSearch
	subInReplace
)

type subSpecParser struct {
	file           string
	edits          []SubEdit
	cur            SubEdit
	oldBuf, newBuf []string
	section        int
	lineNo         int
}

func (p *subSpecParser) line(line string) error {
	mark := strings.TrimSpace(line)
	switch p.section {
	case subInSearch:
		return p.searchLine(line, mark)
	case subInReplace:
		return p.replaceLine(line, mark)
	}
	if mark == "" {
		return nil
	}
	if rest, found := strings.CutPrefix(mark, subMarkSearch); found {
		want := 1
		if rest = strings.TrimSpace(rest); rest != "" {
			n, err := strconv.Atoi(rest)
			if err != nil || n < 1 {
				return fmt.Errorf("line %d: expected match count must be an integer >= 1, got %q", p.lineNo, rest)
			}
			want = n
		}
		p.cur = SubEdit{Want: want, Origin: fmt.Sprintf("block %d (line %d)", len(p.edits)+1, p.lineNo)}
		p.section, p.oldBuf, p.newBuf = subInSearch, nil, nil
		return nil
	}
	if mark == subMarkDivider || mark == subMarkReplace {
		return fmt.Errorf("line %d: %q outside a SEARCH block", p.lineNo, mark)
	}
	if !subSamePath(mark, p.file) {
		return fmt.Errorf("line %d: path %q differs from the FILE argument %q (lex sub edits one file; for other forms use the sub tool)", p.lineNo, mark, p.file)
	}
	return nil
}

func (p *subSpecParser) searchLine(line, mark string) error {
	switch {
	case mark == subMarkDivider:
		p.section = subInReplace
	case mark == subMarkReplace || strings.HasPrefix(mark, subMarkSearch):
		return fmt.Errorf("line %d: %q before %q in %s", p.lineNo, mark, subMarkDivider, p.cur.Origin)
	default:
		p.oldBuf = append(p.oldBuf, line)
	}
	return nil
}

func (p *subSpecParser) replaceLine(line, mark string) error {
	switch {
	case mark == subMarkDivider || strings.HasPrefix(mark, subMarkSearch):
		return fmt.Errorf("line %d: %q inside the REPLACE part of %s", p.lineNo, mark, p.cur.Origin)
	case mark != subMarkReplace:
		p.newBuf = append(p.newBuf, line)
		return nil
	}
	p.cur.Old, p.cur.New = strings.Join(p.oldBuf, "\n"), strings.Join(p.newBuf, "\n")
	if p.cur.Old == "" {
		return fmt.Errorf("%s: empty SEARCH text", p.cur.Origin)
	}
	p.edits = append(p.edits, p.cur)
	p.section = subOutside
	return nil
}

func subSamePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	return errA == nil && errB == nil && absA == absB
}
