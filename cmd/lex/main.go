// Command lex imports instruction files into a fragment codex and builds them.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aiseeq/lex/internal/lex"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		writeError(os.Stderr, err)
		os.Exit(exitCode(err))
	}
}

func writeError(w io.Writer, err error) {
	var target usageError
	if errors.As(err, &target) && target.help {
		fmt.Fprint(w, target.message)
		return
	}
	fmt.Fprintln(w, err)
}

type usageError struct {
	message string
	help    bool
}

func (e usageError) Error() string { return e.message }

func usage(message string) error { return usageError{message: message} }

type commandHelp struct {
	name, flags, description string
}

var commandHelpTable = []commandHelp{
	{"import", "--codex DIR [--refresh] FILE", "импортировать файл во фрагменты и манифест."},
	{"build", "--codex DIR [--check] [--force] [--no-header] [FILE]", "собрать файлы из манифестов."},
	{"absorb", "--codex DIR [--shared] [--force] FILE", "перенести правку собранного файла во фрагменты."},
	{"check", "--codex DIR [--staged] [--outputs-under PATH] [--tree TREE] [--refs-only] [--decisions PATH]", "проверить кодекс и собранные файлы."},
	{"status", "--codex DIR", "показать изменившиеся файлы и команды для них."},
	{"code-debt", "--codex DIR [--drop TARGET]", "показать или удалить запись о долге ссылок на код."},
	{"code-confirm", "--codex DIR FRAGMENT", "подтвердить ссылки фрагмента на код."},
	{"quotes-to-basis", "--codex DIR --report FILE [--apply] [--outputs-under PATH] [--tree TREE]", "перенести цитаты в поле основания."},
	{"override-confirm", "--codex DIR FRAGMENT", "подтвердить переопределения правил."},
	{"override-count", "--codex DIR", "посчитать переопределения правил."},
}

func commandHelpLine(command commandHelp) string {
	return fmt.Sprintf("  %s %s — %s\n", command.name, command.flags, command.description)
}

func commandUsageText(command commandHelp) string {
	return fmt.Sprintf("usage: lex %s %s\n%s\n", command.name, command.flags, command.description)
}

func helpText() string {
	var b strings.Builder
	b.WriteString("lex собирает кодекс инструкций из фрагментов по манифестам.\n\nКоманды:\n")
	for _, command := range commandHelpTable {
		b.WriteString(commandHelpLine(command))
	}
	b.WriteString("\nподробно: lex guide\n")
	return b.String()
}

const guideText = `## Кодекс
Кодекс состоит из фрагментов frag/<файл>/<пункт>.md и манифестов manifest/*.yaml. Имена, начинающиеся с точки, видны через ls -A. Собранные файлы отмечены строкой <!-- lex: собран из … -->.

## Статус
lex status --codex <кодекс> показывает файл, который отличается от последней сборки или записанного источника. Если манифест содержит built_sha256, status сверяет с ним файл и текущую сборку; без built_sha256 сверяет файл с source_sha256, убирая шапку lex при header: true. Правку только во фрагментах status не показывает: её находит lex check. Если собранного файла нет или у изменённого файла не читается фрагмент, status называет причину и продолжает список. Если изменился только файл, при header: true подходит lex absorb. При header: false status предлагает lex import --refresh, если refresh перенесёт правку во фрагменты и пройдёт проверки; иначе — lex absorb. Если файл и фрагменты разошлись, status называет обе команды: lex absorb --force сохраняет текст файла и теряет правки текста фрагментов, которых нет в файле; lex build --force сохраняет фрагменты и теряет правки файла. Код выхода: 0 — список пуст, 1 — в списке есть файлы или status не смог прочесть кодекс (тогда ошибка в stderr), 2 — ошибка вызова.

## Правка собранного файла
Правь собранный файл прямо в нём, затем выполни команду из его шапки: lex absorb --codex <кодекс> <файл>. Если absorb отказывает из-за правок фрагментов после сборки, запусти lex absorb --force --codex <кодекс> <файл>, чтобы сохранить файл, либо lex build --force --codex <кодекс> <файл>, чтобы сохранить фрагменты. После этого запусти lex check --codex <кодекс>; успешная проверка возвращает код 0. Правку, пересекающую границу двух пунктов, и правку, после переноса которой сборка не дала бы тот же файл, absorb не переносит: сохраняет файл черновиком в <кодекс>/drafts и не меняет фрагменты; перенеси её оттуда вручную.

## Поля шапки фрагмента
цель — зачем нужно правило; основание — источник или причина; снять_когда — условие, при котором правило можно убрать. У заголовков и шагов процедур эти поля пусты. Найди фрагмент пункта: grep -rl '<слова пункта>' <кодекс>/frag. После правки пересобери файл: lex build --codex <кодекс> <файл>. lex check предупреждает о правиле (поле тип: правило или любое из трёх полей заполнено), у которого пусто основание или снять_когда, и о цитате пользователя в тексте фрагмента; в конце пишет «правил без основания: N, без условия снятия: M; фрагментов с цитатой пользователя в тексте: K»; с --refs-only правила не проверяются и этой строки нет. N — число предупреждений о пустом основании, M — о пустом условии снятия, K — о цитате пользователя в тексте; код выхода от этого не меняется.

## Не твой файл
Если lex build или lex check называет файл с незавершённой правкой другой сессии, не трогай файл, не ставь --force и дождись завершения той сессии.

## Коммит
lex absorb и lex build печатают изменённые файлы; если кодекс лежит внутри ~/.claude и найден ~/.claude-config.git, они также печатают git add для этого репозитория. Закоммить эти изменения в репозитории конфигурации Claude. Кодекс, лежащий в репозитории проекта, коммить в этом репозитории вместе с изменёнными собранными файлами.
`

func findCommand(name string) (commandHelp, bool) {
	for _, command := range commandHelpTable {
		if command.name == name {
			return command, true
		}
	}
	return commandHelp{}, false
}

func exitCode(err error) int {
	var target usageError
	if errors.As(err, &target) {
		return 2
	}
	return 1
}

func flagValue(args []string, index *int, flag, description string) (string, error) {
	*index++
	if *index >= len(args) || args[*index] == "" || strings.HasPrefix(args[*index], "-") {
		return "", usage(fmt.Sprintf("%s requires %s", flag, description))
	}
	return args[*index], nil
}

type cliOptions struct {
	command, codex, source, outputsUnder, tree, decisions, report, drop string
	refresh, check, force, shared, staged, noHeader, refsOnly           bool
	applyQuotes                                                         bool
}

func run(args []string) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Print(helpText())
		return nil
	}
	if len(args) == 1 && args[0] == "guide" {
		fmt.Print(guideText)
		return nil
	}
	if len(args) == 0 {
		return usageError{message: helpText(), help: true}
	}
	command, known := findCommand(args[0])
	if !known {
		return usageError{message: helpText(), help: true}
	}
	for _, arg := range args[1:] {
		if arg == "-h" || arg == "--help" {
			fmt.Print(commandUsageText(command))
			return nil
		}
	}
	opts, err := parseArguments(args)
	if err != nil {
		return err
	}
	return dispatch(opts)
}
func parseArguments(args []string) (cliOptions, error) {
	if len(args) == 0 {
		return cliOptions{}, usage("usage: lex import|build --codex DIR")
	}
	opts := cliOptions{command: args[0]}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--codex":
			value, err := flagValue(args, &i, "--codex", "a directory")
			if err != nil {
				return opts, err
			}
			opts.codex = value
		case "--drop":
			if err := parseDrop(&opts, args, &i); err != nil {
				return opts, err
			}
		case "--refresh":
			opts.refresh = true
		case "--apply":
			opts.applyQuotes = true
		case "--check":
			opts.check = true
		case "--force":
			opts.force = true
		case "--shared":
			opts.shared = true
		case "--staged":
			opts.staged = true
		case "--decisions", "--outputs-under", "--tree", "--report":
			value, err := flagValue(args, &i, arg, "a path")
			if err != nil {
				return opts, err
			}
			setCheckPath(&opts, arg, value)
		case "--no-header":
			opts.noHeader = true
		default:
			if arg == "--refs-only" {
				opts.refsOnly = true
				continue
			}
			if strings.HasPrefix(arg, "-") {
				return opts, usage(fmt.Sprintf("unknown flag %s", arg))
			}
			if opts.source != "" {
				return opts, usage("import accepts one file")
			}
			opts.source = arg
		}
	}
	if opts.codex == "" {
		return opts, usage("--codex DIR is required")
	}
	return opts, nil
}

func parseDrop(opts *cliOptions, args []string, index *int) error {
	value, err := flagValue(args, index, "--drop", "a target")
	if err != nil {
		return err
	}
	if opts.command != "code-debt" || opts.drop != "" {
		return usage("--drop is only accepted once by code-debt")
	}
	opts.drop = value
	return nil
}

func setCheckPath(opts *cliOptions, flag, value string) {
	switch flag {
	case "--decisions":
		opts.decisions = value
	case "--outputs-under":
		opts.outputsUnder = value
	case "--tree":
		opts.tree = value
	case "--report":
		opts.report = value
	}
}
func dispatch(opts cliOptions) error {
	switch opts.command {
	case "import":
		return importCommand(opts)
	case "build":
		return buildCommand(opts)
	case "absorb":
		return absorbCommand(opts)
	case "check":
		return checkCommand(opts)
	case "status":
		return statusCommand(opts)
	case "code-debt":
		if opts.source != "" {
			return usage("usage: lex code-debt --codex DIR [--drop TARGET]")
		}
		if opts.drop != "" {
			return lex.DropCodeDebt(opts.codex, opts.drop)
		}
		entries, err := lex.CodeDebt(opts.codex)
		for _, entry := range entries {
			fmt.Println(entry)
		}
		return err
	case "code-confirm":
		if opts.source == "" {
			return usage("usage: lex code-confirm --codex DIR FRAGMENT")
		}
		return lex.ConfirmCodeLinks(opts.codex, opts.source)
	case "quotes-to-basis":
		return quotesToBasisCommand(opts)
	case "override-confirm":
		if opts.source == "" {
			return usage("usage: lex override-confirm --codex DIR FRAGMENT")
		}
		return lex.ConfirmOverrides(opts.codex, opts.source)
	case "override-count":
		if opts.source != "" {
			return usage("usage: lex override-count --codex DIR")
		}
		entries, err := lex.CountOverrides(opts.codex)
		for _, entry := range entries {
			fmt.Println(entry)
		}
		return err
	default:
		return usage(fmt.Sprintf("unknown command %q", opts.command))
	}
}

func quotesToBasisCommand(o cliOptions) error {
	if o.source != "" || o.check || o.refresh || o.force || o.shared || o.staged || o.noHeader || o.refsOnly || o.decisions != "" || o.report == "" || o.tree != "" && o.outputsUnder == "" {
		return usage("usage: lex quotes-to-basis --codex DIR --report FILE [--apply] [--outputs-under PATH [--tree TREE]]")
	}
	return lex.QuotesToBasis(o.codex, lex.QuotesToBasisOptions{Apply: o.applyQuotes, Report: o.report, OutputsUnder: o.outputsUnder, Tree: o.tree})
}
func importCommand(o cliOptions) error {
	if o.source == "" || o.check {
		return usage("usage: lex import --codex DIR [--refresh] FILE")
	}
	return lex.Import(o.codex, o.source, o.refresh)
}
func buildCommand(o cliOptions) error {
	if o.force && o.source == "" {
		return usage("назови файл: lex build --force <файл>")
	}
	if o.refresh {
		return usage("usage: lex build --codex DIR [--check] [--no-header] [FILE]; --force requires FILE and permits overwriting manual edits during a build")
	}
	if o.noHeader && !o.check {
		return usage("usage: lex build --codex DIR --check --no-header [FILE]; --force requires FILE and permits overwriting manual edits during a build")
	}
	if o.noHeader {
		return lex.BuildCheckOptions(o.codex, lex.BuildCheckOptionsValue{NoHeader: true, Output: o.source})
	}
	paths, err := lex.BuildWithOptionsAndPaths(o.codex, lex.BuildOptions{Check: o.check, Force: o.force, Output: o.source})
	if len(paths) > 0 {
		printCodexChangeHint(o.codex, paths)
	}
	return err
}
func absorbCommand(o cliOptions) error {
	if o.source == "" || o.check {
		return usage("usage: lex absorb --codex DIR [--shared] [--force] FILE")
	}
	paths, err := lex.AbsorbWithPaths(o.codex, o.source, lex.AbsorbOptions{Shared: o.shared, Force: o.force, DraftDir: o.codex})
	if len(paths) > 0 {
		printCodexChangeHint(o.codex, paths)
	}
	return err
}

func printCodexChangeHint(codex string, paths []string) {
	for _, path := range paths {
		fmt.Println(path)
	}
	home, err := os.UserHomeDir()
	if err == nil {
		home, err = filepath.Abs(home)
		if err == nil {
			printConfigAddCommand(home, codex, paths)
		}
	}
}

func printConfigAddCommand(home, codex string, paths []string) {
	configRepo := filepath.Join(home, ".claude-config.git")
	info, statErr := os.Stat(configRepo)
	if statErr == nil && info.IsDir() {
		codex, codexErr := filepath.Abs(codex)
		if codexErr == nil {
			if resolved, resolveErr := filepath.EvalSymlinks(codex); resolveErr == nil {
				codex = resolved
			}
			if resolved, resolveErr := filepath.EvalSymlinks(home); resolveErr == nil {
				home = resolved
			}
			rel, relErr := filepath.Rel(filepath.Join(home, ".claude"), codex)
			if relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
				fmt.Printf("git --git-dir=%s --work-tree=%s add -- %s\n", configRepo, home, strings.Join(paths, " "))
			}
		}
	}
}
func checkCommand(o cliOptions) error {
	if o.source != "" || o.check || o.refresh {
		return usage("usage: lex check --codex DIR [--staged] [--outputs-under PATH] [--tree TREE]")
	}

	if o.tree != "" && o.outputsUnder == "" {
		return usage("--tree requires --outputs-under")
	}
	if o.staged && o.outputsUnder != "" {
		return usage("--staged cannot be combined with --outputs-under")
	}

	warnings, err := lex.CheckWithWarnings(o.codex, lex.CheckOptions{Staged: o.staged, OutputsUnder: o.outputsUnder, Tree: o.tree, RefsOnly: o.refsOnly, Decisions: o.decisions})
	for _, warning := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", warning)
	}
	if !o.refsOnly {
		fmt.Fprintln(os.Stderr, lex.RuleWarningSummary(warnings))
	}
	return err
}
func statusCommand(o cliOptions) error {
	if o.source != "" || o.check || o.refresh {
		return usage("usage: lex status --codex DIR")
	}
	entries, err := lex.Status(o.codex)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		fmt.Println(statusLine(entry))
	}
	if len(entries) > 0 {
		return errors.New("files differ from the codex")
	}
	return nil
}

func statusLine(entry lex.StatusEntry) string {
	prefix := entry.Manifest + ": " + entry.Output + "; "
	switch {
	case entry.Problem != "":
		if entry.ProblemFile {
			return prefix + "cannot read the file: " + entry.Problem
		}
		return prefix + "cannot build from the fragments: " + entry.Problem
	case entry.KeepFragments != "":
		summary := entry.ConflictSummary
		if summary == "" {
			summary = "the file and its fragments both changed since the last build"
		}
		return prefix + summary + "; run " + entry.Command +
			" to keep the file's text and drop the fragment text edits that are not in the file, or " + entry.KeepFragments +
			" to keep the fragments and drop the file's edits"
	default:
		return prefix + "run " + entry.Command
	}
}
