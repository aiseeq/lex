package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aiseeq/lex/internal/lex"
)

func captureRun(t *testing.T, args ...string) (string, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdout")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = f
	err = run(args)
	os.Stdout = old
	if closeErr := f.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(data), err
}

func dispatchCommandNames(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "dispatch" {
			return true
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			clause, ok := node.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expression := range clause.List {
				if literal, ok := expression.(*ast.BasicLit); ok {
					name := strings.Trim(literal.Value, "\"")
					if name != "" {
						names = append(names, name)
					}
				}
			}
			return true
		})
		return false
	})
	return names
}

func TestGuideCommandTableMatchesDispatch(t *testing.T) {
	dispatch := make(map[string]bool)
	for _, name := range dispatchCommandNames(t) {
		dispatch[name] = true
	}
	table := make(map[string]bool)
	for _, command := range commandHelpTable {
		if table[command.name] {
			t.Errorf("command %q appears more than once in the help table", command.name)
		}
		table[command.name] = true
	}
	for name := range dispatch {
		if !table[name] {
			t.Errorf("dispatch command %q has no help table row", name)
		}
	}
	for name := range table {
		if !dispatch[name] {
			t.Errorf("help table command %q has no dispatch case", name)
		}
	}
}

func TestGuideHelpCommands(t *testing.T) {
	for _, alias := range []string{"-h", "--help", "help"} {
		got, err := captureRun(t, alias)
		if err != nil {
			t.Fatalf("%s returned %v", alias, err)
		}
		for _, command := range dispatchCommandNames(t) {
			if !strings.Contains(got, "  "+command+" ") {
				t.Errorf("%s lacks dispatch command %q", alias, command)
			}
		}
		if !strings.HasSuffix(got, "подробно: lex guide\n") {
			t.Errorf("%s lacks final guide hint: %q", alias, got)
		}
	}
}

func TestGuideHelpNoArgs(t *testing.T) {
	got, err := captureRun(t)
	if err == nil || exitCode(err) != 2 {
		t.Fatalf("no arguments error = %v, want exit code 2", err)
	}
	var stderr bytes.Buffer
	writeError(&stderr, err)
	if !strings.Contains(stderr.String(), "подробно: lex guide") || !strings.Contains(stderr.String(), "Команды:") {
		t.Fatalf("stderr lacks help: %q", stderr.String())
	}
	if got != "" {
		t.Fatalf("no-arguments help went to stdout: %q", got)
	}
	_, err = captureRun(t, "unknown-command")
	if err == nil || exitCode(err) != 2 {
		t.Fatalf("unknown command error = %v, want exit code 2", err)
	}
	stderr.Reset()
	writeError(&stderr, err)
	if !strings.Contains(stderr.String(), "подробно: lex guide") {
		t.Fatalf("unknown-command stderr lacks help: %q", stderr.String())
	}
}

func TestGuideSections(t *testing.T) {
	got, err := captureRun(t, "guide")
	if err != nil {
		t.Fatalf("guide returned %v", err)
	}
	for _, section := range []string{"## Кодекс", "## Правка собранного файла", "## Поля шапки фрагмента", "## Не твой файл", "## Коммит"} {
		if !strings.Contains(got, section) {
			t.Errorf("guide lacks section %q", section)
		}
	}
}

func TestGuideFlagsExist(t *testing.T) {
	flags := regexp.MustCompile(`--[a-z][a-z-]*`).FindAllString(helpText()+guideText, -1)
	if len(flags) == 0 {
		t.Fatal("help and guide contain no flags to validate")
	}
	seen := map[string]bool{}
	for _, flag := range flags {
		if seen[flag] {
			continue
		}
		seen[flag] = true
		if flag == "--codex" {
			if _, err := parseArguments([]string{"build", flag, "codex"}); err != nil {
				t.Errorf("documented flag %s is rejected by argument parser: %v", flag, err)
			}
			continue
		}
		command := "build"
		args := []string{command, "--codex", "codex", flag}
		switch flag {
		case "--drop":
			args = []string{"code-debt", "--codex", "codex", flag, "target"}
		case "--decisions", "--outputs-under", "--tree", "--report":
			args = append(args, "path")
		}
		if _, err := parseArguments(args); err != nil {
			t.Errorf("documented flag %s is rejected by argument parser: %v", flag, err)
		}
	}
}

func TestRuleCodeDebtDrop(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	output := filepath.Join(root, "rule.md")
	if err := os.WriteFile(output, []byte("rule\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := lex.ImportWithOptions(codex, output, lex.ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	debtPath := filepath.Join(codex, "code-link-debt.json")
	debt := `[{"fragment":"one","target":"target-a"},{"fragment":"two","target":"target-a"},{"fragment":"three","target":"target-b"}]`
	if err := os.WriteFile(debtPath, []byte(debt), 0644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"code-debt", "--codex", codex, "--drop", "target-a"}); err != nil {
		t.Fatal(err)
	}
	entries, err := lex.CodeDebt(codex)
	if err != nil || len(entries) != 1 || !strings.Contains(entries[0], "target-b") {
		t.Fatalf("remaining debt %v, error %v", entries, err)
	}
	if err := run([]string{"code-debt", "--codex", codex, "--drop", "target-a"}); err == nil || exitCode(err) == 0 {
		t.Fatalf("dropping unknown target succeeded: %v", err)
	}
}

func TestBuildSingleOutputIgnoresManualEditElsewhere(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	selected := filepath.Join(root, "project", "selected.md")
	other := filepath.Join(root, "project", "other.md")
	for path, body := range map[string]string{selected: "selected source\n", other: "other source\n"} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		if err := lex.ImportWithOptions(codex, path, lex.ImportOptions{Home: root}); err != nil {
			t.Fatal(err)
		}
	}
	if err := lex.Build(codex, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("manual edit\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"build", "--codex", codex, selected}); err != nil {
		t.Fatalf("build selected output: %v", err)
	}
	if got, err := os.ReadFile(selected); err != nil || string(got) != "selected source\n" {
		t.Fatalf("selected output = %q, %v", got, err)
	}
	if got, err := os.ReadFile(other); err != nil || string(got) != "manual edit\n" {
		t.Fatalf("other output changed: %q, %v", got, err)
	}
}

func TestBuildForceRequiresFileAndPreservesOutputs(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	output := filepath.Join(root, "project", "output.md")
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("source\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := lex.ImportWithOptions(codex, output, lex.ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	if err := lex.Build(codex, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("manual edit\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"build", "--codex", codex, "--force"},
		{"build", "--codex", codex, "--check", "--force"},
	} {
		err := run(args)
		if err == nil || exitCode(err) == 0 || err.Error() != "назови файл: lex build --force <файл>" {
			t.Fatalf("run(%q) error = %v, want required-file error", args, err)
		}
		if got, readErr := os.ReadFile(output); readErr != nil || string(got) != "manual edit\n" {
			t.Fatalf("refused build changed output: %q, %v", got, readErr)
		}
	}
}

func TestBuildForceOverwritesManualEditForFile(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	output := filepath.Join(root, "project", "output.md")
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("source\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := lex.ImportWithOptions(codex, output, lex.ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	if err := lex.Build(codex, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("manual edit\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"build", "--codex", codex, "--force", output}); err != nil {
		t.Fatalf("forced build: %v", err)
	}
	if got, err := os.ReadFile(output); err != nil || string(got) != "source\n" {
		t.Fatalf("forced output = %q, %v; want rebuilt source", got, err)
	}
}

func TestBuildSingleOutputRefusesForeignCodexWithoutForce(t *testing.T) {
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	foreign := filepath.Join(root, "foreign")
	output := filepath.Join(root, "project", "shared.md")
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("shared instructions\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := lex.ImportWithOptions(owner, output, lex.ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	manifests, err := filepath.Glob(filepath.Join(owner, "manifest", "*.yaml"))
	if err != nil || len(manifests) != 1 {
		t.Fatalf("list owner manifests: %v, %v", manifests, err)
	}
	manifest, err := os.ReadFile(manifests[0])
	if err != nil {
		t.Fatal(err)
	}
	manifest = []byte(strings.Replace(string(manifest), "header: false\n", "header: true\n", 1))
	if err := os.WriteFile(manifests[0], manifest, 0644); err != nil {
		t.Fatal(err)
	}
	if err := lex.Build(owner, false); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(foreign, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(foreign, os.DirFS(owner)); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	err = run([]string{"build", "--codex", foreign, output})
	if err == nil || exitCode(err) == 0 {
		t.Fatalf("build of foreign output succeeded without --force: %v", err)
	}
	got, err := os.ReadFile(output)
	if err != nil || string(got) != string(original) {
		t.Fatalf("refused build changed output: %q, %v", got, err)
	}
	if err := run([]string{"build", "--codex", foreign, "--force", output}); err != nil {
		t.Fatalf("forced build of foreign output: %v", err)
	}
	got, err = os.ReadFile(output)
	if err != nil || string(got) == string(original) || !strings.Contains(string(got), foreign+string(filepath.Separator)+"manifest") {
		t.Fatalf("forced build did not replace owner marker: %q, %v", got, err)
	}
}

func TestBuildSkipsManualEditAndBuildsOtherOutputs(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	manual := filepath.Join(root, "project", "manual.md")
	updated := filepath.Join(root, "project", "updated.md")
	for path, body := range map[string]string{manual: "manual source\n", updated: "old source\n"} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		if err := lex.ImportWithOptions(codex, path, lex.ImportOptions{Home: root}); err != nil {
			t.Fatal(err)
		}
	}
	if err := lex.Build(codex, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manual, []byte("hand edit\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Выход без ручной правки удалён: общая сборка должна его восстановить, даже если выход с ручной правкой идёт раньше.
	if err := os.Remove(updated); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"build", "--codex", codex})
	if err == nil || exitCode(err) == 0 || !strings.Contains(err.Error(), manual) {
		t.Fatalf("build error = %v, want named manual output and nonzero exit", err)
	}
	if got, readErr := os.ReadFile(updated); readErr != nil || string(got) != "old source\n" {
		t.Fatalf("unmodified output was not rebuilt: %q, %v", got, readErr)
	}
	if got, readErr := os.ReadFile(manual); readErr != nil || string(got) != "hand edit\n" {
		t.Fatalf("manual output was overwritten: %q, %v", got, readErr)
	}
}

func TestCheckCommandPassesOutputsUnder(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	projectDir := filepath.Join(root, "project")
	otherDir := filepath.Join(root, "other")
	project := filepath.Join(projectDir, "CLAUDE.md")
	other := filepath.Join(otherDir, "CLAUDE.md")
	for _, file := range []string{project, other} {
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("body\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := lex.ImportWithOptions(codex, file, lex.ImportOptions{Home: root}); err != nil {
			t.Fatal(err)
		}
	}
	if err := lex.Build(codex, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("manual edit\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := checkCommand(cliOptions{codex: codex, outputsUnder: projectDir}); err != nil {
		t.Fatalf("check command included foreign output: %v", err)
	}
}

func TestCheckTreeCLI(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	main := filepath.Join(root, "project")
	output := filepath.Join(main, "CLAUDE.md")
	if err := os.MkdirAll(main, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("body\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := lex.ImportWithOptions(codex, output, lex.ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	if err := lex.Build(codex, false); err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(root, "tree-file")
	if err := os.WriteFile(tree, nil, 0644); err != nil {
		t.Fatal(err)
	}
	args := []string{"check", "--codex", codex, "--outputs-under", main, "--tree", tree}
	opts, err := parseArguments(args)
	if err != nil || opts.tree != tree {
		t.Fatalf("parse --tree: opts=%+v err=%v", opts, err)
	}
	if err := checkCommand(opts); err == nil || !strings.Contains(err.Error(), "tree path is not a directory") {
		t.Fatalf("check command did not pass --tree through: %v", err)
	}
	for _, args := range [][]string{
		{"check", "--codex", codex, "--outputs-under", main, "--tree"},
		{"check", "--codex", codex, "--outputs-under", main, "--tree", ""},
	} {
		if _, err := parseArguments(args); err == nil {
			t.Fatalf("invalid --tree value was accepted: %q", args)
		}
	}
}

func TestExitCodeDistinguishesUsageAndOperationalErrors(t *testing.T) {
	if got := exitCode(run([]string{"check", "--codex", "codex", "--nope"})); got != 2 {
		t.Fatalf("usage error exit code = %d, want 2", got)
	}
	if got := exitCode(run([]string{"check", "--codex", filepath.Join(t.TempDir(), "missing")})); got != 1 {
		t.Fatalf("operational error exit code = %d, want 1", got)
	}
}

func TestCheckOutputsUnderCLIAndStagedConflict(t *testing.T) {
	opts, err := parseArguments([]string{"check", "--codex", "codex", "--outputs-under", "project/"})
	if err != nil || opts.outputsUnder != "project/" {
		t.Fatalf("parse outputs-under: opts=%+v err=%v", opts, err)
	}
	for _, args := range [][]string{
		{"check", "--codex", "codex", "--outputs-under"},
		{"check", "--codex", "codex", "--outputs-under", ""},
	} {
		if _, err := parseArguments(args); err == nil {
			t.Fatalf("invalid outputs-under value was accepted: %q", args)
		}
	}
	err = checkCommand(cliOptions{codex: "codex", staged: true, outputsUnder: "project"})
	if err == nil || exitCode(err) != 2 || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("expected staged conflict usage error, got %v", err)
	}
}

func TestAbsorbPrintsChangedCodexPathsAndConfigCommand(t *testing.T) {
	for _, test := range []struct {
		name       string
		configRepo bool
		changed    bool
	}{
		{name: "with config repository", configRepo: true, changed: true},
		{name: "without config repository", changed: true},
		{name: "without changes", configRepo: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOME", root)
			if test.configRepo {
				if err := os.Mkdir(filepath.Join(root, ".claude-config.git"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			codex := filepath.Join(root, "codex")
			output := filepath.Join(root, "project", "instructions.md")
			if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(output, []byte("first point\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := lex.ImportWithOptions(codex, output, lex.ImportOptions{Home: root}); err != nil {
				t.Fatal(err)
			}
			if err := lex.Build(codex, false); err != nil {
				t.Fatal(err)
			}
			if test.changed {
				file, err := os.OpenFile(output, os.O_APPEND|os.O_WRONLY, 0644)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := file.WriteString("new point\n"); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			got := captureStdout(t, func() error {
				return run([]string{"absorb", "--codex", codex, output})
			})
			var newFragment string
			err := filepath.WalkDir(filepath.Join(codex, "frag"), func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() {
					return nil
				}
				body, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if strings.Contains(string(body), "new point") {
					newFragment = path
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if test.changed {
				if newFragment == "" || !strings.Contains(got, newFragment+"\n") {
					t.Fatalf("absorb output %q does not contain new fragment path %q", got, newFragment)
				}
			} else if got != "" {
				t.Fatalf("absorb without changes printed %q", got)
			}
			manifest := filepath.Join(codex, "manifest", "project-instructions.md.yaml")
			command := "git --git-dir=" + filepath.Join(root, ".claude-config.git") + " --work-tree=" + root + " add -- " + newFragment + " " + manifest + "\n"
			if test.configRepo && test.changed && !strings.Contains(got, command) {
				t.Fatalf("absorb output %q does not contain command %q", got, command)
			}
			if (!test.configRepo || !test.changed) && strings.Contains(got, "git --git-dir=") {
				t.Fatalf("absorb unexpectedly printed config command: %q", got)
			}
		})
	}
}

func captureStdout(t *testing.T, action func() error) string {
	t.Helper()
	old := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	runErr := action()
	_ = writer.Close()
	os.Stdout = old
	output, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if runErr != nil {
		t.Fatal(runErr)
	}
	return string(output)
}

func TestReadCommandsRejectInvalidCodex(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing")
	file := filepath.Join(root, "file")
	empty := filepath.Join(root, "empty")
	manifestFile := filepath.Join(root, "manifest-file")
	output := filepath.Join(root, "output.md")
	for _, path := range []string{file, output} {
		if err := os.WriteFile(path, []byte("body\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(empty, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(manifestFile, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestFile, "manifest"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	commands := [][]string{
		{"check"},
		{"check", "--outputs-under", root},
		{"check", "--staged"},
		{"build"},
		{"build", "--check"},
		{"build", "--check", "--no-header"},
		{"status"},
		{"absorb", output},
	}
	for _, codex := range []string{missing, file, empty, manifestFile} {
		for _, command := range commands {
			args := append([]string{command[0], "--codex", codex}, command[1:]...)
			err := run(args)
			if err == nil || exitCode(err) != 1 || !strings.Contains(err.Error(), codex) {
				t.Errorf("%v: error = %v, want operational error containing %q", args, err, codex)
			}
		}
	}
	importErr := run([]string{"import", "--codex", file, output})
	if importErr == nil || exitCode(importErr) != 1 || !strings.Contains(importErr.Error(), file) {
		t.Errorf("import into file: error = %v, want operational error containing %q", importErr, file)
	}
}
