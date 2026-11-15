package lex

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type absorbLineOwner struct {
	id, goal string
	count    int
	head     bool // the first nonblank line of its fragment: the rule itself, not a continuation
}

// collectAbsorbLineOwners tracks only unambiguous nonblank lines. Lines that
// occur more than once cannot establish which rule they came from.
func collectAbsorbLineOwners(codex string, parts []string, overrides map[string][]byte) (map[string]absorbLineOwner, error) {
	owners := make(map[string]absorbLineOwner)
	for _, id := range parts {
		path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
		raw, ok := overrides[path]
		if !ok {
			var err error
			raw, err = os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("%s: read fragment for purpose check: %w", path, err)
			}
		}
		body, err := fragmentBody(raw)
		if err != nil {
			return nil, err
		}
		head := true
		for _, line := range absorbLines(body) {
			text := strings.TrimRight(string(line), "\r\n")
			if strings.TrimSpace(text) == "" {
				continue
			}
			owner := owners[text]
			owner.id, owner.goal, owner.count, owner.head = id, purposeValue(raw), owner.count+1, head
			owners[text] = owner
			head = false
		}
	}
	return owners, nil
}

// checkAbsorbPurposeOwners prevents a plan that rebuilds the right text from
// silently assigning an unchanged rule line to another fragment's goal.
func checkAbsorbPurposeOwners(codex string, original manifest, plan absorbPlan) error {
	before, err := collectAbsorbLineOwners(codex, original.parts, nil)
	if err != nil {
		return err
	}
	overrides := make(map[string][]byte)
	for _, edit := range plan.edits {
		overrides[edit.path] = edit.updated
	}
	for _, fragments := range plan.fragments {
		for _, fragment := range fragments {
			overrides[fragment.path] = fragment.raw
		}
	}
	after, err := collectAbsorbLineOwners(codex, plan.manifest.parts, overrides)
	if err != nil {
		return err
	}
	for line, old := range before {
		next, ok := after[line]
		// A rule line keeps its goal; a continuation split off into a fragment of its
		// own starts without one, as a split does elsewhere.
		if ok && old.head && old.count == 1 && next.count == 1 && old.goal != next.goal {
			return fmt.Errorf("%s: cannot safely preserve the purpose of %q while moving and editing rules; first move the rule and run lex absorb, then make the other edit and run lex absorb again", old.id, line)
		}
	}
	return nil
}

// repairAbsorbPurposeOwners assigns complete unchanged units to their original
// fragments and retains the line plan's owners for other units where possible.
func repairAbsorbPurposeOwners(codex, selected string, original manifest, actual, fileBytes []byte, catalog *manifestCatalog) (absorbPlan, bool, error) {
	units := split(actual)
	old, err := collectAbsorbLineOwners(codex, original.parts, nil)
	if err != nil {
		return absorbPlan{}, false, err
	}
	units = splitUnitsByOldOwner(units, old)
	units, assigned, used, ok := assignUnchangedUnits(units, old)
	if !ok {
		return absorbPlan{}, false, nil
	}
	if !assignEditedUnits(units, assigned, used, old, original.parts) {
		return absorbPlan{}, false, nil
	}
	return buildRepairedAbsorbPlan(codex, selected, original, units, assigned, used, fileBytes, catalog)
}

// Assign each unchanged line to its original fragment.
func assignUnchangedUnits(units [][]byte, old map[string]absorbLineOwner) ([][]byte, []string, map[string]bool, bool) {
	assigned := make([]string, len(units))
	used := make(map[string]bool)
	for i, unit := range units {
		for _, line := range absorbLines(unit) {
			owner := old[strings.TrimRight(string(line), "\r\n")]
			if owner.count != 1 {
				continue
			}
			if assigned[i] != "" && assigned[i] != owner.id {
				return nil, nil, nil, false
			}
			assigned[i] = owner.id
		}
	}
	for i := 1; i < len(units); {
		if assigned[i] != "" && assigned[i] == assigned[i-1] {
			units[i-1] = append(units[i-1], units[i]...)
			units = append(units[:i], units[i+1:]...)
			assigned = append(assigned[:i], assigned[i+1:]...)
			continue
		}
		i++
	}
	for _, id := range assigned {
		if id == "" {
			continue
		}
		used[id] = true
	}
	return units, assigned, used, true
}

// An extension retains a complete old line. A rewrite without such evidence
// cannot safely be distinguished from deleting one rule and adding another.
func assignEditedUnits(units [][]byte, assigned []string, used map[string]bool, old map[string]absorbLineOwner, parts []string) bool {
	for i, unit := range units {
		if assigned[i] != "" {
			continue
		}
		candidate := ""
		for _, line := range absorbLines(unit) {
			text := strings.TrimRight(string(line), "\r\n")
			for previous, owner := range old {
				if owner.count != 1 || used[owner.id] || !strings.HasPrefix(text, previous+" ") {
					continue
				}
				if candidate != "" && candidate != owner.id {
					return false
				}
				candidate = owner.id
			}
		}
		if candidate != "" {
			assigned[i] = candidate
			used[candidate] = true
		}
	}
	return unassignedUnitsAreNew(assigned, used, old, parts)
}

func unassignedUnitsAreNew(assigned []string, used map[string]bool, old map[string]absorbLineOwner, parts []string) bool {
	rights := make([]int, len(assigned))
	right := len(parts)
	for i := len(assigned) - 1; i >= 0; i-- {
		rights[i] = right
		if assigned[i] != "" {
			right = slices.Index(parts, assigned[i])
		}
	}
	left := -1
	for i, id := range assigned {
		if id != "" {
			left = slices.Index(parts, id)
			continue
		}
		right = rights[i]
		for _, owner := range old {
			at := slices.Index(parts, owner.id)
			if owner.goal != "" && !used[owner.id] && (left >= right || left < at && at < right) {
				return false
			}
		}
	}
	return true
}

// Preserve separate old headers when an edited list has merged their lines.
func splitUnitsByOldOwner(units [][]byte, old map[string]absorbLineOwner) [][]byte {
	var result [][]byte
	for _, unit := range units {
		start, owner := 0, ""
		lines := absorbLines(unit)
		for i, line := range lines {
			match := old[strings.TrimRight(string(line), "\r\n")]
			if match.count != 1 {
				continue
			}
			if owner != "" && owner != match.id {
				result = append(result, bytes.Join(lines[start:i], nil))
				start = i
			}
			owner = match.id
		}
		result = append(result, bytes.Join(lines[start:], nil))
	}
	return result
}

func buildRepairedAbsorbPlan(codex, selected string, original manifest, units [][]byte, assigned []string, used map[string]bool, fileBytes []byte, catalog *manifestCatalog) (absorbPlan, bool, error) {
	updated := original
	updated.parts = nil
	updated.fragmentHashes = make(map[string]string)
	updated.builtHash = sha(fileBytes)
	plan := absorbPlan{fragments: make(map[int][]plannedFragment)}
	var changed []int
	seen := make(map[string]bool)
	for i, unit := range units {
		id := assigned[i]
		if id == "" {
			frag, ok := newRepairedFragment(codex, selected, original.parts, used, unit)
			if !ok {
				return absorbPlan{}, false, nil
			}
			plan.fragments[i] = []plannedFragment{frag}
			updated.parts = append(updated.parts, frag.id)
			updated.fragmentHashes[frag.id] = sha(frag.raw)
			continue
		}
		path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
		raw, err := os.ReadFile(path)
		if err != nil {
			return absorbPlan{}, false, fmt.Errorf("%s: read fragment for purpose repair: %w", path, err)
		}
		body, err := fragmentBody(raw)
		if err != nil {
			return absorbPlan{}, false, err
		}
		if seen[id] {
			frag, ok := newRepairedFragment(codex, selected, original.parts, used, unit)
			if !ok {
				return absorbPlan{}, false, nil
			}
			plan.fragments[i] = []plannedFragment{frag}
			updated.parts = append(updated.parts, frag.id)
			updated.fragmentHashes[frag.id] = sha(frag.raw)
			continue
		}
		seen[id] = true
		data := replaceFragmentBody(raw, body, unit)
		if string(data) != string(raw) {
			for n, part := range original.parts {
				if part == id {
					changed = append(changed, n)
					break
				}
			}
		}
		updated.parts = append(updated.parts, id)
		updated.fragmentHashes[id] = sha(data)
		plan.edits = append(plan.edits, preparedFragmentEdit{part: id, path: path, updated: data, hash: sha(data)})
	}
	affected, err := firstSharedEdit(original.parts, changed, catalog)
	if err != nil {
		return absorbPlan{}, false, err
	}
	if len(affected) > 0 {
		return absorbPlan{}, false, nil
	}
	for _, id := range original.parts {
		if used[id] {
			continue
		}
		affected, err := catalog.manifestsUsing(id)
		if err != nil {
			return absorbPlan{}, false, err
		}
		if len(affected) <= 1 {
			plan.removePaths = append(plan.removePaths, filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
		}
	}
	plan.manifest = updated
	if err := checkAbsorbPurposeOwners(codex, original, plan); err != nil {
		return absorbPlan{}, false, err
	}
	return plan, true, nil
}

func newRepairedFragment(codex, selected string, parts []string, used map[string]bool, unit []byte) (plannedFragment, bool) {
	label := slug(unit)
	if label == "" {
		label = "fragment"
	}
	var id string
	for n := 1; ; n++ {
		id = fmt.Sprintf("%s/%s-%d", manifestKey(selected), label, n)
		if !slices.Contains(parts, id) && !used[id] {
			break
		}
	}
	path := filepath.Join(codex, "frag", filepath.FromSlash(id)+".md")
	if _, err := os.Lstat(path); err == nil || !os.IsNotExist(err) {
		return plannedFragment{}, false
	}
	used[id] = true
	raw := append([]byte("---\nid: "+id+"\nцель: \nоснование: \nснять_когда: \n---\n"), unit...)
	return plannedFragment{id: id, path: path, raw: raw}, true
}
