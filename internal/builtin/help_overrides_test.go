package builtin

import (
	"strings"
	"testing"
)

// TestHelpOverridesAreNeeded checks that the hand-written descriptions really win
// over the game's for the mnemonics listed in scriptHelpKeep: both of the game's
// record families write those without the tolerance operand (bapz a b c), so the
// init overlay must skip them.
func TestHelpOverridesAreNeeded(t *testing.T) {
	if len(scriptHelpKeep) == 0 {
		t.Fatal("scriptHelpKeep 是空的，覆盖没了")
	}
	for m := range scriptHelpKeep {
		ins, ok := IC10Instructions[m]
		if !ok {
			t.Errorf("IC10Instructions 缺 %q（scriptHelpKeep 里还留着它）", m)
			continue
		}
		if !strings.Contains(ins.Desc, "max") {
			t.Errorf("%s 的手写说明没保下来（丢了容差操作数 b）：%q", m, ins.Desc)
		}
		if ScriptCommandHelp[m] == "" {
			t.Errorf("ScriptCommandHelp 缺 %q（生成的说明表不全）", m)
		}
	}
}

// TestHelpOverridesStillDiffer is the "should this override still exist" signal:
// once the game's text for one of them carries the tolerance operand, the override
// is unnecessary and should be dropped from scriptHelpKeep.
func TestHelpOverridesStillDiffer(t *testing.T) {
	for m := range scriptHelpKeep {
		game := ScriptCommandHelp[m]
		ins, ok := IC10Instructions[m]
		if !ok || game == "" {
			continue
		}
		if game == ins.Desc {
			t.Errorf("%s 的游戏说明和手写版已经一样了（%q）—— 可从 scriptHelpKeep 去掉", m, game)
		}
	}
}
