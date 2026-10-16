package builtin

// scriptHelpKeep lists the mnemonics whose hand-written description in
// instructions.go must survive the init overlay in scripthelp_gen.go.
//
// Both of the game's localization families describe these instructions with an
// expression that drops the tolerance operand, which makes them wrong rather
// than merely terse. bapz is `bapz a b c` — branch to c when a is approximately
// zero, with b as the tolerance — and the game says
//
//	Branch to line c if abs(a) <= float.epsilon * 8
//
// while the table says
//
//	Branch to line c if abs(a) <= max(b * abs(a), float.epsilon * 8)
//
// The same happens to the -al and relative forms, and to the sapz/snaz
// comparison results, which the game renders as `|a| <= float.epsilon * 8`.
//
// This is a hand-maintained list on purpose. It is a claim about the game's
// strings, so it has to be re-checked when the game rewrites them:
// TestHelpOverridesAreNeeded fails if one of these mnemonics stops carrying the
// override, and TestHelpOverridesStillDiffer fails once the game's text becomes
// good enough that the override can be dropped.
var scriptHelpKeep = map[string]bool{
	"bapz":   true,
	"bapzal": true,
	"bnaz":   true,
	"bnazal": true,
	"brapz":  true,
	"brnaz":  true,
	"sapz":   true,
	"snaz":   true,
}
