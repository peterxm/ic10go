package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"ic10go/internal/ast"
	"ic10go/internal/cfg"
	"ic10go/internal/cli"
	"ic10go/internal/diag"
	"ic10go/internal/disasm"
	"ic10go/internal/flow"
	"ic10go/internal/ic10asm"
	"ic10go/internal/lexer"
	"ic10go/internal/lsp"
	"ic10go/internal/minify"
	"ic10go/internal/parser"
	"ic10go/internal/source"
	"ic10go/internal/version"
	"ic10go/internal/vm"
	"ic10go/pkg/ic10"
)

// lang is the resolved output language for the current invocation.
var lang = cli.EN

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	lang = cli.Detect()

	i := 0
parse:
	for i < len(argv) {
		switch a := argv[i]; {
		case a == "-L" || a == "--lang":
			if i+1 >= len(argv) {
				fmt.Fprintln(os.Stderr, missingLang())
				return 2
			}
			l, ok := cli.ParseLang(argv[i+1])
			if !ok {
				fmt.Fprintln(os.Stderr, cli.UnknownCommand(lang, argv[i+1]))
				return 2
			}
			lang = l
			i += 2
		case strings.HasPrefix(a, "--lang="):
			if l, ok := cli.ParseLang(strings.TrimPrefix(a, "--lang=")); ok {
				lang = l
			}
			i++
		case strings.HasPrefix(a, "-L="):
			if l, ok := cli.ParseLang(strings.TrimPrefix(a, "-L=")); ok {
				lang = l
			}
			i++
		default:
			break parse
		}
	}

	rest := argv[i:]
	if len(rest) == 0 {
		fmt.Fprint(os.Stderr, cli.Usage(lang))
		return 2
	}
	cmd := rest[0]
	args := rest[1:]

	switch cmd {
	case "-h", "--help", "help":
		if cmd == "help" && len(args) > 0 {
			if h, ok := cli.CommandHelp(lang, args[0]); ok {
				fmt.Print(h)
				return 0
			}
			fmt.Fprintln(os.Stderr, cli.UnknownCommand(lang, args[0]))
			fmt.Fprint(os.Stderr, cli.Usage(lang))
			return 2
		}
		fmt.Print(cli.Usage(lang))
		return 0
	case "-v", "--version":
		fmt.Println("ic10c " + version.Short())
		return 0
	case "version":
		fmt.Print(version.Details())
		return 0
	}

	if hasHelp(args) {
		if h, ok := cli.CommandHelp(lang, cmd); ok {
			fmt.Print(h)
			return 0
		}
	}

	switch cmd {
	case "build":
		return cmdBuild(args)
	case "run":
		return cmdRun(args)
	case "minify":
		return cmdMinify(args)
	case "stats":
		return cmdStats(args)
	case "size":
		return cmdSize(args)
	case "graph":
		return cmdGraph(args)
	case "fmt":
		return cmdFmt(args)
	case "disasm":
		return cmdDisasm(args)
	case "decompile":
		return cmdDecompile(args)
	case "lsp":
		return cmdLSP()
	case "testbench":
		return cmdTestbench(args)
	case "lex":
		return cmdLex(args)
	case "ast":
		return cmdAST(args)
	default:
		if strings.HasPrefix(cmd, "-") {
			fmt.Fprintln(os.Stderr, cli.UnknownOption(lang, cmd))
		} else {
			fmt.Fprintln(os.Stderr, cli.UnknownCommand(lang, cmd))
		}
		fmt.Fprint(os.Stderr, cli.Usage(lang))
		return 2
	}
}

func hasHelp(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}

func missingLang() string {
	if lang == cli.ZH {
		return "ic10c: --lang 需要一个参数（en 或 zh）"
	}
	return "ic10c: --lang requires an argument (en or zh)"
}

// splitLibArgs extracts repeatable `--lib DIR` / `--lib=DIR` import search
// directories, leaving the remaining arguments for the command's own parser.
func splitLibArgs(args []string) ([]string, []string) {
	rest := make([]string, 0, len(args))
	var dirs []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--lib":
			if i+1 < len(args) {
				dirs = append(dirs, args[i+1])
				i++
			}
		case strings.HasPrefix(a, "--lib="):
			dirs = append(dirs, strings.TrimPrefix(a, "--lib="))
		default:
			rest = append(rest, a)
		}
	}
	return rest, dirs
}

// limitArgs are the IC10 editor limit overrides parsed from the command line.
// A zero field means "use the default / environment".
type limitArgs struct {
	lines int
	bytes int
	line  int
}

// splitLimitArgs extracts `--max-lines/--max-bytes/--max-line N` (or `=N`)
// overrides, leaving the remaining arguments. It reports malformed values.
func splitLimitArgs(args []string) ([]string, limitArgs, bool) {
	rest := make([]string, 0, len(args))
	var lim limitArgs
	set := func(name, val string) bool {
		n, err := strconv.Atoi(val)
		if err != nil || n <= 0 {
			fmt.Fprintf(os.Stderr, "ic10c: %s must be a positive integer\n", name)
			return false
		}
		switch name {
		case "--max-lines":
			lim.lines = n
		case "--max-bytes":
			lim.bytes = n
		case "--max-line":
			lim.line = n
		}
		return true
	}
	limitName := func(s string) (string, bool) {
		for _, n := range []string{"--max-lines", "--max-bytes", "--max-line"} {
			if s == n || strings.HasPrefix(s, n+"=") {
				return n, true
			}
		}
		return "", false
	}
	for i := 0; i < len(args); i++ {
		name, ok := limitName(args[i])
		if !ok {
			rest = append(rest, args[i])
			continue
		}
		if eq := strings.IndexByte(args[i], '='); eq >= 0 {
			if !set(name, args[i][eq+1:]) {
				return nil, lim, false
			}
			continue
		}
		if i+1 >= len(args) {
			fmt.Fprintf(os.Stderr, "ic10c: %s requires a positive integer\n", name)
			return nil, lim, false
		}
		if !set(name, args[i+1]) {
			return nil, lim, false
		}
		i++
	}
	return rest, lim, true
}

func cmdBuild(args []string) int {
	args, libDirs := splitLibArgs(args)
	args, lim, ok := splitLimitArgs(args)
	if !ok {
		return 2
	}
	stableIns := false
	dataOnly := false
	noDataCheck := false
	unsafe := false
	autoTable := false
	jumpTable := false
	fast := false
	relJump := false
	dataAccessStack := false
	spillStack := false
	dynamicStack := false
	userStack := 0
	redundantWrites := false
	mergeRenamedTails := false
	dataLayout := ""
	dataOut := ""
	chipName := ""
	jsonOut := false
	var files []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--stable-ins":
			stableIns = true
		case "--jump-table":
			jumpTable = true
		case "--fast":
			fast = true
		case "--rel-jump":
			relJump = true
		case "--unsafe":
			unsafe = true
		case "--auto-table":
			autoTable = true
		case "--dynamic-stack":
			dynamicStack = true
		case "--redundant-device-writes":
			redundantWrites = true
		case "--merge-renamed-tails":
			mergeRenamedTails = true
		case "--user-stack":
			if i+1 < len(args) {
				n, err := strconv.Atoi(args[i+1])
				if err != nil || n <= 0 {
					fmt.Fprintln(os.Stderr, "ic10c: --user-stack must be a positive integer")
					return 2
				}
				userStack = n
				i++
			}
		case "--chip":
			if i+1 < len(args) {
				chipName = args[i+1]
				i++
			}
		case "--split-data":
			// Kept for compatibility: the one-time loader is now emitted
			// automatically whenever the program needs one (data segment
			// and/or hoisted setup writes).
		case "--data-only":
			dataOnly = true
		case "--no-data-check":
			noDataCheck = true
		case "--json":
			jsonOut = true
		case "--data-layout":
			if i+1 < len(args) {
				dataLayout = args[i+1]
				i++
			}
		case "--data-out":
			if i+1 < len(args) {
				dataOut = args[i+1]
				i++
			}
		case "--data-access":
			if i+1 < len(args) {
				switch args[i+1] {
				case "get":
					dataAccessStack = false
				case "stack":
					dataAccessStack = true
				default:
					fmt.Fprintln(os.Stderr, "ic10c: --data-access must be get or stack")
					return 2
				}
				i++
			}
		case "--spill":
			if i+1 < len(args) {
				switch args[i+1] {
				case "db":
					spillStack = false
				case "stack":
					spillStack = true
				default:
					fmt.Fprintln(os.Stderr, "ic10c: --spill must be db or stack")
					return 2
				}
				i++
			}
		default:
			files = append(files, args[i])
		}
	}
	if len(files) != 1 {
		fmt.Fprintln(os.Stderr, cli.UsageLine(lang, "build"))
		return 2
	}

	opts := ic10.Options{
		StableInsOrder:        stableIns,
		NoDataCheck:           noDataCheck,
		Unsafe:                unsafe,
		AutoTable:             autoTable,
		JumpTable:             jumpTable,
		Fast:                  fast,
		RelJump:               relJump,
		DataAccessStack:       dataAccessStack,
		SpillStack:            spillStack,
		DataLayout:            dataLayout,
		DynamicStack:          dynamicStack,
		UserStackLimit:        userStack,
		RedundantDeviceWrites: redundantWrites,
		MergeRenamedTails:     mergeRenamedTails,
		MaxLines:              lim.lines,
		MaxBytes:              lim.bytes,
		MaxLineLen:            lim.line,
		Imports:               true,
		LibDirs:               libDirs,
	}

	data, err := os.ReadFile(files[0])
	if err != nil {
		if jsonOut {
			return emitJSON(jsonIOError(files[0], err, opts), 2)
		}
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}

	if jsonOut {
		res, _ := ic10.BuildJSON(files[0], data, opts)
		if res.OK {
			return emitJSON(res, 0)
		}
		return emitJSON(res, 1)
	}

	ic10Hint(files[0])
	if unsafe {
		fmt.Fprintln(os.Stderr, cli.UnsafeHint(lang))
	}

	if dataOnly {
		compiled, _, cerr := ic10.CompileResult(files[0], data, opts)
		if cerr != nil {
			fmt.Fprintln(os.Stderr, "ic10c:", cerr)
			return 1
		}
		multi := false
		for _, ch := range compiled.Chips {
			if ch.Name != "" {
				multi = true
			}
		}
		if multi && chipName == "" {
			fmt.Fprintln(os.Stderr, "ic10c: --data-only needs --chip NAME for a multi-chip program")
			return 2
		}
		loader := ""
		for _, ch := range compiled.Chips {
			if chipName == "" || ch.Name == chipName {
				loader = ch.Loader
				break
			}
		}
		if loader == "" {
			fmt.Fprintln(os.Stderr, "ic10c: source has no one-time loader")
			return 1
		}
		fmt.Print(loader)
		maxLines := ic10.LimitsFor(opts).Lines
		if n := strings.Count(loader, "\n"); n > maxLines {
			fmt.Fprintf(os.Stderr, "ic10c: loader is %d lines; split it into %d chunks (each <= %d lines) and run them in order\n",
				n, (n+maxLines-1)/maxLines, maxLines)
		}
		return 0
	}

	compiled, diags, err := ic10.CompileResult(files[0], data, opts)
	file := source.NewFile(files[0], data)
	if rc := report(file, diags); rc != 0 {
		return rc
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}

	base := strings.TrimSuffix(files[0], ".icg")
	multi := false
	for _, ch := range compiled.Chips {
		if ch.Name != "" {
			multi = true
		}
	}

	// Single chip: runtime to stdout, one-time loader beside it.
	if !multi {
		fmt.Print(compiled.Code)
		if compiled.Loader == "" {
			return 0
		}
		out := dataOut
		if out == "" {
			out = base + ".data.ic"
		}
		return writeLoaders(out, compiled.Loaders)
	}

	// Multi-chip with --chip NAME: that chip to stdout.
	if chipName != "" {
		for _, ch := range compiled.Chips {
			if ch.Name != chipName {
				continue
			}
			fmt.Print(ch.Code)
			if ch.Loader == "" {
				return 0
			}
			out := dataOut
			if out == "" {
				out = base + "." + ch.Name + ".data.ic"
			}
			return writeLoaders(out, ch.Loaders)
		}
		fmt.Fprintf(os.Stderr, "ic10c: no chip named %q\n", chipName)
		return 1
	}

	// Multi-chip without --chip: write one runtime (and loader) per chip.
	for _, ch := range compiled.Chips {
		out := base + "." + ch.Name + ".ic"
		if err := os.WriteFile(out, []byte(ch.Code), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "ic10c:", err)
			return 1
		}
		msg := fmt.Sprintf("%s (%d lines)", out, strings.Count(ch.Code, "\n"))
		if ch.Loader != "" {
			lout := base + "." + ch.Name + ".data.ic"
			if rc := writeLoaders(lout, ch.Loaders); rc != 0 {
				return rc
			}
			msg += " + loader"
		}
		fmt.Fprintln(os.Stderr, "ic10c: wrote "+msg)
	}
	return 0
}

// writeLoaders writes a one-time loader. A loader that fits the chip editor is
// written to path; a larger one is split into numbered chunks (path with ".N"
// before ".ic") that must be run in order.
func writeLoaders(path string, loaders []string) int {
	if len(loaders) == 0 {
		return 0
	}
	if len(loaders) == 1 {
		if err := os.WriteFile(path, []byte(loaders[0]), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "ic10c:", err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "ic10c: one-time loader written to %s (run it once, then use the runtime)\n", path)
		return 0
	}
	stem := strings.TrimSuffix(path, ".ic")
	for i, chunk := range loaders {
		p := fmt.Sprintf("%s.%d.ic", stem, i+1)
		if err := os.WriteFile(p, []byte(chunk), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "ic10c:", err)
			return 1
		}
	}
	fmt.Fprintf(os.Stderr, "ic10c: one-time loader split into %d chunks (%s.1.ic..%s.%d.ic); run them in order, then use the runtime\n",
		len(loaders), stem, stem, len(loaders))
	return 0
}

func cmdRun(args []string) int {
	args, libDirs := splitLibArgs(args)
	args, lim, ok := splitLimitArgs(args)
	if !ok {
		return 2
	}
	steps := 1000
	trace := false
	stableIns := false
	var sets []string
	var file string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--steps":
			if i+1 < len(args) {
				steps, _ = strconv.Atoi(args[i+1])
				i++
			}
		case "--set":
			if i+1 < len(args) {
				sets = append(sets, args[i+1])
				i++
			}
		case "--stable-ins":
			stableIns = true
		case "--trace":
			trace = true
		default:
			file = args[i]
		}
	}
	if file == "" {
		fmt.Fprintln(os.Stderr, cli.UsageLine(lang, "run"))
		return 2
	}
	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	ic10Hint(file)
	compiled, diags, err := ic10.CompileResult(file, data, ic10.Options{
		StableInsOrder: stableIns, MaxLines: lim.lines, MaxBytes: lim.bytes, MaxLineLen: lim.line,
		Imports: true, LibDirs: libDirs,
	})
	if rc := report(source.NewFile(file, data), diags); rc != 0 {
		return rc
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}

	multi := false
	for _, ch := range compiled.Chips {
		if ch.Name != "" {
			multi = true
		}
	}

	if !multi {
		m := vm.New()
		// Run the one-time loader first (data segment and/or hoisted setup), so
		// a program that needs it runs like it would on the chip.
		for _, chunk := range compiled.Loaders {
			if err := m.Load(chunk); err != nil {
				fmt.Fprintln(os.Stderr, "ic10c:", err)
				return 1
			}
			if err := m.Run(strings.Count(chunk, "\n") + 1); err != nil && err != vm.ErrStepLimit {
				fmt.Fprintln(os.Stderr, "ic10c: loader:", err)
				return 1
			}
		}
		for _, s := range sets {
			name, logic, value, ok := parseSet(s)
			if !ok {
				fmt.Fprintf(os.Stderr, "ic10c: bad --set %q (want name.logic=value)\n", s)
				return 2
			}
			m.Set(name, logic, value)
		}
		if err := m.Load(compiled.Code); err != nil {
			fmt.Fprintln(os.Stderr, "ic10c:", err)
			return 1
		}
		if trace {
			m.Trace = os.Stdout
		}
		if err := m.Run(steps); err != nil && err != vm.ErrStepLimit {
			fmt.Fprintln(os.Stderr, "ic10c:", err)
			return 1
		}
		printDevices(m)
		return 0
	}

	// Multi-chip: run every chip lockstep in a shared world (chips referencing
	// the same device/connection share its network channels).
	w := vm.NewWorld()
	for _, s := range sets {
		name, logic, value, ok := parseSet(s)
		if !ok {
			fmt.Fprintf(os.Stderr, "ic10c: bad --set %q (want name.logic=value)\n", s)
			return 2
		}
		w.Set(name, logic, value)
	}
	for _, ch := range compiled.Chips {
		m := w.AddChip()
		if err := m.Load(ch.Code); err != nil {
			fmt.Fprintf(os.Stderr, "ic10c: chip %s: %v\n", ch.Name, err)
			return 1
		}
		if trace {
			m.Trace = os.Stdout
		}
	}
	// Wire each bus slot's access points across the chips that use it, so the
	// run matches the declared buses (a connection shared by several slots is
	// merged into one network).
	busAccess := map[string][]string{}
	for _, ch := range compiled.Chips {
		for slot, conns := range ch.BusAccess {
			busAccess[slot] = append(busAccess[slot], conns...)
		}
	}
	for _, conns := range busAccess {
		w.Wire(conns...)
	}
	if err := w.Run(steps); err != nil && err != vm.ErrStepLimit {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	printDeviceMap(w.Devices)
	return 0
}

// parseSet parses a "name.logic=value" device initialiser.
func parseSet(s string) (name, logic string, value float64, ok bool) {
	eq := strings.IndexByte(s, '=')
	if eq < 0 {
		return "", "", 0, false
	}
	v, err := strconv.ParseFloat(s[eq+1:], 64)
	if err != nil {
		return "", "", 0, false
	}
	lhs := s[:eq]
	dot := strings.LastIndexByte(lhs, '.')
	if dot < 0 {
		return "", "", 0, false
	}
	return lhs[:dot], lhs[dot+1:], v, true
}

func printDevices(m *vm.Machine) { printDeviceMap(m.Devices) }

func printDeviceMap(devices map[string]*vm.Device) {
	names := make([]string, 0, len(devices))
	for n := range devices {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		d := devices[n]
		keys := make([]string, 0, len(d.Values))
		for k := range d.Values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("%s.%s = %v\n", n, k, d.Values[k])
		}
	}
}

func cmdMinify(args []string) int {
	opt := minify.Options{DeadCode: true}
	write := false
	outFile := ""
	var file string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--keep-defines":
			opt.KeepDefines = true
		case "--keep-labels":
			opt.KeepLabels = true
		case "--no-dead-code":
			opt.DeadCode = false
		case "--max-line":
			if i+1 < len(args) {
				n, err := strconv.Atoi(args[i+1])
				if err != nil || n <= 0 {
					fmt.Fprintln(os.Stderr, "ic10c: --max-line must be a positive integer")
					return 2
				}
				opt.MaxLineLen = n
				i++
			}
		case "-w", "--write":
			write = true
		case "-o", "--output":
			if i+1 < len(args) {
				outFile = args[i+1]
				i++
			}
		default:
			file = args[i]
		}
	}
	if file == "" {
		fmt.Fprintln(os.Stderr, cli.UsageLine(lang, "minify"))
		return 2
	}
	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	out, err := minify.Minify(string(data), opt)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	switch {
	case write:
		if err := os.WriteFile(file, []byte(out), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "ic10c:", err)
			return 1
		}
	case outFile != "":
		if err := os.WriteFile(outFile, []byte(out), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "ic10c:", err)
			return 1
		}
	default:
		fmt.Print(out)
	}
	return 0
}

func cmdStats(args []string) int {
	args, libDirs := splitLibArgs(args)
	args, lim, ok := splitLimitArgs(args)
	if !ok {
		return 2
	}
	dataLayout := ""
	unsafe := false
	autoTable := false
	spillStack := false
	dynamicStack := false
	userStack := 0
	redundantWrites := false
	mergeRenamedTails := false
	var files []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--data-layout":
			if i+1 < len(args) {
				dataLayout = args[i+1]
				i++
			}
		case "--unsafe":
			unsafe = true
		case "--auto-table":
			autoTable = true
		case "--dynamic-stack":
			dynamicStack = true
		case "--redundant-device-writes":
			redundantWrites = true
		case "--merge-renamed-tails":
			mergeRenamedTails = true
		case "--user-stack":
			if i+1 < len(args) {
				n, err := strconv.Atoi(args[i+1])
				if err != nil || n <= 0 {
					fmt.Fprintln(os.Stderr, "ic10c: --user-stack must be a positive integer")
					return 2
				}
				userStack = n
				i++
			}
		case "--spill":
			if i+1 < len(args) {
				if args[i+1] == "stack" {
					spillStack = true
				} else if args[i+1] != "db" {
					fmt.Fprintln(os.Stderr, "ic10c: --spill must be db or stack")
					return 2
				}
				i++
			}
		default:
			files = append(files, args[i])
		}
	}
	if len(files) != 1 {
		fmt.Fprintln(os.Stderr, cli.UsageLine(lang, "stats"))
		return 2
	}
	opts := ic10.Options{DataLayout: dataLayout, Unsafe: unsafe, AutoTable: autoTable, SpillStack: spillStack,
		DynamicStack: dynamicStack, UserStackLimit: userStack, RedundantDeviceWrites: redundantWrites,
		MergeRenamedTails: mergeRenamedTails, MaxLines: lim.lines, MaxBytes: lim.bytes, MaxLineLen: lim.line,
		Imports: true, LibDirs: libDirs}
	limits := ic10.LimitsFor(opts)
	data, err := os.ReadFile(files[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	ic10Hint(files[0])
	compiled, diags, err := ic10.CompileResult(files[0], data, opts)
	file := source.NewFile(files[0], data)
	if rc := report(file, diags); rc != 0 {
		return rc
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	multi := false
	for _, ch := range compiled.Chips {
		if ch.Name != "" {
			multi = true
		}
	}
	if multi {
		// Each chip has its own line / byte budget.
		for _, ch := range compiled.Chips {
			s := ic10.StatsOf(ch.Code)
			fmt.Printf("chip %s\n", ch.Name)
			fmt.Printf("  lines      %3d / %d\n", s.Lines, limits.Lines)
			fmt.Printf("  bytes      %3d / %d\n", s.Bytes, limits.Bytes)
			fmt.Printf("  max line   %3d / %d\n", s.MaxLineLen, limits.MaxLine)
			fmt.Printf("  registers  %3d / %d\n", s.RegsUsed, ic10.NumRegs)
			if ch.Loader != "" {
				ls := ic10.StatsOf(ch.Loader)
				if n := len(ch.Loaders); n > 1 {
					fmt.Printf("  loader     %3d lines · %d / %d bytes (run once, %d chunks)\n",
						ls.Lines, ls.Bytes, limits.Bytes, n)
				} else {
					fmt.Printf("  loader     %3d / %d lines · %d / %d bytes (run once)\n",
						ls.Lines, limits.Lines, ls.Bytes, limits.Bytes)
				}
			}
		}
		return 0
	}
	code := compiled.Code
	s := ic10.StatsOf(code)
	fmt.Printf("lines      %3d / %d\n", s.Lines, limits.Lines)
	fmt.Printf("bytes      %3d / %d\n", s.Bytes, limits.Bytes)
	fmt.Printf("max line   %3d / %d\n", s.MaxLineLen, limits.MaxLine)
	fmt.Printf("registers  %3d / %d\n", s.RegsUsed, ic10.NumRegs)
	var stack ic10.StackReport
	haveStack := false
	if rep, serr := ic10.Size(files[0], data, opts); serr == nil {
		stack, haveStack = rep.Stack, true
		fmt.Printf("peak live  %3d / %d\n", rep.PeakLive, ic10.NumRegs)
		if rep.Spills > 0 {
			fmt.Printf("spills     %3d slots\n", rep.Spills)
		}
	}
	base, size, autoTabled, warn := ic10.DataStats(files[0], data, opts)
	if haveStack {
		user := fmt.Sprintf("%d", stack.UserUsed)
		if stack.UserUnbounded {
			user = "unbounded"
		}
		mode := "dynamic"
		if !stack.Dynamic {
			mode = "fixed"
		}
		fmt.Printf("stack user %3s / %d (%s", user, stack.UserLimit, mode)
		if stack.UserMax > 0 {
			fmt.Printf(", max slot %d", stack.UserMax-1)
		}
		fmt.Printf(")\n")
		if stack.CompilerBase >= ic10.StackSize {
			fmt.Printf("stack comp   0 (unused)\n")
		} else {
			fmt.Printf("stack comp %3d @ [%d..%d] (data %d + spills %d)\n",
				stack.CompilerUsed, stack.CompilerBase, ic10.StackSize-1, stack.DataSlots, stack.SpillSlots)
		}
	}
	if compiled.Loader != "" {
		ls := ic10.StatsOf(compiled.Loader)
		if n := len(compiled.Loaders); n > 1 {
			fmt.Printf("loader     %3d lines · %d / %d bytes (run once, %d chunks)\n",
				ls.Lines, ls.Bytes, limits.Bytes, n)
		} else {
			fmt.Printf("loader     %3d / %d lines · %d / %d bytes (run once)\n",
				ls.Lines, limits.Lines, ls.Bytes, limits.Bytes)
		}
	}
	if base >= 0 {
		fmt.Printf("data       slots %d..%d (%d values)\n", base, base+size-1, size)
		if warn != "" {
			fmt.Printf("warning    %s\n", warn)
		}
		if autoTabled > 0 {
			fmt.Printf("warning    auto-tabled %d switch(es) into the data segment; reinstall the loader\n", autoTabled)
		}
	}
	if haveStack && stack.UserUnbounded {
		fmt.Printf("warning    push depth is unbounded (a loop grows the stack); keep the data segment clear\n")
	}
	return 0
}

func cmdSize(args []string) int {
	args, libDirs := splitLibArgs(args)
	args, lim, ok := splitLimitArgs(args)
	if !ok {
		return 2
	}
	dataLayout := ""
	unsafe := false
	autoTable := false
	spillStack := false
	var files []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--data-layout":
			if i+1 < len(args) {
				dataLayout = args[i+1]
				i++
			}
		case "--unsafe":
			unsafe = true
		case "--auto-table":
			autoTable = true
		case "--spill":
			if i+1 < len(args) {
				if args[i+1] == "stack" {
					spillStack = true
				} else if args[i+1] != "db" {
					fmt.Fprintln(os.Stderr, "ic10c: --spill must be db or stack")
					return 2
				}
				i++
			}
		default:
			files = append(files, args[i])
		}
	}
	if len(files) != 1 {
		fmt.Fprintln(os.Stderr, cli.UsageLine(lang, "size"))
		return 2
	}
	opts := ic10.Options{DataLayout: dataLayout, Unsafe: unsafe, AutoTable: autoTable, SpillStack: spillStack,
		MaxLines: lim.lines, MaxBytes: lim.bytes, MaxLineLen: lim.line, Imports: true, LibDirs: libDirs}
	data, err := os.ReadFile(files[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	rep, err := ic10.Size(files[0], data, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	fmt.Printf("lines  %3d / %d\n", rep.Total, rep.Limit)
	type entry struct {
		name  string
		lines int
	}
	var entries []entry
	for name, n := range rep.ByFunc {
		if name == "" {
			name = "(main)"
		}
		entries = append(entries, entry{name, n})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].lines != entries[j].lines {
			return entries[i].lines > entries[j].lines
		}
		return entries[i].name < entries[j].name
	})
	for _, e := range entries {
		fmt.Printf("  %-16s %3d\n", e.name, e.lines)
	}
	if len(rep.Outlined) > 0 {
		fmt.Printf("outlined: %s\n", strings.Join(rep.Outlined, ", "))
	}
	return 0
}

func cmdFmt(args []string) int {
	write := false
	noAlign := false
	var files []string
	for _, a := range args {
		switch a {
		case "-w", "--write":
			write = true
		case "--no-align":
			noAlign = true
		default:
			files = append(files, a)
		}
	}
	if len(files) != 1 {
		fmt.Fprintln(os.Stderr, cli.UsageLine(lang, "fmt"))
		return 2
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	var out string
	if isNativeIC10(files[0]) {
		if noAlign {
			out = ic10asm.Format(string(data))
		} else {
			out = ic10asm.FormatAligned(string(data))
		}
	} else {
		ic10Hint(files[0])
		formatted, diags, ferr := ic10.Format(files[0], data)
		if rc := report(source.NewFile(files[0], data), diags); rc != 0 {
			return rc
		}
		if ferr != nil {
			fmt.Fprintln(os.Stderr, "ic10c:", ferr)
			return 1
		}
		out = formatted
	}
	if write {
		if err := os.WriteFile(files[0], []byte(out), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "ic10c:", err)
			return 1
		}
		return 0
	}
	fmt.Print(out)
	return 0
}

// isNativeIC10 reports whether a path is a native IC10 source file.
func isNativeIC10(path string) bool {
	return strings.HasSuffix(path, ".ic") || strings.HasSuffix(path, ".ic10")
}

func cmdGraph(args []string) int {
	args, libDirs := splitLibArgs(args)
	level := "source"
	funcName := ""
	showLines := true
	full := false
	out := ""
	dataLayout := ""
	unsafe := false
	autoTable := false
	var files []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--level":
			if i+1 < len(args) {
				level = args[i+1]
				i++
			}
		case "--func":
			if i+1 < len(args) {
				funcName = args[i+1]
				i++
			}
		case "--no-lines":
			showLines = false
		case "--full":
			full = true
		case "-o", "--out":
			if i+1 < len(args) {
				out = args[i+1]
				i++
			}
		case "--data-layout":
			if i+1 < len(args) {
				dataLayout = args[i+1]
				i++
			}
		case "--unsafe":
			unsafe = true
		case "--auto-table":
			autoTable = true
		default:
			files = append(files, args[i])
		}
	}
	if len(files) != 1 {
		fmt.Fprintln(os.Stderr, cli.UsageLine(lang, "graph"))
		return 2
	}
	opts := ic10.Options{DataLayout: dataLayout, Unsafe: unsafe, AutoTable: autoTable, Imports: true, LibDirs: libDirs}
	data, err := os.ReadFile(files[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}

	var text string
	if level == "ir" {
		res, diags, err := ic10.Graph(files[0], data, opts)
		if rc := report(source.NewFile(files[0], data), diags); rc != 0 {
			return rc
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "ic10c:", err)
			return 1
		}
		if res == nil {
			fmt.Fprintln(os.Stderr, "ic10c: no control-flow graph produced")
			return 1
		}
		maxInstr := 8
		if full {
			maxInstr = 0
		}
		text = cfg.Mermaid(res.Fn, res.Order, res.Start, res.Colors, cfg.Options{
			ShowLines: showLines,
			MaxInstr:  maxInstr,
		})
	} else {
		tree, diags, err := ic10.Flow(files[0], data, opts)
		if rc := report(source.NewFile(files[0], data), diags); rc != 0 {
			return rc
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "ic10c:", err)
			return 1
		}
		if tree == nil {
			fmt.Fprintln(os.Stderr, "ic10c: no control-flow graph produced")
			return 1
		}
		text = flow.Mermaid(tree, flow.Options{Func: funcName, ShowLine: showLines, Coalesce: !full})
	}

	if out != "" {
		if err := os.WriteFile(out, []byte(text), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "ic10c:", err)
			return 1
		}
		return 0
	}
	fmt.Print(text)
	return 0
}

func cmdDisasm(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, cli.UsageLine(lang, "disasm"))
		return 2
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	fmt.Print(disasm.Disassemble(string(data)))
	return 0
}

func cmdDecompile(args []string) int {
	in, out := "", ""
	structured := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-o", "--output":
			if i+1 < len(args) {
				out = args[i+1]
				i++
			}
		case "-s", "--structured":
			structured = true
		default:
			in = args[i]
		}
	}
	if in == "" {
		fmt.Fprintln(os.Stderr, cli.UsageLine(lang, "decompile"))
		return 2
	}
	data, err := os.ReadFile(in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	code, warns, err := ic10.Decompile(in, data, structured)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	for _, w := range warns {
		fmt.Fprintf(os.Stderr, "ic10c: %s:%d: unsupported instruction: %s\n", in, w.Line+1, w.Text)
	}
	if out != "" {
		if err := os.WriteFile(out, []byte(code), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "ic10c:", err)
			return 1
		}
		return 0
	}
	fmt.Print(code)
	return 0
}

func cmdLSP() int {
	srv := lsp.New()
	if err := srv.Run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "ic10c lsp:", err)
		return 1
	}
	return 0
}

func cmdLex(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, cli.UsageLine(lang, "lex"))
		return 2
	}
	file, diags, err := readSource(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	ic10Hint(args[0])
	toks := lexer.Tokenize(file, diags)
	for _, t := range toks {
		fmt.Printf("%-12s %q\t%s\n", t.Kind, t.Text, t.Pos)
	}
	return report(file, diags)
}

func cmdAST(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, cli.UsageLine(lang, "ast"))
		return 2
	}
	file, diags, err := readSource(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	ic10Hint(args[0])
	toks := lexer.Tokenize(file, diags)
	tree := parser.Parse(file, toks, diags)
	if rc := report(file, diags); rc != 0 {
		return rc
	}
	fmt.Print(ast.Format(tree))
	return 0
}

func readSource(path string) (*source.File, *diag.Bag, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	return source.NewFile(path, data), &diag.Bag{}, nil
}

// ic10Hint prints a hint when a raw IC10 script is passed to an .icg command.
func ic10Hint(path string) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ic", ".ic10":
		fmt.Fprintln(os.Stderr, cli.IC10Hint(lang, path))
	}
}

// report prints diagnostics and returns a process exit code.
func report(file *source.File, diags *diag.Bag) int {
	diags.Sort()
	for _, d := range diags.Diags {
		fmt.Fprintln(os.Stderr, d)
		if d.Pos.IsValid() {
			line := file.LineText(d.Pos.Line)
			if line != "" {
				fmt.Fprintln(os.Stderr, "  "+line)
				col := d.Pos.Col
				if col < 1 {
					col = 1
				}
				fmt.Fprintln(os.Stderr, "  "+strings.Repeat(" ", col-1)+"^")
			}
		}
	}
	if diags.HasErrors() {
		return 1
	}
	return 0
}

// emitJSON writes v as compact JSON to stdout and returns the given exit code.
func emitJSON(v any, code int) int {
	b, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 2
	}
	os.Stdout.Write(b)
	fmt.Println()
	return code
}

// jsonIOError builds a BuildResult that reports a file/IO failure, so that
// `--json` always produces a parseable document.
func jsonIOError(name string, err error, opts ic10.Options) ic10.BuildResult {
	return ic10.BuildResult{
		APIVersion: ic10.APIVersion,
		Lines:      []string{},
		Limits:     ic10.LimitsFor(opts),
		Diagnostics: []ic10.Diagnostic{{
			Severity: "error",
			Code:     "io-error",
			File:     name,
			Message:  err.Error(),
		}},
	}
}
