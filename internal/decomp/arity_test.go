package decomp

import (
	"testing"

	"ic10go/internal/builtin"
)

// TestFallbackArityDoesNotShadowTheTable: the fallback exists for mnemonics the
// game has no record for. An entry that is *in* IC10Instructions is dead weight
// (arity checks the table first) and, worse, hides the fact that it was added for
// a premise that no longer holds — exactly what the table's old "the generated
// metadata omits them" comment did for eleven mnemonics.
func TestFallbackArityDoesNotShadowTheTable(t *testing.T) {
	for m := range fallbackArity {
		if _, ok := builtin.IC10Instructions[m]; ok {
			t.Errorf("fallbackArity[%q] is dead: it is in IC10Instructions already", m)
		}
	}
}

// TestArityCoversTheTableAndRet pins both halves: a native mnemonic resolves
// through the generated table, and `ret` (ours, emitted for `j ra`) through the
// fallback. The expected counts are the table's Sig field counts.
func TestArityCoversTheTableAndRet(t *testing.T) {
	if n, ok := arity("ret"); !ok || n != 0 {
		t.Errorf("arity(ret) = %d, %v; want 0, true", n, ok)
	}
	for op, want := range map[string]int{
		"clamp": 4, "rmap": 3, "clr": 1, "clrd": 1, "sgn": 2, "pow": 3,
		"move": 2, "yield": 0,
	} {
		if n, ok := arity(op); !ok || n != want {
			t.Errorf("arity(%s) = %d, %v; want %d, true", op, n, ok, want)
		}
	}
	if _, ok := arity("nosuchop"); ok {
		t.Error("arity must fail for an unknown mnemonic")
	}
}
