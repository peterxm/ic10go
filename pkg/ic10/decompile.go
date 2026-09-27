package ic10

import (
	"ic10go/internal/decomp"
)

// dynamicStackDirective makes the compiler size the user stack from the actual
// data/spill usage, so a program may use high stack slots. The decompiler
// prepends it when the translated program only fails the default fixed
// [0..127] user bound.
const dynamicStackDirective = "// icg: dynamic-stack\n"

// Decompile converts raw IC10 source into .icg source.
//
// When structured is true it first tries the structured decompiler and falls
// back to the flat (label/goto) form if the structured result does not compile
// — the same policy as `ic10c decompile --structured`. Structuring is
// best-effort: it can produce a program that compiles but behaves differently,
// so callers that need exact behaviour should use the flat form.
//
// Legacy IC10 often manages the whole 512-slot stack. Such a program only fails
// the default fixed user-stack bound, so the output gets the `dynamic-stack`
// pragma and builds without flags (CLI and editor alike).
func Decompile(name string, src []byte, structured bool) (string, []decomp.Warning, error) {
	if !structured {
		code, warns, err := decomp.Decompile(string(src))
		if err != nil {
			return code, warns, err
		}
		code, _ = compileOrMarkDynamic(name, code)
		return code, warns, nil
	}
	code, warns, err := decomp.DecompileStructured(string(src))
	if err != nil {
		return code, warns, err
	}
	if marked, ok := compileOrMarkDynamic(name, code); ok {
		return marked, warns, nil
	}
	// Structuring could not be trusted; fall back to the flat form.
	flat, fwarns, ferr := decomp.Decompile(string(src))
	if ferr != nil {
		return flat, fwarns, ferr
	}
	flat, _ = compileOrMarkDynamic(name, flat)
	return flat, fwarns, nil
}

// compileOrMarkDynamic reports whether code compiles, prepending the
// dynamic-stack pragma when that is what makes it compile (a legacy program
// using slots above the fixed user stack). The first result is code unchanged
// when neither variant compiles, so callers can still fall back to the flat
// form.
func compileOrMarkDynamic(name, code string) (string, bool) {
	if compiles(name, code) {
		return code, true
	}
	if marked := dynamicStackDirective + code; compiles(name, marked) {
		return marked, true
	}
	return code, false
}

// compiles reports whether code compiles without errors.
func compiles(name, code string) bool {
	_, diags, err := Compile(name, []byte(code))
	return !diags.HasErrors() && err == nil
}
