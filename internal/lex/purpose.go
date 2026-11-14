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

func (p purposeLayout) span(id string) purposeSpan {
	for _, span := range p.spans {
		if span.id == id {
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
	if err := p.checkMovedGoals(oldLines, matches, newLines, newToOld); err != nil {
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

func (p purposeLayout) checkMovedGoals(oldLines [][]byte, matches [][2]int, newLines [][]byte, newToOld map[int]int) error {
	for _, pair := range matches {
		id, isGoal := p.owners[pair[0]]
		if !isGoal {
			continue
		}
		previous := pair[1] - 1
		for previous >= 0 && strings.TrimSpace(string(newLines[previous])) == "" {
			previous--
		}
		if oldIndex, ok := newToOld[previous]; ok {
			span := p.span(id)
			if oldIndex < span.start || oldIndex >= span.end {
				return fmt.Errorf("%s: цель moved to another fragment; put the line %q back under its rule", id, strings.TrimRight(string(oldLines[pair[0]]), "\r\n"))
			}
		}
	}
	return nil
}

func (p purposeLayout) checkRemovedGoals(oldLines, newLines [][]byte, matchedOld map[int]bool) error {
	hunks := splitPurposeHunks(p.spans, p.owners, oldLines, newLines, changedLineHunks(oldLines, newLines))
	failed, failedHunk := []int{}, map[int]int{}
	for index := range oldLines {
		id, isGoal := p.owners[index]
		if !isGoal || matchedOld[index] {
			continue
		}
		span := p.span(id)
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
	first := failed[0]
	id := p.owners[first]
	quoted := []string{fmt.Sprintf("%q", strings.TrimRight(string(oldLines[first]), "\r\n"))}
	for _, index := range failed[1:] {
		if failedHunk[index] == failedHunk[first] {
			quoted = append(quoted, fmt.Sprintf("%q", strings.TrimRight(string(oldLines[index]), "\r\n")))
		}
	}
	apart := ""
	if hi := failedHunk[first]; hi >= 0 && purposeSpanCovered(p.owners, oldLines, p.span(id), hunks[hi].oldStart, hunks[hi].oldEnd) {
		apart = "; if the rule was deleted on purpose, delete it in one pass and make the edit next to it in another"
	}
	if len(quoted) == 1 {
		return fmt.Errorf("%s: missing or edited цель; put the line %s back under its rule or delete the rule together with it%s", id, quoted[0], apart)
	}
	return fmt.Errorf("%s: missing or edited цель; put each of the lines %s back under its rule or delete that rule together with it%s", id, strings.Join(quoted, ", "), apart)
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
