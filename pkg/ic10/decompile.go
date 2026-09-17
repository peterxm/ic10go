package ic10

import (
	"ic10go/internal/decomp"
)

// Decompile converts raw IC10 source into .icg source.
//
// When structured is true it first tries the structured decompiler and falls
// back to the flat (label/goto) form if the structured result does not compile
// — the same policy as `ic10c decompile --structured`. Structuring is
// best-effort: it can produce a program that compiles but behaves differently,
// so callers that need exact behaviour should use the flat form.
func Decompile(name string, src []byte, structured bool) (string, []decomp.Warning, error) {
	if !structured {
		return decomp.Decompile(string(src))
	}
	code, warns, err := decomp.DecompileStructured(string(src))
	if err != nil {
		return code, warns, err
	}
	if _, diags, cerr := Compile(name, []byte(code)); diags.HasErrors() || cerr != nil {
		return decomp.Decompile(string(src))
	}
	return code, warns, nil
}
