package lex

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const purposeMarker = "  Зачем: "

func readPurposeSetting(codex string, read func(string) ([]byte, error)) (bool, error) {
	path := filepath.Join(codex, "purpose.json")
	data, err := read(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	var setting struct {
		Print bool `json:"print"`
	}
	if err := json.Unmarshal(data, &setting); err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	return setting.Print, nil
}

func purposeValue(raw []byte) string {
	values := fragmentHeaderValues(raw)["цель"]
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}

func renderPurpose(path string, raw, body []byte, enabled bool) ([]byte, string, error) {
	if !enabled {
		return body, "", nil
	}
	for line := range bytes.SplitSeq(body, []byte("\n")) {
		if strings.HasPrefix(string(line), purposeMarker) {
			return nil, "", fmt.Errorf("%s: body contains a purpose line", path)
		}
	}
	purpose := purposeValue(raw)
	if purpose == "" {
		return body, "", nil
	}
	first := ""
	for line := range bytes.SplitSeq(body, []byte("\n")) {
		if strings.TrimSpace(string(line)) != "" {
			first = string(line)
			break
		}
	}
	_, _, _, fence := opensFence(first)
	if first == "" || heading(first) || fence || frontMatterLength(bytes.SplitAfter(body, []byte("\n"))) > 0 {
		return body, path + ": purpose is not printed for this fragment", nil
	}
	return insertPurposeLine(body, purpose), "", nil
}

func insertPurposeLine(body []byte, purpose string) []byte {
	end := len(body)
	for end > 0 && (body[end-1] == '\n' || body[end-1] == '\r' || body[end-1] == ' ' || body[end-1] == '\t') {
		end--
	}
	lineEnd := end
	for lineEnd < len(body) && body[lineEnd] != '\n' {
		lineEnd++
	}
	if lineEnd < len(body) {
		lineEnd++
	}
	result := append([]byte(nil), body[:lineEnd]...)
	if len(result) > 0 && result[len(result)-1] != '\n' {
		result = append(result, '\n')
	}
	result = append(result, []byte(purposeMarker+purpose+"\n")...)
	result = append(result, body[lineEnd:]...)
	return result
}

func diskPurposeSetting(codex string) (bool, error) { return readPurposeSetting(codex, os.ReadFile) }

func prepareRefreshPurpose(codex string, m manifest, data []byte) (bool, []byte, error) {
	enabled, err := diskPurposeSetting(codex)
	if err != nil {
		return false, nil, err
	}
	if !enabled && !bytes.Contains(data, []byte(purposeMarker)) {
		return false, data, nil
	}
	_, clean, err := stripPurposeForAbsorb(codex, m, data, enabled)
	return enabled, clean, err
}

func stagedHasPurpose(commit *stagedPaths, codex string, m manifest) (bool, error) {
	for _, id := range m.parts {
		path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
		raw, err := commit.read(path)
		if err != nil {
			return false, err
		}
		part, err := fragmentBody(raw)
		if err != nil {
			return false, fmt.Errorf("%s: %w", path, err)
		}
		rendered, _, err := renderPurpose(path, raw, part, true)
		if err != nil {
			return false, err
		}
		if !bytes.Equal(rendered, part) {
			return true, nil
		}
	}
	return false, nil
}

// stripPurposeForAbsorb removes derived lines only after proving that each
// retained one still has its original value. A removed rule may lose its line.
func stripPurposeForAbsorb(codex string, m manifest, actual []byte, enabled bool) ([]byte, []byte, error) {
	if !enabled {
		if err := rejectStalePurposes(codex, m, actual); err != nil {
			return nil, nil, err
		}
		plain, err := manifestBody(codex, m)
		return plain, actual, err
	}
	layout, err := readPurposeLayout(codex, m)
	if err != nil {
		return nil, nil, err
	}
	clean, err := layout.strip(actual, m.out)
	if err != nil {
		return nil, nil, err
	}
	return layout.plain, clean, nil
}

type purposeSpan struct {
	id         string
	start, end int
}
type purposeLayout struct {
	rendered, plain []byte
	owners          map[int]string
	spans           []purposeSpan
}

func rejectStalePurposes(codex string, m manifest, actual []byte) error {
	for _, id := range m.parts {
		path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		goal := purposeValue(raw)
		if goal == "" {
			continue
		}
		for _, line := range absorbLines(actual) {
			if strings.TrimRight(string(line), "\r\n") == purposeMarker+goal {
				return fmt.Errorf("%s: purpose line remains after disabling printing; run lex build", id)
			}
		}
	}
	return nil
}

func readPurposeLayout(codex string, m manifest) (purposeLayout, error) {
	return readPurposeLayoutWithRead(codex, m, os.ReadFile)
}

func readPurposeLayoutWithRead(codex string, m manifest, read func(string) ([]byte, error)) (purposeLayout, error) {
	layout := purposeLayout{owners: map[int]string{}}
	line := 0
	for _, id := range m.parts {
		path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
		raw, err := read(path)
		if err != nil {
			return layout, fmt.Errorf("%s: %w", path, err)
		}
		body, err := fragmentBody(raw)
		if err != nil {
			return layout, fmt.Errorf("%s: %w", path, err)
		}
		part, _, err := renderPurpose(path, raw, body, true)
		if err != nil {
			return layout, err
		}
		start := line
		for _, l := range absorbLines(part) {
			if strings.TrimRight(string(l), "\r\n") == purposeMarker+purposeValue(raw) && purposeValue(raw) != "" {
				layout.owners[line] = id
			}
			line++
		}
		layout.spans = append(layout.spans, purposeSpan{id, start, line})
		layout.rendered = append(layout.rendered, part...)
		layout.plain = append(layout.plain, body...)
	}
	return layout, nil
}

func (p purposeLayout) spanAt(line int) purposeSpan {
	for _, span := range p.spans {
		if span.start <= line && line < span.end {
			return span
		}
	}
	return purposeSpan{}
}

func (p purposeLayout) strip(actual []byte, output string) ([]byte, error) {
	oldLines, newLines := absorbLines(p.rendered), absorbLines(actual)
	matches := myersLineMatches(oldLines, newLines)
	matchedOld, matchedNew := map[int]bool{}, map[int]bool{}
	newToOld := map[int]int{}
	for _, pair := range matches {
		matchedOld[pair[0]] = true
		matchedNew[pair[1]] = true
		newToOld[pair[1]] = pair[0]
	}
	if err, _ := p.checkMovedGoals(oldLines, matches, newLines, newToOld, nil); err != nil {
		return nil, err
	}
	if err := p.checkRemovedGoals(oldLines, newLines, matchedOld); err != nil {
		return nil, err
	}
	var clean []byte
	for i, l := range newLines {
		if strings.HasPrefix(string(l), purposeMarker) {
			if !matchedNew[i] {
				return nil, fmt.Errorf("%s: unexpected or edited цель; delete the line %q or put its original text back", p.ownerBefore(matches, i, output), strings.TrimRight(string(l), "\r\n"))
			}
			continue
		}
		clean = append(clean, l...)
	}
	return clean, nil
}

func (p purposeLayout) checkMovedGoals(oldLines [][]byte, matches [][2]int, newLines [][]byte, newToOld map[int]int, only map[int]bool) (error, bool) {
	for _, pair := range matches {
		id, isGoal := p.owners[pair[0]]
		if !isGoal || (only != nil && !only[pair[0]]) {
			continue
		}
		previous := pair[1] - 1
		for previous >= 0 && strings.TrimSpace(string(newLines[previous])) == "" {
			previous--
		}
		span := p.spanAt(pair[0])
		oldIndex, ok := newToOld[previous]
		if previous < 0 || (ok && (oldIndex < span.start || oldIndex >= span.end)) {
			if !p.ruleTextPresent(span, oldLines, newLines, matches) {
				return fmt.Errorf("%s: цель line %q has no rule text; delete the line or restore its rule", id, strings.TrimRight(string(oldLines[pair[0]]), "\r\n")), false
			}
			return fmt.Errorf("%s: цель moved to another fragment; put the line %q back under its rule", id, strings.TrimRight(string(oldLines[pair[0]]), "\r\n")), true
		}
	}
	return nil, false
}

func (p purposeLayout) ruleTextPresent(span purposeSpan, oldLines, newLines [][]byte, matches [][2]int) bool {
	if p.existingRuleText(span, oldLines, newLines, matches) {
		return true
	}
	matchedNew := make(map[int]bool)
	beforeOld, beforeNew := -1, -1
	afterOld, afterNew := len(oldLines), len(newLines)
	for _, pair := range matches {
		matchedNew[pair[1]] = true
		if pair[0] < span.start && pair[0] > beforeOld {
			beforeOld, beforeNew = pair[0], pair[1]
		}
		if pair[0] >= span.end && pair[0] < afterOld {
			afterOld, afterNew = pair[0], pair[1]
		}
	}
	start, end := beforeNew+1, afterNew
	if p.editedRuleInWindow(span, oldLines, newLines, matchedNew, start, end) {
		return true
	}
	for _, pair := range matches {
		if _, isGoal := p.owners[pair[0]]; isGoal && p.spanAt(pair[0]) == span && pair[1] >= start && pair[1] < end {
			end = pair[1]
			break
		}
	}
	if start > end {
		start, end = end, start
	}
	for index := start; index < end; index++ {
		if !matchedNew[index] && strings.TrimSpace(string(newLines[index])) != "" && !strings.HasPrefix(string(newLines[index]), purposeMarker) {
			return true
		}
	}
	return false
}

func (p purposeLayout) existingRuleText(span purposeSpan, oldLines, newLines [][]byte, matches [][2]int) bool {
	for index := span.start; index < span.end; index++ {
		if !p.isRuleText(index, oldLines) {
			continue
		}
		if purposeLineMatched(index, matches) || ruleLinePresent(oldLines[index], oldLines, newLines) {
			return true
		}
	}
	return false
}

// A changed rule can remain after its goal was moved above it. Only unmatched
// lines within this rule's anchors can represent that edit.
func (p purposeLayout) editedRuleInWindow(span purposeSpan, oldLines, newLines [][]byte, matchedNew map[int]bool, start, end int) bool {
	for index := start; index < end; index++ {
		if matchedNew[index] {
			continue
		}
		text := bytes.TrimSpace(newLines[index])
		for oldIndex := span.start; oldIndex < span.end; oldIndex++ {
			if p.isRuleText(oldIndex, oldLines) && bytes.HasPrefix(text, bytes.TrimSpace(oldLines[oldIndex])) {
				return true
			}
		}
	}
	return false
}

func (p purposeLayout) isRuleText(index int, lines [][]byte) bool {
	_, isGoal := p.owners[index]
	return !isGoal && strings.TrimSpace(string(lines[index])) != ""
}

func purposeLineMatched(index int, matches [][2]int) bool {
	for _, pair := range matches {
		if pair[0] == index {
			return true
		}
	}
	return false
}

func ruleLinePresent(line []byte, oldLines, newLines [][]byte) bool {
	oldText := bytes.TrimSpace(line)
	oldCount, newCount := 0, 0
	for _, line := range oldLines {
		if bytes.Equal(bytes.TrimSpace(line), oldText) {
			oldCount++
		}
	}
	for _, line := range newLines {
		if bytes.Equal(bytes.TrimSpace(line), oldText) {
			newCount++
		}
	}
	if newCount >= oldCount {
		return true
	}
	return false
}

func (p purposeLayout) checkRemovedGoals(oldLines, newLines [][]byte, matchedOld map[int]bool) error {
	hunks := splitPurposeHunks(p.spans, p.owners, oldLines, newLines, changedLineHunks(oldLines, newLines))
	failed, failedHunk := []int{}, map[int]int{}
	for index := range oldLines {
		_, isGoal := p.owners[index]
		if !isGoal || matchedOld[index] {
			continue
		}
		span := p.spanAt(index)
		removed := true
		for i := span.start; i < span.end; i++ {
			if matchedOld[i] && strings.TrimSpace(string(oldLines[i])) != "" {
				removed = false
			}
		}
		hunkIndex := -1
		for hi, h := range hunks {
			if h.oldStart <= index && index < h.oldEnd && h.newStart != h.newEnd {
				removed = false
				hunkIndex = hi
			}
		}
		if !removed {
			failed = append(failed, index)
			failedHunk[index] = hunkIndex
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return p.refuseRemovedGoals(oldLines, newLines, failed, failedHunk, hunks)
}

func (p purposeLayout) refuseRemovedGoals(oldLines, newLines [][]byte, failed []int, failedHunk map[int]int, hunks []lineHunk) error {
	if p.missingFinalPurposeNewline(oldLines, newLines, failed) {
		return fmt.Errorf("%s: missing final newline; restore the newline at the end of the file", p.owners[failed[0]])
	}
	if hasCRLFPurposeCounterpart(oldLines, newLines, failed) {
		normalized := make([][]byte, len(newLines))
		for i, line := range newLines {
			normalized[i] = bytes.ReplaceAll(line, []byte("\r\n"), []byte("\n"))
		}
		matches := myersLineMatches(oldLines, normalized)
		for _, index := range failed {
			for i, line := range normalized {
				if !bytes.Equal(line, oldLines[index]) || bytes.Equal(newLines[i], line) {
					continue
				}
				candidate, newToOld := purposeMatchesWithCandidate(matches, index, i)
				if err, moved := p.checkMovedGoals(oldLines, candidate, normalized, newToOld, map[int]bool{index: true}); moved {
					return fmt.Errorf("purpose line has CRLF line endings; %w", err)
				}
			}
		}
		return fmt.Errorf("%s: purpose lines did not match because of CRLF line endings; preserve the file's line endings", p.owners[failed[0]])
	}
	first := failed[0]
	id := p.owners[first]
	quoted := []string{fmt.Sprintf("%q", strings.TrimRight(string(oldLines[first]), "\r\n"))}
	for _, index := range failed[1:] {
		if failedHunk[index] == failedHunk[first] {
			quoted = append(quoted, fmt.Sprintf("%q", strings.TrimRight(string(oldLines[index]), "\r\n")))
		}
	}
	apart := ""
	if hi := failedHunk[first]; hi >= 0 && purposeSpanCovered(p.owners, oldLines, p.spanAt(first), hunks[hi].oldStart, hunks[hi].oldEnd) {
		apart = "; if the rule was deleted on purpose, delete it in one pass and make the edit next to it in another"
	}
	if len(quoted) == 1 {
		return fmt.Errorf("%s: missing or edited цель; put the line %s back under its rule or delete the rule together with it%s", id, quoted[0], apart)
	}
	return fmt.Errorf("%s: missing or edited цель; put each of the lines %s back under its rule or delete that rule together with it%s", id, strings.Join(quoted, ", "), apart)
}

func purposeMatchesWithCandidate(matches [][2]int, oldIndex, newIndex int) ([][2]int, map[int]int) {
	candidate := make([][2]int, 0, len(matches)+1)
	newToOld := make(map[int]int, len(matches)+1)
	for _, pair := range matches {
		if pair[0] != oldIndex && pair[1] != newIndex {
			candidate = append(candidate, pair)
			newToOld[pair[1]] = pair[0]
		}
	}
	candidate = append(candidate, [2]int{oldIndex, newIndex})
	newToOld[newIndex] = oldIndex
	return candidate, newToOld
}

func (p purposeLayout) missingFinalPurposeNewline(oldLines, newLines [][]byte, failed []int) bool {
	if len(failed) != 1 || len(newLines) == 0 || len(newLines[len(newLines)-1]) == 0 || bytes.HasSuffix(newLines[len(newLines)-1], []byte("\n")) {
		return false
	}
	withoutNewline := bytes.TrimSuffix(oldLines[failed[0]], []byte("\n"))
	return bytes.Equal(newLines[len(newLines)-1], withoutNewline)
}

func hasCRLFPurposeCounterpart(oldLines, newLines [][]byte, failed []int) bool {
	for _, index := range failed {
		if !bytes.HasSuffix(oldLines[index], []byte("\n")) || bytes.HasSuffix(oldLines[index], []byte("\r\n")) {
			continue
		}
		withCRLF := append(append([]byte(nil), bytes.TrimSuffix(oldLines[index], []byte("\n"))...), '\r', '\n')
		found := false
		for _, line := range newLines {
			if bytes.Equal(line, withCRLF) {
				found = true
				break
			}
		}
		if !found {
			return allLinesCRLF(newLines)
		}
	}
	return len(failed) > 0
}

func allLinesCRLF(lines [][]byte) bool {
	seen := false
	for _, line := range lines {
		if bytes.HasSuffix(line, []byte("\r\n")) {
			seen = true
			continue
		}
		if bytes.HasSuffix(line, []byte("\n")) {
			return false
		}
	}
	return seen
}

func (p purposeLayout) ownerBefore(matches [][2]int, line int, fallback string) string {
	owner := fallback
	for _, pair := range matches {
		if pair[1] >= line {
			break
		}
		for _, span := range p.spans {
			if span.start <= pair[0] && pair[0] < span.end {
				owner = span.id
				break
			}
		}
	}
	return owner
}

func refreshPurposeUnits(data []byte, headers []preservedFragmentHeader) ([][]byte, error) {
	units := split(data)
	used := make([]bool, len(headers))
	for i, unit := range units {
		var clean []byte
		var goal string
		for _, line := range absorbLines(unit) {
			if strings.HasPrefix(string(line), purposeMarker) {
				if goal != "" {
					return nil, fmt.Errorf("multiple цель lines; run lex build")
				}
				goal = strings.TrimSpace(strings.TrimPrefix(string(line), purposeMarker))
				continue
			}
			clean = append(clean, line...)
		}
		units[i] = clean
		if goal == "" {
			continue
		}
		found := -1
		matches := 0
		for j, old := range headers {
			if !used[j] && purposeValue(old.header) == goal {
				found = j
				matches++
				if old.bodyHash == sha(clean) {
					matches = 1
					break
				}
			}
		}
		if found < 0 {
			return nil, fmt.Errorf("unrecognized цель %q; run lex build", goal)
		}
		if matches > 1 {
			return nil, fmt.Errorf("ambiguous цель %q; run lex build", goal)
		}
		used[found] = true
		headers[found].bodyHash = sha(clean)
	}
	return units, nil
}
