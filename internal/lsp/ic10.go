package lsp

import (
	"fmt"
	"strings"

	"ic10go/internal/builtin"
	"ic10go/internal/ic10asm"
)

// Native IC10 (.ic/.ic10) support: instruction completion/hover/diagnostics,
// plus prefab completion/hover inside HASH("…"). It reuses the shared
// builtin.IC10Instructions / Prefabs tables.

// ic10Diagnostics reports unknown IC10 instructions (and duplicate labels).
func ic10Diagnostics(text string) []lspDiagnostic {
	var out []lspDiagnostic
	labels := map[string]int{}
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(ic10asm.StripComment(raw))
		if line == "" {
			continue
		}
		if ic10asm.IsLabel(line) {
			name := strings.TrimSuffix(line, ":")
			if prev, ok := labels[name]; ok {
				out = append(out, lspDiagnostic{
					Range:    lspRange{Start: lspPosition{i, 0}, End: lspPosition{i, len(name)}},
					Severity: 2,
					Source:   "ic10c",
					Code:     "ic10-duplicate-label",
					Message:  fmt.Sprintf("label %q already defined on line %d", name, prev+1),
				})
			}
			labels[name] = i
			continue
		}
		fields := ic10asm.Tokenize(line)
		if len(fields) == 0 {
			continue
		}
		op := strings.ToLower(fields[0])
		if op == "alias" || op == "define" {
			continue
		}
		if _, ok := builtin.IC10Instructions[op]; ok {
			continue
		}
		col := strings.Index(raw, fields[0])
		if col < 0 {
			col = 0
		}
		out = append(out, lspDiagnostic{
			Range:    lspRange{Start: lspPosition{i, col}, End: lspPosition{i, col + len(fields[0])}},
			Severity: 1,
			Source:   "ic10c",
			Code:     "ic10-unknown-instruction",
			Message:  fmt.Sprintf("unknown IC10 instruction %q", fields[0]),
		})
	}
	return out
}

// ic10CompletionItems offers instructions, logic types, batch modes and, inside
// HASH("…"), prefab names.
func ic10CompletionItems(text string, pos lspPosition) []completionItem {
	off := posToOffset(text, pos)
	if start, prefix, ok := hashArgContext(text, off); ok {
		return prefabItems(text, start, off, prefix)
	}
	items := make([]completionItem, 0, len(builtin.IC10Instructions)+32)
	// HASH("…") / STR("…") are IC10 macros, not in the instruction table.
	for _, m := range []string{"HASH", "STR"} {
		items = append(items, completionItem{Label: m, Kind: 3, Detail: m + `("…")`})
	}
	for name, ins := range builtin.IC10Instructions {
		items = append(items, completionItem{
			Label: name, Kind: 3, Detail: ins.Sig,
			Data: map[string]any{"label": name, "kind": "ic10"},
		})
	}
	for name := range builtin.LogicTypes {
		items = append(items, completionItem{Label: name, Kind: 21, Detail: "logic type"})
	}
	for name := range builtin.BatchModes {
		items = append(items, completionItem{Label: name, Kind: 21, Detail: "batch mode"})
	}
	sortItems(items)
	return items
}

// ic10Hover describes an instruction, logic type, defined name or prefab.
func (s *Server) ic10Hover(text string, pos lspPosition) string {
	if content := s.prefabHoverAt(text, pos); content != "" {
		return content
	}
	word, _ := wordAtOffset(text, pos)
	if word == "" {
		return ""
	}
	switch strings.ToUpper(word) {
	case "HASH":
		return "`HASH(\"PrefabName\")` — CRC-32 prefab hash"
	case "STR":
		return "`STR(\"text\")` — display string"
	}
	if ins, ok := builtin.IC10Instructions[strings.ToLower(word)]; ok {
		sig := strings.ToLower(word)
		if ins.Sig != "" {
			sig += " " + ins.Sig
		}
		if ins.Desc == "" {
			return "```ic10\n" + sig + "\n```"
		}
		return "```ic10\n" + sig + "\n```\n\n" + ins.Desc
	}
	if d, ok := builtin.LogicTypeDocs[word]; ok {
		return docText(d, s.zh)
	}
	if builtin.LogicTypes[word] {
		if s.zh {
			return "logic type `" + word + "`（设备逻辑属性）"
		}
		return "logic type `" + word + "` (device property)"
	}
	if builtin.SlotTypes[word] {
		if s.zh {
			return "slot type `" + word + "`（槽位属性）"
		}
		return "slot type `" + word + "` (slot property)"
	}
	if v, ok := ic10Defines(text)[word]; ok {
		if s.zh {
			return "`" + word + "` = `" + v + "`（alias/define）"
		}
		return "`" + word + "` = `" + v + "` (alias/define)"
	}
	return ""
}

// ic10Defines collects alias/define name → value pairs.
func ic10Defines(text string) map[string]string {
	out := map[string]string{}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(ic10asm.StripComment(raw))
		fields := ic10asm.Tokenize(line)
		if len(fields) < 3 {
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "alias", "define":
			out[fields[1]] = strings.Join(fields[2:], " ")
		}
	}
	return out
}
