package lex

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

type codeLink struct {
	Target string `json:"target"`
	Hash   string `json:"hash"`
}

type lostCodeLink struct {
	Fragment string `json:"fragment"`
	Target   string `json:"target"`
}

func fragmentIsRecord(raw []byte) bool {
	_, err := fragmentBody(raw)
	return err == nil && bytes.Contains(fragmentHeader(raw), []byte("\nзапись: true\n"))
}

// fragmentHeaderValues reads fields from the fragment header.
// Repeated fields are kept in order, as code links may occur more than once.
func fragmentHeaderValues(raw []byte) map[string][]string {
	values := make(map[string][]string)
	for line := range strings.SplitSeq(string(fragmentHeader(raw)), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok {
			values[key] = append(values[key], strings.TrimSpace(value))
		}
	}
	return values
}

func codeLinks(raw []byte) ([]codeLink, string) {
	var links []codeLink
	var bodyHash string
	for line := range strings.SplitSeq(string(fragmentHeader(raw)), "\n") {
		switch {
		case strings.HasPrefix(line, "код: "):
			links = append(links, codeLink{Target: strings.TrimSpace(strings.TrimPrefix(line, "код: "))})
		case strings.HasPrefix(line, "код_sha256: ") && len(links) > 0:
			links[len(links)-1].Hash = strings.TrimSpace(strings.TrimPrefix(line, "код_sha256: "))
		case strings.HasPrefix(line, "текст_sha256: "):
			bodyHash = strings.TrimSpace(strings.TrimPrefix(line, "текст_sha256: "))
		}
	}
	return links, bodyHash
}

// A card is an absolute Markdown path. A test is repository:package:TestName;
// its fingerprint is the source of the test function itself.
func codeTargetHash(target string) (string, error) {
	if filepath.IsAbs(target) && strings.HasSuffix(target, ".md") {
		b, err := os.ReadFile(target)
		if err != nil {
			return "", fmt.Errorf("read card %s: %w", target, err)
		}
		return sha(cardWithoutStamps(b)), nil
	}
	parts := strings.Split(target, ":")
	if len(parts) != 3 || !filepath.IsAbs(parts[0]) || parts[1] == "" || !strings.HasPrefix(parts[2], "Test") {
		return "", fmt.Errorf("invalid code target %q", target)
	}
	dir := filepath.Join(parts[0], parts[1])
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return "", fmt.Errorf("list tests in %s: %w", dir, err)
	}
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read test %s: %w", file, err)
		}
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, file, b, 0)
		if err != nil {
			return "", fmt.Errorf("parse test %s: %w", file, err)
		}
		for _, decl := range parsed.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == parts[2] {
				return sha(b[fset.Position(fn.Pos()).Offset:fset.Position(fn.End()).Offset]), nil
			}
		}
	}
	return "", fmt.Errorf("test %s in %s: %w", parts[2], dir, os.ErrNotExist)
}

func cardWithoutStamps(b []byte) []byte {
	var result []byte
	for _, line := range bytes.SplitAfter(b, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if bytes.HasPrefix(trimmed, []byte("<!-- stamp:")) && bytes.HasSuffix(trimmed, []byte("-->")) {
			continue
		}
		result = append(result, line...)
	}
	return result
}

func codeLinkWarnings(path string, raw []byte) []string {
	body, err := fragmentBody(raw)
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", path, err)}
	}
	links, bodyHash := codeLinks(raw)
	var warnings []string
	for _, link := range links {
		prefix := fmt.Sprintf("%s: code link %s: ", path, link.Target)
		hash, err := codeTargetHash(link.Target)
		switch {
		case errors.Is(err, os.ErrNotExist):
			warnings = append(warnings, prefix+"target not found")
		case err != nil:
			warnings = append(warnings, prefix+"target unavailable: "+err.Error())
		case link.Hash != "" && hash != link.Hash:
			warnings = append(warnings, prefix+"target changed")
		}
		if link.Hash == "" || bodyHash == "" {
			warnings = append(warnings, prefix+"link unconfirmed")
		} else if sha(body) != bodyHash {
			warnings = append(warnings, prefix+"rule text changed")
		}
	}
	return warnings
}

func diskRecordWarnings(codex string, referenced map[string]bool) ([]string, error) {
	root := filepath.Join(codex, "frag")
	var warnings []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if referenced[strings.TrimSuffix(filepath.ToSlash(rel), ".md")] {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if fragmentIsRecord(b) {
			warnings = append(warnings, codeLinkWarnings(path, b)...)
			warnings = append(warnings, overrideWarnings(codex, strings.TrimSuffix(filepath.ToSlash(rel), ".md"), b, os.ReadFile)...)
		}
		return nil
	})
	return warnings, err
}

func lostCodePath(codex string) string { return filepath.Join(codex, "code-link-debt.json") }

func readLostCodeLinks(codex string) ([]lostCodeLink, error) {
	b, err := os.ReadFile(lostCodePath(codex))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read lost code links %s: %w", lostCodePath(codex), err)
	}
	var links []lostCodeLink
	if err := json.Unmarshal(b, &links); err != nil {
		return nil, fmt.Errorf("parse lost code links %s: %w", lostCodePath(codex), err)
	}
	return links, nil
}

func saveLostCodeLinks(codex string, old preservedFragmentHeader) error {
	links, _ := codeLinks(old.header)
	if len(links) == 0 {
		return nil
	}
	lost, err := readLostCodeLinks(codex)
	if err != nil {
		return err
	}
	for _, link := range links {
		entry := lostCodeLink{Fragment: old.id, Target: link.Target}
		if !slices.Contains(lost, entry) {
			lost = append(lost, entry)
		}
	}
	return writeLostCodeLinks(codex, lost)
}

func writeLostCodeLinks(codex string, lost []lostCodeLink) error {
	b, err := json.MarshalIndent(lost, "", "  ")
	if err != nil {
		return fmt.Errorf("encode lost code links: %w", err)
	}
	return os.WriteFile(lostCodePath(codex), append(b, '\n'), fileMode)
}

// CodeDebt lists mismatches and links whose fragments disappeared during refresh.
func CodeDebt(codex string) ([]string, error) {
	codex, err := canonicalCodex(codex, false)
	if err != nil {
		return nil, err
	}
	lost, err := readLostCodeLinks(codex)
	if err != nil {
		return nil, err
	}
	var result []string
	for _, link := range lost {
		result = append(result, fmt.Sprintf("%s: code link %s lost during refresh", link.Fragment, link.Target))
	}
	err = filepath.WalkDir(filepath.Join(codex, "frag"), func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result = append(result, codeLinkWarnings(path, b)...)
		return nil
	})
	sort.Strings(result)
	return result, err
}

// ConfirmCodeLinks updates both snapshots and every manifest containing the fragment.
func ConfirmCodeLinks(codex, fragment string) error {
	codex, err := canonicalCodex(codex, false)
	if err != nil {
		return err
	}
	path, rel, err := codeFragmentPath(codex, fragment)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read fragment %s: %w", path, err)
	}
	updated, err := confirmedCodeFragment(path, raw)
	if err != nil {
		return err
	}
	if err := writeCodeConfirmation(codex, path, rel, updated); err != nil {
		return err
	}
	return clearConfirmedCodeDebt(codex, updated)
}

func clearConfirmedCodeDebt(codex string, raw []byte) error {
	lost, err := readLostCodeLinks(codex)
	if err != nil {
		return err
	}
	links, _ := codeLinks(raw)
	remaining := slices.DeleteFunc(lost, func(entry lostCodeLink) bool {
		return slices.ContainsFunc(links, func(link codeLink) bool { return link.Target == entry.Target })
	})
	if len(remaining) == len(lost) {
		return nil
	}
	return writeLostCodeLinks(codex, remaining)
}

// DropCodeDebt removes lost links to a target without confirming a fragment.
func DropCodeDebt(codex, target string) error {
	codex, err := canonicalCodex(codex, false)
	if err != nil {
		return err
	}
	lost, err := readLostCodeLinks(codex)
	if err != nil {
		return err
	}
	remaining := slices.DeleteFunc(lost, func(entry lostCodeLink) bool { return entry.Target == target })
	if len(remaining) == len(lost) {
		return fmt.Errorf("%s: target is not in lost code link debt", target)
	}
	return writeLostCodeLinks(codex, remaining)
}

func codeFragmentPath(codex, fragment string) (string, string, error) {
	path := fragment
	if !filepath.IsAbs(path) {
		path = filepath.Join(codex, "frag", filepath.FromSlash(fragment))
		if filepath.Ext(path) == "" {
			path += ".md"
		}
	}
	path = filepath.Clean(path)
	rel, err := filepath.Rel(filepath.Join(codex, "frag"), path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.Ext(rel) != ".md" {
		return "", "", fmt.Errorf("%s: fragment must be inside codex/frag", path)
	}
	return path, rel, nil
}

func confirmedCodeFragment(path string, raw []byte) ([]byte, error) {
	body, err := fragmentBody(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	links, _ := codeLinks(raw)
	if len(links) == 0 {
		return nil, fmt.Errorf("%s: no code links", path)
	}
	for i := range links {
		links[i].Hash, err = codeTargetHash(links[i].Target)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", path, links[i].Target, err)
		}
	}
	var header strings.Builder
	for line := range strings.SplitSeq(string(fragmentHeader(raw)), "\n") {
		if line == "---" || line == "" || strings.HasPrefix(line, "код: ") || strings.HasPrefix(line, "код_sha256: ") || strings.HasPrefix(line, "текст_sha256: ") || line == "draft: true" {
			continue
		}
		header.WriteString(line + "\n")
	}
	var result strings.Builder
	result.WriteString("---\n" + header.String())
	for _, link := range links {
		fmt.Fprintf(&result, "код: %s\nкод_sha256: %s\n", link.Target, link.Hash)
	}
	fmt.Fprintf(&result, "текст_sha256: %s\n", sha(body))
	if fragmentIsDraft(raw) {
		result.WriteString("draft: true\n")
	}
	result.WriteString("---\n")
	return append([]byte(result.String()), body...), nil
}

func writeCodeConfirmation(codex, path, rel string, updated []byte) error {
	paths, err := filepath.Glob(filepath.Join(codex, "manifest", "*.yaml"))
	if err != nil {
		return fmt.Errorf("list manifests: %w", err)
	}
	id := strings.TrimSuffix(filepath.ToSlash(rel), ".md")
	type change struct {
		path string
		m    manifest
	}
	var changes []change
	for _, mp := range paths {
		m, err := readManifest(mp)
		if err != nil {
			return err
		}
		if slices.Contains(m.parts, id) {
			m.fragmentHashes[id] = sha(updated)
			changes = append(changes, change{mp, m})
		}
	}
	if err := os.WriteFile(path, updated, fileMode); err != nil {
		return fmt.Errorf("write fragment %s: %w", path, err)
	}
	for _, c := range changes {
		if err := writeManifest(c.path, c.m, c.m.links); err != nil {
			return err
		}
	}
	return nil
}
