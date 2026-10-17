package builtin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

// TestGrammarCoversEveryInstruction is the editor-side counterpart of
// TestGameInstructionCoverage: every mnemonic the table knows must be highlighted
// by the IC10 grammar's keyword pattern, or a valid instruction renders as plain
// text. The pattern is an explicit alternation (RE2-compatible, so it can be
// compiled here), which makes "the table gained a mnemonic and the grammar did not"
// a test failure instead of something a user notices by eye.
//
// Mismatches are reported as "matched only the prefix" when the pattern covers a
// shorter alternative: that is how `l`/`s` hid behind `ld`/`sd` and `ls`/`ss`.
func TestGrammarCoversEveryInstruction(t *testing.T) {
	path := filepath.Join("..", "..", "editors", "vscode", "syntaxes", "ic10.tmLanguage.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("grammar not found: %v", err)
	}
	var doc struct {
		Repository map[string]struct {
			Patterns []struct {
				Name  string `json:"name"`
				Match string `json:"match"`
			} `json:"patterns"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	pattern := ""
	for _, group := range doc.Repository {
		for _, p := range group.Patterns {
			if p.Name == "keyword.instruction.ic10" {
				pattern = p.Match
			}
		}
	}
	if pattern == "" {
		t.Fatalf("%s: no keyword.instruction.ic10 pattern", path)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("keyword.instruction.ic10 is not RE2-compatible: %v", err)
	}

	// Entries of IC10Instructions that are not game mnemonics: the compiler
	// directives and the decompiler's placeholder row.
	notMnemonics := map[string]bool{"alias": true, "define": true, "label": true, "command": true}

	names := make([]string, 0, len(IC10Instructions))
	for m := range IC10Instructions {
		if !notMnemonics[m] {
			names = append(names, m)
		}
	}
	sort.Strings(names)
	for _, m := range names {
		if got := re.FindString(m); got != m {
			t.Errorf("the editor does not highlight %q (the pattern matched %q); add it to keyword.instruction.ic10", m, got)
		}
	}
}
