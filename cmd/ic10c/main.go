package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"ic10go/internal/ast"
	"ic10go/internal/cli"
	"ic10go/internal/codegen"
	"ic10go/internal/decomp"
	"ic10go/internal/diag"
	"ic10go/internal/disasm"
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
	case "fmt":
		return cmdFmt(args)
	case "disasm":
		return cmdDisasm(args)
	case "decompile":
		return cmdDecompile(args)
	case "lsp":
		return cmdLSP()
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

func cmdBuild(args []string) int {
	stableIns := false
	splitData := false
	dataOnly := false
	noDataCheck := false
	unsafe := false
	autoTable := false
	dataAccessStack := false
	dataLayout := ""
	dataOut := ""
	var files []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--stable-ins":
			stableIns = true
		case "--unsafe":
			unsafe = true
		case "--auto-table":
			autoTable = true
		case "--split-data":
			splitData = true
		case "--data-only":
			dataOnly = true
		case "--no-data-check":
			noDataCheck = true
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
		default:
			files = append(files, args[i])
		}
	}
	if len(files) != 1 {
		fmt.Fprintln(os.Stderr, cli.UsageLine(lang, "build"))
		return 2
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	ic10Hint(files[0])

	opts := ic10.Options{
		StableInsOrder:  stableIns,
		NoDataCheck:     noDataCheck,
		Unsafe:          unsafe,
		AutoTable:       autoTable,
		DataAccessStack: dataAccessStack,
		DataLayout:      dataLayout,
	}
	if unsafe {
		fmt.Fprintln(os.Stderr, cli.UnsafeHint(lang))
	}

	if dataOnly {
		loader, err := ic10.DataLoaderWithOptions(files[0], data, opts)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ic10c:", err)
			return 1
		}
		if loader == "" {
			fmt.Fprintln(os.Stderr, "ic10c: source has no data tables")
			return 1
		}
		fmt.Print(loader)
		return 0
	}

	code, diags, err := ic10.CompileWithOptions(files[0], data, opts)
	file := source.NewFile(files[0], data)
	if rc := report(file, diags); rc != 0 {
		return rc
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	fmt.Print(code)

	if splitData {
		loader, err := ic10.DataLoaderWithOptions(files[0], data, opts)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ic10c:", err)
			return 1
		}
		if loader == "" {
			return 0
		}
		if dataOut == "" {
			dataOut = strings.TrimSuffix(files[0], ".icg") + ".data.ic"
		}
		if err := os.WriteFile(dataOut, []byte(loader), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "ic10c:", err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "ic10c: data loader written to %s (run it once, then use the runtime)\n", dataOut)
	}
	return 0
}

func cmdRun(args []string) int {
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
	code, diags, err := ic10.CompileWithOptions(file, data, ic10.Options{StableInsOrder: stableIns})
	if rc := report(source.NewFile(file, data), diags); rc != 0 {
		return rc
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}

	m := vm.New()
	for _, s := range sets {
		name, logic, value, ok := parseSet(s)
		if !ok {
			fmt.Fprintf(os.Stderr, "ic10c: bad --set %q (want name.logic=value)\n", s)
			return 2
		}
		m.Set(name, logic, value)
	}
	if err := m.Load(code); err != nil {
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

func printDevices(m *vm.Machine) {
	names := make([]string, 0, len(m.Devices))
	for n := range m.Devices {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		d := m.Devices[n]
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
	dataLayout := ""
	unsafe := false
	autoTable := false
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
		default:
			files = append(files, args[i])
		}
	}
	if len(files) != 1 {
		fmt.Fprintln(os.Stderr, cli.UsageLine(lang, "stats"))
		return 2
	}
	opts := ic10.Options{DataLayout: dataLayout, Unsafe: unsafe, AutoTable: autoTable}
	data, err := os.ReadFile(files[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	ic10Hint(files[0])
	code, diags, err := ic10.CompileWithOptions(files[0], data, opts)
	file := source.NewFile(files[0], data)
	if rc := report(file, diags); rc != 0 {
		return rc
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	s := ic10.StatsOf(code)
	fmt.Printf("lines      %3d / %d\n", s.Lines, codegen.MaxLines)
	fmt.Printf("bytes      %3d / %d\n", s.Bytes, codegen.MaxBytes)
	fmt.Printf("max line   %3d / %d\n", s.MaxLineLen, codegen.MaxLineLen)
	fmt.Printf("registers  %3d / %d\n", s.RegsUsed, ic10.NumRegs)
	if base, size, autoTabled, warn := ic10.DataStats(files[0], data, opts); base >= 0 {
		fmt.Printf("data       slots %d..%d (%d values)\n", base, base+size-1, size)
		if warn != "" {
			fmt.Printf("warning    %s\n", warn)
		}
		if autoTabled > 0 {
			fmt.Printf("warning    auto-tabled %d switch(es) into the data segment; reinstall the loader\n", autoTabled)
		}
		if depth, unbounded, err := ic10.MaxStackDepth(files[0], data, opts); err == nil {
			switch {
			case unbounded:
				fmt.Printf("warning    push depth is unbounded (a loop grows the stack); keep the data segment clear\n")
			case depth > base:
				fmt.Printf("warning    max push depth %d reaches the data segment (base %d)\n", depth, base)
			}
		}
	}
	return 0
}

func cmdFmt(args []string) int {
	write := false
	var files []string
	for _, a := range args {
		switch a {
		case "-w", "--write":
			write = true
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
	ic10Hint(files[0])
	out, diags, err := ic10.Format(files[0], data)
	if rc := report(source.NewFile(files[0], data), diags); rc != 0 {
		return rc
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
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
	var code string
	var warns []decomp.Warning
	if structured {
		code, warns, err = decomp.DecompileStructured(string(data))
		if err == nil {
			if _, diags, cerr := ic10.Compile(in, []byte(code)); diags.HasErrors() || cerr != nil {
				reason := ""
				if len(diags.Diags) > 0 {
					reason = diags.Diags[0].String()
				} else if cerr != nil {
					reason = cerr.Error()
				}
				fmt.Fprintf(os.Stderr, "ic10c: structured decompilation is invalid (%s), falling back to goto form\n", reason)
				code, warns, err = decomp.Decompile(string(data))
			}
		}
	} else {
		code, warns, err = decomp.Decompile(string(data))
	}
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
