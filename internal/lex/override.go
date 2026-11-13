package lex

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

type overrideLink struct {
	target, hash string
	broken       bool
}

func overrideLinks(raw []byte) []overrideLink {
	var links []overrideLink
	for line := range strings.SplitSeq(string(fragmentHeader(raw)), "\n") {
		switch {
		case strings.HasPrefix(line, "переопределяет: "):
			links = append(links, overrideLink{target: strings.TrimPrefix(line, "переопределяет: ")})
		case strings.HasPrefix(line, "переопределяет_обрыв: "):
			links = append(links, overrideLink{target: strings.TrimPrefix(line, "переопределяет_обрыв: "), broken: true})
		case strings.HasPrefix(line, "переопределяет_sha256: ") && len(links) > 0:
			links[len(links)-1].hash = strings.TrimPrefix(line, "переопределяет_sha256: ")
		}
	}
	return links
}

func overrideTarget(codex, id string, read func(string) ([]byte, error)) (string, error) {
	if id == "" || filepath.IsAbs(id) || strings.HasSuffix(id, ".md") {
		return "", fmt.Errorf("invalid fragment id %q", id)
	}
	path, _, err := codeFragmentPath(codex, id)
	if err != nil {
		return "", err
	}
	raw, err := read(path)
	if err != nil {
		return "", err
	}
	body, err := fragmentBody(raw)
	if err != nil {
		return "", err
	}
	return sha(body), nil
}

func overrideWarnings(codex, id string, raw []byte, read func(string) ([]byte, error)) []string {
	links := overrideLinks(raw)
	if len(links) == 0 {
		return nil
	}
	var warnings []string
	values := fragmentHeaderValues(raw)
	if len(values["цель"]) > 0 && len(values["основание"]) > 0 && values["цель"][0] == "" && values["основание"][0] == "" {
		warnings = append(warnings, id+": пусты цель и основание поправки")
	}
	for _, link := range links {
		prefix := fmt.Sprintf("%s: переопределяет %s: ", id, link.target)
		if link.broken {
			warnings = append(warnings, prefix+"обрыв связи")
			continue
		}
		hash, err := overrideTarget(codex, link.target, read)
		if err != nil {
			warnings = append(warnings, prefix+"id не найден или недоступен: "+err.Error())
			continue
		}
		if link.hash == "" {
			warnings = append(warnings, prefix+"связь не подтверждена")
		} else if hash != link.hash {
			warnings = append(warnings, prefix+"текст правила изменился")
		}
	}
	return warnings
}

func setOverrideLinks(raw []byte, links []overrideLink) []byte {
	header := fragmentHeader(raw)
	var out strings.Builder
	inserted := false
	writeLinks := func() {
		for _, link := range links {
			key := "переопределяет"
			if link.broken {
				key = "переопределяет_обрыв"
			}
			fmt.Fprintf(&out, "%s: %s\n", key, link.target)
			if link.hash != "" {
				fmt.Fprintf(&out, "переопределяет_sha256: %s\n", link.hash)
			}
		}
	}
	for line := range strings.SplitSeq(string(header), "\n") {
		if strings.HasPrefix(line, "переопределяет: ") || strings.HasPrefix(line, "переопределяет_обрыв: ") || strings.HasPrefix(line, "переопределяет_sha256: ") {
			continue
		}
		if (line == "draft: true" || line == "---" && out.Len() > 0) && !inserted {
			writeLinks()
			inserted = true
		}
		if line != "" {
			out.WriteString(line + "\n")
		}
	}
	return append([]byte(out.String()), raw[len(header):]...)
}

// ConfirmOverrides records the current body of every named rule.
func ConfirmOverrides(codex, fragment string) error {
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
	if _, err = fragmentBody(raw); err != nil {
		return err
	}
	links := overrideLinks(raw)
	if len(links) == 0 {
		return fmt.Errorf("%s: no override links", path)
	}
	for i := range links {
		if links[i].broken {
			return fmt.Errorf("%s: %s: обрыв связи", path, links[i].target)
		}
		links[i].hash, err = overrideTarget(codex, links[i].target, os.ReadFile)
		if err != nil {
			return fmt.Errorf("%s: %s: %w", path, links[i].target, err)
		}
	}
	return writeCodeConfirmation(codex, path, rel, setOverrideLinks(raw, links))
}

// CountOverrides reports each rule and its distinct amendment locations.
func CountOverrides(codex string) ([]string, error) {
	codex, err := canonicalCodex(codex, false)
	if err != nil {
		return nil, err
	}
	paths, err := filepath.Glob(filepath.Join(codex, "manifest", "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("list manifests: %w", err)
	}
	groups := map[string]map[string][]string{}
	for _, mp := range paths {
		m, err := readManifest(mp)
		if err != nil {
			return nil, err
		}
		for _, id := range m.parts {
			raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
			if err != nil {
				return nil, fmt.Errorf("read fragment %s: %w", id, err)
			}
			for _, link := range overrideLinks(raw) {
				if link.broken {
					continue
				}
				if groups[link.target] == nil {
					groups[link.target] = map[string][]string{}
				}
				location := m.out + ": " + id
				if !slices.Contains(groups[link.target][id], location) {
					groups[link.target][id] = append(groups[link.target][id], location)
				}
			}
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(groups[keys[i]]) != len(groups[keys[j]]) {
			return len(groups[keys[i]]) > len(groups[keys[j]])
		}
		return keys[i] < keys[j]
	})
	var result []string
	for _, key := range keys {
		var locations []string
		for _, entries := range groups[key] {
			locations = append(locations, entries...)
		}
		sort.Strings(locations)
		result = append(result, fmt.Sprintf("%s: %d\n  %s", key, len(groups[key]), strings.Join(locations, "\n  ")))
	}
	return result, nil
}

func rewriteOverridesAfterRefresh(codex, topic string, units [][]byte, old []preservedFragmentHeader) error {
	ids, matched, _ := matchImportedHeaders(topic, units, old)
	renames := map[string]string{}
	occupants := map[string]int{}
	for i, id := range ids {
		occupants[id] = matched[i]
		if matched[i] >= 0 && old[matched[i]].bodyHash == sha(units[i]) {
			renames[old[matched[i]].id] = id
		}
	}
	root := filepath.Join(codex, "frag")
	return filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		links := overrideLinks(raw)
		changed := false
		for i := range links {
			if links[i].broken {
				continue
			}
			if id, ok := renames[links[i].target]; ok {
				if id != links[i].target {
					links[i].target = id
					changed = true
				}
				continue
			}
			if idx, ok := occupants[links[i].target]; ok && idx >= 0 && old[idx].id != links[i].target {
				links[i].broken = true
				changed = true
			}
		}
		if !changed {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		updated := setOverrideLinks(raw, links)
		if bytes.Equal(updated, raw) {
			return nil
		}
		if err := writeCodeConfirmation(codex, path, rel, updated); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "refresh: %s: переопределяет обновлено или оборвано\n", strings.TrimSuffix(filepath.ToSlash(rel), ".md"))
		return nil
	})
}
