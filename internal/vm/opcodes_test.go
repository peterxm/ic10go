package vm

import (
	"sort"
	"testing"

	"ic10go/internal/builtin"
)

// TestOpCodesKnownToLSP checks every native mnemonic the VM executes is in the
// LSP's instruction table. The game's localization omits several valid
// mnemonics (clamp/lerp/sgn/pow/rol/ror/ext/ins/clr/clrd/rmap), so the table is
// the only thing keeping the editor from flagging them as unknown.
func TestOpCodesKnownToLSP(t *testing.T) {
	var missing []string
	for op := range opArity {
		if _, ok := builtin.IC10Instructions[op]; !ok {
			missing = append(missing, op)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("VM opcodes missing from builtin.IC10Instructions: %v", missing)
	}
}
