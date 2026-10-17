package builtin

import "testing"

// TestModPrefabsAreInTable keeps modPrefabs a subset of Prefabs: the list is a
// claim about names that do exist (with the mod installed), so a stale or
// mistyped entry would otherwise silently do nothing.
func TestModPrefabsAreInTable(t *testing.T) {
	if len(modPrefabs) == 0 {
		t.Fatal("modPrefabs is empty")
	}
	for name := range modPrefabs {
		if _, ok := Prefabs[name]; !ok {
			t.Errorf("%q is listed as a mod prefab but is not in Prefabs", name)
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
	const want = 138
	if got := len(modPrefabs); got != want {
		t.Errorf("modPrefabs has %d entries, want %d; if the mod set changed, "+
			"re-derive the list and update this count", got, want)
	}
}
