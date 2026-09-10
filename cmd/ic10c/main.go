package main

import (
	"fmt"
	"os"
	"strings"

	"ic10go/internal/ast"
	"ic10go/internal/cli"
	"ic10go/internal/codegen"
	"ic10go/internal/decomp"
	"ic10go/internal/diag"
	"ic10go/internal/disasm"
	"ic10go/internal/lexer"
	"ic10go/internal/lsp"
	"ic10go/internal/parser"
	"ic10go/internal/source"
	"ic10go/pkg/ic10"
)

const version = "0.6.0"

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
	case "-v", "--version", "version":
		fmt.Println("ic10c " + version)
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
		fmt.Fprintln(os.Stderr, cli.UnknownCommand(lang, cmd))
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
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, cli.UsageLine(lang, "build"))
		return 2
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	code, diags, err := ic10.Compile(args[0], data)
	file := source.NewFile(args[0], data)
	if rc := report(file, diags); rc != 0 {
		return rc
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	fmt.Print(code)
	return 0
}

func cmdStats(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, cli.UsageLine(lang, "stats"))
		return 2
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "ic10c:", err)
		return 1
	}
	code, diags, err := ic10.Compile(args[0], data)
	file := source.NewFile(args[0], data)
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
