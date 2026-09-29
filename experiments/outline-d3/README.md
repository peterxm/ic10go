# D3 outline measurement

Evidence for the non-leaf outlining change (D3a, see `docs/backlog.md` §D3).

- `lib/util.icg`, `lib/control.icg`, `main.icg` — a layered library: the
  non-leaf `step` (calls `clamp`) is used by three control loops. Before D3a it
  was inlined three times: **53 lines / 8 registers**.
- With D3a (`main.icg` now): **28 lines / 2 registers**, same VM writes.
- `leaf.icg` is the same program with `clamp` written out inside `step` so it is
  a leaf; the compiler outlines it once (27 lines). It was the pre-change proxy
  for the D3a shape.

The in-repo regression tests are `internal/lower` `TestPlanOutlinesNonLeaf` and
`pkg/ic10` `TestOutlineNonLeaf`; the real-machine scenario is
`testdata/bench/ingame/s50_nonleaf_outline.json`.

```sh
ic10c stats experiments/outline-d3/main.icg     # 28 lines (D3a)
IC10C_NO_OUTLINE=1 ic10c stats experiments/outline-d3/main.icg  # 53 lines
```
