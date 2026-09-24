package builtin

import "testing"

// TestPrefabsFromGame checks the prefab table was generated from the game and
// still hashes back.
func TestPrefabsFromGame(t *testing.T) {
	if len(Prefabs) < 2000 {
		t.Errorf("Prefabs has %d entries, want 2000+ (regenerate with tools/gengamedata)", len(Prefabs))
	}
	if len(PrefabByHash) != len(Prefabs) {
		t.Errorf("PrefabByHash has %d entries, Prefabs has %d", len(PrefabByHash), len(Prefabs))
	}
	if got := PrefabByHash[Hash("ItemIronOre")]; got != "ItemIronOre" {
		t.Errorf("PrefabByHash[Hash(ItemIronOre)] = %q", got)
	}
}

// TestScriptCommandHelp checks the instruction help came from the game and was
// applied over the instruction table.
func TestScriptCommandHelp(t *testing.T) {
	if len(ScriptCommandHelp) < 100 {
		t.Fatalf("ScriptCommandHelp has %d entries, want 100+", len(ScriptCommandHelp))
	}
	for _, m := range []string{"add", "sub", "mul", "div", "l", "s", "beq", "bdse", "yield"} {
		if ScriptCommandHelp[m] == "" {
			t.Errorf("ScriptCommandHelp missing %q", m)
		}
		if ins, ok := IC10Instructions[m]; ok && ins.Desc != ScriptCommandHelp[m] {
			t.Errorf("IC10Instructions[%s].Desc not refreshed from the game:\n got %q\nwant %q", m, ins.Desc, ScriptCommandHelp[m])
		}
	}
}
