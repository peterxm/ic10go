#!/usr/bin/env sh
# Run the in-game regression scenarios from ingame-test-plan.md against the
# running game (mod tools/ingame-testbench). Each scenario selects its own chip
# and pauses the world for a deterministic run.
#
#   sh testdata/bench/ingame/run.sh           # real machine + VM diff
#   IC10C=/path/to/ic10c sh .../run.sh
#
# Exit code 0 only if every scenario passes.
set -u

IC10C=${IC10C:-ic10c}
here=$(cd "$(dirname "$0")" && pwd)

pass=0
fail=0
for j in "$here"/*.json; do
	[ -e "$j" ] || continue
	printf '=== %s ===\n' "$(basename "$j")"
	if "$IC10C" testbench run "$j" --diff; then
		pass=$((pass + 1))
	else
		fail=$((fail + 1))
	fi
	echo
done

echo "scenarios: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
