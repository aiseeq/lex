// Прогон снимков кодекса и сверка с описью.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type inventoryItem struct {
	File, Quote string
	Line, Kind  int
}
type location struct {
	file       string
	line, kind int
}
type finding struct {
	location
	text string
}

var findingPattern = regexp.MustCompile(`^(?:warning: )?(.*?):([0-9]+): kind ([1-4]): `)
var home string

const privateDir = 0700

func archive(rev, dir string) error {
	git := exec.Command("git", "-C", home, "--git-dir="+filepath.Join(home, ".claude-config.git"), "archive", rev, ".claude")
	tar := exec.Command("tar", "-x", "-C", dir)
	var data bytes.Buffer
	git.Stdout = &data
	git.Stderr = os.Stderr
	if err := git.Run(); err != nil {
		return fmt.Errorf("archive %s: %w", rev, err)
	}
	tar.Stdin = &data
	tar.Stderr = os.Stderr
	if err := tar.Run(); err != nil {
		return fmt.Errorf("extract %s: %w", rev, err)
	}
	return nil
}

func scan(binary, dir string) ([]finding, error) {
	tree := filepath.Join(dir, ".claude")
	cmd := exec.Command(binary, "check", "--refs-only", "--codex", filepath.Join(tree, "codex"), "--outputs-under", filepath.Join(home, ".claude"), "--tree", tree)
	out, err := cmd.CombinedOutput()
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return nil, fmt.Errorf("lex check: %w: %s", err, out)
		}
	}
	var found []finding
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		m := findingPattern.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("неожиданный вывод lex: %s", line)
		}
		rel, err := filepath.Rel(tree, m[1])
		if err != nil {
			return nil, fmt.Errorf("путь находки: %w", err)
		}
		n, err := strconv.Atoi(m[2])
		if err != nil {
			return nil, fmt.Errorf("номер строки: %w", err)
		}
		kind, err := strconv.Atoi(m[3])
		if err != nil {
			return nil, fmt.Errorf("класс: %w", err)
		}
		found = append(found, finding{location{rel, n, kind}, strings.ReplaceAll(line, tree, filepath.Join(home, ".claude"))})
	}
	return found, nil
}

func inventory(path string) ([]inventoryItem, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("открыть опись: %w", err)
	}
	defer f.Close()
	var items []inventoryItem
	s := bufio.NewScanner(f)
	for s.Scan() {
		var item inventoryItem
		if err := json.Unmarshal(s.Bytes(), &item); err != nil {
			return nil, fmt.Errorf("разобрать опись: %w", err)
		}
		if item.Kind == 1 || item.Kind == 2 || item.Kind == 4 {
			items = append(items, item)
		}
	}
	if err := s.Err(); err != nil {
		return nil, fmt.Errorf("читать опись: %w", err)
	}
	return items, nil
}

func report(items []inventoryItem, findings []finding) {
	locations := map[location]bool{}
	for _, f := range findings {
		locations[f.location] = true
	}
	var missed []string
	for _, item := range items {
		rel, ok := strings.CutPrefix(item.File, filepath.Join(home, ".claude")+string(filepath.Separator))
		reason := "форма ссылки или нумерации не распознана"
		if !ok {
			reason = "файл вне проверяемого дерева кодекса"
		}
		if ok {
			matched := locations[location{rel, item.Line, item.Kind}]
			if matched {
				continue
			}
			for _, f := range findings {
				if f.file == rel && strings.Contains(f.text, item.Quote) {
					matched = true
					break
				}
			}
			if matched {
				continue
			}
		}
		missed = append(missed, fmt.Sprintf("пропуск %s:%d kind %d: %s; %s", item.File, item.Line, item.Kind, reason, item.Quote))
	}
	fmt.Printf("найдено %d из 43\n", len(items)-len(missed))
	for _, line := range missed {
		fmt.Println(line)
	}
}

func run() error {
	var err error
	home, err = os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("домашний каталог: %w", err)
	}
	if len(os.Args) != 2 || os.Args[1] == "" {
		return fmt.Errorf("нужна внешняя опись: укажите путь аргументом")
	}
	items, err := inventory(os.Args[1])
	if err != nil {
		return err
	}
	root, err := os.MkdirTemp(os.Getenv("TMPDIR"), "lex-text-refs-")
	if err != nil {
		return fmt.Errorf("временная папка: %w", err)
	}
	defer os.RemoveAll(root)
	binary := filepath.Join(root, "lex")
	threads := os.Getenv("GRAFT_THREADS")
	if threads == "" {
		threads = "1"
	}
	build := exec.Command("go", "build", "-p", threads, "-o", binary, "./cmd/lex")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("сборка: %w", err)
	}
	old, current := filepath.Join(root, "old"), filepath.Join(root, "current")
	for _, dir := range []string{old, current} {
		if err := os.Mkdir(dir, privateDir); err != nil {
			return fmt.Errorf("создать снимок: %w", err)
		}
	}
	if err := archive("33bda589", old); err != nil {
		return err
	}
	if err := archive("HEAD", current); err != nil {
		return err
	}
	findings, err := scan(binary, old)
	if err != nil {
		return err
	}
	report(items, findings)
	findings, err = scan(binary, current)
	if err != nil {
		return err
	}
	counts := [5]int{}
	for _, f := range findings {
		counts[f.kind]++
	}
	fmt.Printf("HEAD: kind 1: %d, kind 2: %d, kind 3: %d, kind 4: %d\n", counts[1], counts[2], counts[3], counts[4])
	for _, f := range findings {
		fmt.Println(f.text)
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
