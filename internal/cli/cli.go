// Package cli provides localised command help for ic10c.
package cli

import (
	"fmt"
	"os"
	"strings"
)

// Lang is an output language.
type Lang string

const (
	EN Lang = "en"
	ZH Lang = "zh"
)

func (l Lang) String() string { return string(l) }

// ParseLang parses a language tag such as "en", "en-US", "zh" or "zh-CN".
func ParseLang(s string) (Lang, bool) {
	v := strings.ToLower(strings.TrimSpace(s))
	v = strings.ReplaceAll(v, "_", "-")
	switch {
	case v == "en" || strings.HasPrefix(v, "en-") || v == "english":
		return EN, true
	case v == "zh" || strings.HasPrefix(v, "zh-") || v == "chinese" || v == "cn" || v == "中文":
		return ZH, true
	}
	return "", false
}

// Detect chooses a language from IC10C_LANG, LC_ALL, LC_MESSAGES or LANG,
// defaulting to English.
func Detect() Lang {
	if v := os.Getenv("IC10C_LANG"); v != "" {
		if l, ok := ParseLang(v); ok {
			return l
		}
	}
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		v := os.Getenv(key)
		if v == "" {
			continue
		}
		if l, ok := ParseLang(v); ok {
			return l
		}
		if strings.HasPrefix(strings.ToLower(v), "zh") {
			return ZH
		}
	}
	return EN
}

type text map[Lang]string

func (t text) get(l Lang) string {
	if s, ok := t[l]; ok {
		return s
	}
	return t[EN]
}

// Flag describes a command-line option.
type Flag struct {
	Short string
	Long  string
	Arg   string
	Desc  text
}

func (f Flag) label() string {
	var parts []string
	if f.Short != "" {
		parts = append(parts, f.Short)
	}
	if f.Long != "" {
		parts = append(parts, f.Long)
	}
	s := strings.Join(parts, ", ")
	if f.Arg != "" {
		s += " " + f.Arg
	}
	return s
}

// Command describes a subcommand.
type Command struct {
	Name     string
	Args     string
	Summary  text
	Long     text
	Flags    []Flag
	Examples []string
}

var commonHelp = Flag{Short: "-h", Long: "--help", Desc: text{
	EN: "show this help",
	ZH: "显示本帮助",
}}

// Commands is the ordered command table.
var Commands = []Command{
	{
		Name: "build", Args: "<file.icg>",
		Summary: text{EN: "compile to IC10", ZH: "编译为 IC10"},
		Long: text{
			EN: "Compile a .icg source file to IC10 machine code and write it to\n" +
				"standard output.\n\n" +
				"The output favours size and speed over readability: no aliases,\n" +
				"defines, comments, blank lines or labels are emitted, jumps use\n" +
				"absolute line numbers, constants are inlined at compile time and\n" +
				"CPU registers are reused by liveness. The IC10 editor limits\n" +
				"(128 lines, 4096 bytes, 90 characters per line) are enforced.",
			ZH: "将 .icg 源码编译为 IC10 机器码并输出到标准输出。\n\n" +
				"产物优先保证体积与执行效率，而非可读性：不生成 alias、define、\n" +
				"注释、空行或标签，跳转使用绝对行号，常量在编译期内联，CPU 寄存器\n" +
				"按活跃区间复用。编译前会校验 IC10 编辑器限制\n" +
				"（128 行 / 4096 字节 / 每行 90 字符）。",
		},
		Flags: []Flag{commonHelp},
		Examples: []string{
			"ic10c build blink.icg",
			"ic10c build blink.icg > blink.ic",
		},
	},
	{
		Name: "stats", Args: "<file.icg>",
		Summary: text{EN: "report the line/byte/register budget", ZH: "报告行/字节/寄存器预算"},
		Long: text{
			EN: "Compile the file and print how much of the IC10 editor budget it\n" +
				"uses: line count, byte size, longest line and the number of CPU\n" +
				"registers referenced.",
			ZH: "编译文件并打印它占用的 IC10 编辑器预算：行数、字节数、最长行\n" +
				"以及引用到的 CPU 寄存器数量。",
		},
		Flags:    []Flag{commonHelp},
		Examples: []string{"ic10c stats blink.icg"},
	},
	{
		Name: "fmt", Args: "[-w] <file.icg>",
		Summary: text{EN: "format .icg source", ZH: "格式化 .icg 源码"},
		Long: text{
			EN: "Parse and re-print a .icg file in canonical form. Without -w the\n" +
				"result is written to standard output.",
			ZH: "将 .icg 文件解析并以规范格式重新打印。不加 -w 时结果输出到\n" +
				"标准输出。",
		},
		Flags: []Flag{
			{Short: "-w", Long: "--write", Desc: text{
				EN: "write the result back to the file",
				ZH: "将结果写回文件",
			}},
			commonHelp,
		},
		Examples: []string{
			"ic10c fmt blink.icg",
			"ic10c fmt -w blink.icg",
		},
	},
	{
		Name: "disasm", Args: "<file.ic>",
		Summary: text{EN: "annotate an IC10 program", ZH: "为 IC10 程序添加注释"},
		Long: text{
			EN: "Parse an IC10 program and print an annotated listing. Jump and\n" +
				"branch targets are resolved to labels so that old scripts are\n" +
				"easier to read. Use 'ic10c decompile' to get editable .icg source.",
			ZH: "解析 IC10 程序并打印带注释的清单。跳转与分支目标会被解析为标签，\n" +
				"便于阅读。若要可编辑的 .icg 源码，请用 `ic10c decompile`。",
		},
		Flags:    []Flag{commonHelp},
		Examples: []string{"ic10c disasm old.ic"},
	},
	{
		Name: "decompile", Args: "[-s] [-o out.icg] <file.ic>",
		Summary: text{EN: "translate IC10 into .icg source", ZH: "将 IC10 反编译为 .icg 源码"},
		Long: text{
			EN: "Translate an IC10 program into .icg source. Aliases and defines\n" +
				"are substituted, registers become variables r0..r15, and control\n" +
				"flow becomes label/goto/call/ret. Unsupported instructions are\n" +
				"reported on stderr and emitted as comments.\n\n" +
				"With --structured the decompiler also tries to recover if/else/for;\n" +
				"this is best-effort and falls back to goto for complex flow.",
			ZH: "将 IC10 程序翻译为 .icg 源码。alias 与 define 会被替换，寄存器变为\n" +
				"变量 r0..r15，控制流变为 label/goto/call/ret。不支持的指令会在\n" +
				"stderr 报告，并在源码中作为注释保留。\n\n" +
				"加 --structured 时还会尝试还原 if/else/for；这是尽力而为，复杂控制流\n" +
				"会回退为 goto。",
		},
		Flags: []Flag{
			{Short: "-s", Long: "--structured", Desc: text{
				EN: "attempt to recover if/else/for control flow",
				ZH: "尝试还原 if/else/for 控制流",
			}},
			{Short: "-o", Long: "--output", Arg: "<file>", Desc: text{
				EN: "write the result to a file instead of stdout",
				ZH: "将结果写入文件而不是标准输出",
			}},
			commonHelp,
		},
		Examples: []string{
			"ic10c decompile old.ic",
			"ic10c decompile --structured -o old.icg old.ic",
		},
	},
	{
		Name:    "lsp",
		Summary: text{EN: "run the language server on stdio", ZH: "在 stdio 上运行语言服务器"},
		Long: text{
			EN: "Run the Language Server Protocol server over stdio. It publishes\n" +
				"diagnostics on open/change and provides completion for builtins,\n" +
				"logic types and device ports.",
			ZH: "通过 stdio 运行 Language Server Protocol 服务器。在打开/修改时\n" +
				"发布诊断，并提供内建函数、逻辑类型与设备端口的补全。",
		},
		Flags:    []Flag{commonHelp},
		Examples: []string{"ic10c lsp"},
	},
	{
		Name: "lex", Args: "<file.icg>",
		Summary: text{EN: "print the token stream", ZH: "打印词法单元流"},
		Long: text{
			EN: "Print every token produced by the lexer with its source position.\n" +
				"Intended for debugging the compiler.",
			ZH: "打印词法分析器产生的每个词法单元及其源码位置。用于调试编译器。",
		},
		Flags:    []Flag{commonHelp},
		Examples: []string{"ic10c lex blink.icg"},
	},
	{
		Name: "ast", Args: "<file.icg>",
		Summary: text{EN: "print the parsed AST", ZH: "打印解析后的 AST"},
		Long: text{
			EN: "Parse the file and print its abstract syntax tree in .icg form.\n" +
				"Intended for debugging the compiler.",
			ZH: "解析文件并以 .icg 形式打印其抽象语法树。用于调试编译器。",
		},
		Flags:    []Flag{commonHelp},
		Examples: []string{"ic10c ast blink.icg"},
	},
	{
		Name:    "version",
		Summary: text{EN: "print version", ZH: "打印版本"},
		Long: text{
			EN: "Print the ic10c version.",
			ZH: "打印 ic10c 版本。",
		},
		Flags:    []Flag{commonHelp},
		Examples: []string{"ic10c version"},
	},
	{
		Name: "help", Args: "[command]",
		Summary: text{EN: "show help", ZH: "显示帮助"},
		Long: text{
			EN: "Show general help, or detailed help for a single command.",
			ZH: "显示总体帮助，或某个命令的详细帮助。",
		},
		Flags:    []Flag{commonHelp},
		Examples: []string{"ic10c help", "ic10c help build"},
	},
}

// Lookup returns the command with the given name.
func Lookup(name string) (*Command, bool) {
	for i := range Commands {
		if Commands[i].Name == name {
			return &Commands[i], true
		}
	}
	return nil, false
}

var (
	lblUsage       = text{EN: "Usage", ZH: "用法"}
	lblCommands    = text{EN: "Commands", ZH: "命令"}
	lblGlobalFlags = text{EN: "Global options", ZH: "全局选项"}
	lblEnv         = text{EN: "Environment", ZH: "环境变量"}
	lblOptions     = text{EN: "Options", ZH: "选项"}
	lblExamples    = text{EN: "Examples", ZH: "示例"}
	lblNotes       = text{EN: "Notes", ZH: "说明"}
)

// Title returns the program banner.
func Title(l Lang) string {
	if l == ZH {
		return "ic10c - IC10 Go 编译器"
	}
	return "ic10c - IC10 Go compiler"
}

// Usage returns the general help text.
func Usage(l Lang) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", Title(l))
	fmt.Fprintf(&b, "%s:\n", lblUsage.get(l))
	fmt.Fprintf(&b, "  ic10c [global options] <command> [arguments...]\n\n")

	width := 0
	for _, c := range Commands {
		if n := len(c.Name) + len(c.Args) + 2; n > width {
			width = n
		}
	}
	fmt.Fprintf(&b, "%s:\n", lblCommands.get(l))
	for _, c := range Commands {
		label := c.Name
		if c.Args != "" {
			label += " " + c.Args
		}
		fmt.Fprintf(&b, "  %-*s  %s\n", width, label, c.Summary.get(l))
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s:\n", lblGlobalFlags.get(l))
	writeFlag(&b, "-L, --lang", "en|zh", text{
		EN: "output language (defaults to $LANG)",
		ZH: "输出语言（默认跟随 $LANG）",
	}.get(l))
	writeFlag(&b, "-h, --help", "", text{
		EN: "show this help",
		ZH: "显示本帮助",
	}.get(l))
	writeFlag(&b, "-v, --version", "", text{
		EN: "print version",
		ZH: "打印版本",
	}.get(l))
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s:\n", lblEnv.get(l))
	writeFlag(&b, "IC10C_LANG", "", text{
		EN: "output language (en|zh)",
		ZH: "输出语言（en|zh）",
	}.get(l))
	writeFlag(&b, "IC10C_NO_CHECK", "", text{
		EN: "disable device logic type checks",
		ZH: "关闭设备 logic type 校验",
	}.get(l))
	writeFlag(&b, "IC10C_NO_OPT", "", text{
		EN: "disable the optimiser (debugging)",
		ZH: "关闭优化器（调试用）",
	}.get(l))
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s:\n", lblExamples.get(l))
	fmt.Fprintf(&b, "  ic10c build blink.icg > blink.ic\n")
	fmt.Fprintf(&b, "  ic10c stats blink.icg\n")
	fmt.Fprintf(&b, "  ic10c fmt -w blink.icg\n")
	return b.String()
}

// CommandHelp returns detailed help for one command.
func CommandHelp(l Lang, name string) (string, bool) {
	c, ok := Lookup(name)
	if !ok {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: ic10c %s", lblUsage.get(l), c.Name)
	if c.Args != "" {
		fmt.Fprintf(&b, " %s", c.Args)
	}
	b.WriteString("\n\n")
	b.WriteString(c.Long.get(l))
	b.WriteString("\n")
	if len(c.Flags) > 0 {
		b.WriteString("\n")
		fmt.Fprintf(&b, "%s:\n", lblOptions.get(l))
		for _, f := range c.Flags {
			writeFlag(&b, f.label(), "", f.Desc.get(l))
		}
	}
	if len(c.Examples) > 0 {
		b.WriteString("\n")
		fmt.Fprintf(&b, "%s:\n", lblExamples.get(l))
		for _, e := range c.Examples {
			fmt.Fprintf(&b, "  %s\n", e)
		}
	}
	if l == ZH {
		fmt.Fprintf(&b, "\n%s:\n", lblNotes.get(l))
		b.WriteString("  使用 `ic10c help <command>` 查看任一命令的详细说明。\n")
	} else {
		fmt.Fprintf(&b, "\n%s:\n", lblNotes.get(l))
		b.WriteString("  Use `ic10c help <command>` for details on any command.\n")
	}
	return b.String(), true
}

// UsageLine returns a one-line usage reminder for a command.
func UsageLine(l Lang, name string) string {
	c, ok := Lookup(name)
	if !ok {
		return "usage: ic10c " + name
	}
	line := "ic10c " + c.Name
	if c.Args != "" {
		line += " " + c.Args
	}
	if l == ZH {
		return "用法: " + line
	}
	return "usage: " + line
}

// UnknownCommand formats an unknown-command error.
func UnknownCommand(l Lang, name string) string {
	if l == ZH {
		return fmt.Sprintf("ic10c: 未知命令 %q", name)
	}
	return fmt.Sprintf("ic10c: unknown command %q", name)
}

func writeFlag(b *strings.Builder, label, arg, desc string) {
	l := label
	if arg != "" {
		l += " " + arg
	}
	fmt.Fprintf(b, "  %-22s %s\n", l, desc)
}
