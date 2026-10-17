package builtin

import "testing"

// TestModPrefabsAreNotInTable: the tables are the game's, not the enabled mods'.
// tools/import-devices drops every name in modPrefabs on import, so a name that
// is back means either the filter was bypassed or the export smuggled a mod in.
func TestModPrefabsAreNotInTable(t *testing.T) {
	if len(modPrefabs) == 0 {
		t.Fatal("modPrefabs is empty")
	}
	for name := range modPrefabs {
		if _, ok := Prefabs[name]; ok {
			t.Errorf("%q comes from a third-party mod but is in Prefabs; re-run tools/import-devices", name)
		}
		for _, r := range name {
			if r > 127 {
				t.Errorf("%q is not ASCII", name)
				break
			}
		}
	}
}

// TestModPrefabsCount pins the size of the list. Re-deriving it after a new
// export is a deliberate act (see the comment on modPrefabs): update both.
func TestModPrefabsCount(t *testing.T) {
	const want = 395
	if got := len(modPrefabs); got != want {
		t.Errorf("modPrefabs has %d entries, want %d; if the mod set changed, "+
			"re-derive the list and update this count", got, want)
	}
}
