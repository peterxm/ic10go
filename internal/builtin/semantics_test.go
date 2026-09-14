package builtin

import "testing"

// TestBuiltinSemantics makes the classification exhaustive: adding a builtin to
// Funcs without classifying it in semantics.go fails the test, so a side effect
// cannot be silently forgotten (the clrById bug).
func TestBuiltinSemantics(t *testing.T) {
	for name := range Funcs {
		if _, ok := semantics[name]; !ok {
			t.Errorf("builtin %q has no semantics entry; classify it in semantics.go", name)
		}
	}
}

func TestSemOfDefaults(t *testing.T) {
	if s := SemOf("no-such-builtin"); s.SideEffect || s.DeviceArg != -1 {
		t.Errorf("SemOf(unknown) = %+v, want a pure no-device value", s)
	}
	if s := SemOf("clrById"); !s.SideEffect || !s.WritesDev || !s.Barrier {
		t.Errorf("SemOf(clrById) = %+v, want a device-writing side effect", s)
	}
}
