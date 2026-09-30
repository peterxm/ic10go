package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"ic10go/internal/cli"
	"ic10go/internal/source"
	"ic10go/internal/testbench"
	"ic10go/internal/vm"
	"ic10go/pkg/ic10"
)

// cmdTestbench drives the in-game testbench mod (tools/ingame-testbench).
// See docs/ingame-testbench.md.
func cmdTestbench(args []string) int {
	args, libDirs := splitLibArgs(args)
	args, lim, ok := splitLimitArgs(args)
	if !ok {
		return 2
	}

	addr := testbench.Addr()
	stableIns, asJSON, all, diff := false, false, false, false
	force := false
	pulse := false
	dataAccessStack := false
	chipName := ""
	asName := ""
	programFile := ""
	offset := 0.0
	offsetSet := false
	interval := 250
	count := 0
	sub := ""
	var rest []string

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--addr" && i+1 < len(args):
			addr = args[i+1]
			i++
		case strings.HasPrefix(a, "--addr="):
			addr = strings.TrimPrefix(a, "--addr=")
		case a == "--chip" && i+1 < len(args):
			chipName = args[i+1]
			i++
		case strings.HasPrefix(a, "--chip="):
			chipName = strings.TrimPrefix(a, "--chip=")
		case a == "--as" && i+1 < len(args):
			asName = args[i+1]
			i++
		case strings.HasPrefix(a, "--as="):
			asName = strings.TrimPrefix(a, "--as=")
		case a == "--program" && i+1 < len(args):
			programFile = args[i+1]
			i++
		case strings.HasPrefix(a, "--program="):
			programFile = strings.TrimPrefix(a, "--program=")
		case a == "--offset" && i+1 < len(args):
			offset, _ = strconv.ParseFloat(args[i+1], 64)
			offsetSet = true
			i++
		case strings.HasPrefix(a, "--offset="):
			offset, _ = strconv.ParseFloat(strings.TrimPrefix(a, "--offset="), 64)
			offsetSet = true
		case a == "--interval" && i+1 < len(args):
			interval, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--interval="):
			interval, _ = strconv.Atoi(strings.TrimPrefix(a, "--interval="))
		case a == "--count" && i+1 < len(args):
			count, _ = strconv.Atoi(args[i+1])
			i++
		case a == "--json":
			asJSON = true
		case a == "--all":
			all = true
		case a == "--force":
			force = true
		case a == "--pulse":
			pulse = true
		case a == "--data-access" && i+1 < len(args):
			dataAccessStack = args[i+1] == "stack"
			i++
		case strings.HasPrefix(a, "--data-access="):
			dataAccessStack = strings.TrimPrefix(a, "--data-access=") == "stack"
		case a == "--diff":
			diff = true
		case a == "--stable-ins":
			stableIns = true
		case a == "-h" || a == "--help":
			if h, ok := cli.CommandHelp(lang, "testbench"); ok {
				fmt.Print(h)
				return 0
			}
			return 2
		default:
			if sub == "" {
				sub = a
			} else {
				rest = append(rest, a)
			}
		}
	}

	var chip any
	if chipName != "" {
		chip = chipSelector(chipName)
	}

	switch sub {
	case "ping":
		return benchPing(addr, asJSON)
	case "list":
		return benchList(addr, asJSON)
	case "locate":
		return benchLocate(addr, chipName, programFile, asJSON)
	case "hud":
		return benchHud(addr, chipName, rest, offset, offsetSet, asJSON)
	case "push":
		var file string
		if len(rest) > 0 {
			file = rest[0]
		}
		return benchPush(addr, file, chip, asName, stableIns, dataAccessStack, libDirs, lim, asJSON)
	case "state":
		return benchState(addr, chip, all, asJSON)
	case "program":
		return benchProgram(addr, chip, asJSON)
	case "set":
		return benchSet(addr, chip, rest, force, pulse, asJSON)
	case "step":
		n := 1
		if len(rest) > 0 {
			n, _ = strconv.Atoi(rest[0])
		}
		return benchStep(addr, chip, n, asJSON)
	case "ports":
		return benchPorts(addr, chip, asJSON)
	case "pause":
		return benchPause(addr, rest, asJSON)
	case "run":
		var file string
		if len(rest) > 0 {
			file = rest[0]
		}
		return benchRun(addr, file, chip, stableIns, libDirs, lim, diff, asJSON)
	case "watch":
		return benchWatch(addr, chip, interval, count, asJSON)
	case "saves":
		return benchSaves(addr, asJSON)
	case "load":
		return benchLoad(addr, rest, asJSON)
	case "world":
		return benchWorld(addr, asJSON)
	case "help", "":
		if h, ok := cli.CommandHelp(lang, "testbench"); ok {
			fmt.Print(h)
			return 0
		}
		return 2
	default:
		fmt.Fprintln(os.Stderr, cli.UnknownCommand(lang, "testbench "+sub))
		return 2
	}
}

func benchDial(addr string) (*testbench.Client, int) {
	c, err := testbench.Dial(addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return nil, 2
	}
	return c, 0
}

func benchPing(addr string, asJSON bool) int {
	c, rc := benchDial(addr)
	if c == nil {
		return rc
	}
	defer c.Close()
	h, err := c.Ping()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 2
	}
	if asJSON {
		return printJSON(h)
	}
	fmt.Printf("mod        %s %s\n", h.Mod, h.Version)
	fmt.Printf("game       %s\n", h.GameVersion)
	fmt.Printf("paused     %v\n", h.Paused)
	fmt.Printf("chips      %d\n", h.Chips)
	return 0
}

func benchList(addr string, asJSON bool) int {
	c, rc := benchDial(addr)
	if c == nil {
		return rc
	}
	defer c.Close()
	chips, err := c.ListChips()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	if asJSON {
		return printJSON(map[string]any{"chips": chips})
	}
	if len(chips) == 0 {
		fmt.Println("no chips (load the test-bench save)")
		return 0
	}
	for _, ch := range chips {
		line := fmt.Sprintf("  [%d] %-24s %s", ch.Index, ch.Name, ch.Prefab)
		if ch.Lines > 0 {
			line += fmt.Sprintf("  %d lines", ch.Lines)
		}
		if ch.Fingerprint != "" {
			line += "  fp=" + ch.Fingerprint
		}
		if ch.Pos != nil {
			line += fmt.Sprintf("  @(%s)", ch.Pos.String())
		}
		fmt.Println(line)
	}
	return 0
}

// benchLocate reports where a chip is. With --program FILE it finds the chip(s)
// running that program (matching the source fingerprint the mod reports);
// otherwise it prints the position of the chip selected by --chip (name, prefab
// or index).
func benchLocate(addr, chipName, programFile string, asJSON bool) int {
	c, rc := benchDial(addr)
	if c == nil {
		return rc
	}
	defer c.Close()
	chips, err := c.ListChips()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}

	if programFile != "" {
		data, err := os.ReadFile(programFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ic10c:", err)
			return 2
		}
		fp := testbench.Fingerprint(string(data))
		var hits []testbench.Chip
		for _, ch := range chips {
			if ch.Fingerprint != "" && ch.Fingerprint == fp {
				hits = append(hits, ch)
			}
		}
		if asJSON {
			return printJSON(map[string]any{"fp": fp, "program": programFile, "chips": hits})
		}
		if len(hits) == 0 {
			fmt.Printf("no chip runs %s (fp %s)\n", programFile, fp)
			return 1
		}
		fmt.Printf("%d chip(s) run %s (fp %s):\n", len(hits), programFile, fp)
		for _, ch := range hits {
			printChipLocation(ch)
		}
		return 0
	}

	matches := locateMatches(chips, chipName)
	if asJSON {
		return printJSON(map[string]any{"chips": matches})
	}
	if len(matches) == 0 {
		fmt.Fprintln(os.Stderr, "ic10c: no matching chip")
		return 1
	}
	for _, ch := range matches {
		printChipLocation(ch)
	}
	return 0
}

// locateMatches selects the chips to report: by index or loose name/prefab match
// when one is given, else the first chip that has a program.
func locateMatches(chips []testbench.Chip, chipName string) []testbench.Chip {
	var matches []testbench.Chip
	if chipName != "" {
		if n, err := strconv.Atoi(chipName); err == nil {
			for _, ch := range chips {
				if ch.Index == n {
					matches = append(matches, ch)
				}
			}
			return matches
		}
		norm := normalizeName(chipName)
		for _, ch := range chips {
			n := normalizeName(ch.Name)
			p := normalizeName(ch.Prefab)
			if n == norm || p == norm || strings.Contains(n, norm) || strings.Contains(p, norm) {
				matches = append(matches, ch)
			}
		}
		return matches
	}
	for _, ch := range chips {
		if ch.Programmable == nil || *ch.Programmable {
			return append(matches, ch)
		}
	}
	if len(chips) > 0 {
		return append(matches, chips[0])
	}
	return matches
}

// benchHud drives the in-game overlay: `hud` prints its state, `hud on|off`
// toggles it, `hud clear` drops the target, `hud X Y Z` tracks a point and
// `hud --chip NAME|INDEX` tracks a chip's host.
func benchHud(addr, chipName string, args []string, offset float64, offsetSet, asJSON bool) int {
	c, rc := benchDial(addr)
	if c == nil {
		return rc
	}
	defer c.Close()

	req := map[string]any{}
	if offsetSet {
		req["offset"] = offset
	}
	if chipName != "" {
		req["target"] = map[string]any{"chip": chipSelector(chipName)}
	}
	if len(args) > 0 && chipName == "" {
		switch strings.ToLower(args[0]) {
		case "on", "true", "1":
			req["on"] = true
		case "off", "false", "0":
			req["on"] = false
		case "clear", "none":
			req["clear"] = true
		default:
			if len(args) < 3 {
				fmt.Fprintln(os.Stderr, "ic10c: usage: ic10c testbench hud [on|off|clear|X Y Z] [--chip NAME|INDEX]")
				return 2
			}
			x, e1 := strconv.ParseFloat(args[0], 64)
			y, e2 := strconv.ParseFloat(args[1], 64)
			z, e3 := strconv.ParseFloat(args[2], 64)
			if e1 != nil || e2 != nil || e3 != nil {
				fmt.Fprintf(os.Stderr, "ic10c: bad coordinates %q %q %q\n", args[0], args[1], args[2])
				return 2
			}
			req["target"] = map[string]any{"x": x, "y": y, "z": z}
		}
	}

	st, err := c.Hud(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	if asJSON {
		return printJSON(st)
	}
	fmt.Printf("HUD   %s   朝向偏移 %.0f°\n", onOff(st.On), st.Offset)
	if st.Player != nil {
		fmt.Printf("玩家  X %7.1f  Y %7.1f  Z %7.1f    朝向 %.0f°  俯仰 %.0f°\n",
			st.Player.X, st.Player.Y, st.Player.Z, st.Player.Yaw, st.Player.Pitch)
	}
	if st.Target != nil {
		label := ""
		if st.Target.Label != "" {
			label = "  " + st.Target.Label
		}
		fmt.Printf("目标  X %7.1f  Y %7.1f  Z %7.1f%s\n", st.Target.X, st.Target.Y, st.Target.Z, label)
		if st.Player != nil {
			dx, dy, dz := st.Target.X-st.Player.X, st.Target.Y-st.Player.Y, st.Target.Z-st.Player.Z
			dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
			fmt.Printf("      距离 %.1f m\n", dist)
		}
	} else {
		fmt.Println("目标  未设置（hud X Y Z / hud --chip NAME）")
	}
	return 0
}

// chipSelector maps a CLI chip argument to a protocol selector: a plain number
// is an index, anything else a name.
func chipSelector(s string) any {
	if n, err := strconv.Atoi(s); err == nil {
		return map[string]any{"index": n}
	}
	return map[string]any{"name": s}
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func printChipLocation(ch testbench.Chip) {
	name := ch.Name
	if name == "" {
		name = ch.Prefab
	}
	fmt.Printf("[%d] %s  %s", ch.Index, name, ch.Prefab)
	if ch.Lines > 0 {
		fmt.Printf("  %d lines", ch.Lines)
	}
	if ch.Fingerprint != "" {
		fmt.Printf("  fp=%s", ch.Fingerprint)
	}
	fmt.Println()
	if ch.Pos != nil {
		fmt.Printf("     at (%s)\n", ch.Pos.String())
	}
}

func benchStep(addr string, chip any, ticks int, asJSON bool) int {
	c, rc := benchDial(addr)
	if c == nil {
		return rc
	}
	defer c.Close()
	res, err := c.Run(ticks, chip)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	if asJSON {
		return printJSON(res)
	}
	fmt.Printf("stepped %d ticks, line=%v\n", res.Ticks, res.Line)
	return 0
}

func benchSaves(addr string, asJSON bool) int {
	c, rc := benchDial(addr)
	if c == nil {
		return rc
	}
	defer c.Close()
	raw, err := c.Call("world.saves", nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	var res struct {
		Saves []string `json:"saves"`
	}
	if json.Unmarshal(raw, &res) != nil {
		fmt.Println(string(raw))
		return 0
	}
	if asJSON {
		return printJSON(res)
	}
	for _, s := range res.Saves {
		fmt.Println(" ", s)
	}
	return 0
}

func benchLoad(addr string, args []string, asJSON bool) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "ic10c: usage: ic10c testbench load <save-name>")
		return 2
	}
	c, rc := benchDial(addr)
	if c == nil {
		return rc
	}
	defer c.Close()
	raw, err := c.Call("world.load", map[string]any{"save": args[0]})
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	var res struct {
		Result string `json:"result"`
	}
	json.Unmarshal(raw, &res)
	if asJSON {
		return printJSON(res)
	}
	fmt.Printf("load %q: %s\n", args[0], res.Result)
	return 0
}

func benchWorld(addr string, asJSON bool) int {
	c, rc := benchDial(addr)
	if c == nil {
		return rc
	}
	defer c.Close()
	raw, err := c.Call("world.state", nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	var res struct {
		State  string `json:"state"`
		World  string `json:"world"`
		Paused bool   `json:"paused"`
	}
	json.Unmarshal(raw, &res)
	if asJSON {
		return printJSON(res)
	}
	fmt.Printf("state  %s\nworld  %s\npaused %v\n", res.State, res.World, res.Paused)
	return 0
}

func benchPorts(addr string, chip any, asJSON bool) int {
	c, rc := benchDial(addr)
	if c == nil {
		return rc
	}
	defer c.Close()
	args := map[string]any{}
	if chip != nil {
		args["chip"] = chip
	}
	raw, err := c.Call("ports", args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	if asJSON {
		fmt.Println(string(raw))
		return 0
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		fmt.Println(string(raw))
		return 0
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
	return 0
}

func benchPush(addr, file string, chip any, asName string, stableIns, dataAccessStack bool, libDirs []string, lim limitArgs, asJSON bool) int {
	if file == "" {
		fmt.Fprintln(os.Stderr, cli.UsageLine(lang, "testbench"))
		return 2
	}
	opts := ic10.Options{
		StableInsOrder:  stableIns,
		DataAccessStack: dataAccessStack,
		MaxLines:        lim.lines,
		MaxBytes:        lim.bytes,
		MaxLineLen:      lim.line,
		Imports:         true,
		LibDirs:         libDirs,
	}
	compiled, rc := benchCompileResult(file, opts)
	if rc != 0 {
		return rc
	}
	block, err := pickChipBlock(compiled.Chips, asName, chipNameOf(chip))
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 2
	}
	code, loaders := block.Code, block.Loaders

	c, rc := benchDial(addr)
	if c == nil {
		return rc
	}
	defer c.Close()
	res, err := c.Push(code, loaders, chip)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	if asJSON {
		return printJSON(res)
	}
	label := ""
	if block.Name != "" {
		label = " (block " + block.Name + ")"
	}
	fmt.Printf("uploaded %d lines to %s%s", res.Lines, chipName(res.Chip), label)
	if len(loaders) > 0 {
		fmt.Printf(" (+%d loader chunks)", len(loaders))
	}
	fmt.Println()
	return 0
}

// pickChipBlock chooses which `chip` block to upload. `asName` is the explicit
// block name (--as); `target` is the in-game chip name (--chip), matched
// loosely so `chip A` maps to a housing named "A CHIP".
func pickChipBlock(chips []ic10.ChipResult, asName, target string) (ic10.ChipResult, error) {
	if len(chips) == 0 {
		return ic10.ChipResult{}, fmt.Errorf("no compiled chip")
	}
	if asName != "" {
		for _, c := range chips {
			if strings.EqualFold(c.Name, asName) {
				return c, nil
			}
		}
		return ic10.ChipResult{}, fmt.Errorf("no chip block named %q (have %s)", asName, chipBlockNames(chips))
	}
	if len(chips) == 1 {
		return chips[0], nil
	}
	if target != "" {
		norm := normalizeName(target)
		for _, c := range chips {
			n := normalizeName(c.Name)
			if n == norm || strings.HasPrefix(norm, n) || strings.HasPrefix(n, norm) {
				return c, nil
			}
		}
	}
	return ic10.ChipResult{}, fmt.Errorf("source has several chip blocks (%s); pass --as NAME or --chip NAME", chipBlockNames(chips))
}

func chipBlockNames(chips []ic10.ChipResult) string {
	var b strings.Builder
	for i, c := range chips {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(c.Name)
	}
	return b.String()
}

func normalizeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func chipNameOf(chip any) string {
	if m, ok := chip.(map[string]any); ok {
		if n, ok := m["name"].(string); ok {
			return n
		}
	}
	return ""
}

func benchProgram(addr string, chip any, asJSON bool) int {
	c, rc := benchDial(addr)
	if c == nil {
		return rc
	}
	defer c.Close()
	args := map[string]any{}
	if chip != nil {
		args["chip"] = chip
	}
	raw, err := c.Call("program", args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	if asJSON {
		fmt.Println(string(raw))
		return 0
	}
	var res struct {
		Lines int    `json:"lines"`
		Code  string `json:"code"`
	}
	if json.Unmarshal(raw, &res) != nil {
		fmt.Println(string(raw))
		return 0
	}
	// Print the source verbatim so it can be redirected to a file; add a final
	// newline only when the chip's source has none (shells dislike that).
	fmt.Print(res.Code)
	if res.Code != "" && !strings.HasSuffix(res.Code, "\n") {
		fmt.Println()
	}
	return 0
}

func benchState(addr string, chip any, all, asJSON bool) int {
	c, rc := benchDial(addr)
	if c == nil {
		return rc
	}
	defer c.Close()
	st, err := c.State(nil, all, chip)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	if asJSON {
		return printJSON(st)
	}
	fmt.Printf("chip   %s\n", chipName(st.Chip))
	if st.Line != 0 || st.PC != 0 {
		fmt.Printf("line   %v  (pc %d)\n", st.Line, st.PC)
	}
	if len(st.Registers) > 0 {
		fmt.Println("regs  ", formatRegisters(st.Registers))
	}
	if st.Stack != nil {
		fmt.Printf("stack  sp=%d size=%d\n", st.Stack.SP, st.Stack.Size)
		for i := 0; i <= st.Stack.SP; i++ {
			if v, ok := st.Stack.Values[strconv.Itoa(i)]; ok && v != 0 {
				fmt.Printf("       [%d] = %v\n", i, v)
			}
		}
	}
	for _, d := range st.Devices {
		keys := make([]string, 0, len(d.Logic))
		for k := range d.Logic {
			keys = append(keys, k)
		}
		sortStrings(keys)
		if len(keys) == 0 {
			continue
		}
		fmt.Printf("%s  %s\n", d.Port, d.Prefab)
		for _, k := range keys {
			fmt.Printf("       %s = %v\n", k, d.Logic[k])
		}
	}
	if st.Errors != nil && (st.Errors.Code != "" || st.Errors.Compilation) {
		fmt.Printf("error  %s line=%d\n", st.Errors.Code, st.Errors.Line)
	}
	return 0
}

func benchSet(addr string, chip any, args []string, force, pulse, asJSON bool) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "ic10c: usage: ic10c testbench set [--force] [--pulse] d1.Setting=10 ...")
		return 2
	}
	writes := make([]testbench.DeviceWrite, 0, len(args))
	for _, a := range args {
		w, ok := parseWrite(a)
		if !ok {
			fmt.Fprintf(os.Stderr, "ic10c: bad write %q (want dN.Logic=value)\n", a)
			return 2
		}
		writes = append(writes, w)
	}
	c, rc := benchDial(addr)
	if c == nil {
		return rc
	}
	defer c.Close()
	req := map[string]any{"writes": writes}
	if force {
		req["force"] = true
	}
	if pulse {
		req["pulse"] = true
	}
	if chip != nil {
		req["chip"] = chip
	}
	var res struct {
		Applied int `json:"applied"`
	}
	if err := c.CallInto("set", req, &res); err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	if asJSON {
		return printJSON(res)
	}
	fmt.Printf("set %d values\n", res.Applied)
	return 0
}

func benchPause(addr string, args []string, asJSON bool) int {
	on := true
	if len(args) > 0 {
		switch args[0] {
		case "off", "false", "0", "no":
			on = false
		}
	}
	c, rc := benchDial(addr)
	if c == nil {
		return rc
	}
	defer c.Close()
	var res struct {
		Paused bool `json:"paused"`
	}
	if err := c.CallInto("pause", map[string]any{"on": on}, &res); err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	if asJSON {
		return printJSON(res)
	}
	fmt.Printf("paused = %v\n", res.Paused)
	return 0
}

// benchPauseForRun pauses the world unless it already is, and returns a function
// that restores the previous state. Stepping with a paused world keeps the run
// deterministic.
func benchPauseForRun(c *testbench.Client) func() {
	h, err := c.Ping()
	if err != nil || h.Paused {
		return func() {}
	}
	if _, err := c.Call("pause", map[string]any{"on": true}); err != nil {
		fmt.Fprintln(os.Stderr, "ic10c: warning: could not pause the game:", err)
		return func() {}
	}
	return func() {
		if _, err := c.Call("pause", map[string]any{"on": false}); err != nil {
			fmt.Fprintln(os.Stderr, "ic10c: warning: could not unpause the game:", err)
		}
	}
}

func benchRun(addr, file string, chip any, stableIns bool, libDirs []string, lim limitArgs, diff, asJSON bool) int {
	if file == "" {
		fmt.Fprintln(os.Stderr, cli.UsageLine(lang, "testbench"))
		return 2
	}
	sc, err := testbench.LoadScenario(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 2
	}

	// Scenario args are build flags; command-line limits/lib dirs also apply.
	opts, err := benchOptions(sc.Args, lim, libDirs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 2
	}
	opts.StableInsOrder = opts.StableInsOrder || stableIns
	prog := sc.Program
	if !filepath.IsAbs(prog) {
		prog = filepath.Join(filepath.Dir(file), prog)
	}
	// The CLI --chip overrides the scenario's selector.
	if chip != nil {
		if b, err := json.Marshal(chip); err == nil {
			sc.Chip = b
		}
	}
	code, loaders, rc := benchCompileOpts(prog, opts)
	if rc != 0 {
		return rc
	}

	c, rc := benchDial(addr)
	if c == nil {
		return rc
	}
	defer c.Close()

	// A testbench wants control over ticks: pause the world for the run (the
	// chip is stepped with ProgrammableChip.Execute) and restore it after.
	resume := benchPauseForRun(c)
	defer resume()

	rep, err := (&testbench.Runner{C: c, Code: code, Loaders: loaders}).Run(sc)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}

	if diff {
		vmReport, err := benchDiff(sc, code, loaders)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ic10c: vm diff:", err)
		} else if !asJSON {
			fmt.Println("VM diff:")
			fmt.Println(vmReport)
		}
	}

	if asJSON {
		printJSON(rep)
	} else {
		fmt.Println(rep.String())
	}
	if !rep.OK() {
		return 1
	}
	return 0
}

func benchWatch(addr string, chip any, interval, count int, asJSON bool) int {
	c, rc := benchDial(addr)
	if c == nil {
		return rc
	}
	defer c.Close()
	var res struct {
		Watching bool `json:"watching"`
	}
	if err := c.CallInto("watch", map[string]any{"on": true, "intervalMs": interval, "chip": chip}, &res); err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	fmt.Fprintln(os.Stderr, "watching (Ctrl+C to stop)")
	n := 0
	for raw := range c.Events() {
		var ev struct {
			Event string          `json:"event"`
			Seq   int             `json:"seq"`
			State json.RawMessage `json:"state"`
		}
		if json.Unmarshal(raw, &ev) != nil || ev.Event != "state" {
			continue
		}
		if asJSON {
			fmt.Println(string(raw))
		} else {
			fmt.Printf("seq %d: %s\n", ev.Seq, summarizeState(ev.State))
		}
		n++
		if count > 0 && n >= count {
			break
		}
	}
	return 0
}

// benchCompileOpts compiles a .icg with the given options.
func benchCompileOpts(file string, opts ic10.Options) (string, []string, int) {
	res, rc := benchCompileResult(file, opts)
	if rc != 0 {
		return "", nil, rc
	}
	return res.Code, res.Loaders, 0
}

func benchCompileResult(file string, opts ic10.Options) (ic10.Result, int) {
	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return ic10.Result{}, 2
	}
	ic10Hint(file)
	opts.Imports = true
	compiled, diags, err := ic10.CompileResult(file, data, opts)
	if rc := report(source.NewFile(file, data), diags); rc != 0 {
		return ic10.Result{}, rc
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return ic10.Result{}, 1
	}
	return compiled, 0
}

// benchOptions maps the build flags allowed in a scenario's "args".
func benchOptions(flags []string, lim limitArgs, libDirs []string) (ic10.Options, error) {
	opts := ic10.Options{
		MaxLines:   lim.lines,
		MaxBytes:   lim.bytes,
		MaxLineLen: lim.line,
		Imports:    true,
		LibDirs:    libDirs,
	}
	for i := 0; i < len(flags); i++ {
		switch a := flags[i]; {
		case a == "--stable-ins":
			opts.StableInsOrder = true
		case a == "--rel-jump":
			opts.RelJump = true
		case a == "--jump-table":
			opts.JumpTable = true
		case a == "--auto-table":
			opts.AutoTable = true
		case a == "--fast":
			opts.Fast = true
		case a == "--unsafe":
			opts.Unsafe = true
		case a == "--redundant-device-writes":
			opts.RedundantDeviceWrites = true
		case a == "--merge-renamed-tails":
			opts.MergeRenamedTails = true
		case a == "--extract-setup":
			opts.ExtractSetup = true
		case a == "--data-layout" && i+1 < len(flags):
			opts.DataLayout = flags[i+1]
			i++
		case strings.HasPrefix(a, "--data-layout="):
			opts.DataLayout = strings.TrimPrefix(a, "--data-layout=")
		case a == "--data-access" && i+1 < len(flags):
			opts.DataAccessStack = flags[i+1] == "stack"
			i++
		case strings.HasPrefix(a, "--data-access="):
			opts.DataAccessStack = strings.TrimPrefix(a, "--data-access=") == "stack"
		case a == "--lib" && i+1 < len(flags):
			opts.LibDirs = append(opts.LibDirs, flags[i+1])
			i++
		case strings.HasPrefix(a, "--lib="):
			opts.LibDirs = append(opts.LibDirs, strings.TrimPrefix(a, "--lib="))
		default:
			return opts, fmt.Errorf("scenario args: unsupported flag %q", a)
		}
	}
	return opts, nil
}

// benchDiff runs the scenario in the built-in VM and reports its expectations.
// Ticks are modelled the way the game does them: Execute runs up to 128
// instructions per tick, and a program that yields once per loop advances one
// tick per iteration.
func benchDiff(sc *testbench.Scenario, code string, loaders []string) (*testbench.Report, error) {
	m := vm.New()
	for _, chunk := range loaders {
		if err := m.Load(chunk); err != nil {
			return nil, err
		}
		m.Run(strings.Count(chunk, "\n") + 1)
	}
	if err := m.Load(code); err != nil {
		return nil, err
	}
	rep := &testbench.Report{}
	for _, cs := range sc.Cases {
		res := testbench.CaseResult{Name: cs.Name, Passed: true}
		for k, v := range cs.Set {
			port, logic, err := splitPortLogic(k)
			if err != nil {
				return nil, err
			}
			m.Set(port, logic, v)
		}
		if cs.Run > 0 {
			if err := m.RunTicks(cs.Run); err != nil && err != vm.ErrStepLimit {
				return nil, err
			}
		}
		for k, want := range cs.Expect {
			port, logic, err := splitPortLogic(k)
			if err != nil {
				return nil, err
			}
			got := m.Device(port).Values[logic]
			if got != want {
				res.Failures = append(res.Failures, testbench.Failure{
					Key: k, Want: fmt.Sprint(want), Got: fmt.Sprint(got),
				})
			}
		}
		res.Passed = len(res.Failures) == 0
		rep.Cases = append(rep.Cases, res)
		if res.Passed {
			rep.Passed++
		} else {
			rep.Failed++
		}
	}
	return rep, nil
}

func parseWrite(s string) (testbench.DeviceWrite, bool) {
	eq := strings.IndexByte(s, '=')
	if eq < 0 {
		return testbench.DeviceWrite{}, false
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s[eq+1:]), 64)
	if err != nil {
		return testbench.DeviceWrite{}, false
	}
	port, logic, err := splitPortLogic(s[:eq])
	if err != nil {
		return testbench.DeviceWrite{}, false
	}
	return testbench.DeviceWrite{Port: port, Logic: logic, Value: v}, true
}

func splitPortLogic(s string) (string, string, error) {
	dot := strings.IndexByte(s, '.')
	if dot <= 0 || dot == len(s)-1 {
		return "", "", fmt.Errorf("bad key %q", s)
	}
	port := s[:dot]
	if port[0] != 'd' && port[0] != 'D' {
		port = "d" + port
	}
	return port, s[dot+1:], nil
}

func chipName(c testbench.Chip) string {
	if c.Name != "" {
		return c.Name
	}
	if c.Prefab != "" {
		return c.Prefab
	}
	return fmt.Sprintf("chip#%d", c.Index)
}

func formatRegisters(r map[string]float64) string {
	order := []string{"r0", "r1", "r2", "r3", "r4", "r5", "r6", "r7",
		"r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15", "ra", "sp"}
	var b strings.Builder
	for _, k := range order {
		if v, ok := r[k]; ok {
			if b.Len() > 0 {
				b.WriteString("  ")
			}
			fmt.Fprintf(&b, "%s=%v", k, v)
		}
	}
	return b.String()
}

func summarizeState(raw json.RawMessage) string {
	var st testbench.State
	if json.Unmarshal(raw, &st) != nil {
		return string(raw)
	}
	s := fmt.Sprintf("%s line=%v", chipName(st.Chip), st.Line)
	if len(st.Registers) > 0 {
		s += "  " + formatRegisters(st.Registers)
	}
	return s
}

func printJSON(v any) int {
	data, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	fmt.Println(string(data))
	return 0
}

// sortStrings is a tiny insertion sort to avoid importing sort in this file.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
