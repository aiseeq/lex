package lex

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestSymlinkImportTracksTargetAndRefresh(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	target := filepath.Join(root, "CLAUDE.md")
	link := filepath.Join(root, "AGENTS.md")
	mustWrite(t, target, []byte("# old\n"))
	if err := os.Symlink("CLAUDE.md", link); err != nil {
		t.Fatal(err)
	}
	importLink := func(refresh bool) error {
		return ImportWithOptions(codex, link, ImportOptions{Refresh: refresh, Home: root})
	}
	if err := importLink(false); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(codex, "manifest", codexKey("CLAUDE.md")+".yaml")
	m, err := readManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.links) != 1 || m.links[0].path != link || m.links[0].target != "CLAUDE.md" {
		t.Fatalf("manifest links = %#v", m.links)
	}
	if err := importLink(false); err != nil {
		t.Fatalf("unchanged symlink import: %v", err)
	}
	mustWrite(t, target, []byte("# changed\n"))
	if err := importLink(false); err == nil || !strings.Contains(err.Error(), "source changed; re-import with --refresh") {
		t.Fatalf("changed target import must require refresh, got %v", err)
	}
	if err := importLink(true); err != nil {
		t.Fatalf("refresh through symlink: %v", err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "# changed\n" {
		t.Fatalf("refreshed target = %q, %v", got, err)
	}
}

func TestSymlinkChecksAndBuildGuard(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	target := filepath.Join(root, "CLAUDE.md")
	linkDir := filepath.Join(root, "links")
	link := filepath.Join(linkDir, "AGENTS.md")
	if err := os.MkdirAll(linkDir, directoryMode); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, target, []byte("# source\n"))
	if err := os.Symlink("../CLAUDE.md", link); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, link, ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	outsideDir := filepath.Join(root, "outside")
	if err := os.MkdirAll(outsideDir, directoryMode); err != nil {
		t.Fatal(err)
	}
	outsideLink := filepath.Join(outsideDir, "GEMINI.md")
	if err := os.Symlink("../CLAUDE.md", outsideLink); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, outsideLink, ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	for _, options := range []CheckOptions{{}, {OutputsUnder: linkDir}} {
		if _, err := CheckWithWarnings(codex, options); err != nil {
			t.Fatalf("valid link check %v: %v", options, err)
		}
	}
	if err := os.Remove(outsideLink); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, target, []byte("manually edited target\n"))
	if _, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: linkDir}); err != nil {
		t.Fatalf("out-of-scope links or output content affected link-only check: %v", err)
	}
	mustWrite(t, target, []byte("# source\n"))
	checkFailure := func(want string) {
		t.Helper()
		for _, options := range []CheckOptions{{}, {OutputsUnder: linkDir}} {
			_, err := CheckWithWarnings(codex, options)
			if err == nil || !strings.Contains(err.Error(), link) || !strings.Contains(err.Error(), "CLAUDE.md") || !strings.Contains(err.Error(), want) {
				t.Fatalf("link check %v error = %v", options, err)
			}
		}
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	checkFailure("missing")
	mustWrite(t, link, []byte("# replacement\n"))
	checkFailure("ln -sfn")
	if err := Build(codex, false); err == nil {
		t.Fatal("build overwrote a regular file")
	}
	if got, err := os.ReadFile(link); err != nil || string(got) != "# replacement\n" {
		t.Fatalf("build changed occupied file: %q, %v", got, err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatalf("build did not recreate missing symlink: %v", err)
	}
	if got, err := os.Readlink(link); err != nil || got != "../CLAUDE.md" {
		t.Fatalf("rebuilt link = %q, %v", got, err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	otherTarget := filepath.Join(root, "other.md")
	mustWrite(t, otherTarget, []byte("another existing target\n"))
	if err := os.Symlink("other.md", link); err != nil {
		t.Fatal(err)
	}
	checkFailure("ln -sfn")
	if err := Build(codex, false); err == nil {
		t.Fatal("build accepted a link to another existing target")
	}
}

func TestStagedSymlinkChangesAreGuarded(t *testing.T) {
	repo := t.TempDir()
	codex := filepath.Join(repo, "cx")
	target := filepath.Join(repo, "CLAUDE.md")
	link := filepath.Join(repo, "AGENTS.md")
	mustWrite(t, target, []byte("# source\n"))
	mustWrite(t, filepath.Join(repo, "OTHER.md"), []byte("other target exists\n"))
	if err := os.Symlink("CLAUDE.md", link); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, link, ImportOptions{Home: repo}); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) error {
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		cmd.Dir = repo
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %s: %w", args, output, err)
		}
		return nil
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"commit", "-q", "-m", "init"}} {
		if err := git(args...); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, filepath.Join(repo, "other.txt"), []byte("unrelated\n"))
	if err := git("add", "other.txt"); err != nil {
		t.Fatal(err)
	}
	if err := checkIn(t, repo, codex); err != nil {
		t.Fatalf("unrelated staged change: %v", err)
	}
	if err := git("reset", "-q"); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"file", "file-target", "wrong-target", "deleted"} {
		t.Run(mode, func(t *testing.T) {
			if err := git("reset", "-q", "--hard"); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "file":
				mustWrite(t, link, []byte("file\n"))
			case "file-target":
				mustWrite(t, link, []byte("CLAUDE.md"))
			case "wrong-target":
				if err := os.Symlink("OTHER.md", link); err != nil {
					t.Fatal(err)
				}
			}
			if err := git("add", "-A"); err != nil {
				t.Fatal(err)
			}
			err := checkIn(t, repo, codex)
			if err == nil || !strings.Contains(err.Error(), link) || !strings.Contains(err.Error(), "CLAUDE.md") {
				t.Fatalf("staged link change error = %v", err)
			}
			if mode == "file" && (!strings.Contains(err.Error(), "staged as a regular file") || !strings.Contains(err.Error(), "expected symlink to \"CLAUDE.md\"")) {
				t.Fatalf("staged regular-file diagnostic is unclear: %v", err)
			}
		})
	}
}

func TestSymlinkHeaderAndRefreshPersistence(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	target := filepath.Join(root, "CLAUDE.md")
	link := filepath.Join(root, "AGENTS.md")
	mustWrite(t, target, []byte("# original\n"))
	if err := os.Symlink("CLAUDE.md", link); err != nil {
		t.Fatal(err)
	}
	opts := ImportOptions{Home: root}
	if err := ImportWithOptions(codex, target, opts); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(codex, "manifest", codexKey("CLAUDE.md")+".yaml")
	if err := setManifestHeader(codex, "CLAUDE.md", true); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, target, opts); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(manifestPath)
	if err != nil || !m.header {
		t.Fatalf("unchanged import lost header: header=%v err=%v", m.header, err)
	}
	if err := ImportWithOptions(codex, link, opts); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, link, opts); err != nil {
		t.Fatal(err)
	}
	m, err = readManifest(manifestPath)
	if err != nil || !m.header || len(m.links) != 1 {
		t.Fatalf("symlink import changed header or lost link: %#v err=%v", m, err)
	}
	mustWrite(t, target, []byte("# refreshed\n"))
	if err := ImportWithOptions(codex, link, ImportOptions{Home: root, Refresh: true}); err != nil {
		t.Fatal(err)
	}
	m, err = readManifest(manifestPath)
	if err != nil || !m.header || len(m.links) != 1 || m.links[0].path != link {
		t.Fatalf("refresh lost header or link: %#v err=%v", m, err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []string{"# first manual edit\n", "# second manual edit\n"} {
		mustWrite(t, target, append(generatedMarker(manifestPath), []byte(edit)...))
		if err := ImportWithOptions(codex, target, ImportOptions{Home: root, Refresh: true}); err != nil {
			t.Fatalf("refresh edited output: %v", err)
		}
		if err := Build(codex, false); err != nil {
			t.Fatal(err)
		}
		built, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if got := bytes.Count(built, []byte("<!-- lex: собран из ")); got != 1 || !bytes.Contains(built, []byte(edit)) {
			t.Fatalf("refresh cycle lost edits or duplicated generated marker (%d): %q", got, built)
		}
	}
}

func TestRepeatedImportDoesNotRewriteManifest(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	target := filepath.Join(root, "CLAUDE.md")
	link := filepath.Join(root, "AGENTS.md")
	mustWrite(t, target, []byte("# stable\n"))
	if err := os.Symlink("CLAUDE.md", link); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, link, ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	manifestPath := manifestOf(t, codex, "CLAUDE.md")
	stamp := time.Unix(1_000_000_000, 0)
	if err := os.Chtimes(manifestPath, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, link, ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(stamp) {
		t.Fatalf("unchanged import rewrote manifest: mtime=%v want %v", info.ModTime(), stamp)
	}
}

func TestRefreshThroughTargetPreservesLinks(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	target := filepath.Join(root, "CLAUDE.md")
	link := filepath.Join(root, "AGENTS.md")
	mustWrite(t, target, []byte("old\n"))
	if err := os.Symlink("CLAUDE.md", link); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, link, ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, target, []byte("new\n"))
	if err := ImportWithOptions(codex, target, ImportOptions{Home: root, Refresh: true}); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(manifestOf(t, codex, "CLAUDE.md"))
	if err != nil || len(m.links) != 1 || m.links[0].path != link || m.links[0].target != "CLAUDE.md" {
		t.Fatalf("direct target refresh lost symlink: links=%#v err=%v", m.links, err)
	}
}

func TestImportDanglingSymlinkReportsPath(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "AGENTS.md")
	if err := os.Symlink("missing.md", link); err != nil {
		t.Fatal(err)
	}
	err := ImportWithOptions(filepath.Join(root, "codex"), link, ImportOptions{Home: root})
	if err == nil || !strings.Contains(err.Error(), link) {
		t.Fatalf("dangling symlink import error = %v", err)
	}
}

func TestEqualLinksComparesTargets(t *testing.T) {
	a := []outputLink{{path: "/tmp/AGENTS.md", target: "CLAUDE.md"}}
	b := []outputLink{{path: "/tmp/AGENTS.md", target: "OTHER.md"}}
	if equalLinks(a, b) {
		t.Fatal("links with the same path and different targets compare equal")
	}
}

func TestSymlinkChainRejectedAndDirectoryPathCanonicalized(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	target := filepath.Join(root, "CLAUDE.md")
	chain := filepath.Join(root, "AGENTS.md")
	mustWrite(t, target, []byte("# source\n"))
	intermediate := filepath.Join(root, "B.md")
	if err := os.Symlink("CLAUDE.md", intermediate); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("B.md", chain); err != nil {
		t.Fatal(err)
	}
	err := ImportWithOptions(codex, chain, ImportOptions{Home: root})
	if err == nil || !strings.Contains(err.Error(), chain) || !strings.Contains(err.Error(), intermediate) {
		t.Fatalf("symlink chain should name both links, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(codex, "manifest")); !os.IsNotExist(err) {
		t.Fatalf("chain import wrote a manifest: %v", err)
	}
	linkDir := filepath.Join(root, "real")
	if err := os.MkdirAll(linkDir, directoryMode); err != nil {
		t.Fatal(err)
	}
	realLink := filepath.Join(linkDir, "AGENTS.md")
	if err := os.Symlink("../CLAUDE.md", realLink); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(linkDir, alias); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, filepath.Join(alias, "AGENTS.md"), ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(filepath.Join(codex, "manifest", codexKey("CLAUDE.md")+".yaml"))
	if err != nil || len(m.links) != 1 || m.links[0].path != realLink {
		t.Fatalf("directory symlink was not canonicalized: %#v err=%v", m.links, err)
	}
}

func TestBuildCheckGuardsAllLinksWithoutCreating(t *testing.T) {
	codex := t.TempDir()
	root := t.TempDir()
	out := filepath.Join(root, "CLAUDE.md")
	mustWrite(t, out, []byte("body\n"))
	links := []outputLink{
		{path: filepath.Join(root, "b", "GEMINI.md"), target: "../CLAUDE.md"},
		{path: filepath.Join(root, "a", "AGENTS.md"), target: "../CLAUDE.md"},
	}
	if err := importDataWithLinks(codex, out, "CLAUDE.md", []byte("body\n"), false, links); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(filepath.Join(codex, "manifest", codexKey("CLAUDE.md")+".yaml"))
	if err != nil || len(m.links) != 2 || m.links[0].path != links[1].path || m.links[1].path != links[0].path {
		t.Fatalf("manifest links were not sorted: %#v err=%v", m.links, err)
	}
	checks := []func() error{
		func() error { return BuildWithOptions(codex, BuildOptions{Check: true}) },
		func() error { return BuildCheckOptions(codex, BuildCheckOptionsValue{NoHeader: true}) },
	}
	for _, check := range checks {
		err := check()
		if err == nil || !strings.Contains(err.Error(), links[0].path) || !strings.Contains(err.Error(), links[1].path) {
			t.Fatalf("link check did not report all missing links: %v", err)
		}
		for _, link := range links {
			if _, statErr := os.Lstat(link.path); !os.IsNotExist(statErr) {
				t.Fatalf("check created %s: %v", link.path, statErr)
			}
		}
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	for _, link := range links {
		if got, err := os.Readlink(link.path); err != nil || got != link.target {
			t.Fatalf("build did not create %s: target=%q err=%v", link.path, got, err)
		}
	}
}

func TestCheckTreeGuardsRecordedLinks(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	main := filepath.Join(root, "main")
	tree := filepath.Join(root, "tree")
	if err := os.MkdirAll(main, directoryMode); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(main, "CLAUDE.md")
	link := filepath.Join(main, "AGENTS.md")
	mustWrite(t, target, []byte("# source\n"))
	if err := os.Symlink("CLAUDE.md", link); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, link, ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tree, directoryMode); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(tree, "CLAUDE.md"), []byte("# source\n"))
	treeLink := filepath.Join(tree, "AGENTS.md")
	if err := os.Symlink("CLAUDE.md", treeLink); err != nil {
		t.Fatal(err)
	}
	opts := CheckOptions{OutputsUnder: main, Tree: tree}
	if _, err := CheckWithWarnings(codex, opts); err != nil {
		t.Fatalf("valid tree links did not pass: %v", err)
	}
	if err := os.Remove(treeLink); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, treeLink, []byte("replacement\n"))
	if _, err := CheckWithWarnings(codex, opts); err == nil || !strings.Contains(err.Error(), treeLink) || !strings.Contains(err.Error(), "CLAUDE.md") {
		t.Fatalf("tree regular file replacement was accepted: %v", err)
	} else if !strings.Contains(err.Error(), "ln -sfn") || strings.Contains(err.Error(), "lex build") {
		t.Fatalf("tree link diagnostic has an unusable repair hint: %v", err)
	}
	if err := os.Remove(treeLink); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(tree, "OTHER.md"), []byte("existing other target\n"))
	if err := os.Symlink("OTHER.md", treeLink); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckWithWarnings(codex, opts); err == nil || !strings.Contains(err.Error(), treeLink) {
		t.Fatalf("tree link to existing other target was accepted: %v", err)
	}
}

func TestManifestRejectsMalformedLinks(t *testing.T) {
	base := "out: \"/tmp/out.md\"\nsource_sha256: abc\nlinks:\n"
	cases := map[string]string{
		"invalid quoting":   "  - path: /tmp/AGENTS.md\n    target: \"CLAUDE.md\"\n",
		"relative path":     "  - path: \"relative/AGENTS.md\"\n    target: \"CLAUDE.md\"\n",
		"missing target":    "  - path: \"/tmp/AGENTS.md\"\n",
		"empty target":      "  - path: \"/tmp/AGENTS.md\"\n    target: \"\"\n",
		"duplicate target":  "  - path: \"/tmp/AGENTS.md\"\n    target: \"A\"\n    target: \"B\"\n",
		"unknown entry key": "  - pth: \"/tmp/AGENTS.md\"\n    target: \"CLAUDE.md\"\n",
	}
	for name, section := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseManifest("fixture.yaml", []byte(base+section+"parts:\nfragment_sha256:\n")); err == nil || !strings.Contains(err.Error(), "fixture.yaml") {
				t.Fatalf("malformed links accepted without manifest path: %v", err)
			}
		})
	}
	if _, err := parseManifest("fixture.yaml", []byte("out: \"/tmp/out.md\"\nsource_sha256: abc\nlinks: x\nparts:\nfragment_sha256:\n")); err == nil || !strings.Contains(err.Error(), "fixture.yaml") {
		t.Fatalf("malformed top-level links value accepted without manifest path: %v", err)
	}
	for _, tc := range []struct{ line, diagnostic string }{
		{"  - path: /tmp/link\n", "decode symlink path"},
		{"  - path: \"/tmp/link\"\n    target: /tmp/target\n", "decode symlink target"},
	} {
		if _, err := parseManifest("fixture.yaml", []byte(base+tc.line+"parts:\nfragment_sha256:\n")); err == nil || !strings.Contains(err.Error(), tc.diagnostic) {
			t.Fatalf("malformed link error lacks %q: %v", tc.diagnostic, err)
		}
	}
	blank := base + "  - path: \"/tmp/link\"\n    target: \"target\"\n\nparts:\nfragment_sha256:\n"
	if _, err := parseManifest("fixture.yaml", []byte(blank)); err != nil {
		t.Fatalf("blank line in links section was rejected: %v", err)
	}
}

func TestMissingManifestOutputErrorNamesPathOnce(t *testing.T) {
	codex := t.TempDir()
	manifestPath := filepath.Join(codex, "manifest", "bad.yaml")
	if err := os.MkdirAll(filepath.Dir(manifestPath), directoryMode); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, manifestPath, []byte("source_sha256: abc\n"))
	checks := []struct {
		name string
		call func() error
	}{
		{"build", func() error { return Build(codex, false) }},
		{"build check", func() error { return Build(codex, true) }},
		{"check", func() error { _, err := CheckWithWarnings(codex, CheckOptions{}); return err }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			err := check.call()
			if err == nil || !strings.HasPrefix(err.Error(), manifestPath+": ") || strings.Count(err.Error(), manifestPath) != 1 {
				t.Fatalf("manifest path should occur exactly once: %v", err)
			}
		})
	}
}

func TestStatusBrokenManifestNamesPathOnce(t *testing.T) {
	codex := t.TempDir()
	manifestPath := filepath.Join(codex, "manifest", "broken.yaml")
	if err := os.MkdirAll(filepath.Dir(manifestPath), directoryMode); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, manifestPath, []byte("source_sha256: abc\n"))
	_, err := StatusWithHome(codex, t.TempDir())
	if err == nil || !strings.HasPrefix(err.Error(), manifestPath+": ") || strings.Count(err.Error(), manifestPath) != 1 {
		t.Fatalf("status error should start with manifest path once: %v", err)
	}
}

func TestOutputsUnderDoesNotSelectLinkAtParentPath(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	out := filepath.Join(root, "outside.md")
	if err := importData(codex, out, "outside.md", []byte("body\n"), false); err != nil {
		t.Fatal(err)
	}
	manifestPath := manifestOf(t, codex, "outside.md")
	m, err := readManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	parentLinkPath := filepath.Join(root, "scope")
	m.links = []outputLink{{path: parentLinkPath, target: "somewhere"}}
	if err := writeManifest(manifestPath, m, m.links); err != nil {
		t.Fatal(err)
	}
	under := filepath.Join(parentLinkPath, "child")
	if _, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: under}); err != nil {
		t.Fatalf("link at the parent path was incorrectly selected: %v", err)
	}
}

func TestOutputsUnderChecksSelectedOutputsAndLinksSeparately(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	under := filepath.Join(root, "scope")
	if err := os.MkdirAll(under, directoryMode); err != nil {
		t.Fatal(err)
	}
	selectedOutput := filepath.Join(under, "selected.md")
	mustWrite(t, selectedOutput, []byte("selected output\n"))
	if err := importData(codex, selectedOutput, "selected.md", []byte("selected output\n"), false); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "outside.md")
	mustWrite(t, target, []byte("outside output\n"))
	link := filepath.Join(under, "AGENTS.md")
	if err := os.Symlink("../outside.md", link); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, link, ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, target, []byte("manually edited outside output\n"))
	if _, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: under}); err != nil {
		t.Fatalf("link scope incorrectly expanded to its output target: %v", err)
	}
}

func TestStagedCorrectSymlinkModePasses(t *testing.T) {
	repo := t.TempDir()
	codex := filepath.Join(repo, "cx")
	target := filepath.Join(repo, "CLAUDE.md")
	link := filepath.Join(repo, "AGENTS.md")
	mustWrite(t, target, []byte("# body\n"))
	if err := os.Symlink("CLAUDE.md", link); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, link, ImportOptions{Home: repo}); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, link, []byte("regular file in HEAD\n"))
	git := func(args ...string) error {
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		cmd.Dir = repo
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %s: %w", args, output, err)
		}
		return nil
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"commit", "-q", "-m", "regular baseline"}} {
		if err := git(args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("CLAUDE.md", link); err != nil {
		t.Fatal(err)
	}
	if err := git("add", "AGENTS.md"); err != nil {
		t.Fatal(err)
	}
	if err := checkIn(t, repo, codex); err != nil {
		t.Fatalf("correct staged 120000 symlink was rejected: %v", err)
	}
}

func TestOutputsUnderMalformedLinkNamesManifest(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	out := filepath.Join(root, "CLAUDE.md")
	if err := importData(codex, out, "CLAUDE.md", []byte("body\n"), false); err != nil {
		t.Fatal(err)
	}
	manifestPath := manifestOf(t, codex, "CLAUDE.md")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	malformed := bytes.Replace(raw, []byte("parts:\n"), []byte("links:\n  - path: \"relative/AGENTS.md\"\n    target: \"CLAUDE.md\"\nparts:\n"), 1)
	mustWrite(t, manifestPath, malformed)
	_, err = CheckWithWarnings(codex, CheckOptions{OutputsUnder: root})
	if err == nil || !strings.Contains(err.Error(), manifestPath) {
		t.Fatalf("outputs-under omitted broken manifest path: %v", err)
	}
}

func TestSymTailMalformedManifestErrorStartsWithPath(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	out := filepath.Join(root, "CLAUDE.md")
	if err := importData(codex, out, "CLAUDE.md", []byte("body\n"), false); err != nil {
		t.Fatal(err)
	}
	manifestPath := manifestOf(t, codex, "CLAUDE.md")
	mustWrite(t, manifestPath, []byte("out: not-quoted\nsource_sha256: abc\n"))
	_, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: root})
	if err == nil || !strings.HasPrefix(err.Error(), manifestPath+": invalid manifest:") {
		t.Fatalf("malformed manifest error should start with its path %q, got %v", manifestPath, err)
	}
}

func TestSymTailManifestParseErrorCarriesPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.yaml")
	_, err := parseManifest(path, []byte("out: not-quoted\nsource_sha256: abc\n"))
	var manifestErr *manifestError
	if !errors.As(err, &manifestErr) || manifestErr.path != path {
		t.Fatalf("parse error should carry manifest path %q, got %#v", path, err)
	}
}

func TestSymTailOutputsUnderRejectsSymlinkInOutputDirectory(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	actualDir := filepath.Join(root, "actual")
	linkDir := filepath.Join(root, "linked")
	if err := os.MkdirAll(actualDir, directoryMode); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(linkDir, "CLAUDE.md")
	if err := importData(codex, out, "CLAUDE.md", []byte("body\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	built, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(out); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(linkDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(actualDir, "CLAUDE.md"), built, fileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(actualDir, linkDir); err != nil {
		t.Fatal(err)
	}
	_, err = CheckWithWarnings(codex, CheckOptions{OutputsUnder: root})
	if err == nil || !strings.Contains(err.Error(), linkDir) || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("check should reject symlinked output directory %q, got %v", linkDir, err)
	}
}

func TestSymTailHeadPartsRejectsMalformedManifest(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "codex", "manifest", "broken.yaml")
	if err := os.MkdirAll(filepath.Dir(manifestPath), directoryMode); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, manifestPath, []byte("out: not-quoted\nsource_sha256: abc\n"))
	git := func(args ...string) error {
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %s: %w", args, output, err)
		}
		return nil
	}
	if err := git("init", "-q"); err != nil {
		t.Fatal(err)
	}
	if err := git("add", "codex/manifest/broken.yaml"); err != nil {
		t.Fatal(err)
	}
	if err := git("commit", "-q", "-m", "broken manifest"); err != nil {
		t.Fatal(err)
	}
	commit := &stagedPaths{root: root, paths: map[string]bool{"codex/manifest/broken.yaml": true}, index: map[string]string{}, modes: map[string]string{}, blobs: map[string][]byte{}}
	_, err := commit.headParts(filepath.Join(root, "codex", "manifest"))
	if err == nil || !strings.Contains(err.Error(), manifestPath) {
		t.Fatalf("headParts should report malformed HEAD manifest %q, got %v", manifestPath, err)
	}
}

func TestStagedIgnoresLinkOutsideRepository(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, directoryMode); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, "seed.txt"), []byte("seed\n"))
	codex := filepath.Join(root, "codex")
	target := filepath.Join(root, "CLAUDE.md")
	link := filepath.Join(root, "AGENTS.md")
	mustWrite(t, target, []byte("# body\n"))
	if err := os.Symlink("CLAUDE.md", link); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, link, ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) error {
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		cmd.Dir = repo
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %s: %w", args, output, err)
		}
		return nil
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"commit", "-q", "-m", "init"}} {
		if err := git(args...); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, filepath.Join(repo, "other.txt"), []byte("unrelated\n"))
	if err := git("add", "other.txt"); err != nil {
		t.Fatal(err)
	}
	if err := checkIn(t, repo, codex); err != nil {
		t.Fatalf("external broken link affected unrelated staged commit: %v", err)
	}
}

func TestImportBuildSynthetic(t *testing.T) {
	fixtures := []struct {
		name, rel string
		data      []byte
		count     int
		first     string
	}{
		{"paragraphs.md", "fixtures/paragraphs.md", fixture(t, "paragraphs.md"), 2, "First paragraph line one."},
		{"frontmatter.md", "fixtures/frontmatter.md", fixture(t, "frontmatter.md"), 3, "---\ntitle: fixture\n---\n"},
		{"nested-list.md", "fixtures/nested-list.md", fixture(t, "nested-list.md"), 2, "- parent\n  - nested\n    continuation\n"},
		{"fenced-list.md", "fixtures/fenced-list.md", fixture(t, "fenced-list.md"), 2, "- item\n"},
		{"empty", "fixtures/empty.md", nil, 0, ""},
		{"no-final-newline", "fixtures/no-final-newline.md", []byte("last bytes"), 1, "last bytes"},
		{"CRLF", "fixtures/crlf.md", []byte("one\r\n\r\ntwo\r\n"), 2, "one\r\n\r\n"},
		{"BOM", "fixtures/bom.md", []byte("\xef\xbb\xbftext\n"), 1, "\xef\xbb\xbftext\n"},
		{"tabs", "fixtures/tabs.md", []byte("- item\n\t- nested\n\t continuation\n- next\n"), 2, "- item\n\t- nested\n\t continuation\n"},
		{"numbered", "fixtures/numbered.md", []byte("1. first\n2. second\n"), 2, "1. first\n"},
		{"headings", "fixtures/headings.md", []byte("# One\n## Two\n"), 2, "# One\n"},
		{"single-paragraph", "fixtures/single.md", []byte("a\nb\nc\n"), 1, "a\nb\nc\n"},
	}
	for _, tc := range fixtures {
		t.Run(tc.name, func(t *testing.T) {
			codex := t.TempDir()
			out := filepath.Join(t.TempDir(), "out.md")
			if err := importData(codex, out, tc.rel, tc.data, false); err != nil {
				t.Fatal(err)
			}
			m, err := readManifest(filepath.Join(codex, "manifest", codexKey(tc.rel)+".yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if len(m.parts) != tc.count {
				t.Fatalf("got %d fragments, want %d", len(m.parts), tc.count)
			}
			if len(m.parts) > 0 {
				p := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md")
				b, e := os.ReadFile(p)
				if e != nil {
					t.Fatal(e)
				}
				body, e := fragmentBody(b)
				if e != nil {
					t.Fatal(e)
				}
				if !bytes.HasPrefix(body, []byte(tc.first)) {
					t.Fatalf("first body %q, want prefix %q", body, tc.first)
				}
			}
			if err := Build(codex, false); err != nil {
				t.Fatal(err)
			}
			if err := Build(codex, true); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tc.data) {
				t.Fatalf("built %q, want %q", got, tc.data)
			}
		})
	}
}

func TestImportUsesHomeEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	source := filepath.Join(home, "source.md")
	mustWrite(t, source, []byte("# source\n"))
	codex := filepath.Join(t.TempDir(), "codex")
	if err := Import(codex, source, false); err != nil {
		t.Fatalf("Import with source under HOME: %v", err)
	}
}

func TestStatusWithHomeUsesHomeEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codex := filepath.Join(t.TempDir(), "codex")
	out := filepath.Join(home, "source.md")
	mustWrite(t, out, []byte("original\n"))
	if err := ImportWithOptions(codex, out, ImportOptions{Home: home}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, out, []byte("changed\n"))
	entries, err := StatusWithHome(codex, "")
	if err != nil {
		t.Fatal(err)
	}
	want := "lex import --refresh --codex " + strconv.Quote(codex) + " " + strconv.Quote(out)
	if len(entries) != 1 || entries[0].Command != want {
		t.Fatalf("status command = %#v, want %q", entries, want)
	}
}

func TestImportPublicAPIIsIdempotentAndChecksSource(t *testing.T) {
	codex := t.TempDir()
	home := t.TempDir()
	data, err := os.ReadFile(filepath.Join("testdata", "paragraphs.md"))
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(home, "paragraphs.md")
	mustWrite(t, source, data)
	options := ImportOptions{Home: home}
	if err := ImportWithOptions(codex, source, options); err != nil {
		t.Fatal(err)
	}
	before := treeBytes(t, codex)
	if err := ImportWithOptions(codex, source, options); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, treeBytes(t, codex)) {
		t.Fatal("repeat import changed codex")
	}
	if err := Build(codex, true); err != nil {
		t.Fatal(err)
	}
}

func TestImportRefreshGuards(t *testing.T) {
	for _, mode := range []string{"unrefreshed", "edited", "unlisted"} {
		t.Run(mode, func(t *testing.T) {
			codex := t.TempDir()
			out := filepath.Join(t.TempDir(), "source.md")
			rel := "refresh/source.md"
			if err := importData(codex, out, rel, []byte("old\n"), false); err != nil {
				t.Fatal(err)
			}
			m, err := readManifest(filepath.Join(codex, "manifest", codexKey(rel)+".yaml"))
			if err != nil {
				t.Fatal(err)
			}
			frag := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md")
			switch mode {
			case "edited":
				b, _ := os.ReadFile(frag)
				mustWrite(t, frag, append(b, []byte("manual\n")...))
			case "unlisted":
				mustWrite(t, filepath.Join(filepath.Dir(frag), "extra.md"), []byte("extra"))
			}
			err = importData(codex, out, rel, []byte("new\n"), mode != "unrefreshed")
			if err == nil {
				t.Fatal("expected refresh error")
			}
			if mode == "unrefreshed" && !strings.Contains(err.Error(), "--refresh") {
				t.Fatalf("unexpected error: %v", err)
			}
			if mode != "unrefreshed" && !strings.Contains(err.Error(), "fragment") {
				t.Fatalf("expected fragment path diagnostic, got %v", err)
			}
			if _, statErr := os.Stat(frag); statErr != nil {
				t.Fatalf("preflight removed fragment: %v", statErr)
			}
		})
	}
}

func TestRefreshRejectsEditedHeader(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "source.md")
	rel := "refresh/header.md"
	if err := importData(codex, out, rel, []byte("old body\n"), false); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(codex, "manifest", codexKey(rel)+".yaml")
	m, err := readManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	fragment := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md")
	original, err := os.ReadFile(fragment)
	if err != nil {
		t.Fatal(err)
	}
	edited := bytes.Replace(original, []byte("цель: "), []byte("цель: важная цель"), 1)
	mustWrite(t, fragment, edited)
	err = importData(codex, out, rel, []byte("new body\n"), true)
	if err == nil || !strings.Contains(err.Error(), fragment) {
		t.Fatalf("expected fragment path in refresh error, got %v", err)
	}
	after, err := os.ReadFile(fragment)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, edited) {
		t.Fatal("refresh changed the manually edited header")
	}
}

func TestRefreshSuccessBuildsNewSource(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "source.md")
	rel := "refresh/success.md"
	if err := importData(codex, out, rel, []byte("old source\n"), false); err != nil {
		t.Fatal(err)
	}
	newSource := []byte("# New section\nnew body\n")
	if err := importData(codex, out, rel, newSource, true); err != nil {
		t.Fatalf("refresh should succeed without fragment edits: %v", err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, true); err != nil {
		t.Fatalf("refreshed codex should check cleanly: %v", err)
	}
	built, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(built, newSource) {
		t.Fatalf("built %q, want refreshed bytes %q", built, newSource)
	}
}

func TestImportKeyCollisionAndDeterminism(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	for _, codex := range []string{first, second} {
		if err := importData(codex, "/srv/example/a-b-c.md", "a/b-c.md", []byte("same\n"), false); err != nil {
			t.Fatal(err)
		}
		if err := importData(codex, "/srv/example/a-b-c.md", "a-b/c.md", []byte("same\n"), false); err != nil {
			t.Fatal(err)
		}
	}
	if codexKey("a/b-c.md") == codexKey("a-b/c.md") {
		t.Fatal("path key collision")
	}
	if !bytes.Equal(treeBytes(t, first), treeBytes(t, second)) {
		t.Fatal("imports are not deterministic")
	}
	if err := importData(first, "/another/out.md", "a/b-c.md", []byte("other\n"), true); err == nil || !strings.Contains(err.Error(), "already points") {
		t.Fatalf("expected out collision error, got %v", err)
	}
}

func TestBuildReportsAllMismatches(t *testing.T) {
	codex := t.TempDir()
	outA := filepath.Join(t.TempDir(), "a.md")
	outB := filepath.Join(t.TempDir(), "b.md")
	if err := importData(codex, outA, "a.md", []byte("A\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := importData(codex, outB, "b.md", []byte("B\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, outA, []byte("bad\n"))
	mustWrite(t, outB, []byte("bad\n"))
	err := Build(codex, true)
	if err == nil || !strings.Contains(err.Error(), outA) || !strings.Contains(err.Error(), outB) || !strings.Contains(err.Error(), "2 of 2 differ") {
		t.Fatalf("expected both mismatch paths and total, got %v", err)
	}
}

func TestBuildRejectsMalformedFragments(t *testing.T) {
	t.Run("missing final newline before next", func(t *testing.T) {
		codex := t.TempDir()
		out := filepath.Join(t.TempDir(), "out.md")
		rel := "bad/newline.md"
		if err := importData(codex, out, rel, []byte("# One\n\n# Two\n"), false); err != nil {
			t.Fatal(err)
		}
		m, _ := readManifest(filepath.Join(codex, "manifest", codexKey(rel)+".yaml"))
		p := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md")
		b, _ := os.ReadFile(p)
		mustWrite(t, p, bytes.TrimRight(b, "\n"))
		if err := Build(codex, false); err == nil || !strings.Contains(err.Error(), p) {
			t.Fatalf("expected path-specific missing newline error, got %v", err)
		}
	})
	t.Run("malformed header", func(t *testing.T) {
		codex := t.TempDir()
		out := filepath.Join(t.TempDir(), "out.md")
		rel := "bad/header.md"
		if err := importData(codex, out, rel, []byte("text\n"), false); err != nil {
			t.Fatal(err)
		}
		m, _ := readManifest(filepath.Join(codex, "manifest", codexKey(rel)+".yaml"))
		p := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md")
		b, _ := os.ReadFile(p)
		b = bytes.Replace(b, []byte("снять_когда:"), []byte("unknown:"), 1)
		mustWrite(t, p, b)
		if err := Build(codex, false); err == nil || !strings.Contains(err.Error(), p) {
			t.Fatalf("expected malformed-header path error, got %v", err)
		}
	})
}

func TestFragmentBodyPreservesBodyBeginningWithDelimiter(t *testing.T) {
	frag := []byte("---\nid: id\nцель: \nоснование: \nснять_когда: \n---\n---\n---\nx\n---\nY\n")
	body, err := fragmentBody(frag)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "---\n---\nx\n---\nY\n" {
		t.Fatalf("body lost bytes: %q", body)
	}
	if _, err := fragmentBody([]byte("---\nid: id\nextra: x\n---\nbody")); err == nil {
		t.Fatal("accepted invalid fragment header")
	}
}

func TestRemoveFailurePropagatesWithPath(t *testing.T) {
	codex := t.TempDir()
	want := errors.New("denied")
	err := removeOldFragments(codex, []string{"topic/part-1"}, func(string) error { return want })
	if !errors.Is(err, want) || !strings.Contains(err.Error(), filepath.Join(codex, "frag", "topic", "part-1.md")) {
		t.Fatalf("remove error lost path or cause: %v", err)
	}
}

func TestUTF8TruncationAndSlug(t *testing.T) {
	input := strings.Repeat("я", 130)
	if got := trunc(input); !utf8.ValidString(got) || len([]rune(got)) != 120 {
		t.Fatalf("invalid truncation: %q", got)
	}
	if got := slug([]byte(input)); !utf8.ValidString(got) || len(got) > 48 {
		t.Fatalf("invalid slug: %q", got)
	}
}

func TestCutoverBuildGuardAndForce(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.md")
	if err := importData(codex, out, "a.md", []byte("source\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, out, []byte("manual\n"))
	if err := Build(codex, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected guarded refusal, got %v", err)
	}
	b, _ := os.ReadFile(out)
	if string(b) != "manual\n" {
		t.Fatalf("overwrote manual edit: %q", b)
	}
	if err := BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(filepath.Join(codex, "manifest", codexKey("a.md")+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	frag := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md")
	raw, err := os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	body, err := fragmentBody(raw)
	if err != nil {
		t.Fatal(err)
	}
	updated := bytes.Replace(body, []byte("source"), []byte("next"), 1)
	mustWrite(t, frag, replaceFragmentBody(raw, body, updated))
	mustWrite(t, out, updated)
	if err := Build(codex, false); err != nil {
		t.Fatalf("output equal to changed build refused: %v", err)
	}
	m, err = readManifest(filepath.Join(codex, "manifest", codexKey("a.md")+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if m.builtHash != sha(updated) {
		t.Fatalf("built hash was not updated: %s", m.builtHash)
	}
	raw, err = os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	body, err = fragmentBody(raw)
	if err != nil {
		t.Fatal(err)
	}
	updated = bytes.Replace(body, []byte("next"), []byte("again"), 1)
	mustWrite(t, frag, replaceFragmentBody(raw, body, updated))
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatalf("second build after hash refresh failed: %v", err)
	}
}

func TestForeignCodexOutputGuard(t *testing.T) {
	root := t.TempDir()
	owner := filepath.Join(root, "A")
	foreign := filepath.Join(root, "B")
	out := filepath.Join(root, "source", "OUT.md")
	if err := os.MkdirAll(filepath.Dir(out), directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := importData(owner, out, "OUT.md", []byte("# Original\n\nBody\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := setManifestHeader(owner, "OUT.md", true); err != nil {
		t.Fatal(err)
	}
	if err := Build(owner, false); err != nil {
		t.Fatal(err)
	}
	ownerOutput, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := Absorb(owner, out, AbsorbOptions{}); err != nil {
		t.Fatalf("owner absorb refused: %v", err)
	}
	unchanged, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(ownerOutput, unchanged) {
		t.Fatalf("owner absorb changed output: %v", err)
	}
	if err := os.CopyFS(foreign, os.DirFS(owner)); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"build", func() error { return Build(foreign, false) }},
		{"absorb", func() error { return Absorb(foreign, out, AbsorbOptions{}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil || !strings.Contains(err.Error(), owner) || !strings.Contains(err.Error(), foreign) {
				t.Fatalf("expected refusal naming both codices, got %v", err)
			}
			got, readErr := os.ReadFile(out)
			if readErr != nil || !bytes.Equal(got, original) {
				t.Fatalf("output changed: %v", readErr)
			}
		})
	}

	importCodex := filepath.Join(root, "C")
	if err := ImportWithOptions(importCodex, out, ImportOptions{Home: root}); err == nil || !strings.Contains(err.Error(), owner) || !strings.Contains(err.Error(), importCodex) {
		t.Fatalf("expected import refusal naming both codices, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(importCodex, "manifest")); !os.IsNotExist(err) {
		t.Fatalf("import changed codex before refusal: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("import changed source: %v", err)
	}
	ownerSource := filepath.Join(root, "source", "OWNED.md")
	if err := os.WriteFile(ownerSource, original, fileMode); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(owner, ownerSource, ImportOptions{Home: root}); err != nil {
		t.Fatalf("owner import refused: %v", err)
	}
	ownerSourceAfter, err := os.ReadFile(ownerSource)
	if err != nil || !bytes.Equal(ownerSourceAfter, original) {
		t.Fatalf("owner import changed source: %v", err)
	}

	if err := BuildWithOptions(foreign, BuildOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	rewritten, err := os.ReadFile(out)
	if err != nil || !bytes.Contains(rewritten, []byte(foreign+"/manifest/OUT.md.yaml")) {
		t.Fatalf("force did not rewrite owner header: %v", err)
	}
	if err := Build(foreign, false); err != nil {
		t.Fatalf("owner build refused: %v", err)
	}
	stable, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(stable, rewritten) {
		t.Fatalf("owner build changed output: %v", err)
	}

	plainCodex := filepath.Join(root, "D")
	plain := filepath.Join(root, "source", "PLAIN.md")
	plainBody := []byte("# Plain\n\nNo generated header\n")
	if err := os.WriteFile(plain, plainBody, fileMode); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(plainCodex, plain, ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	if err := Build(plainCodex, false); err != nil {
		t.Fatal(err)
	}
	builtPlain, err := os.ReadFile(plain)
	if err != nil || !bytes.Equal(builtPlain, plainBody) {
		t.Fatalf("plain output behavior changed: %v", err)
	}
}

func TestForeignCodexHeaderAfterFrontMatterAndPlainYamlText(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "source", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(out), directoryMode); err != nil {
		t.Fatal(err)
	}
	body := []byte("---\nname: example\n---\n# Skill\n\nInstructions\n")
	owner := filepath.Join(root, "owner")
	if err := importData(owner, out, "SKILL.md", body, false); err != nil {
		t.Fatal(err)
	}
	if err := setManifestHeader(owner, "SKILL.md", true); err != nil {
		t.Fatal(err)
	}
	if err := Build(owner, false); err != nil {
		t.Fatal(err)
	}
	built, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	marker := []byte("<!-- lex: собран из ")
	if bytes.Index(built, marker) <= bytes.Index(built, []byte("---\nname: example\n---\n")) {
		t.Fatalf("generated marker is not after front matter: %q", built)
	}
	foreign := filepath.Join(root, "copy")
	if err := os.CopyFS(foreign, os.DirFS(owner)); err != nil {
		t.Fatal(err)
	}
	if err := Build(foreign, false); err == nil || !strings.Contains(err.Error(), owner) || !strings.Contains(err.Error(), foreign) {
		t.Fatalf("expected foreign-owner refusal for front-matter output, got %v", err)
	}
	unchanged, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(unchanged, built) {
		t.Fatalf("foreign build changed front-matter output: %v", err)
	}

	plain := filepath.Join(root, "source", "PLAIN.md")
	plainBody := []byte("# config.yaml. notes\n\nText\n")
	if err := os.WriteFile(plain, plainBody, fileMode); err != nil {
		t.Fatal(err)
	}
	plainCodex := filepath.Join(root, "plain-codex")
	if err := ImportWithOptions(plainCodex, plain, ImportOptions{Home: root}); err != nil {
		t.Fatal(err)
	}
	if err := Build(plainCodex, false); err != nil {
		t.Fatalf("plain .yaml. text was mistaken for a foreign header: %v", err)
	}
	plainBuilt, err := os.ReadFile(plain)
	if err != nil || !bytes.Equal(plainBuilt, plainBody) {
		t.Fatalf("plain output changed: %v", err)
	}
}

func TestForeignCodexUnderManifestWithManifestInBody(t *testing.T) {
	root := t.TempDir()
	owner := filepath.Join(root, "s", "manifest", "codex")
	foreign := filepath.Join(root, "s", "manifest", "codex-copy")
	out := filepath.Join(root, "source", "Q.md")
	if err := os.MkdirAll(filepath.Dir(out), directoryMode); err != nil {
		t.Fatal(err)
	}
	body := []byte("# Q\n\n- /manifest/y.yaml. appears in the body\n")
	if err := importData(owner, out, "Q.md", body, false); err != nil {
		t.Fatal(err)
	}
	if err := setManifestHeader(owner, "Q.md", true); err != nil {
		t.Fatal(err)
	}
	if err := Build(owner, false); err != nil {
		t.Fatal(err)
	}
	built, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := Build(owner, false); err != nil {
		t.Fatalf("repeat build for codex under manifest directory refused: %v", err)
	}
	unchanged, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(unchanged, built) {
		t.Fatalf("repeat owner build changed output: %v", err)
	}
	if err := os.CopyFS(foreign, os.DirFS(owner)); err != nil {
		t.Fatal(err)
	}
	if err := Build(foreign, false); err == nil || !strings.Contains(err.Error(), owner) || !strings.Contains(err.Error(), foreign) {
		t.Fatalf("expected copied codex refusal naming both codices, got %v", err)
	}
	unchanged, err = os.ReadFile(out)
	if err != nil || !bytes.Equal(unchanged, built) {
		t.Fatalf("copy build changed owner output: %v", err)
	}
}

func TestNestedCodexCannotOverwriteEitherOwner(t *testing.T) {
	root := t.TempDir()
	owner := filepath.Join(root, "A")
	nested := filepath.Join(owner, "manifest", "sub")
	out := filepath.Join(root, "source", "OUT.md")
	if err := os.MkdirAll(filepath.Dir(out), directoryMode); err != nil {
		t.Fatal(err)
	}
	body := []byte("# Shared output\n")
	if err := importData(owner, out, "OUT.md", body, false); err != nil {
		t.Fatal(err)
	}
	if err := setManifestHeader(owner, "OUT.md", true); err != nil {
		t.Fatal(err)
	}
	if err := Build(owner, false); err != nil {
		t.Fatal(err)
	}
	ownedByA, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	stagedCopy := filepath.Join(root, "B-copy")
	if err := os.CopyFS(stagedCopy, os.DirFS(owner)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(nested), directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(stagedCopy, nested); err != nil {
		t.Fatal(err)
	}
	if err := Build(nested, false); err == nil || !strings.Contains(err.Error(), owner) || !strings.Contains(err.Error(), nested) {
		t.Fatalf("expected nested B to refuse A output, got %v", err)
	}
	unchanged, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(unchanged, ownedByA) {
		t.Fatalf("nested B changed A output: %v", err)
	}
	if err := BuildWithOptions(nested, BuildOptions{Force: true}); err != nil {
		t.Fatalf("force build B: %v", err)
	}
	ownedByB, err := os.ReadFile(out)
	if err != nil || bytes.Equal(ownedByA, ownedByB) {
		t.Fatalf("force did not change owner header: %v", err)
	}
	if err := Build(owner, false); err == nil || !strings.Contains(err.Error(), owner) || !strings.Contains(err.Error(), nested) {
		t.Fatalf("expected A to refuse nested B output, got %v", err)
	}
	unchanged, err = os.ReadFile(out)
	if err != nil || !bytes.Equal(unchanged, ownedByB) {
		t.Fatalf("A changed nested B output: %v", err)
	}
}

func TestForeignCodexDiagnosticWithYamlDirectory(t *testing.T) {
	root := t.TempDir()
	owner := filepath.Join(root, "x.yaml.d", "codex")
	foreign := filepath.Join(root, "copy")
	out := filepath.Join(root, "source", "OUT.md")
	if err := os.MkdirAll(filepath.Dir(out), directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := importData(owner, out, "OUT.md", []byte("# Output\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := setManifestHeader(owner, "OUT.md", true); err != nil {
		t.Fatal(err)
	}
	if err := Build(owner, false); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(foreign, os.DirFS(owner)); err != nil {
		t.Fatal(err)
	}
	err = Build(foreign, false)
	if err == nil || !strings.Contains(err.Error(), owner) || !strings.Contains(err.Error(), foreign) {
		t.Fatalf("expected refusal naming full owner and caller paths, got %v", err)
	}
	after, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("foreign build changed output: %v", err)
	}
}

func TestCutoverAbsorbAndBoundaryDraft(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.md")
	original := []byte("# One\nalpha\n\n# Two\nbeta\n")
	if err := importData(codex, out, "a.md", original, false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	changed := []byte("# One\nchanged\n\n# Two\nbeta\n")
	mustWrite(t, out, changed)
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, true); err != nil {
		t.Fatal(err)
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("absorb left a draft warning: %v", warnings)
	}
	changed = []byte("# Changed changed\n\n# Two\nbeta\n")
	mustWrite(t, out, changed)
	err = Absorb(codex, out, AbsorbOptions{DraftDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "draft") {
		t.Fatalf("expected boundary draft conflict, got %v", err)
	}
}

func TestAbsorbRepeatedLineUsesLegacySingleFragmentRange(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.md")
	original := []byte("# T\n\n## R\n- B\n- B\n- C\n")
	if err := importData(codex, out, "repeat.md", original, false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	edited := bytes.Replace(original, []byte("- B\n"), []byte("- B правка\n"), 1)
	mustWrite(t, out, edited)
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(filepath.Join(codex, "manifest", codexKey("repeat.md")+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var bodies [][]byte
	for _, part := range m.parts {
		raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(part)+".md"))
		if err != nil {
			t.Fatal(err)
		}
		body, err := fragmentBody(raw)
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, body)
	}
	if !bytes.Equal(bytes.Join(bodies, nil), edited) {
		t.Fatalf("absorbed output = %q, want %q", bytes.Join(bodies, nil), edited)
	}
	if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
		t.Fatalf("check after absorb: %v", err)
	}
}

func TestAbsorbWithoutFinalNewlineKeepsLastFragment(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.md")
	if err := importData(codex, out, "eof.md", []byte("# T\n\n## R\n- B\n- A\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	edited := []byte("# T\n\n## R\n- B\n- A")
	mustWrite(t, out, edited)
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(filepath.Join(codex, "manifest", codexKey("eof.md")+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var bodies [][]byte
	for _, part := range m.parts {
		raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(part)+".md"))
		if err != nil {
			t.Fatal(err)
		}
		body, err := fragmentBody(raw)
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, body)
	}
	if got := bytes.Join(bodies, nil); !bytes.Equal(got, edited) {
		t.Fatalf("absorbed output = %q, want %q", got, edited)
	}
	if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
		t.Fatalf("check after absorb: %v", err)
	}
}

func TestAbsorbRejectsEditedRepeatedManifestPart(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.md")
	if err := importData(codex, out, "duplicate.md", []byte("first\n\nsecond\n"), false); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(codex, "manifest", codexKey("duplicate.md")+".yaml")
	m, err := readManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.parts) != 2 {
		t.Fatalf("got %d parts, want 2", len(m.parts))
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(strconv.Quote(m.parts[1])), []byte(strconv.Quote(m.parts[0])), 1)
	mustWrite(t, manifestPath, raw)
	if err := BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	beforeFragments := treeBytes(t, filepath.Join(codex, "frag"))
	beforeManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, out, []byte("FIRST\n\nSECOND\n"))
	err = Absorb(codex, out, AbsorbOptions{})
	fragmentPath := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md")
	if err == nil || !strings.Contains(err.Error(), fragmentPath) {
		t.Fatalf("expected duplicate fragment diagnostic naming %s, got %v", fragmentPath, err)
	}
	afterManifest, readErr := os.ReadFile(manifestPath)
	if readErr != nil || !bytes.Equal(beforeManifest, afterManifest) {
		t.Fatalf("manifest changed after duplicate part refusal: %v", readErr)
	}
	if !bytes.Equal(beforeFragments, treeBytes(t, filepath.Join(codex, "frag"))) {
		t.Fatal("fragment changed after duplicate part refusal")
	}
}

func TestAbsorbInsertionsShiftFollowingFragmentAndUpdateHashes(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.md")
	original := []byte("# T\n\np1\np2\np3\n\n## B\n- b1\n- b2\n")
	if err := importData(codex, out, "shift.md", original, false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	edited := bytes.Replace(original, []byte("p1\n"), []byte("p1\nnew1\n"), 1)
	edited = bytes.Replace(edited, []byte("p2\n"), []byte("p2\nnew2\n"), 1)
	edited = bytes.Replace(edited, []byte("- b1\n"), []byte("- b1 правка\n"), 1)
	mustWrite(t, out, edited)
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(filepath.Join(codex, "manifest", codexKey("shift.md")+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var bodies [][]byte
	for _, part := range m.parts {
		path := filepath.Join(codex, "frag", filepath.FromSlash(part)+".md")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := fragmentBody(raw)
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, body)
		if m.fragmentHashes[part] != sha(raw) {
			t.Fatalf("manifest hash for %s does not match fragment", path)
		}
	}
	if got := bytes.Join(bodies, nil); !bytes.Equal(got, edited) {
		t.Fatalf("absorbed output = %q, want %q", got, edited)
	}
	if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
		t.Fatalf("check after absorb: %v", err)
	}
}

func TestAbsorbHeaderBuiltHashIncludesGeneratedHeader(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.md")
	if err := importData(codex, out, "header-absorb.md", []byte("# T\n\n## A\n- a1\n\n## B\n- b1\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := setManifestHeader(codex, "header-absorb.md", true); err != nil {
		t.Fatal(err)
	}
	if err := BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	edited, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	edited = bytes.Replace(edited, []byte("- a1\n"), []byte("- a1 правка\n"), 1)
	edited = bytes.Replace(edited, []byte("- b1\n"), []byte("- b1 правка\n"), 1)
	mustWrite(t, out, edited)
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(filepath.Join(codex, "manifest", codexKey("header-absorb.md")+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if m.builtHash != sha(edited) {
		t.Fatalf("built_sha256 = %s, want hash of full output %s", m.builtHash, sha(edited))
	}
	if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
		t.Fatalf("check after absorb: %v", err)
	}
}

func TestAbsorbMultipleDiffHunksAcrossFragments(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.md")
	if err := importData(codex, out, "multi.md", []byte("alpha\n\nbeta\n\ngamma\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, out, []byte("ALPHA\n\nbeta\n\nGAMMA\n"))
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(filepath.Join(codex, "manifest", codexKey("multi.md")+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.parts) != 3 {
		t.Fatalf("got %d fragments, want 3", len(m.parts))
	}
	for i, want := range []string{"ALPHA\n\n", "beta\n\n", "GAMMA\n"} {
		raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(m.parts[i])+".md"))
		if err != nil {
			t.Fatal(err)
		}
		body, err := fragmentBody(raw)
		if err != nil || string(body) != want {
			t.Fatalf("fragment %d body = %q, %v; want %q", i, body, err, want)
		}
		if fragmentIsDraft(raw) {
			t.Fatalf("fragment %d draft=%v", i, fragmentIsDraft(raw))
		}
	}
	got, err := os.ReadFile(filepath.Join(codex, "manifest", codexKey("multi.md")+".yaml"))
	if err != nil || !bytes.Contains(got, []byte("built_sha256: "+sha([]byte("ALPHA\n\nbeta\n\nGAMMA\n")))) {
		t.Fatalf("built hash not updated: %v", err)
	}
}

func TestAbsorbMultipleHunksWithinOneFragment(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.md")
	if err := importData(codex, out, "same.md", []byte("one\nkeep\nthree\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, out, []byte("ONE\nkeep\nTHREE\n"))
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(filepath.Join(codex, "manifest", codexKey("same.md")+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := fragmentBody(raw)
	if err != nil || string(body) != "ONE\nkeep\nTHREE\n" || fragmentIsDraft(raw) {
		t.Fatalf("body = %q draft=%v err=%v", body, fragmentIsDraft(raw), err)
	}
}

func TestAbsorbAddBetweenFragments(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "between.md")
	original := []byte("alpha\n\nbeta\n")
	if err := importData(codex, out, "between.md", original, false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, out, []byte("alpha\nnew rule\nbeta\n"))
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(filepath.Join(codex, "manifest", codexKey("between.md")+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.parts) != 3 || !strings.HasSuffix(m.parts[1], "/new-rule-1") {
		t.Fatalf("parts = %v", m.parts)
	}
	if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestAbsorbMergeAndMoveDoesNotMisnameUntouchedFragment(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "merge-move.md")
	original := []byte("first\n\nsecond\n\nthird\n")
	if err := importData(codex, out, "merge-move.md", original, false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(codex, "manifest", codexKey("merge-move.md")+".yaml")
	beforeManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeFragments := treeBytes(t, filepath.Join(codex, "frag"))
	mustWrite(t, out, []byte("third\n\nfirst second\n"))
	edited := []byte("third\n\nfirst second\n")
	err = Absorb(codex, out, AbsorbOptions{DraftDir: codex})
	fragment := filepath.Join(codex, "frag")
	if err == nil {
		if err := Build(codex, false); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(out)
		if err != nil || !bytes.Equal(got, edited) {
			t.Fatalf("rebuilt output = %q, want %q (read error: %v)", got, edited, err)
		}
	} else {
		if !strings.Contains(err.Error(), fragment) || !strings.Contains(err.Error(), "fragment") {
			t.Fatalf("expected diagnostic to name affected fragment, got %v", err)
		}
		afterManifest, readErr := os.ReadFile(manifestPath)
		if readErr != nil || !bytes.Equal(beforeManifest, afterManifest) {
			t.Fatalf("manifest changed after refusal: %v", readErr)
		}
		if !bytes.Equal(beforeFragments, treeBytes(t, filepath.Join(codex, "frag"))) {
			t.Fatal("fragments changed after refusal")
		}
	}
}

func TestAbsorbDeleteAndInsertKeepsRenderedOrder(t *testing.T) {
	for _, tc := range []struct {
		name, original, edited string
	}{
		{"before-next", "first\n\nsecond\n\nthird\n", "first\n\nnew\n\nthird\n"},
		{"append", "first\n\nsecond\n", "first\n\nnew\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codex, out := t.TempDir(), filepath.Join(t.TempDir(), "delete-insert.md")
			if err := importData(codex, out, "delete-insert.md", []byte(tc.original), false); err != nil {
				t.Fatal(err)
			}
			if err := Build(codex, false); err != nil {
				t.Fatal(err)
			}
			mustWrite(t, out, []byte(tc.edited))
			if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
				t.Fatalf("absorb: %v", err)
			}
			assertAbsorbedOutputEquals(t, codex, out, []byte(tc.edited))
		})
	}
}

func assertAbsorbedOutputEquals(t *testing.T, codex, out string, want []byte) {
	t.Helper()
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("absorbed output = %q, want %q", got, want)
	}
}

func TestAbsorbMoveWithoutMergeRejectsInvalidInsertionPosition(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "move.md")
	if err := importData(codex, out, "move.md", []byte("first\n\nsecond\n\nthird\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(codex, "manifest", codexKey("move.md")+".yaml")
	beforeManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeFragments := treeBytes(t, filepath.Join(codex, "frag"))
	mustWrite(t, out, []byte("third\n\nfirst\n\nsecond\n"))
	err = Absorb(codex, out, AbsorbOptions{})
	if err == nil || !strings.Contains(err.Error(), "invalid edited fragment line bounds") {
		t.Fatalf("expected invalid edited fragment line bounds, got %v", err)
	}
	afterManifest, readErr := os.ReadFile(manifestPath)
	if readErr != nil || !bytes.Equal(beforeManifest, afterManifest) {
		t.Fatalf("manifest changed after refusal: %v", readErr)
	}
	if !bytes.Equal(beforeFragments, treeBytes(t, filepath.Join(codex, "frag"))) {
		t.Fatal("fragments changed after refusal")
	}
}

func TestAbsorbAddAtEndOfSectionAndFile(t *testing.T) {
	for _, tc := range []struct {
		name             string
		original, edited []byte
	}{
		{"section", []byte("# A\nalpha\n\n# B\nbeta\n"), []byte("# A\nalpha\nnew rule\n\n# B\nbeta\n")},
		{"file", []byte("alpha\n"), []byte("alpha\nnew rule\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codex, out := t.TempDir(), filepath.Join(t.TempDir(), "end.md")
			if err := importData(codex, out, "end.md", tc.original, false); err != nil {
				t.Fatal(err)
			}
			if err := Build(codex, false); err != nil {
				t.Fatal(err)
			}
			mustWrite(t, out, tc.edited)
			if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
				t.Fatal(err)
			}
			m, err := readManifest(filepath.Join(codex, "manifest", codexKey("end.md")+".yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if len(m.parts) != len(split(tc.original))+1 {
				t.Fatalf("parts = %v", m.parts)
			}
			newPart := -1
			for i, id := range m.parts {
				if strings.Contains(id, "/new-rule-") {
					newPart = i
					break
				}
			}
			if newPart < 0 {
				t.Fatalf("new fragment missing: %v", m.parts)
			}
			raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(m.parts[newPart])+".md"))
			if err != nil {
				t.Fatal(err)
			}
			body, err := fragmentBody(raw)
			if err != nil || !bytes.Contains(body, []byte("new rule")) {
				t.Fatalf("body = %q, %v", body, err)
			}
		})
	}
}

func TestAbsorbRemoveFragment(t *testing.T) {
	codex, out := t.TempDir(), filepath.Join(t.TempDir(), "remove.md")
	if err := importData(codex, out, "remove.md", []byte("alpha\n\nbeta\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(filepath.Join(codex, "manifest", codexKey("remove.md")+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	removed := m.parts[1]
	mustWrite(t, out, []byte("alpha\n"))
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatal(err)
	}
	m, err = readManifest(filepath.Join(codex, "manifest", codexKey("remove.md")+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(m.parts, removed) {
		t.Fatalf("removed fragment remains in manifest: %v", m.parts)
	}
	if _, err := os.Stat(filepath.Join(codex, "frag", filepath.FromSlash(removed)+".md")); !os.IsNotExist(err) {
		t.Fatalf("removed fragment still exists: %v", err)
	}
}

func TestAbsorbRemoveSharedFragmentKeepsFile(t *testing.T) {
	codex := t.TempDir()
	outA, outB := filepath.Join(t.TempDir(), "a.md"), filepath.Join(t.TempDir(), "b.md")
	if err := importData(codex, outA, "a.md", []byte("alpha\n\nbeta\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := importData(codex, outB, "b.md", []byte("beta\n"), false); err != nil {
		t.Fatal(err)
	}
	manifestA := filepath.Join(codex, "manifest", codexKey("a.md")+".yaml")
	manifestB := filepath.Join(codex, "manifest", codexKey("b.md")+".yaml")
	ma, err := readManifest(manifestA)
	if err != nil {
		t.Fatal(err)
	}
	mb, err := readManifest(manifestB)
	if err != nil {
		t.Fatal(err)
	}
	shared := ma.parts[1]
	orphan := mb.parts[0]
	raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(shared)+".md"))
	if err != nil {
		t.Fatal(err)
	}
	mb.parts, mb.fragmentHashes, mb.hash, mb.builtHash = []string{shared}, map[string]string{shared: sha(raw)}, sha([]byte("beta\n")), sha([]byte("beta\n"))
	if err := writeManifest(manifestB, mb, mb.links); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(codex, "frag", filepath.FromSlash(orphan)+".md")); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, outA, []byte("alpha\n"))
	if err := Absorb(codex, outA, AbsorbOptions{}); err != nil {
		t.Fatal(err)
	}
	ma, err = readManifest(manifestA)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(ma.parts, shared) {
		t.Fatalf("shared part remains in A: %v", ma.parts)
	}
	if _, err := os.Stat(filepath.Join(codex, "frag", filepath.FromSlash(shared)+".md")); err != nil {
		t.Fatalf("shared fragment was deleted: %v", err)
	}
	if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestAbsorbDraftClearedAfterCommit(t *testing.T) {
	repo := t.TempDir()
	codex, out := filepath.Join(repo, "cx"), filepath.Join(repo, "out.md")
	if err := importData(codex, out, "out.md", []byte("original\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	fragment := fragmentOf(t, codex, "out.md")
	data, err := os.ReadFile(fragment)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, fragment, bytes.Replace(data, []byte("снять_когда: \n---\n"), []byte("снять_когда: \ndraft: true\n---\n"), 1))
	mustWrite(t, out, []byte("edited\n"))
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "test"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s %v", args, output, err)
		}
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil || len(warnings) != 0 {
		t.Fatalf("check after commit: warnings=%v err=%v", warnings, err)
	}
}

func TestAbsorbBoundaryHunkMixedWithValidHunkIsAtomic(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.md")
	if err := importData(codex, out, "mixed.md", []byte("alpha\nkeep\n# Boundary\nbeta\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	before := treeBytes(t, filepath.Join(codex, "frag"))
	edited := []byte("ALPHA\nkeep\n# Changed\nBETA\n")
	mustWrite(t, out, edited)
	err := Absorb(codex, out, AbsorbOptions{DraftDir: codex})
	if err == nil || !strings.Contains(err.Error(), "draft") {
		t.Fatalf("expected boundary draft, got %v", err)
	}
	if !bytes.Equal(before, treeBytes(t, filepath.Join(codex, "frag"))) {
		t.Fatal("mixed valid and boundary hunks partially changed fragments")
	}
}

func TestAbsorbSharedFragmentPreflightAcrossHunks(t *testing.T) {
	codex := t.TempDir()
	outA, outB := filepath.Join(t.TempDir(), "a.md"), filepath.Join(t.TempDir(), "b.md")
	if err := importData(codex, outA, "a.md", []byte("alpha\n\nbeta\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := importData(codex, outB, "b.md", []byte("other\n"), false); err != nil {
		t.Fatal(err)
	}
	manifestA := filepath.Join(codex, "manifest", codexKey("a.md")+".yaml")
	manifestB := filepath.Join(codex, "manifest", codexKey("b.md")+".yaml")
	ma, err := readManifest(manifestA)
	if err != nil {
		t.Fatal(err)
	}
	mb, err := readManifest(manifestB)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(manifestB)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.ReplaceAll(raw, []byte(strconv.Quote(mb.parts[0])), []byte(strconv.Quote(ma.parts[1])))
	mustWrite(t, manifestB, raw)
	if err := BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	before := treeBytes(t, filepath.Join(codex, "frag"))
	mustWrite(t, outA, []byte("ALPHA\n\nBETA\n"))
	err = Absorb(codex, outA, AbsorbOptions{})
	if err == nil || !strings.Contains(err.Error(), "--shared") {
		t.Fatalf("expected shared fragment diagnostic, got %v", err)
	}
	if !bytes.Equal(before, treeBytes(t, filepath.Join(codex, "frag"))) {
		t.Fatal("shared-fragment error partially changed fragments")
	}
}

func TestStatusUsesBuiltHashAndSuggestsCommandByHeader(t *testing.T) {
	t.Run("build after fragment edit", func(t *testing.T) {
		codex := t.TempDir()
		out := filepath.Join(t.TempDir(), "built.md")
		if err := importData(codex, out, "built.md", []byte("before\n"), false); err != nil {
			t.Fatal(err)
		}
		if err := setManifestHeader(codex, "built.md", true); err != nil {
			t.Fatal(err)
		}
		if err := Build(codex, false); err != nil {
			t.Fatal(err)
		}
		m, err := readManifest(filepath.Join(codex, "manifest", codexKey("built.md")+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		fragment := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md")
		raw, err := os.ReadFile(fragment)
		if err != nil {
			t.Fatal(err)
		}
		body, err := fragmentBody(raw)
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, fragment, replaceFragmentBody(raw, body, []byte("after\n")))
		if err := Build(codex, false); err != nil {
			t.Fatal(err)
		}
		entries, err := StatusWithHome(codex, filepath.Dir(out))
		if err != nil || len(entries) != 0 {
			t.Fatalf("status after build = %#v, %v", entries, err)
		}
	})

	t.Run("absorb", func(t *testing.T) {
		codex := t.TempDir()
		out := filepath.Join(t.TempDir(), "built.md")
		if err := importData(codex, out, "built.md", []byte("before\n"), false); err != nil {
			t.Fatal(err)
		}
		if err := setManifestHeader(codex, "built.md", true); err != nil {
			t.Fatal(err)
		}
		if err := Build(codex, false); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, out, []byte("after\n"))
		if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
			t.Fatal(err)
		}
		entries, err := StatusWithHome(codex, filepath.Dir(out))
		if err != nil || len(entries) != 0 {
			t.Fatalf("status after absorb = %#v, %v", entries, err)
		}
	})

	t.Run("edited built output suggests absorb", func(t *testing.T) {
		codex := t.TempDir()
		out := filepath.Join(t.TempDir(), "built.md")
		if err := importData(codex, out, "built.md", []byte("before\n"), false); err != nil {
			t.Fatal(err)
		}
		if err := setManifestHeader(codex, "built.md", true); err != nil {
			t.Fatal(err)
		}
		if err := Build(codex, false); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, out, []byte("edited\n"))
		entries, err := StatusWithHome(codex, filepath.Dir(out))
		if err != nil || len(entries) != 1 || !strings.Contains(entries[0].Command, "lex absorb --codex") {
			t.Fatalf("status = %#v, %v; want absorb command", entries, err)
		}
	})

	t.Run("edited source suggests refresh", func(t *testing.T) {
		codex := t.TempDir()
		out := filepath.Join(t.TempDir(), "source.md")
		if err := importData(codex, out, "source.md", []byte("before\n"), false); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, out, []byte("edited\n"))
		entries, err := StatusWithHome(codex, filepath.Dir(out))
		if err != nil || len(entries) != 1 || !strings.Contains(entries[0].Command, "lex import --refresh --codex") {
			t.Fatalf("status = %#v, %v; want refresh command", entries, err)
		}
	})

	t.Run("legacy manifest uses source hash", func(t *testing.T) {
		codex := t.TempDir()
		out := filepath.Join(t.TempDir(), "source.md")
		if err := importData(codex, out, "source.md", []byte("before\n"), false); err != nil {
			t.Fatal(err)
		}
		manifestPath := filepath.Join(codex, "manifest", codexKey("source.md")+".yaml")
		raw, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, manifestPath, bytes.Replace(raw, []byte("built_sha256: "+sha([]byte("before\n"))+"\n"), nil, 1))
		mustWrite(t, out, []byte("edited\n"))
		entries, err := StatusWithHome(codex, filepath.Dir(out))
		if err != nil || len(entries) != 1 || !strings.Contains(entries[0].Command, "lex import --refresh --codex") {
			t.Fatalf("status = %#v, %v; want legacy refresh command", entries, err)
		}
	})

	t.Run("legacy built manifest suggests absorb", func(t *testing.T) {
		codex := t.TempDir()
		out := filepath.Join(t.TempDir(), "built.md")
		if err := importData(codex, out, "built.md", []byte("before\n"), false); err != nil {
			t.Fatal(err)
		}
		if err := setManifestHeader(codex, "built.md", true); err != nil {
			t.Fatal(err)
		}
		if err := Build(codex, false); err != nil {
			t.Fatal(err)
		}
		manifestPath := filepath.Join(codex, "manifest", codexKey("built.md")+".yaml")
		raw, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatal(err)
		}
		built, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, manifestPath, bytes.Replace(raw, []byte("built_sha256: "+sha(built)+"\n"), nil, 1))
		mustWrite(t, out, []byte("edited\n"))
		entries, err := StatusWithHome(codex, filepath.Dir(out))
		if err != nil || len(entries) != 1 || !strings.Contains(entries[0].Command, "lex absorb --codex") {
			t.Fatalf("status = %#v, %v; want legacy absorb command", entries, err)
		}
	})
}

func TestStatusUsesGeneratedOutputAndRefreshability(t *testing.T) {
	t.Run("refresh with generated header stays clean", func(t *testing.T) {
		root := t.TempDir()
		codex := filepath.Join(root, "codex")
		out := filepath.Join(root, "refresh.md")
		mustWrite(t, out, []byte("item one\n"))
		if err := ImportWithOptions(codex, out, ImportOptions{Home: root}); err != nil {
			t.Fatal(err)
		}
		if err := setManifestHeader(codex, "refresh.md", true); err != nil {
			t.Fatal(err)
		}
		if err := Build(codex, false); err != nil {
			t.Fatal(err)
		}
		built, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, out, bytes.Replace(built, []byte("item one"), []byte("item one edited"), 1))
		if err := ImportWithOptions(codex, out, ImportOptions{Home: root, Refresh: true}); err != nil {
			t.Fatal(err)
		}
		entries, err := StatusWithHome(codex, root)
		if err != nil || len(entries) != 0 {
			t.Fatalf("status after refresh = %#v, %v", entries, err)
		}
	})

	t.Run("fragment edits suggest absorb without header", func(t *testing.T) {
		codex := t.TempDir()
		out := filepath.Join(t.TempDir(), "plain.md")
		if err := importData(codex, out, "plain.md", []byte("line one\n"), false); err != nil {
			t.Fatal(err)
		}
		if err := Build(codex, false); err != nil {
			t.Fatal(err)
		}
		m, err := readManifest(filepath.Join(codex, "manifest", codexKey("plain.md")+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		fragment := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md")
		raw, err := os.ReadFile(fragment)
		if err != nil {
			t.Fatal(err)
		}
		body, err := fragmentBody(raw)
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, fragment, replaceFragmentBody(raw, body, []byte("line one rebuilt\n")))
		if err := Build(codex, false); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, out, []byte("line one rebuilt by hand\n"))
		entries, err := StatusWithHome(codex, filepath.Dir(out))
		if err != nil || len(entries) != 1 || !strings.Contains(entries[0].Command, "lex absorb --codex") {
			t.Fatalf("status = %#v, %v; want absorb command", entries, err)
		}
	})

	t.Run("legacy manifest matches current fragments", func(t *testing.T) {
		codex := t.TempDir()
		out := filepath.Join(t.TempDir(), "legacy.md")
		if err := importData(codex, out, "legacy.md", []byte("before\n"), false); err != nil {
			t.Fatal(err)
		}
		if err := Build(codex, false); err != nil {
			t.Fatal(err)
		}
		m, err := readManifest(filepath.Join(codex, "manifest", codexKey("legacy.md")+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		fragment := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md")
		raw, err := os.ReadFile(fragment)
		if err != nil {
			t.Fatal(err)
		}
		body, err := fragmentBody(raw)
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, fragment, replaceFragmentBody(raw, body, []byte("after\n")))
		if err := Build(codex, false); err != nil {
			t.Fatal(err)
		}
		manifestPath := filepath.Join(codex, "manifest", codexKey("legacy.md")+".yaml")
		manifestData, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatal(err)
		}
		legacy := bytes.Replace(manifestData, []byte("built_sha256: "+sha([]byte("after\n"))+"\n"), nil, 1)
		if bytes.Contains(legacy, []byte("built_sha256")) {
			t.Fatalf("manifest still records the build hash: %s", legacy)
		}
		mustWrite(t, manifestPath, legacy)
		entries, err := StatusWithHome(codex, filepath.Dir(out))
		if err != nil || len(entries) != 0 {
			t.Fatalf("status for matching legacy output = %#v, %v", entries, err)
		}
	})

	t.Run("removed generated header remains changed", func(t *testing.T) {
		codex := t.TempDir()
		out := filepath.Join(t.TempDir(), "built.md")
		if err := importData(codex, out, "built.md", []byte("body\n"), false); err != nil {
			t.Fatal(err)
		}
		if err := setManifestHeader(codex, "built.md", true); err != nil {
			t.Fatal(err)
		}
		if err := Build(codex, false); err != nil {
			t.Fatal(err)
		}
		manifestPath := filepath.Join(codex, "manifest", codexKey("built.md")+".yaml")
		built, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, out, withoutGeneratedHeader(built, manifestPath))
		entries, err := StatusWithHome(codex, filepath.Dir(out))
		if err != nil || len(entries) != 1 {
			t.Fatalf("status after header removal = %#v, %v; want changed file", entries, err)
		}
	})
}

func TestStatusConflictDiagnosticsAndPartialFragmentErrors(t *testing.T) {
	t.Run("header and fragment changes offer both ways", func(t *testing.T) {
		for _, header := range []bool{true, false} {
			name := "without header"
			if header {
				name = "with header"
			}
			t.Run(name, func(t *testing.T) {
				codex := t.TempDir()
				out := filepath.Join(t.TempDir(), "conflict.md")
				if err := importData(codex, out, "conflict.md", []byte("alpha one\nbeta two\n"), false); err != nil {
					t.Fatal(err)
				}
				if header {
					if err := setManifestHeader(codex, "conflict.md", true); err != nil {
						t.Fatal(err)
					}
				}
				if err := Build(codex, false); err != nil {
					t.Fatal(err)
				}
				manifestPath := filepath.Join(codex, "manifest", codexKey("conflict.md")+".yaml")
				m, err := readManifest(manifestPath)
				if err != nil {
					t.Fatal(err)
				}
				fragment := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[len(m.parts)-1])+".md")
				raw, err := os.ReadFile(fragment)
				if err != nil {
					t.Fatal(err)
				}
				body, err := fragmentBody(raw)
				if err != nil {
					t.Fatal(err)
				}
				mustWrite(t, fragment, replaceFragmentBody(raw, body, []byte("beta in fragment\n")))
				built, err := os.ReadFile(out)
				if err != nil {
					t.Fatal(err)
				}
				mustWrite(t, out, bytes.Replace(built, []byte("alpha one"), []byte("alpha in file"), 1))
				entries, err := StatusWithHome(codex, filepath.Dir(out))
				if err != nil || len(entries) != 1 {
					t.Fatalf("status = %#v, %v; want one conflict", entries, err)
				}
				if !strings.HasPrefix(entries[0].Command, "lex absorb --codex ") || !strings.HasPrefix(entries[0].KeepFragments, "lex build --force --codex ") || entries[0].Problem != "" {
					t.Errorf("conflict entry = %#v; want absorb to keep the file and build --force to keep the fragments", entries[0])
				}
			})
		}
	})

	t.Run("missing fragment does not hide other files", func(t *testing.T) {
		root := t.TempDir()
		codex := filepath.Join(root, "codex")
		lost := filepath.Join(root, "lost.md")
		kept := filepath.Join(root, "kept.md")
		if err := importData(codex, lost, "a-lost.md", []byte("lost line\n"), false); err != nil {
			t.Fatal(err)
		}
		if err := importData(codex, kept, "b-kept.md", []byte("kept line\n"), false); err != nil {
			t.Fatal(err)
		}
		if err := setManifestHeader(codex, "a-lost.md", true); err != nil {
			t.Fatal(err)
		}
		if err := setManifestHeader(codex, "b-kept.md", true); err != nil {
			t.Fatal(err)
		}
		if err := Build(codex, false); err != nil {
			t.Fatal(err)
		}
		m, err := readManifest(filepath.Join(codex, "manifest", codexKey("a-lost.md")+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		fragment := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md")
		if err := os.Remove(fragment); err != nil {
			t.Fatal(err)
		}
		lostData, err := os.ReadFile(lost)
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, lost, bytes.Replace(lostData, []byte("lost line"), []byte("lost edited"), 1))
		keptData, err := os.ReadFile(kept)
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, kept, bytes.Replace(keptData, []byte("kept line"), []byte("kept edited"), 1))
		entries, err := StatusWithHome(codex, root)
		if err != nil || len(entries) != 2 {
			t.Fatalf("status = %#v, %v; want two entries", entries, err)
		}
		foundLost, foundKept := false, false
		for _, entry := range entries {
			switch entry.Output {
			case lost:
				foundLost = strings.Count(entry.Problem, fragment) == 1 && !entry.ProblemFile && entry.Command == ""
			case kept:
				foundKept = strings.HasPrefix(entry.Command, "lex absorb --codex ") && entry.KeepFragments == "" && entry.Problem == ""
			}
		}
		if !foundLost || !foundKept {
			t.Fatalf("status entries = %#v; missing fragment error or kept file command", entries)
		}
	})
}

func TestRefreshStoresDiskHashAndRecognizesHeaderVariants(t *testing.T) {
	t.Run("refresh stores complete file hash and next edit suggests absorb", func(t *testing.T) {
		root := t.TempDir()
		codex := filepath.Join(root, "codex")
		out := filepath.Join(root, "refresh.md")
		mustWrite(t, out, []byte("item one\n"))
		if err := ImportWithOptions(codex, out, ImportOptions{Home: root}); err != nil {
			t.Fatal(err)
		}
		if err := setManifestHeader(codex, "refresh.md", true); err != nil {
			t.Fatal(err)
		}
		if err := Build(codex, false); err != nil {
			t.Fatal(err)
		}
		built, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, out, bytes.Replace(built, []byte("item one"), []byte("item one edited"), 1))
		if err := ImportWithOptions(codex, out, ImportOptions{Home: root, Refresh: true}); err != nil {
			t.Fatal(err)
		}
		disk, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		m, err := readManifest(filepath.Join(codex, "manifest", codexKey("refresh.md")+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if m.builtHash != sha(disk) {
			t.Fatalf("built hash = %s, want disk hash %s", m.builtHash, sha(disk))
		}
		mustWrite(t, out, bytes.Replace(disk, []byte("item one edited"), []byte("item one edited again"), 1))
		entries, err := StatusWithHome(codex, root)
		if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Command, "lex absorb --codex ") || entries[0].KeepFragments != "" {
			t.Fatalf("status after next file edit = %#v, %v; want only absorb", entries, err)
		}
	})

	t.Run("alternate generated header is equivalent", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("HOME", root)
		codex := filepath.Join(root, "codex")
		out := filepath.Join(root, "document.md")
		if err := importData(codex, out, "document.md", []byte("document body\n"), false); err != nil {
			t.Fatal(err)
		}
		if err := setManifestHeader(codex, "document.md", true); err != nil {
			t.Fatal(err)
		}
		if err := Build(codex, false); err != nil {
			t.Fatal(err)
		}
		built, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		alternate := bytes.ReplaceAll(built, []byte("~/codex"), []byte(resolvePath(codex)))
		if bytes.Equal(alternate, built) {
			t.Fatal("generated header did not contain preferred home-relative path")
		}
		mustWrite(t, out, alternate)
		entries, err := StatusWithHome(codex, root)
		if err != nil || len(entries) != 0 {
			t.Fatalf("status for alternate generated header = %#v, %v", entries, err)
		}
	})
}

func TestStatusLegacyFragmentsAndHints(t *testing.T) {
	for _, header := range []bool{false, true} {
		name := "without header"
		if header {
			name = "with header"
		}
		t.Run(name, func(t *testing.T) {
			codex := t.TempDir()
			out := filepath.Join(t.TempDir(), "legacy.md")
			source := []byte("# Start\n\n- alpha old\n\n## Interlude\n\n- beta old\n")
			if err := importData(codex, out, "legacy.md", source, false); err != nil {
				t.Fatal(err)
			}
			if header {
				if err := setManifestHeader(codex, "legacy.md", true); err != nil {
					t.Fatal(err)
				}
			}
			if err := Build(codex, false); err != nil {
				t.Fatal(err)
			}
			manifestPath := filepath.Join(codex, "manifest", codexKey("legacy.md")+".yaml")
			manifestData, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			manifestData = bytes.Replace(manifestData, []byte("built_sha256: "+sha(source)+"\n"), nil, 1)
			mustWrite(t, manifestPath, manifestData)
			m, err := readManifest(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			fragment := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[len(m.parts)-1])+".md")
			raw, err := os.ReadFile(fragment)
			if err != nil {
				t.Fatal(err)
			}
			body, err := fragmentBody(raw)
			if err != nil {
				t.Fatal(err)
			}
			mustWrite(t, fragment, replaceFragmentBody(raw, body, []byte("beta in fragment\n")))
			entries, err := StatusWithHome(codex, filepath.Dir(out))
			if err != nil || len(entries) != 0 {
				t.Fatalf("status after fragment-only edit = %#v, %v; want clean", entries, err)
			}
			built, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			mustWrite(t, out, bytes.Replace(built, []byte("alpha old"), []byte("alpha in file"), 1))
			entries, err = StatusWithHome(codex, filepath.Dir(out))
			if err != nil || len(entries) != 1 {
				t.Fatalf("status after both edits = %#v, %v; want one entry", entries, err)
			}
			entry := entries[0]
			if !strings.HasPrefix(entry.Command, "lex absorb --codex ") ||
				!strings.HasPrefix(entry.KeepFragments, "lex build --force --codex ") {
				t.Fatalf("legacy conflict entry = %#v", entry)
			}
			if err := BuildWithOptions(codex, BuildOptions{Force: true, Output: out}); err != nil {
				t.Fatal(err)
			}
			entries, err = StatusWithHome(codex, filepath.Dir(out))
			if err != nil || len(entries) != 0 {
				t.Fatalf("status after suggested build --force = %#v, %v", entries, err)
			}
		})
	}
}

func TestStatusRefreshNoOpSuggestsAbsorb(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "back.md")
	source := []byte("# Back\n\n- back one\n")
	if err := importData(codex, out, "back.md", source, false); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, out, []byte("# Back\n\n- back one edited\n"))
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, out, source)
	entries, err := StatusWithHome(codex, filepath.Dir(out))
	if err != nil || len(entries) != 1 ||
		!strings.HasPrefix(entries[0].Command, "lex absorb --codex ") ||
		entries[0].KeepFragments != "" {
		t.Fatalf("status after restoring source text = %#v, %v; want one absorb", entries, err)
	}
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatalf("run suggested absorb: %v", err)
	}
	entries, err = StatusWithHome(codex, filepath.Dir(out))
	if err != nil || len(entries) != 0 {
		t.Fatalf("status after suggested absorb = %#v, %v; want clean", entries, err)
	}
}

func TestStatusMissingOutputContinues(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	missing := filepath.Join(root, "missing.md")
	present := filepath.Join(root, "present.md")
	if err := importData(codex, missing, "a-missing.md", []byte("missing text\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := importData(codex, present, "b-present.md", []byte("present text\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := setManifestHeader(codex, "a-missing.md", true); err != nil {
		t.Fatal(err)
	}
	if err := setManifestHeader(codex, "b-present.md", true); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	presentData, err := os.ReadFile(present)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, present, bytes.Replace(presentData, []byte("present text"), []byte("present edited"), 1))
	entries, err := StatusWithHome(codex, root)
	if err != nil || len(entries) != 2 {
		t.Fatalf("status = %#v, %v; want problem and changed output", entries, err)
	}
	foundMissing, foundPresent := false, false
	for _, entry := range entries {
		switch entry.Output {
		case missing:
			foundMissing = strings.Contains(entry.Problem, missing)
		case present:
			foundPresent = strings.HasPrefix(entry.Command, "lex absorb --codex ")
		}
	}
	if !foundMissing || !foundPresent {
		t.Fatalf("status entries = %#v; missing output path or other output command not reported", entries)
	}
	for _, entry := range entries {
		if entry.Output == missing && !entry.ProblemFile {
			t.Fatalf("missing output problem = %#v; want file read classification", entry)
		}
	}
	if err := BuildWithOptions(codex, BuildOptions{Force: true, Output: missing}); err != nil {
		t.Fatalf("rebuild missing output: %v", err)
	}
	if err := Absorb(codex, present, AbsorbOptions{}); err != nil {
		t.Fatalf("run suggested absorb: %v", err)
	}
	entries, err = StatusWithHome(codex, root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("status after repairs = %#v, %v; want clean", entries, err)
	}
}

func TestStatusHeaderFormAfterAbsorb(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	codex := filepath.Join(root, "codex")
	out := filepath.Join(root, "form.md")
	if err := importData(codex, out, "form.md", []byte("form text\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := setManifestHeader(codex, "form.md", true); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	built, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	alternate := bytes.ReplaceAll(built, []byte("~/codex"), []byte(resolvePath(codex)))
	if bytes.Equal(alternate, built) {
		t.Fatal("generated header had no alternate path form")
	}
	mustWrite(t, out, bytes.Replace(alternate, []byte("form text"), []byte("form text absorbed"), 1))
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatal(err)
	}
	absorbed, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(filepath.Join(codex, "manifest", codexKey("form.md")+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	currentBody, err := manifestBody(codex, m)
	if err != nil {
		t.Fatal(err)
	}
	if m.builtHash != sha(absorbed) || sha(generatedHeader(filepath.Join(codex, "manifest", codexKey("form.md")+".yaml"), currentBody)) == m.builtHash {
		t.Fatalf("fixture does not preserve the alternate header in the absorbed hash")
	}
	entries, err := StatusWithHome(codex, root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("status after absorb = %#v, %v; want clean", entries, err)
	}
	edited, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, out, bytes.Replace(edited, []byte("form text absorbed"), []byte("form text edited"), 1))
	entries, err = StatusWithHome(codex, root)
	if err != nil || len(entries) != 1 ||
		!strings.HasPrefix(entries[0].Command, "lex absorb --codex ") ||
		entries[0].KeepFragments != "" {
		t.Fatalf("status after file edit = %#v, %v; want one absorb", entries, err)
	}
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatalf("run suggested absorb: %v", err)
	}
	entries, err = StatusWithHome(codex, root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("status after suggested absorb = %#v, %v; want clean", entries, err)
	}
}

func TestStatusRefreshAfterHeaderRemovalHasSingleRecommendation(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	out := filepath.Join(root, "refresh.md")
	if err := importData(codex, out, "refresh.md", []byte("original\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := setManifestHeader(codex, "refresh.md", true); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	manifestPath := manifestOf(t, codex, "refresh.md")
	built, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, out, withoutGeneratedHeader(built, manifestPath))
	mustWrite(t, out, []byte("refreshed\n"))
	if err := ImportWithOptions(codex, out, ImportOptions{Home: root, Refresh: true}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, out, []byte("refreshed by hand\n"))
	entries, err := StatusWithHome(codex, root)
	if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Command, "lex absorb --codex ") ||
		entries[0].KeepFragments != "" {
		t.Fatalf("status after refresh and edit = %#v, %v; want one absorb", entries, err)
	}
	if err := Absorb(codex, out, AbsorbOptions{}); err != nil {
		t.Fatalf("run suggested absorb: %v", err)
	}
	entries, err = StatusWithHome(codex, root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("status after suggested absorb = %#v, %v; want clean", entries, err)
	}
}

func TestStatusLegacyHeaderStripsBeforeSourceHash(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	out := filepath.Join(root, "legacy.md")
	if err := importData(codex, out, "legacy.md", []byte("original\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := setManifestHeader(codex, "legacy.md", true); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	manifestPath := manifestOf(t, codex, "legacy.md")
	m, err := readManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	m.builtHash = ""
	if err := writeManifest(manifestPath, m, m.links); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := fragmentBody(raw)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md"),
		replaceFragmentBody(raw, body, []byte("changed fragment\n")))
	entries, err := StatusWithHome(codex, root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("status for unchanged legacy output = %#v, %v; want clean", entries, err)
	}
}

func TestStatusPurposeRefreshNoOpWithEditedFragments(t *testing.T) {
	codex, out, _, m := purposeFixture(t, "- first\n- second\n")
	mustWrite(t, out, []byte("- first\n- second\n"))
	raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := fragmentBody(raw)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md"),
		replaceFragmentBody(raw, body, []byte("- changed\n")))
	before := treeBytes(t, codex)
	if err := ImportWithOptions(codex, out, ImportOptions{Home: filepath.Dir(out), Refresh: true}); err != nil {
		t.Fatalf("unchanged refresh should be a no-op despite fragment edits: %v", err)
	}
	if after := treeBytes(t, codex); !bytes.Equal(before, after) {
		t.Fatal("no-op refresh changed codex")
	}
}

func TestStatusPurposeAssessmentErrorDoesNotClaimFragmentChange(t *testing.T) {
	codex, out, manifestPath, m := purposeFixture(t, "- first\n- second\n")
	purposeFragment(t, codex, manifestPath, m, 0, "reason")
	purposeSet(t, codex, true)
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	m.builtHash = ""
	if err := writeManifest(manifestPath, m, m.links); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, out, bytes.Replace(data, []byte(purposeMarker+"reason"), []byte(purposeMarker+"edited"), 1))
	entries, err := StatusWithHome(codex, filepath.Dir(out))
	if err != nil || len(entries) != 1 || entries[0].KeepFragments != "" ||
		!strings.HasPrefix(entries[0].Command, "lex absorb --codex ") {
		t.Fatalf("status after purpose edit = %#v, %v; want a single absorb recommendation", entries, err)
	}
}

func TestStatusFragmentProblemDoesNotRepeatPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frag", "part.md")
	_, readErr := os.ReadFile(path)
	if readErr == nil {
		t.Fatal("reading a missing fragment succeeded")
	}
	got := statusFragmentProblem(fmt.Errorf("%s: %w", path, readErr))
	if !strings.HasPrefix(got, path+": ") || strings.Count(got, path) != 1 {
		t.Fatalf("fragment problem = %q; want the path once, at the start", got)
	}
	plain := fmt.Errorf("%s: record must not be in a manifest", path)
	if got := statusFragmentProblem(plain); got != plain.Error() {
		t.Fatalf("fragment problem = %q; want the error text unchanged", got)
	}
}

func TestCutoverHeaderAndStatus(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.md")
	if err := importData(codex, out, "a.md", []byte("body\n<!-- author -->\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := setManifestHeader(codex, "a.md", true); err != nil {
		t.Fatal(err)
	}
	if err := BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	if !bytes.HasPrefix(b, []byte("<!-- lex: собран из "+resolvePath(codex)+"/manifest/a.md.yaml. Правь фрагменты в "+resolvePath(codex)+"/frag и запусти `lex build --codex "+resolvePath(codex)+"`; правку прямо в этом файле перенеси во фрагменты: `lex absorb --codex "+resolvePath(codex)+" <этот файл>` -->\n")) {
		t.Fatalf("missing generated header: %q", b)
	}
	if err := BuildCheckOptions(codex, BuildCheckOptionsValue{NoHeader: true}); err != nil {
		t.Fatalf("no-header check failed: %v", err)
	}
	entries, err := StatusWithHome(codex, filepath.Dir(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("generated header looked like source drift: %#v", entries)
	}
}

func TestCutoverCheckDetectsOrphanAndDraft(t *testing.T) {
	bodyText := []byte("---\nid: x\nцель: \nоснование: \nснять_когда: \n---\nbody mentions draft: true here\n")
	if fragmentIsDraft(bodyText) {
		t.Fatal("body text was mistaken for a draft header")
	}
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.md")
	if err := importData(codex, out, "a.md", []byte("body\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("ordinary body text triggered draft warning: %v", warnings)
	}
	mustWrite(t, filepath.Join(codex, "frag", "orphan.md"), []byte("orphan"))
	if _, err := CheckWithWarnings(codex, CheckOptions{}); err == nil || !strings.Contains(err.Error(), "not in any manifest") {
		t.Fatalf("expected orphan diagnostic, got %v", err)
	}
}

func TestCutoverSymlinkSkipped(t *testing.T) {
	codex := t.TempDir()
	home := t.TempDir()
	sourceDir := home
	var err error
	src := filepath.Join(sourceDir, "real.md")
	alias := filepath.Join(filepath.Dir(src), "alias.md")
	mustWrite(t, src, []byte("same\n"))
	if err := os.Symlink(src, alias); err != nil {
		t.Fatal(err)
	}
	if err := ImportWithOptions(codex, src, ImportOptions{Home: home}); err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	importErr := ImportWithOptions(codex, alias, ImportOptions{Home: home})
	_ = writer.Close()
	os.Stdout = oldStdout
	message, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if importErr != nil {
		t.Fatal(importErr)
	}
	if strings.Contains(string(message), "skipped") {
		t.Fatalf("symlink import was skipped: %q", message)
	}
	manifestPath := filepath.Join(codex, "manifest", codexKey("real.md")+".yaml")
	m, err := readManifest(manifestPath)
	if err != nil || len(m.links) != 1 || m.links[0].path != alias || m.links[0].target != src {
		t.Fatalf("imported link = %#v, %v", m.links, err)
	}
	paths, _ := filepath.Glob(filepath.Join(codex, "manifest", "*.yaml"))
	if len(paths) != 1 {
		t.Fatalf("got %d manifests, want 1", len(paths))
	}
}

func TestCutoverStagedDetectsIndexDiskDrift(t *testing.T) {
	repo := t.TempDir()
	out := filepath.Join(repo, "instructions.md")
	codex := t.TempDir()
	if err := os.WriteFile(out, []byte("indexed\n"), fileMode); err != nil {
		t.Fatal(err)
	}
	if err := importData(codex, out, "instructions.md", []byte("indexed\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repo
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("git %v: %s %v", args, b, e)
		}
	}
	run("init", "-q")
	run("add", "instructions.md")
	mustWrite(t, out, []byte("disk edit\n"))
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	_, err = CheckWithWarnings(codex, CheckOptions{Staged: true})
	if err == nil || !strings.Contains(err.Error(), "index and disk differ") || strings.Contains(err.Error(), "built output differs") {
		t.Fatalf("expected index/disk drift while checking indexed bytes, got %v", err)
	}
}
func TestCutoverSharedAbsorbRequiresFlag(t *testing.T) {
	codex := t.TempDir()
	outA := filepath.Join(t.TempDir(), "a.md")
	outB := filepath.Join(t.TempDir(), "b.md")
	if err := importData(codex, outA, "a.md", []byte("original\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := importData(codex, outB, "b.md", []byte("other\n"), false); err != nil {
		t.Fatal(err)
	}
	manifestA := filepath.Join(codex, "manifest", codexKey("a.md")+".yaml")
	manifestB := filepath.Join(codex, "manifest", codexKey("b.md")+".yaml")
	ma, err := readManifest(manifestA)
	if err != nil {
		t.Fatal(err)
	}
	mb, err := readManifest(manifestB)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(manifestB)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.ReplaceAll(raw, []byte(strconv.Quote(mb.parts[0])), []byte(strconv.Quote(ma.parts[0])))
	mustWrite(t, manifestB, raw)
	if err := BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	edited := []byte("edited\n")
	mustWrite(t, outA, edited)
	frag := filepath.Join(codex, "frag", filepath.FromSlash(ma.parts[0])+".md")
	before, err := os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	err = Absorb(codex, outA, AbsorbOptions{})
	if err == nil || !strings.Contains(err.Error(), outA) || !strings.Contains(err.Error(), outB) || !strings.Contains(err.Error(), "--shared") {
		t.Fatalf("expected shared outputs and flag diagnostic, got %v", err)
	}
	after, err := os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("shared fragment changed without --shared")
	}
	if err := Absorb(codex, outA, AbsorbOptions{Shared: true}); err != nil {
		t.Fatal(err)
	}
	if err := BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
}

func TestCutoverNoHeaderRemovesOnlyExactMarker(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "front.md")
	source := []byte("---\ntitle: demo\n---\nbody\n<!-- author -->\n")
	if err := importData(codex, out, "front.md", source, false); err != nil {
		t.Fatal(err)
	}
	if err := setManifestHeader(codex, "front.md", true); err != nil {
		t.Fatal(err)
	}
	if err := BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("---\ntitle: demo\n---\n"+string(generatedMarker(filepath.Join(codex, "manifest", codexKey("front.md")+".yaml"))))) {
		t.Fatalf("generated marker not after front matter: %q", b)
	}
	if err := BuildCheckOptions(codex, BuildCheckOptionsValue{NoHeader: true}); err != nil {
		t.Fatal(err)
	}
	middle := append(append([]byte{}, source...), generatedMarker(filepath.Join(codex, "manifest", codexKey("front.md")+".yaml"))...)
	mustWrite(t, out, middle)
	if err := BuildCheckOptions(codex, BuildCheckOptionsValue{NoHeader: true}); err == nil {
		t.Fatal("removed marker-like comment from middle of file")
	}
	authorEdit := bytes.Replace(source, []byte("<!-- author -->"), []byte("<!-- author changed -->"), 1)
	mustWrite(t, out, generatedHeader(filepath.Join(codex, "manifest", codexKey("front.md")+".yaml"), authorEdit))
	if err := BuildCheckOptions(codex, BuildCheckOptionsValue{NoHeader: true}); err == nil {
		t.Fatal("no-header check ignored author comment edit")
	}
}

func TestCutoverCheckDanglingManifestAndDraftWarning(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.md")
	if err := importData(codex, out, "dangling.md", []byte("body\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(filepath.Join(codex, "manifest", codexKey("dangling.md")+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	fragment := filepath.Join(codex, "frag", filepath.FromSlash(m.parts[0])+".md")
	if err := os.Remove(fragment); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckWithWarnings(codex, CheckOptions{}); err == nil || !strings.Contains(err.Error(), "missing fragment") {
		t.Fatalf("expected dangling reference error, got %v", err)
	}
}

func TestCutoverStatusFindsSourceDrift(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "source.md")
	if err := importData(codex, out, "status/source.md", []byte("original\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, out, []byte("changed\n"))
	entries, err := StatusWithHome(codex, filepath.Dir(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !strings.Contains(entries[0].Command, "lex import --refresh") || !strings.Contains(entries[0].Command, strconv.Quote(out)) {
		t.Fatalf("expected refresh command, got %#v", entries)
	}
}
func TestCutoverBuildRejectsSymlinkOutput(t *testing.T) {
	codex := t.TempDir()
	target := filepath.Join(t.TempDir(), "target.md")
	link := filepath.Join(filepath.Dir(target), "link.md")
	mustWrite(t, target, []byte("keep\n"))
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := importData(codex, link, "link.md", []byte("generated\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink refusal, got %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep\n" {
		t.Fatalf("wrote through symlink: %q", got)
	}
}

func TestReviewAbsorbInsertionAndRepeatedDraft(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.md")
	if err := importData(codex, out, "repeat.md", []byte("# One\nalpha\n\n# Two\nbeta\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	first := []byte("# One\nalpha\nalpha2\n\n# Two\nbeta\n")
	mustWrite(t, out, first)
	if err := Absorb(codex, out, AbsorbOptions{DraftDir: codex}); err != nil {
		t.Fatalf("in-fragment insertion rejected: %v", err)
	}
	second := []byte("# One\nbravo\nalpha2\n\n# Two\nbeta\n")
	mustWrite(t, out, second)
	if err := Absorb(codex, out, AbsorbOptions{DraftDir: codex}); err != nil {
		t.Fatalf("second absorb rejected: %v", err)
	}
	m, err := readManifest(filepath.Join(codex, "manifest", codexKey("repeat.md")+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range m.parts {
		raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(id)+".md"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fragmentBody(raw); err != nil {
			t.Fatalf("repeated draft header invalid: %v", err)
		}
	}
	if err := Build(codex, true); err != nil {
		t.Fatal(err)
	}
}

func TestReviewRepeatedAbsorbWritesOneDraftField(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.md")
	if err := importData(codex, out, "twice.md", []byte("# One\nalpha\n\n# Two\nbeta\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"bravo", "charlie"} {
		mustWrite(t, out, []byte("# One\n"+line+"\n\n# Two\nbeta\n"))
		if err := Absorb(codex, out, AbsorbOptions{DraftDir: codex}); err != nil {
			t.Fatal(err)
		}
	}
	m, err := readManifest(filepath.Join(codex, "manifest", codexKey("twice.md")+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(codex, "frag", filepath.FromSlash(m.parts[1])+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if count := bytes.Count(raw, []byte("draft: true")); count != 0 {
		t.Fatalf("absorb left %d draft header fields", count)
	}
	if _, err := fragmentBody(raw); err != nil {
		t.Fatalf("repeated absorb invalidated fragment header: %v", err)
	}
}

func TestCheckOutputsUnderFiltersManifestsAndResolvesPaths(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	firstDir := filepath.Join(root, "projects", "first")
	secondDir := filepath.Join(root, "projects", "second")
	first := filepath.Join(firstDir, "CLAUDE.md")
	second := filepath.Join(secondDir, "CLAUDE.md")
	if err := importData(codex, first, "first.md", []byte("first\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := importData(codex, second, "second.md", []byte("second\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	dotDotDir := filepath.Join(root, "..project")
	dotDotOut := filepath.Join(dotDotDir, "CLAUDE.md")
	if err := importData(codex, dotDotOut, "dot-dot.md", []byte("inside\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, dotDotOut, []byte("manual edit\n"))
	if _, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: root}); err == nil || !strings.Contains(err.Error(), "built output differs") {
		t.Fatalf("output under a directory beginning with .. was not selected: %v", err)
	}
	mustWrite(t, second, []byte("manual edit\n"))
	if warnings, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: firstDir + string(filepath.Separator)}); err != nil || len(warnings) != 0 {
		t.Fatalf("other project's output affected scoped check: warnings=%v err=%v", warnings, err)
	}
	link := filepath.Join(root, "second-link")
	if err := os.Symlink(secondDir, link); err != nil {
		t.Fatal(err)
	}
	_, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: link + string(filepath.Separator)})
	if err == nil || !strings.Contains(err.Error(), second) || !strings.Contains(err.Error(), "lex absorb --codex") {
		t.Fatalf("expected selected output drift and absorb hint, got %v", err)
	}
	if warnings, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: filepath.Join(root, "empty")}); err != nil || len(warnings) != 0 {
		t.Fatalf("empty output scope should pass silently: warnings=%v err=%v", warnings, err)
	}
	t.Chdir(root)
	if warnings, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: "projects/first/"}); err != nil || len(warnings) != 0 {
		t.Fatalf("relative output scope should pass: warnings=%v err=%v", warnings, err)
	}
}

func TestCheckOutputsUnderTree(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	main := filepath.Join(root, "main")
	tree := filepath.Join(root, "tree")
	out := filepath.Join(main, "CLAUDE.md")
	if err := importData(codex, out, "output.md", []byte("built\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tree, directoryMode); err != nil {
		t.Fatal(err)
	}
	built, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	treeOut := filepath.Join(tree, "CLAUDE.md")
	mustWrite(t, treeOut, built)
	if err := setManifestHeader(codex, "output.md", true); err != nil {
		t.Fatal(err)
	}
	if err := BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	built, err = os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, treeOut, built)
	if _, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: main, Tree: tree}); err != nil {
		t.Fatalf("clean tree with generated header should pass: %v", err)
	}
	fragment := fragmentOf(t, codex, "output.md")
	fragmentData, err := os.ReadFile(fragment)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, fragment, bytes.Replace(fragmentData, []byte("снять_когда: \n---\n"), []byte("снять_когда: \ndraft: true\n---\n"), 1))
	warnings, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: main, Tree: tree})
	if err != nil || len(warnings) != 1 || !strings.Contains(warnings[0], "draft: true") {
		t.Fatalf("tree check should preserve draft warning: warnings=%v err=%v", warnings, err)
	}
	manifestPath := filepath.Join(codex, "manifest", codexKey("output.md")+".yaml")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, manifestPath, bytes.Replace(manifestData, []byte("source_sha256: "), []byte("source_sha256 "), 1))
	if _, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: main, Tree: tree}); err == nil || !strings.Contains(err.Error(), "invalid manifest") {
		t.Fatalf("tree check should reject a broken selected manifest: %v", err)
	}
	mustWrite(t, manifestPath, manifestData)

	mustWrite(t, out, []byte("main only edit\n"))
	if _, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: main, Tree: tree}); err != nil {
		t.Fatalf("main checkout edit should not affect tree check: %v", err)
	}
	mustWrite(t, treeOut, []byte("tree edit\n"))
	if _, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: main, Tree: tree}); err == nil || !strings.Contains(err.Error(), treeOut) {
		t.Fatalf("tree output edit should name tree path, got %v", err)
	}
	if err := os.Remove(treeOut); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: main, Tree: tree}); err == nil || !strings.Contains(err.Error(), treeOut) {
		t.Fatalf("missing tree output should name tree path, got %v", err)
	}
	if _, err := CheckWithWarnings(codex, CheckOptions{Tree: tree}); err == nil || !strings.Contains(err.Error(), "--tree requires --outputs-under") {
		t.Fatalf("tree without output scope should fail, got %v", err)
	}
	if _, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: main, Tree: filepath.Join(root, "missing")}); err == nil || !strings.Contains(err.Error(), filepath.Join(root, "missing")) {
		t.Fatalf("missing tree root should fail with its path, got %v", err)
	}
}

func TestCheckOutputsUnderTreeSupportsRelativeManifestOutput(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	main := filepath.Join(root, "main")
	tree := filepath.Join(root, "tree")
	out := filepath.Join(main, "CLAUDE.md")
	if err := os.MkdirAll(main, directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := importData(codex, out, "output.md", []byte("built\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	built, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tree, directoryMode); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(tree, "CLAUDE.md"), built)
	manifestPath := filepath.Join(codex, "manifest", codexKey("output.md")+".yaml")
	if err := updateManifestField(manifestPath, "out", strconv.Quote("CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(main)
	for _, treePath := range []string{"", tree} {
		if _, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: ".", Tree: treePath}); err != nil {
			t.Fatalf("relative manifest output with tree %q: %v", treePath, err)
		}
	}
}

func TestCheckOutputsUnderTreeRejectsOutputSymlink(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	main := filepath.Join(root, "main")
	tree := filepath.Join(root, "tree")
	out := filepath.Join(main, "CLAUDE.md")
	if err := os.MkdirAll(main, directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := importData(codex, out, "output.md", []byte("built\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tree, "ignored"), directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tree, ".git"), directoryMode); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(tree, ".gitignore"), []byte("ignored/\n"))
	outside := filepath.Join(root, "outside.md")
	mustWrite(t, outside, []byte("built\n"))
	mustWrite(t, filepath.Join(tree, "inside.md"), []byte("built\n"))
	mustWrite(t, filepath.Join(tree, "ignored", "x.md"), []byte("built\n"))
	mustWrite(t, filepath.Join(tree, ".git", "x.md"), []byte("built\n"))

	for name, target := range map[string]string{
		"inside tree":     filepath.Join(tree, "inside.md"),
		"outside tree":    outside,
		"gitignored path": filepath.Join(tree, "ignored", "x.md"),
		"git metadata":    filepath.Join(tree, ".git", "x.md"),
	} {
		t.Run(name, func(t *testing.T) {
			treeOut := filepath.Join(tree, "CLAUDE.md")
			if err := os.Symlink(target, treeOut); err != nil {
				t.Fatal(err)
			}
			_, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: main, Tree: tree})
			if err == nil || !strings.Contains(err.Error(), treeOut) || !strings.Contains(err.Error(), "refusing to read through symlink") {
				t.Fatalf("tree output symlink should be refused with its path, got %v", err)
			}
			if err := os.Remove(treeOut); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCheckOutputsUnderTreeUsesManifestPathWithoutResolvingOutputSymlinks(t *testing.T) {
	t.Run("output is a symlink in main", func(t *testing.T) {
		root := t.TempDir()
		codex := filepath.Join(root, "codex")
		main := filepath.Join(root, "main")
		tree := filepath.Join(root, "tree")
		out := filepath.Join(main, "CLAUDE.md")
		if err := os.MkdirAll(main, directoryMode); err != nil {
			t.Fatal(err)
		}
		if err := importData(codex, out, "output.md", []byte("built\n"), false); err != nil {
			t.Fatal(err)
		}
		if err := Build(codex, false); err != nil {
			t.Fatal(err)
		}
		built, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		mainLink := filepath.Join(root, "main-link")
		if err := os.Symlink(main, mainLink); err != nil {
			t.Fatal(err)
		}
		treeOut := filepath.Join(tree, "CLAUDE.md")
		if err := os.MkdirAll(filepath.Dir(treeOut), directoryMode); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, treeOut, append(built, []byte("manual edit\n")...))
		outside := filepath.Join(root, "outside.md")
		if err := os.Rename(out, outside); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, out); err != nil {
			t.Fatal(err)
		}
		if _, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: mainLink, Tree: tree}); err == nil || !strings.Contains(err.Error(), treeOut) {
			t.Fatalf("output symlink in main hid tree drift through root symlink: %v", err)
		}
	})
	t.Run("output directory is a symlink in main", func(t *testing.T) {
		root := t.TempDir()
		codex := filepath.Join(root, "codex")
		main := filepath.Join(root, "main")
		mainDir := filepath.Join(main, ".claude")
		actualDir := filepath.Join(main, "dotclaude")
		tree := filepath.Join(root, "tree")
		out := filepath.Join(mainDir, "rules", "fm.md")
		if err := os.MkdirAll(filepath.Dir(out), directoryMode); err != nil {
			t.Fatal(err)
		}
		if err := importData(codex, out, "rule.md", []byte("built\n"), false); err != nil {
			t.Fatal(err)
		}
		if err := Build(codex, false); err != nil {
			t.Fatal(err)
		}
		built, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		mainLink := filepath.Join(root, "main-link")
		if err := os.Symlink(main, mainLink); err != nil {
			t.Fatal(err)
		}
		treeOut := filepath.Join(tree, ".claude", "rules", "fm.md")
		if err := os.MkdirAll(filepath.Dir(treeOut), directoryMode); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, treeOut, append(built, []byte("manual edit\n")...))
		if err := os.Rename(mainDir, actualDir); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(actualDir, mainDir); err != nil {
			t.Fatal(err)
		}
		if _, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: mainLink, Tree: tree}); err == nil || !strings.Contains(err.Error(), treeOut) {
			t.Fatalf("directory symlink in main changed tree path through root symlink: %v", err)
		}
	})
}

func TestCheckOutputsUnderChecksToolsOnlyForSelectedFragments(t *testing.T) {
	root := t.TempDir()
	codex := filepath.Join(root, "codex")
	firstDir := filepath.Join(root, "one")
	secondDir := filepath.Join(root, "two")
	first := filepath.Join(firstDir, "CLAUDE.md")
	second := filepath.Join(secondDir, "CLAUDE.md")
	if err := importData(codex, first, "first.md", []byte("first\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := importData(codex, second, "second.md", []byte("second\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "registry")
	mustWrite(t, script, []byte("#!/bin/sh\necho '{\"commands\":[{\"path\":[\"known\"]}]}'\n"))
	if err := os.Chmod(script, 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(codex, "tools.json"), fmt.Appendf(nil, `{"tools":[{"name":"tool","help":[%q]}]}`, script))
	for _, out := range []string{first, second} {
		fragment := fragmentOf(t, codex, map[string]string{first: "first.md", second: "second.md"}[out])
		original, err := os.ReadFile(fragment)
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, fragment, append(original, []byte("\n`tool unknown`\n")...))
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: firstDir})
	if err != nil || !strings.Contains(strings.Join(warnings, "\n"), "tool unknown") || !strings.Contains(strings.Join(warnings, "\n"), "first.md") {
		t.Fatalf("selected fragment tool mention was not warned: warnings=%v err=%v", warnings, err)
	}
	// Repair the selected fragment. The other repository's invalid tool mention is outside scope.
	firstFragment := fragmentOf(t, codex, "first.md")
	original, err := os.ReadFile(firstFragment)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, firstFragment, bytes.Replace(original, []byte("\n`tool unknown`\n"), nil, 1))
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	if warnings, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: firstDir}); err != nil || len(warnings) != 0 {
		t.Fatalf("foreign fragment tool mention affected scoped check: warnings=%v err=%v", warnings, err)
	}
}

func TestCheckOutputsUnderRejectsBrokenSelectedManifests(t *testing.T) {
	for _, mode := range []string{"missing source hash", "malformed parts", "unreadable manifest"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			codex := filepath.Join(root, "codex")
			selectedDir := filepath.Join(root, "project")
			foreignDir := filepath.Join(root, "other")
			selectedOut := filepath.Join(selectedDir, "CLAUDE.md")
			foreignOut := filepath.Join(foreignDir, "CLAUDE.md")
			if err := importData(codex, selectedOut, "project.md", []byte("selected\n"), false); err != nil {
				t.Fatal(err)
			}
			if err := importData(codex, foreignOut, "other.md", []byte("foreign\n"), false); err != nil {
				t.Fatal(err)
			}
			if err := Build(codex, false); err != nil {
				t.Fatal(err)
			}
			manifestPath := filepath.Join(codex, "manifest", codexKey("project.md")+".yaml")
			originalManifest, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unreadable manifest" {
				if err := os.Remove(manifestPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(manifestPath, directoryMode); err != nil {
					t.Fatal(err)
				}
			} else {
				data, err := os.ReadFile(manifestPath)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "missing source hash" {
					data = bytes.Replace(data, []byte("source_sha256: "+sha([]byte("selected\n"))+"\n"), nil, 1)
				} else {
					data = bytes.Replace(data, []byte("  - \"project.md/selected-1\""), []byte("  - \"unterminated"), 1)
				}
				mustWrite(t, manifestPath, data)
			}
			mustWrite(t, selectedOut, []byte("manual edit\n"))
			_, err = CheckWithWarnings(codex, CheckOptions{OutputsUnder: selectedDir})
			if err == nil || !strings.Contains(err.Error(), manifestPath) || !strings.Contains(err.Error(), "invalid manifest") {
				t.Fatalf("selected broken manifest was not reported: %v", err)
			}
			if mode == "unreadable manifest" {
				if err := os.Remove(manifestPath); err != nil {
					t.Fatal(err)
				}
			}
			mustWrite(t, manifestPath, originalManifest)
			mustWrite(t, selectedOut, []byte("selected\n"))
			// A parse error with a readable output path remains isolated to that other project.
			foreignManifest := filepath.Join(codex, "manifest", codexKey("other.md")+".yaml")
			data, err := os.ReadFile(foreignManifest)
			if err != nil {
				t.Fatal(err)
			}
			data = bytes.Replace(data, []byte("source_sha256: "+sha([]byte("foreign\n"))+"\n"), nil, 1)
			mustWrite(t, foreignManifest, data)
			if _, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: selectedDir}); err != nil {
				t.Fatalf("foreign broken manifest affected selected scope: %v", err)
			}
			if _, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: foreignDir}); err == nil || !strings.Contains(err.Error(), foreignManifest) {
				t.Fatalf("broken foreign manifest should fail when its output is selected: %v", err)
			}
		})
	}
}

func TestCheckOutputsUnderChecksHeaderDraftDeletedOutputAndSymlinkTarget(t *testing.T) {
	t.Run("generated header", func(t *testing.T) {
		codex := t.TempDir()
		outDir := t.TempDir()
		out := filepath.Join(outDir, "CLAUDE.md")
		if err := importData(codex, out, "header.md", []byte("body\n"), false); err != nil {
			t.Fatal(err)
		}
		if err := setManifestHeader(codex, "header.md", true); err != nil {
			t.Fatal(err)
		}
		if err := BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: outDir}); err != nil {
			t.Fatalf("generated header was not included in scoped check: %v", err)
		}
	})
	t.Run("draft warning", func(t *testing.T) {
		codex := t.TempDir()
		outDir := t.TempDir()
		out := filepath.Join(outDir, "CLAUDE.md")
		if err := importData(codex, out, "draft.md", []byte("body\n"), false); err != nil {
			t.Fatal(err)
		}
		if err := Build(codex, false); err != nil {
			t.Fatal(err)
		}
		fragment := fragmentOf(t, codex, "draft.md")
		data, err := os.ReadFile(fragment)
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, fragment, bytes.Replace(data, []byte("снять_когда: \n---\n"), []byte("снять_когда: \ndraft: true\n---\n"), 1))
		warnings, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: outDir})
		if err != nil || len(warnings) != 1 || !strings.Contains(warnings[0], "draft: true") {
			t.Fatalf("scoped check lost draft warning: warnings=%v err=%v", warnings, err)
		}
	})
	t.Run("deleted output", func(t *testing.T) {
		codex := t.TempDir()
		outDir := t.TempDir()
		out := filepath.Join(outDir, "CLAUDE.md")
		if err := importData(codex, out, "deleted.md", []byte("body\n"), false); err != nil {
			t.Fatal(err)
		}
		if err := Build(codex, false); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(out); err != nil {
			t.Fatal(err)
		}
		if _, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: outDir}); err == nil || !strings.Contains(err.Error(), out) {
			t.Fatalf("deleted selected output was not reported: %v", err)
		}
	})
	t.Run("output symlink resolves outside its link directory", func(t *testing.T) {
		codex := t.TempDir()
		root := t.TempDir()
		targetDir := filepath.Join(root, "target")
		linkDir := filepath.Join(root, "links")
		if err := os.MkdirAll(targetDir, directoryMode); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(linkDir, directoryMode); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(targetDir, "CLAUDE.md")
		link := filepath.Join(linkDir, "CLAUDE.md")
		if err := importData(codex, link, "linked.md", []byte("initial\n"), false); err != nil {
			t.Fatal(err)
		}
		if err := Build(codex, false); err != nil {
			t.Fatal(err)
		}
		built, err := os.ReadFile(link)
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, target, built)
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, target, []byte("manual edit\n"))
		if _, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: targetDir}); err == nil || !strings.Contains(err.Error(), "built output differs") {
			t.Fatalf("target directory did not select symlinked output: %v", err)
		}
		if _, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: linkDir}); err != nil {
			t.Fatalf("symlink link directory should not select canonical output: %v", err)
		}
	})
}

func TestCheckOutputsUnderReportsMissingSelectedFragment(t *testing.T) {
	codex := t.TempDir()
	outDir := t.TempDir()
	out := filepath.Join(outDir, "CLAUDE.md")
	if err := importData(codex, out, "missing.md", []byte("body\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	fragment := fragmentOf(t, codex, "missing.md")
	if err := os.Remove(fragment); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: outDir}); err == nil || !strings.Contains(err.Error(), fragment) || !strings.Contains(err.Error(), "missing fragment") {
		t.Fatalf("missing selected fragment was not reported: %v", err)
	}
}

func TestCheckOutputsUnderEmptyScopeSkipsInvalidToolsConfig(t *testing.T) {
	codex := t.TempDir()
	if err := os.Mkdir(filepath.Join(codex, "manifest"), 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(codex, "tools.json"), []byte("{"))
	warnings, err := CheckWithWarnings(codex, CheckOptions{OutputsUnder: t.TempDir()})
	if err != nil || len(warnings) != 0 {
		t.Fatalf("empty scope should return silently despite tools config: warnings=%v err=%v", warnings, err)
	}
}

func TestCheckOutputsUnderRejectsStaged(t *testing.T) {
	codex := t.TempDir()
	if err := os.Mkdir(filepath.Join(codex, "manifest"), 0755); err != nil {
		t.Fatal(err)
	}
	_, err := CheckWithWarnings(codex, CheckOptions{Staged: true, OutputsUnder: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("expected staged conflict, got %v", err)
	}
}

func TestReviewCheckReportsDiskOutputDrift(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.md")
	if err := importData(codex, out, "check.md", []byte("built\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, out, []byte("disk edit\n"))
	_, err := CheckWithWarnings(codex, CheckOptions{})
	if err == nil || !strings.Contains(err.Error(), "built output differs") {
		t.Fatalf("expected built/disk mismatch, got %v", err)
	}
}

func TestReviewBuildRefusesDanglingOutputSymlink(t *testing.T) {
	codex := t.TempDir()
	dir := t.TempDir()
	out := filepath.Join(dir, "out.md")
	target := filepath.Join(dir, "missing.md")
	if err := importData(codex, out, "dangling.md", []byte("new\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, out); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected dangling symlink refusal, got %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("unexpected symlink target creation: %v", err)
	}
}

func TestReviewBuildRequiresBuiltHash(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.md")
	if err := importData(codex, out, "legacy.md", []byte("original\n"), false); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(codex, "manifest", codexKey("legacy.md")+".yaml")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("built_sha256: "+sha([]byte("original\n"))+"\n"), nil, 1)
	mustWrite(t, manifestPath, raw)
	mustWrite(t, out, []byte("manual\n"))
	if err := Build(codex, false); err == nil || !strings.Contains(err.Error(), "no built_sha256") {
		t.Fatalf("expected missing built hash refusal, got %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "manual\n" {
		t.Fatalf("manual output overwritten: %q", got)
	}
}

func TestReviewDraftNeedsExplicitDirectory(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.md")
	if err := importData(codex, out, "boundary.md", []byte("# One\na\n\n# Two\nb\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, out, []byte("# One\na\nX\nTWO\nb\n"))
	err := Absorb(codex, out, AbsorbOptions{})
	if err == nil || !strings.Contains(err.Error(), "draft directory") {
		t.Fatalf("expected explicit draft directory error, got %v", err)
	}
}

func TestReviewStagedReportsGitLookupFailures(t *testing.T) {
	t.Run("rev-parse", func(t *testing.T) {
		cwd := t.TempDir()
		out := filepath.Join(cwd, "out.md")
		codex := t.TempDir()
		if err := importData(codex, out, "out.md", []byte("body\n"), false); err != nil {
			t.Fatal(err)
		}
		if err := Build(codex, false); err != nil {
			t.Fatal(err)
		}
		old, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err = os.Chdir(cwd); err != nil {
			t.Fatal(err)
		}
		defer os.Chdir(old)
		_, err = CheckWithWarnings(codex, CheckOptions{Staged: true})
		if err == nil || !strings.Contains(err.Error(), "git rev-parse") {
			t.Fatalf("expected explicit rev-parse failure, got %v", err)
		}
	})
	t.Run("show", func(t *testing.T) {
		repo := t.TempDir()
		out := filepath.Join(repo, "out.md")
		codex := filepath.Join(repo, "cx")
		if err := importData(codex, out, "out.md", []byte("body\n"), false); err != nil {
			t.Fatal(err)
		}
		if err := Build(codex, false); err != nil {
			t.Fatal(err)
		}
		// манифест в коммите, а собранного файла в индексе нет
		for _, args := range [][]string{{"init", "-q"}, {"add", "cx"}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = repo
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %s %v", args, b, err)
			}
		}
		old, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err = os.Chdir(repo); err != nil {
			t.Fatal(err)
		}
		defer os.Chdir(old)
		_, err = CheckWithWarnings(codex, CheckOptions{Staged: true})
		if err == nil || !strings.Contains(err.Error(), "not in the index") {
			t.Fatalf("expected explicit index lookup failure, got %v", err)
		}
	})
}
func TestReviewStagedUsesCurrentRepository(t *testing.T) {
	repoA := t.TempDir()
	repoB := t.TempDir()
	out := filepath.Join(repoB, "out.md")
	codex := filepath.Join(repoA, "cx")
	if err := os.MkdirAll(filepath.Join(codex, "manifest"), directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, []byte("indexed\n"), fileMode); err != nil {
		t.Fatal(err)
	}
	if err := importData(codex, out, "out.md", []byte("indexed\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{repoA, repoB} {
		c := exec.Command("git", "init", "-q")
		c.Dir = dir
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("git init: %s %v", b, e)
		}
	}
	c := exec.Command("git", "add", "out.md")
	c.Dir = repoB
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("git add: %s %v", b, e)
	}
	c = exec.Command("git", "add", "cx")
	c.Dir = repoA
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("git add cx: %s %v", b, e)
	}
	mustWrite(t, out, []byte("disk edit\n"))
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(repoA); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	_, err = CheckWithWarnings(codex, CheckOptions{Staged: true})
	if err == nil || !strings.Contains(err.Error(), "built output differs") || strings.Contains(err.Error(), "index and disk differ") {
		t.Fatalf("expected disk check outside current repository, got %v", err)
	}
}

func TestReviewAbsorbBoundaryPreservesFragments(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.md")
	if err := importData(codex, out, "boundary.md", []byte("# One\na\n\n# Two\nb\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	before := treeBytes(t, filepath.Join(codex, "frag"))
	mustWrite(t, out, []byte("# One\na\nX\nTWO\nb\n"))
	err := Absorb(codex, out, AbsorbOptions{DraftDir: codex})
	if err == nil || !strings.Contains(err.Error(), "draft") {
		t.Fatalf("expected same-count boundary conflict, got %v", err)
	}
	if !bytes.Equal(before, treeBytes(t, filepath.Join(codex, "frag"))) {
		t.Fatal("boundary conflict changed fragments")
	}
	matches, err := filepath.Glob(filepath.Join(codex, "drafts", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("got %d drafts, want 1", len(matches))
	}
}
func TestReviewAbsorbBoundaryWithLineCountChangePreservesFragments(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.md")
	original := []byte("# One\nalpha\nalpha2\n\n# Two\nbeta\n")
	if err := importData(codex, out, "boundary-insert.md", original, false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	before := treeBytes(t, filepath.Join(codex, "frag"))
	edited := []byte("# One\nalpha\nX\n# TWO\nbeta\n")
	mustWrite(t, out, edited)
	err := Absorb(codex, out, AbsorbOptions{DraftDir: codex})
	if err == nil || !strings.Contains(err.Error(), "draft") {
		t.Fatalf("expected boundary conflict, got %v", err)
	}
	if !bytes.Equal(before, treeBytes(t, filepath.Join(codex, "frag"))) {
		t.Fatal("boundary conflict changed fragments")
	}
	matches, err := filepath.Glob(filepath.Join(codex, "drafts", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("got %d drafts, want 1", len(matches))
	}
	if got, err := os.ReadFile(out); err != nil || !bytes.Equal(got, edited) {
		t.Fatalf("edited output changed: %q, %v", got, err)
	}
}

func TestReviewDraftTextInBodyDoesNotWarn(t *testing.T) {
	codex := t.TempDir()
	out := filepath.Join(t.TempDir(), "out.md")
	if err := importData(codex, out, "draft-body.md", []byte("draft: true\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	warnings, err := CheckWithWarnings(codex, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("body text triggered draft warning: %v", warnings)
	}
}

func TestReviewSymlinkImportedBeforeTargetIsReportedAndDeduplicated(t *testing.T) {
	codex := t.TempDir()
	home := t.TempDir()
	target := filepath.Join(home, "CLAUDE.md")
	link := filepath.Join(home, "AGENTS.md")
	mustWrite(t, target, []byte("instructions\n"))
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	importErr := ImportWithOptions(codex, link, ImportOptions{Home: home})
	if importErr == nil {
		importErr = ImportWithOptions(codex, target, ImportOptions{Home: home})
	}
	_ = writer.Close()
	os.Stdout = oldStdout
	message, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if importErr != nil {
		t.Fatal(importErr)
	}
	if strings.Contains(string(message), "skipped") {
		t.Fatalf("symlink import was skipped: %q", message)
	}
	manifestPath := filepath.Join(codex, "manifest", codexKey("CLAUDE.md")+".yaml")
	m, err := readManifest(manifestPath)
	if err != nil || len(m.links) != 1 || m.links[0].path != link || m.links[0].target != target {
		t.Fatalf("imported link = %#v, %v", m.links, err)
	}
	paths, err := filepath.Glob(filepath.Join(codex, "manifest", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("got %d manifests, want 1", len(paths))
	}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, fileMode); err != nil {
		t.Fatal(err)
	}
}

func treeBytes(t *testing.T, root string) []byte {
	t.Helper()
	var files []string
	if err := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() {
			r, _ := filepath.Rel(root, p)
			files = append(files, r)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	var b bytes.Buffer
	for _, p := range files {
		x, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString(p)
		b.WriteByte(0)
		b.Write(x)
		b.WriteByte(0)
	}
	return b.Bytes()
}

// Коммит одной сессии не падает из-за незакоммиченной правки другой сессии в соседнем собранном файле;
// входящий в коммит собранный файл с ручной правкой - отказ с подсказкой absorb.
func TestStagedChecksOnlyCommittedOutputs(t *testing.T) {
	repo := t.TempDir()
	codex := filepath.Join(repo, "cx")
	a := filepath.Join(repo, "a.md")
	b := filepath.Join(repo, "b.md")
	for _, f := range []string{a, b} {
		if err := importData(codex, f, filepath.Base(f), []byte("body "+filepath.Base(f)+"\n"), false); err != nil {
			t.Fatal(err)
		}
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, e := c.CombinedOutput(); e != nil {
			t.Fatalf("git %v: %s %v", args, out, e)
		}
	}
	git("init", "-q")
	git("add", ".")
	git("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init")
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	// другая сессия правит b.md и не индексирует; в коммите - посторонний файл
	mustWrite(t, b, []byte("someone else editing\n"))
	mustWrite(t, filepath.Join(repo, "other.txt"), []byte("x\n"))
	git("add", "other.txt")
	if _, err = CheckWithWarnings(codex, CheckOptions{Staged: true}); err != nil {
		t.Fatalf("unrelated commit must pass while b.md is edited but not staged: %v", err)
	}
	// ручная правка a.md в коммите - отказ с подсказкой
	mustWrite(t, a, []byte("manual edit\n"))
	git("add", "a.md")
	_, err = CheckWithWarnings(codex, CheckOptions{Staged: true})
	if err == nil || !strings.Contains(err.Error(), "a.md: built output differs") || !strings.Contains(err.Error(), "lex absorb") || strings.Contains(err.Error(), "b.md") {
		t.Fatalf("expected only a.md rejected with absorb hint, got %v", err)
	}
	// фрагмент b.md в коммите - b.md проверяется, хотя сам не индексирован
	git("reset", "-q", "a.md")
	mustWrite(t, a, []byte("body a.md\n"))
	frags, err := filepath.Glob(filepath.Join(codex, "frag", "b.md", "*.md"))
	if err != nil || len(frags) == 0 {
		t.Fatalf("fragments of b.md: %v %v", frags, err)
	}
	raw, err := os.ReadFile(frags[0])
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, frags[0], append(raw, []byte("fragment edit\n")...))
	git("add", frags[0])
	_, err = CheckWithWarnings(codex, CheckOptions{Staged: true})
	if err == nil || !strings.Contains(err.Error(), "b.md") {
		t.Fatalf("staged fragment must check its output, got %v", err)
	}
	// только манифест a.md в коммите (шапка включена, файл не пересобран) - a.md проверяется
	git("reset", "-q")
	manifests, err := filepath.Glob(filepath.Join(codex, "manifest", "a.md*.yaml"))
	if err != nil || len(manifests) != 1 {
		t.Fatalf("manifest of a.md: %v %v", manifests, err)
	}
	raw, err = os.ReadFile(manifests[0])
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, manifests[0], []byte(strings.Replace(string(raw), "header: false", "header: true", 1)))
	git("add", manifests[0])
	_, err = CheckWithWarnings(codex, CheckOptions{Staged: true})
	if err == nil || !strings.Contains(err.Error(), "a.md: built output differs") {
		t.Fatalf("staged manifest must check its output, got %v", err)
	}
}

// Ревью: переименование, путь через симлинк, чтение фрагментов из индекса, чужая незакоммиченная
// работа в кодексе, вложенный репозиторий.
func stagedRepo(t *testing.T) (repo, codex string, git func(...string) error) {
	t.Helper()
	repo = t.TempDir()
	codex = filepath.Join(repo, "cx")
	for _, n := range []string{"a.md", "b.md"} {
		if err := importData(codex, filepath.Join(repo, n), n, []byte("# "+n+"\nbody\n"), false); err != nil {
			t.Fatal(err)
		}
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	git = func(args ...string) error {
		c := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		c.Dir = repo
		if out, e := c.CombinedOutput(); e != nil {
			return fmt.Errorf("git %v: %s %v", args, out, e)
		}
		return nil
	}
	for _, a := range [][]string{{"init", "-q"}, {"add", "."}, {"commit", "-q", "-m", "init"}} {
		if err := git(a...); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func checkIn(t *testing.T, dir, codex string) error {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	_, err = CheckWithWarnings(codex, CheckOptions{Staged: true})
	return err
}

func fragmentOf(t *testing.T, codex, out string) string {
	t.Helper()
	frags, err := filepath.Glob(filepath.Join(codex, "frag", out, "*.md"))
	if err != nil || len(frags) == 0 {
		t.Fatalf("fragments of %s: %v %v", out, frags, err)
	}
	return frags[0]
}

func TestStagedRenameOfBuiltOutputRejected(t *testing.T) {
	repo, codex, git := stagedRepo(t)
	if err := git("mv", "a.md", "a2.md"); err != nil {
		t.Fatal(err)
	}
	err := checkIn(t, repo, codex)
	if err == nil || !strings.Contains(err.Error(), "a.md: removed from the index in this commit") {
		t.Fatalf("rename of a built output must be rejected, got %v", err)
	}
}

func TestStagedSymlinkedPathsStillCheckFragments(t *testing.T) {
	repo, codex, git := stagedRepo(t)
	link := repo + ".link"
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	frag := fragmentOf(t, codex, "a.md")
	raw, err := os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, frag, append(raw, []byte("more\n")...))
	if err = git("add", frag); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ dir, codex string }{{link, "cx"}, {repo, filepath.Join(link, "cx")}} {
		err = checkIn(t, c.dir, c.codex)
		if err == nil || !strings.Contains(err.Error(), "a.md: built output differs") {
			t.Fatalf("cwd %s codex %s: staged fragment edit must be caught, got %v", c.dir, c.codex, err)
		}
	}
}

func TestStagedReadsFragmentsFromIndex(t *testing.T) {
	repo, codex, git := stagedRepo(t)
	frag := fragmentOf(t, codex, "a.md")
	raw, err := os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, frag, append(raw, []byte("more\n")...))
	if err = Build(codex, false); err != nil {
		t.Fatal(err)
	}
	if err = git("add", "a.md"); err != nil { // собранный файл в коммите, его фрагмент - нет
		t.Fatal(err)
	}
	err = checkIn(t, repo, codex)
	if err == nil || !strings.Contains(err.Error(), "a.md: built output differs") {
		t.Fatalf("committed output must match committed fragments, got %v", err)
	}
	if err = git("add", frag); err != nil {
		t.Fatal(err)
	}
	if err = checkIn(t, repo, codex); err != nil {
		t.Fatalf("output and fragment staged together must pass: %v", err)
	}
}

func TestStagedIgnoresOtherSessionCodexWork(t *testing.T) {
	repo, codex, git := stagedRepo(t)
	if err := os.MkdirAll(filepath.Join(codex, "frag", "zz"), directoryMode); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(codex, "frag", "zz", "orphan.md"), []byte("---\nid: zz/orphan\n---\nx\n"))
	if err := os.Remove(fragmentOf(t, codex, "b.md")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, "o.txt"), []byte("o\n"))
	if err := git("add", "o.txt"); err != nil {
		t.Fatal(err)
	}
	if err := checkIn(t, repo, codex); err != nil {
		t.Fatalf("unstaged codex work of another session must not block this commit: %v", err)
	}
	if err := git("add", filepath.Join(codex, "frag", "zz", "orphan.md")); err != nil {
		t.Fatal(err)
	}
	err := checkIn(t, repo, codex)
	if err == nil || !strings.Contains(err.Error(), "orphan.md: fragment is not in any manifest") {
		t.Fatalf("staged orphan fragment must be rejected, got %v", err)
	}
}

func TestStagedNestedRepoOutputReadFromDisk(t *testing.T) {
	repo, codex, git := stagedRepo(t)
	nested := filepath.Join(repo, "p")
	if err := os.MkdirAll(nested, directoryMode); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("git", "init", "-q")
	c.Dir = nested
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git init nested: %s %v", out, err)
	}
	out := filepath.Join(nested, "AGENTS.md")
	if err := importData(codex, out, "p/AGENTS.md", []byte("# P\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	if err := git("add", "cx"); err != nil {
		t.Fatal(err)
	}
	if err := checkIn(t, repo, codex); err != nil {
		t.Fatalf("output inside a nested repository is checked on disk: %v", err)
	}
	mustWrite(t, out, []byte("# P manual\n"))
	err := checkIn(t, repo, codex)
	if err == nil || !strings.Contains(err.Error(), "AGENTS.md: built output differs") {
		t.Fatalf("manual edit of nested output with its manifest staged must be caught, got %v", err)
	}
}

func TestStagedErrorNamesBothWaysOut(t *testing.T) {
	repo, codex, git := stagedRepo(t)
	mustWrite(t, filepath.Join(repo, "a.md"), []byte("manual\n"))
	if err := git("add", "a.md"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, "a.md"), []byte("manual, then more\n"))
	err := checkIn(t, repo, codex)
	for _, want := range []string{"edit the fragment and run `lex build --codex", "lex absorb --codex", "index and disk differ — stage the whole file"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("error must contain %q, got %v", want, err)
		}
	}
}

func TestStagedReadsManifestFromIndex(t *testing.T) {
	repo, codex, git := stagedRepo(t)
	manifests, err := filepath.Glob(filepath.Join(codex, "manifest", "a.md*.yaml"))
	if err != nil || len(manifests) != 1 {
		t.Fatalf("manifest of a.md: %v %v", manifests, err)
	}
	raw, err := os.ReadFile(manifests[0])
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, manifests[0], []byte(strings.Replace(string(raw), "header: false", "header: true", 1)))
	if err = Build(codex, false); err != nil {
		t.Fatal(err)
	}
	if err = git("add", "a.md"); err != nil { // пересобранный файл в коммите, манифест - нет
		t.Fatal(err)
	}
	err = checkIn(t, repo, codex)
	if err == nil || !strings.Contains(err.Error(), "a.md: built output differs") {
		t.Fatalf("committed output must match the committed manifest, got %v", err)
	}
}

// Ревью: манифест удалён только с диска или только из индекса, битый манифест в коммите,
// refresh с удалёнными фрагментами, удаление фрагмента при кодексе через симлинк, маркер при разных формах пути,
// подсказка про неиндексированный фрагмент.
func manifestOf(t *testing.T, codex, out string) string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(codex, "manifest", out+"*.yaml"))
	if err != nil || len(m) != 1 {
		t.Fatalf("manifest of %s: %v %v", out, m, err)
	}
	return m[0]
}

func TestStagedManifestDeletedOnDiskStillChecked(t *testing.T) {
	repo, codex, git := stagedRepo(t)
	if err := os.Remove(manifestOf(t, codex, "a.md")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, "a.md"), []byte("# A\nHACK\n"))
	if err := git("add", "a.md"); err != nil {
		t.Fatal(err)
	}
	err := checkIn(t, repo, codex)
	if err == nil || !strings.Contains(err.Error(), "a.md: built output differs") {
		t.Fatalf("manifest kept in the index must still guard its output, got %v", err)
	}
}

func TestStagedManifestRemovedFromIndexRejected(t *testing.T) {
	repo, codex, git := stagedRepo(t)
	if err := git("rm", "-q", "--cached", manifestOf(t, codex, "a.md")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, "a.md"), []byte("# A\nHACK\n"))
	if err := git("add", "a.md"); err != nil {
		t.Fatal(err)
	}
	err := checkIn(t, repo, codex)
	if err == nil || !strings.Contains(err.Error(), "removed from the index in this commit") {
		t.Fatalf("manifest removed from the index must be reported, got %v", err)
	}
}

func TestStagedInvalidManifestRejected(t *testing.T) {
	repo, codex, git := stagedRepo(t)
	m := manifestOf(t, codex, "a.md")
	mustWrite(t, m, []byte("garbage\n"))
	if err := git("add", m); err != nil {
		t.Fatal(err)
	}
	err := checkIn(t, repo, codex)
	if err == nil {
		t.Fatal("staged invalid manifest was accepted")
	}
	for line := range strings.SplitSeq(err.Error(), "\n") {
		if strings.Contains(line, m) {
			if !strings.HasPrefix(line, m+": invalid manifest: ") || strings.Count(line, m) != 1 {
				t.Fatalf("staged invalid-manifest line must start with its path exactly once: %s", line)
			}
			return
		}
	}
	t.Fatalf("staged invalid-manifest line missing from error: %v", err)
}

func TestStagedRefreshWithDeletedFragmentsPasses(t *testing.T) {
	repo, codex, git := stagedRepo(t)
	out := filepath.Join(repo, "a.md")
	data := []byte("# a.md renamed\nother body\n")
	mustWrite(t, out, data)
	if err := importData(codex, out, "a.md", data, true); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	if err := git("add", "-A", "cx", "a.md"); err != nil {
		t.Fatal(err)
	}
	if err := checkIn(t, repo, codex); err != nil {
		t.Fatalf("a regular refresh (old fragments deleted) must pass: %v", err)
	}
}

func TestStagedFragmentRemovalViaSymlinkedCodex(t *testing.T) {
	repo, codex, git := stagedRepo(t)
	link := repo + ".link"
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	if err := git("rm", "-q", fragmentOf(t, codex, "a.md")); err != nil {
		t.Fatal(err)
	}
	err := checkIn(t, repo, filepath.Join(link, "cx"))
	if err == nil || !strings.Contains(err.Error(), "a.md") {
		t.Fatalf("fragment removed in the commit must be caught with codex via symlink, got %v", err)
	}
}

func TestMarkerSameForAnyCodexPathForm(t *testing.T) {
	repo := t.TempDir()
	link := repo + ".link"
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	a := generatedMarker(filepath.Join(repo, "cx", "manifest", "a.md.yaml"))
	b := generatedMarker(filepath.Join(link, "cx", "manifest", "a.md.yaml"))
	if !bytes.Equal(a, b) {
		t.Fatalf("marker depends on the path form:\n%s\n%s", a, b)
	}
}

func TestHeaderTildeBuild(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codex := filepath.Join(home, "codex")
	out := filepath.Join(home, "out.md")
	if err := importData(codex, out, "out.md", []byte("body\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := setManifestHeader(codex, "out.md", true); err != nil {
		t.Fatal(err)
	}
	if err := BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	built, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(built, []byte("<!-- lex: собран из ~/codex/manifest/")) {
		t.Fatalf("header did not use home abbreviation: %q", built)
	}
	if err := BuildCheckOptions(codex, BuildCheckOptionsValue{}); err != nil {
		t.Fatalf("build --check rejected generated tilde header: %v", err)
	}
	mustWrite(t, out, bytes.Replace(built, []byte("body"), []byte("edited"), 1))
	if err := BuildWithOptions(codex, BuildOptions{}); err == nil {
		t.Fatal("build did not detect manual body edit")
	}
}

func headerTildeMarker(codex, key string) []byte {
	return []byte("<!-- lex: собран из " + codex + "/manifest/" + key + ".yaml. Правь фрагменты в " + codex + "/frag и запусти `lex build --codex " + codex + "`; правку прямо в этом файле перенеси во фрагменты: `lex absorb --codex " + codex + " <этот файл>` -->\n")
}

func TestHeaderTildeOldForm(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codex := filepath.Join(home, "codex")
	out := filepath.Join(home, "out.md")
	if err := importData(codex, out, "out.md", []byte("body\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := setManifestHeader(codex, "out.md", true); err != nil {
		t.Fatal(err)
	}
	if err := BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(codex, "manifest", codexKey("out.md")+".yaml")
	mustWrite(t, out, append(headerTildeMarker(resolvePath(codex), codexKey("out.md")), []byte("body\n")...))
	if _, err := CheckWithWarnings(codex, CheckOptions{}); err != nil {
		t.Fatalf("lex check rejected old own header: %v", err)
	}
	if err := BuildWithOptions(codex, BuildOptions{}); err != nil {
		t.Fatalf("build treated old own header as manual edit: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want := generatedHeader(path, []byte("body\n"))
	if !bytes.Equal(got, want) {
		t.Fatalf("build did not rewrite old header:\ngot  %q\nwant %q", got, want)
	}
	if err := BuildCheckOptions(codex, BuildCheckOptionsValue{}); err != nil {
		t.Fatalf("build --check after rewrite: %v", err)
	}
}

func TestHeaderTildeForeign(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ours := filepath.Join(home, "ours")
	foreign := filepath.Join(home, "foreign")
	for _, foreignPath := range []string{"~/foreign", resolvePath(foreign)} {
		data := headerTildeMarker(foreignPath, "x")
		err := rejectForeignOutput(ours, "out.md", data)
		if err == nil || !strings.Contains(err.Error(), resolvePath(ours)) || !strings.Contains(err.Error(), resolvePath(foreign)) {
			t.Errorf("foreign marker %q error = %v", foreignPath, err)
		}
	}
}

func TestHeaderTildeOutsideHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codex := filepath.Join(t.TempDir(), "codex")
	marker := generatedMarker(filepath.Join(codex, "manifest", "x.yaml"))
	if !bytes.Contains(marker, []byte(resolvePath(codex)+"/manifest/")) || bytes.Contains(marker, []byte("~/")) {
		t.Fatalf("outside-home marker is not absolute: %s", marker)
	}
}

func TestStagedHintsUnstagedFragment(t *testing.T) {
	repo, codex, git := stagedRepo(t)
	frag := fragmentOf(t, codex, "a.md")
	raw, err := os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, frag, append(raw, []byte("more\n")...))
	if err = Build(codex, false); err != nil {
		t.Fatal(err)
	}
	if err = git("add", "a.md"); err != nil {
		t.Fatal(err)
	}
	err = checkIn(t, repo, codex)
	if err == nil || !strings.Contains(err.Error(), "fragment changed on disk but not staged") {
		t.Fatalf("expected a hint about the unstaged fragment, got %v", err)
	}
}

func TestStagedNonMarkdownInFragIgnored(t *testing.T) {
	repo, codex, git := stagedRepo(t)
	note := filepath.Join(codex, "frag", "notes.txt")
	mustWrite(t, note, []byte("not a fragment\n"))
	if err := git("add", note); err != nil {
		t.Fatal(err)
	}
	if err := checkIn(t, repo, codex); err != nil {
		t.Fatalf("a non-.md file under frag is not a fragment, as in the disk check: %v", err)
	}
}

// twoPartRepo - stagedRepo, где a.md собран из двух фрагментов и закоммичен.
func twoPartRepo(t *testing.T) (repo, codex string, git func(...string) error, frags []string) {
	t.Helper()
	repo, codex, git = stagedRepo(t)
	out := filepath.Join(repo, "a.md")
	data := []byte("# A\none\n\n## B\ntwo\n")
	mustWrite(t, out, data)
	if err := importData(codex, out, "a.md", data, true); err != nil {
		t.Fatal(err)
	}
	if err := Build(codex, false); err != nil {
		t.Fatal(err)
	}
	for _, a := range [][]string{{"add", "-A", "."}, {"commit", "-q", "-m", "two parts"}} {
		if err := git(a...); err != nil {
			t.Fatal(err)
		}
	}
	frags, err := filepath.Glob(filepath.Join(codex, "frag", "a.md", "*.md"))
	if err != nil || len(frags) < 2 {
		t.Fatalf("want two fragments of a.md, got %v %v", frags, err)
	}
	sort.Strings(frags)
	return
}

func TestStagedManifestRemovedWholeWithHandEditRejected(t *testing.T) {
	repo, codex, git := stagedRepo(t)
	if err := git("rm", "-q", manifestOf(t, codex, "a.md")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, "a.md"), []byte("# A\nHACK\n"))
	if err := git("add", "a.md"); err != nil {
		t.Fatal(err)
	}
	err := checkIn(t, repo, codex)
	if err == nil || !strings.Contains(err.Error(), "fragment is not in any manifest") {
		t.Fatalf("fragments left in the index after their manifest is removed are orphans, got %v", err)
	}
}

func TestStagedFullRemovalOfOutputPasses(t *testing.T) {
	repo, codex, git := stagedRepo(t)
	if err := git("rm", "-q", "-r", manifestOf(t, codex, "a.md"), filepath.Join(codex, "frag", "a.md"), "a.md"); err != nil {
		t.Fatal(err)
	}
	if err := checkIn(t, repo, codex); err != nil {
		t.Fatalf("removing an output with its manifest and fragments must pass: %v", err)
	}
}

func TestStagedPartDroppedFromManifestIsOrphan(t *testing.T) {
	repo, codex, git, frags := twoPartRepo(t)
	m := manifestOf(t, codex, "a.md")
	raw, err := os.ReadFile(m)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSuffix(strings.TrimPrefix(filepath.ToSlash(frags[1]), filepath.ToSlash(filepath.Join(codex, "frag"))+"/"), ".md")
	line := "  - " + strconv.Quote(id) + "\n"
	if !bytes.Contains(raw, []byte(line)) {
		t.Fatalf("manifest has no line %q:\n%s", line, raw)
	}
	mustWrite(t, m, bytes.Replace(raw, []byte(line), nil, 1))
	if err = BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if err = git("add", m, "a.md"); err != nil {
		t.Fatal(err)
	}
	err = checkIn(t, repo, codex)
	if err == nil || !strings.Contains(err.Error(), frags[1]+": fragment is not in any manifest") {
		t.Fatalf("a part dropped from the manifest leaves an orphan fragment, got %v", err)
	}
}

func TestStagedHintNamesOnlyChangedFragment(t *testing.T) {
	repo, codex, git, frags := twoPartRepo(t)
	raw, err := os.ReadFile(frags[0])
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, frags[0], append(raw, []byte("more\n")...))
	if err = Build(codex, false); err != nil {
		t.Fatal(err)
	}
	if err = git("add", "a.md"); err != nil {
		t.Fatal(err)
	}
	err = checkIn(t, repo, codex)
	if err == nil || !strings.Contains(err.Error(), frags[0]+": fragment changed on disk") || strings.Contains(err.Error(), frags[1]+": fragment changed on disk") {
		t.Fatalf("the hint must name only the changed fragment, got %v", err)
	}
}

// Сирота, попавшая в HEAD мимо хука, не блокирует коммиты, которые её не делали.
func TestStagedOrphanInHeadDoesNotBlockOtherCommits(t *testing.T) {
	repo, codex, git := stagedRepo(t)
	orphan := filepath.Join(codex, "frag", "zz", "x.md")
	if err := os.MkdirAll(filepath.Dir(orphan), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, orphan, []byte("orphan\n"))
	for _, a := range [][]string{{"add", orphan}, {"commit", "-q", "--no-verify", "-m", "orphan"}} {
		if err := git(a...); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, filepath.Join(repo, "o.txt"), []byte("o\n"))
	if err := git("add", "o.txt"); err != nil {
		t.Fatal(err)
	}
	if err := checkIn(t, repo, codex); err != nil {
		t.Fatalf("an unrelated commit must pass with an orphan already in HEAD: %v", err)
	}
	frag := fragmentOf(t, codex, "b.md")
	raw, err := os.ReadFile(frag)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, frag, append(raw, []byte("more\n")...))
	if err = Build(codex, false); err != nil {
		t.Fatal(err)
	}
	if err = git("add", frag, "b.md"); err != nil {
		t.Fatal(err)
	}
	if err = checkIn(t, repo, codex); err != nil {
		t.Fatalf("a correct fragment commit must pass with an orphan already in HEAD: %v", err)
	}
}

// Сверка без шапки с относительным --codex: шапка снимается и сравнение проходит, как с абсолютным путём.
func TestBuildCheckNoHeaderRelativeCodex(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "a.md")
	data := []byte("# A\nalpha\n")
	mustWrite(t, out, data)
	codex := filepath.Join(dir, "cx")
	if err := importData(codex, out, "a.md", data, false); err != nil {
		t.Fatal(err)
	}
	m := manifestOf(t, codex, "a.md")
	raw, err := os.ReadFile(m)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, m, bytes.Replace(raw, []byte("header: false\n"), []byte("header: true\n"), 1))
	if err = BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if err = BuildCheckOptions("cx", BuildCheckOptionsValue{NoHeader: true}); err != nil {
		t.Fatalf("relative --codex must strip the header like an absolute one: %v", err)
	}
}

func TestStagedHintsFragmentDeletedOnDisk(t *testing.T) {
	repo, codex, git := stagedRepo(t)
	mustWrite(t, filepath.Join(repo, "a.md"), []byte("# A\nHACK\n"))
	if err := git("add", "a.md"); err != nil {
		t.Fatal(err)
	}
	frag := fragmentOf(t, codex, "a.md")
	if err := os.Remove(frag); err != nil {
		t.Fatal(err)
	}
	err := checkIn(t, repo, codex)
	if err == nil || !strings.Contains(err.Error(), frag+": fragment deleted on disk but not staged") {
		t.Fatalf("expected a hint about the fragment deleted on disk, got %v", err)
	}
}

func TestHeaderTildeCodexArgument(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codex := filepath.Join(home, "codex")
	project := filepath.Join(home, "project")
	out := filepath.Join(project, "CLAUDE.md")
	if err := importData(codex, out, "out.md", []byte("body\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := setManifestHeader(codex, "out.md", true); err != nil {
		t.Fatal(err)
	}
	if err := BuildWithOptions(codex, BuildOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	// The graft gate runs lex in the project with the codex path from the header, without a shell.
	t.Chdir(project)
	if warnings, err := CheckWithWarnings("~/codex", CheckOptions{OutputsUnder: project}); err != nil {
		t.Fatalf("~/codex from the header was not expanded: warnings=%v err=%v", warnings, err)
	}
	mustWrite(t, out, []byte("<!-- lex: собран из ~/codex/manifest/out.md.yaml. edited -->\nedited\n"))
	if _, err := CheckWithWarnings("~/codex", CheckOptions{OutputsUnder: project}); err == nil || !strings.Contains(err.Error(), out) {
		t.Fatalf("manual edit was not reported through ~/codex: %v", err)
	}
}
