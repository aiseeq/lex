package lex

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

type QuotesToBasisOptions struct {
	Apply        bool
	Report       string
	OutputsUnder string
	Tree         string
}

type quoteSkip struct {
	Fragment string `json:"fragment"`
	Text     string `json:"text"`
}

type quoteOutput struct {
	Output      string `json:"output"`
	BytesBefore int    `json:"bytes_before"`
	BytesAfter  int    `json:"bytes_after"`
	Reason      string `json:"reason,omitempty"`
}

type quotesReport struct {
	FragmentsMoved int           `json:"fragments_moved"`
	Outputs        []quoteOutput `json:"outputs"`
	Untouched      []quoteSkip   `json:"untouched_parentheses"`
}

var exactUserQuote = regexp.MustCompile(`^пользователь [0-9]{2}\.[0-9]{2}(?:: «.*»)?$`)
var datedUserReference = regexp.MustCompile(`(?:пользователь (?:[0-9]{2}\.[0-9]{2}|[0-9]{4}-[0-9]{2}-[0-9]{2})(?: [0-9]{2}:[0-9]{2}(?::[0-9]{2})?(?: ?[A-Z]{1,5})?)?|(?:слова|уточнение|ответ|реплика|решение|срок|шаблон|выбор|заказ) пользователя\s*\(?\s*(?:[0-9]{2}\.[0-9]{2}|[0-9]{4}-[0-9]{2}-[0-9]{2})|правило решения\s*[-–—:]\s*пользователь (?:[0-9]{2}\.[0-9]{2}|[0-9]{4}-[0-9]{2}-[0-9]{2}))`)
var userMention = regexp.MustCompile(`(?:пользователь|пользователя|пользователю|пользователи|пользователей|пользователям|пользователях|пользователями)(?:[^а-яё]|$)`)
var userReferenceDate = regexp.MustCompile(`[0-9]{2}\.[0-9]{2}|[0-9]{4}-[0-9]{2}-[0-9]{2}`)
var openingQuote = regexp.MustCompile(`«`)
var undatedColleagueQuote = regexp.MustCompile(`пользователь\s+(?:ей|ему)\s*:\s*«`)
var unquotedWordPointer = regexp.MustCompile(`слово пользователя`)

type quoteMove struct {
	fragmentPath string
	fragmentID   string
	updated      []byte
}

type manualOutputError struct {
	output string
}

func (e manualOutputError) Error() string {
	return fmt.Sprintf("%s: output differs from manifest built_sha256; skipping transfer", e.output)
}

func QuotesToBasis(codex string, options QuotesToBasisOptions) error {
	codex, err := canonicalCodex(codex, false)
	if err != nil {
		return fmt.Errorf("resolve codex: %w", err)
	}
	selected, tree, err := selectQuoteManifests(codex, options)
	if err != nil {
		return err
	}
	if options.Apply && tree == "" {
		for _, entry := range selected {
			if !entry.output {
				continue
			}
			actual, readErr := os.ReadFile(entry.m.out)
			if readErr != nil {
				return fmt.Errorf("%s: read output: %w", entry.m.out, readErr)
			}
			if err := rejectForeignOutput(codex, entry.m.out, actual); err != nil {
				return err
			}
		}
	}
	report := quotesReport{}
	seen := map[string]bool{}
	var applyErrors []error
	for _, entry := range selected {
		if !entry.output {
			continue
		}
		output, moves, skips, e := processQuoteManifest(codex, entry, tree, options.Apply)
		if e != nil {
			var manualErr manualOutputError
			if !errors.As(e, &manualErr) {
				return e
			}
			report.Outputs = append(report.Outputs, output)
			report.Untouched = append(report.Untouched, skips...)
			applyErrors = append(applyErrors, e)
			continue
		}
		report.Outputs = append(report.Outputs, output)
		report.Untouched = append(report.Untouched, skips...)
		for _, move := range moves {
			if !seen[move.fragmentPath] {
				report.FragmentsMoved++
				seen[move.fragmentPath] = true
			}
		}
	}
	if err := writeQuotesReport(options.Report, report); err != nil {
		return errors.Join(err, errors.Join(applyErrors...))
	}
	return errors.Join(applyErrors...)
}

func selectQuoteManifests(codex string, options QuotesToBasisOptions) ([]*selectedManifestUnder, string, error) {
	paths, err := filepath.Glob(filepath.Join(codex, "manifest", "*.yaml"))
	if err != nil {
		return nil, "", fmt.Errorf("list manifests: %w", err)
	}
	sort.Strings(paths)
	if options.OutputsUnder == "" {
		selected := make([]*selectedManifestUnder, 0, len(paths))
		for _, path := range paths {
			m, readErr := readManifest(path)
			if readErr != nil {
				return nil, "", fmt.Errorf("%s: %w", path, readErr)
			}
			selected = append(selected, &selectedManifestUnder{path: path, m: m, output: true})
		}
		return selected, "", nil
	}
	abs, err := filepath.Abs(options.OutputsUnder)
	if err != nil {
		return nil, "", fmt.Errorf("resolve output scope: %w", err)
	}
	under := resolvePath(abs)
	tree, err := resolveOptionalTree(options.Tree)
	if err != nil {
		return nil, "", err
	}
	selected, _, problems, err := selectManifestsUnder(paths, filepath.Clean(abs), under, tree)
	if err != nil {
		return nil, "", err
	}
	if len(problems) > 0 {
		return nil, "", fmt.Errorf("select outputs: %s", strings.Join(problems, "; "))
	}
	info, statErr := os.Stat(abs)
	if statErr != nil && !os.IsNotExist(statErr) {
		return nil, "", fmt.Errorf("inspect output scope %s: %w", abs, statErr)
	}
	if statErr == nil && !info.IsDir() {
		filtered := selected[:0]
		for _, entry := range selected {
			out, absErr := filepath.Abs(entry.m.out)
			if absErr != nil {
				return nil, "", fmt.Errorf("resolve output %s: %w", entry.m.out, absErr)
			}
			if resolvePath(out) == under {
				entry.rel = filepath.Base(under)
				filtered = append(filtered, entry)
			}
		}
		selected = filtered
	}
	return selected, tree, nil
}

func processQuoteManifest(codex string, entry *selectedManifestUnder, tree string, apply bool) (quoteOutput, []quoteMove, []quoteSkip, error) {
	m := entry.m
	purposeEnabled, err := diskPurposeSetting(codex)
	if err != nil {
		return quoteOutput{}, nil, nil, err
	}
	moves := make([]quoteMove, 0, len(m.parts))
	bodies := make(map[string][]byte, len(m.parts))
	var skips []quoteSkip
	for _, id := range m.parts {
		path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
		raw, err := os.ReadFile(path)
		if err != nil {
			return quoteOutput{}, nil, skips, fmt.Errorf("%s: %w", path, err)
		}
		body, err := fragmentBody(raw)
		if err != nil {
			return quoteOutput{}, nil, skips, fmt.Errorf("%s: %w", path, err)
		}
		updated, newBody, moved, foundSkips, err := transferUserQuotes(raw, body, id)
		if err != nil {
			return quoteOutput{}, nil, skips, fmt.Errorf("%s: %w", path, err)
		}
		skips = append(skips, foundSkips...)
		rendered, _, err := renderPurpose(path, updated, newBody, purposeEnabled)
		if err != nil {
			return quoteOutput{}, nil, skips, err
		}
		bodies[id] = rendered
		if moved > 0 {
			moves = append(moves, quoteMove{fragmentPath: path, fragmentID: id, updated: updated})
		}
	}
	var after bytes.Buffer
	for _, id := range m.parts {
		after.Write(bodies[id])
	}
	built := after.Bytes()
	if m.header {
		built = generatedHeader(entry.path, built)
	}
	out := m.out
	if tree != "" {
		out = outputInTree(tree, entry.rel, m.out)
	}
	before, err := os.ReadFile(out)
	if err != nil {
		return quoteOutput{}, nil, skips, fmt.Errorf("%s: read output: %w", out, err)
	}
	result := quoteOutput{Output: out, BytesBefore: len(before), BytesAfter: len(built)}
	if apply && len(moves) > 0 {
		if sha(before) != m.builtHash {
			result.Reason = "output differs from manifest built_sha256; fragments, manifest and output left unchanged"
			return result, nil, skips, manualOutputError{output: out}
		}
		if err := applyQuoteMoves(entry, moves, built, out); err != nil {
			return quoteOutput{}, nil, skips, err
		}
	}
	return result, moves, skips, nil
}

func applyQuoteMoves(entry *selectedManifestUnder, moves []quoteMove, built []byte, output string) error {
	m := entry.m
	for _, move := range moves {
		if err := os.WriteFile(move.fragmentPath, move.updated, fileMode); err != nil {
			return fmt.Errorf("%s: write fragment: %w", move.fragmentPath, err)
		}
		m.fragmentHashes[move.fragmentID] = sha(move.updated)
	}
	m.builtHash = sha(built)
	if err := writeManifest(entry.path, m, m.links); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(output), directoryMode); err != nil {
		return fmt.Errorf("%s: create output directory: %w", output, err)
	}
	if err := os.WriteFile(output, built, fileMode); err != nil {
		return fmt.Errorf("%s: write output: %w", output, err)
	}
	return nil
}

func writeQuotesReport(path string, report quotesReport) error {
	sort.Slice(report.Untouched, func(i, j int) bool {
		if report.Untouched[i].Fragment == report.Untouched[j].Fragment {
			return report.Untouched[i].Text < report.Untouched[j].Text
		}
		return report.Untouched[i].Fragment < report.Untouched[j].Fragment
	})
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), directoryMode); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	if err := os.WriteFile(path, data, fileMode); err != nil {
		return fmt.Errorf("%s: write report: %w", path, err)
	}
	return nil
}

func transferUserQuotes(raw, body []byte, id string) ([]byte, []byte, int, []quoteSkip, error) {
	text := string(body)
	var replacements []string
	var untouched []quoteSkip
	remove := make([][2]int, 0)
	for _, match := range userQuoteParentheses(body) {
		if !match.containsUser {
			continue
		}
		if match.exact {
			replacements = append(replacements, text[match.start:match.end])
			start, stop := quoteRemovalBounds(text, match.start, match.end)
			remove = append(remove, [2]int{start, stop})
		} else {
			untouched = append(untouched, quoteSkip{Fragment: id, Text: text[match.start:match.end]})
		}
	}
	if len(replacements) == 0 {
		return raw, body, 0, untouched, nil
	}
	remove = mergeQuoteRanges(text, remove)
	for n := len(remove) - 1; n >= 0; n-- {
		bounds := remove[n]
		text = text[:bounds[0]] + text[bounds[1]:]
	}
	headerBody := raw[:len(raw)-len(body)]
	updated := setBasis(append(headerBody, []byte(text)...), strings.Join(replacements, "; "))
	newBody, err := fragmentBody(updated)
	if err != nil {
		return nil, nil, 0, untouched, err
	}
	return updated, newBody, len(replacements), untouched, nil
}

func hasUserQuoteInText(body []byte) bool {
	text := string(body)
	return datedUserReference.MatchString(text) || (hasDatedUserQuote(text) && !unquotedWordPointer.MatchString(text)) || undatedColleagueQuote.MatchString(text)
}

func hasDatedUserQuote(text string) bool {
	users := userMention.FindAllStringIndex(text, -1)
	dates := userReferenceDate.FindAllStringIndex(text, -1)
	quotes := openingQuote.FindAllStringIndex(text, -1)
	// Keep the date and quote tied to the same short user reference, counting Unicode characters.
	for _, user := range users {
		for _, date := range dates {
			if user[0] > date[0] || utf8.RuneCountInString(text[user[0]:date[0]]) > 110 {
				continue
			}
			for _, quote := range quotes {
				if quote[0] >= date[0] && utf8.RuneCountInString(text[user[0]:quote[0]]) <= 110 {
					return true
				}
			}
		}
	}
	return false
}

type userQuoteParenthesis struct {
	start, end   int
	containsUser bool
	exact        bool
}

func userQuoteParentheses(body []byte) []userQuoteParenthesis {
	text := string(body)
	var found []userQuoteParenthesis
	for i := 0; i < len(text); i++ {
		if text[i] != '(' {
			continue
		}
		depth, end := 1, i+1
		for ; end < len(text) && depth > 0; end++ {
			switch text[end] {
			case '(':
				depth++
			case ')':
				depth--
			}
		}
		if depth != 0 {
			continue
		}
		candidate := text[i+1 : end-1]
		found = append(found, userQuoteParenthesis{
			start: i, end: end, containsUser: strings.Contains(candidate, "пользователь"),
			exact: exactUserQuote.MatchString(candidate),
		})
		i = end - 1
	}
	return found
}

func mergeQuoteRanges(text string, ranges [][2]int) [][2]int {
	merged := make([][2]int, 0, len(ranges))
	for _, span := range ranges {
		last := len(merged) - 1
		if last >= 0 && span[0] <= merged[last][1] {
			if span[1] > merged[last][1] {
				merged[last][1] = span[1]
			}
			continue
		}
		merged = append(merged, span)
	}
	for i := range merged {
		lineStart := strings.LastIndex(text[:merged[i][0]], "\n") + 1
		if strings.TrimSpace(text[lineStart:merged[i][0]]) == "" {
			for merged[i][1] < len(text) && (text[merged[i][1]] == ' ' || text[merged[i][1]] == '\t') {
				merged[i][1]++
			}
		}
	}
	return merged
}

func quoteRemovalBounds(text string, start, end int) (int, int) {
	lineStart := strings.LastIndex(text[:start], "\n") + 1
	if strings.TrimSpace(text[lineStart:start]) == "" {
		for end < len(text) && (text[end] == ' ' || text[end] == '\t') {
			end++
		}
		return start, end
	}
	for start > lineStart && (text[start-1] == ' ' || text[start-1] == '\t') {
		start--
	}
	return start, end
}

func setBasis(raw []byte, added string) []byte {
	lines := bytes.SplitAfter(raw, []byte("\n"))
	for i, line := range lines {
		if after, ok := bytes.CutPrefix(line, []byte("основание: ")); ok {
			value := bytes.TrimSuffix(after, []byte("\n"))
			if len(value) != 0 {
				added = string(value) + "; " + added
			}
			lines[i] = []byte("основание: " + added + "\n")
			break
		}
	}
	return bytes.Join(lines, nil)
}
