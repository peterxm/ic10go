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

var dataLayoutFlag = Flag{Long: "--data-layout", Arg: "top|middle", Desc: text{
	EN: "data segment at the stack top (default) or a fixed middle slot",
	ZH: "数据段位置：栈顶（默认）或固定中段",
}}

var unsafeFlag = Flag{Long: "--unsafe", Desc: text{
	EN: "unsafe: skip the data-segment runtime check to shrink the code",
	ZH: "不安全：跳过数据段运行时校验以进一步压缩代码",
}}

var autoTableFlag = Flag{Long: "--auto-table", Desc: text{
	EN: "auto-table eligible constant switches into the data segment",
	ZH: "自动把符合条件的常量 switch 表化进数据段",
}}

var jumpTableFlag = Flag{Long: "--jump-table", Desc: text{
	EN: "lower dense integer switches (>=8 cases) to a computed jump table",
	ZH: "把稠密整数 switch（≥8 case）降为计算跳转表",
}}

var fastFlag = Flag{Long: "--fast", Desc: text{
	EN: "prefer runtime speed over size (unroll more loops)",
	ZH: "优先运行速度而非体积（展开更多循环）",
}}

var relJumpFlag = Flag{Long: "--rel-jump", Desc: text{
	EN: "use relative jumps (jr / br*) when shorter than the absolute form (verify the base in game)",
	ZH: "在相对跳转（jr / br*）更短时使用（需真机验证基准行）",
}}

var spillFlag = Flag{Long: "--spill", Arg: "db|stack", Desc: text{
	EN: "register spill storage: get/put db (default, 1 line per load) or peek/poke stack (5 lines, fallback)",
	ZH: "寄存器溢出存放：get/put db（默认，每次加载 1 行）或 peek/poke 栈（5 行，回退）",
}}

var redundantDeviceWritesFlag = Flag{Long: "--redundant-device-writes", Desc: text{
	EN: "drop constant device writes that repeat the previous value (shorter, but changes the write sequence)",
	ZH: "删除重复的常量设备写（更短，但会改变写序列）",
}}

var mergeRenamedTailsFlag = Flag{Long: "--merge-renamed-tails", Desc: text{
	EN: "also merge structurally-identical tails whose registers differ, when safe (experimental)",
	ZH: "在安全时额外合并寄存器不同但结构相同的尾块（实验性）",
}}

var dynamicStackFlag = Flag{Long: "--dynamic-stack", Desc: text{
	EN: "size the user stack from the compiler's data/spill usage instead of the fixed --user-stack limit",
	ZH: "用户栈上限按编译器实际数据段/溢出占用动态计算，而非 --user-stack 固定值",
}}

var userStackFlag = Flag{Long: "--user-stack", Arg: "N", Desc: text{
	EN: "fixed user stack slots (default 128); user slots above it are compile errors",
	ZH: "固定的用户栈槽数（默认 128）；超过它的用户槽位会编译报错",
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
				"absolute line numbers (relative with --rel-jump), constants are\n" +
				"inlined at compile time and CPU registers are reused by liveness.\n" +
				"The IC10 editor limits (128 lines, 4096 bytes, 90 characters per\n" +
				"line) are enforced.",
			ZH: "将 .icg 源码编译为 IC10 机器码并输出到标准输出。\n\n" +
				"产物优先保证体积与执行效率，而非可读性：不生成 alias、define、\n" +
				"注释、空行或标签，跳转默认使用绝对行号（--rel-jump 改为相对跳转），\n" +
				"常量在编译期内联，CPU 寄存器按活跃区间复用。编译前会校验 IC10\n" +
				"编辑器限制（128 行 / 4096 字节 / 每行 90 字符）。",
		},
		Flags: []Flag{
			{Long: "--json", Desc: text{
				EN: "emit a machine-readable JSON result (code, data loader, stats, diagnostics)",
				ZH: "输出机器可读的 JSON 结果（代码、数据段装载器、统计、诊断）",
			}},
			{Long: "--stable-ins", Desc: text{
				EN: "emit 'ins' with the stable branch's argument order",
				ZH: "按稳定版的参数顺序生成 `ins`",
			}},
			{Long: "--split-data", Desc: text{
				EN: "kept for compatibility; the one-time loader is emitted automatically when needed",
				ZH: "兼容保留；需要 loader 时会自动输出",
			}},
			{Long: "--chip", Arg: "<name>", Desc: text{
				EN: "multi-chip: emit only the named chip (default: one file per chip)",
				ZH: "多芯片：只输出指定芯片（默认每个芯片各写一份文件）",
			}},
			{Long: "--data-out", Arg: "<file>", Desc: text{
				EN: "loader output path (default <file>.data.ic)",
				ZH: "装载器输出路径（默认 <file>.data.ic）",
			}},
			{Long: "--data-only", Desc: text{
				EN: "emit only the one-time loader (data segment + hoisted setup)",
				ZH: "只输出一次性装载器（数据段 + 外提的设置写入）",
			}},
			{Long: "--no-data-check", Desc: text{
				EN: "do not verify the data segment at runtime",
				ZH: "不在运行时校验数据段",
			}},
			unsafeFlag,
			autoTableFlag,
			jumpTableFlag,
			fastFlag,
			relJumpFlag,
			spillFlag,
			dynamicStackFlag,
			userStackFlag,
			redundantDeviceWritesFlag,
			mergeRenamedTailsFlag,
			{Long: "--data-access", Arg: "get|stack", Desc: text{
				EN: "read the data segment via get/put db (default, IC host) or poke/peek (device host)",
				ZH: "数据段读写方式：get（默认，IC host）或 stack（poke/peek，兼容设备 host）",
			}},
			dataLayoutFlag,
			commonHelp,
		},
		Examples: []string{
			"ic10c build blink.icg",
			"ic10c build blink.icg > blink.ic",
			"ic10c build --split-data table.icg > table.ic",
		},
	},
	{
		Name: "run", Args: "<file.icg> [--steps N] [--set name.logic=v] [--trace] [--stable-ins]",
		Summary: text{EN: "compile and run in the built-in VM", ZH: "编译并在内置 VM 中运行"},
		Long: text{
			EN: "Compile the file and run it in the built-in IC10 VM, then print\n" +
				"the resulting device values. Useful for testing without the game.\n\n" +
				"Initialise devices with --set (repeatable); use --trace to print\n" +
				"every executed instruction.",
			ZH: "编译文件并在内置 IC10 VM 中运行，然后打印设备状态。无需进游戏即可调试。\n\n" +
				"用 --set 初始化设备（可重复）；--trace 打印每条执行的指令。",
		},
		Flags: []Flag{
			{Long: "--steps", Arg: "N", Desc: text{
				EN: "instruction budget (default 1000)",
				ZH: "执行步数上限（默认 1000）",
			}},
			{Long: "--set", Arg: "name.logic=v", Desc: text{
				EN: "set a device value before running (repeatable)",
				ZH: "运行前设置设备值（可重复）",
			}},
			{Long: "--trace", Desc: text{
				EN: "print every executed instruction",
				ZH: "打印每条执行的指令",
			}},
			{Long: "--stable-ins", Desc: text{
				EN: "compile with the stable branch's ins argument order",
				ZH: "用稳定版的 ins 参数顺序编译",
			}},
			commonHelp,
		},
		Examples: []string{
			"ic10c run blink.icg",
			"ic10c run --steps 500 --set d0.Temperature=350 ctrl.icg",
		},
	},
	{
		Name: "minify", Args: "[--keep-defines] [--keep-labels] [--no-dead-code] [-w|-o out] <file.ic>",
		Summary: text{EN: "shrink an IC10 script's line count", ZH: "压缩 IC10 脚本的行数"},
		Long: text{
			EN: "Rewrite an existing IC10 program with fewer lines while preserving\n" +
				"its behaviour: comments and blank lines are dropped, alias/define\n" +
				"symbols are inlined, labels are converted to absolute line numbers\n" +
				"and unreachable instructions are removed.\n\n" +
				"Use --keep-defines/--keep-labels to retain those lines and\n" +
				"--no-dead-code to keep unreachable code.\n\n" +
				"Unlike decompile+build, minify keeps the original instructions,\n" +
				"registers and device access order unchanged, so it is a safe\n" +
				"transformation. To also re-optimise and maintain the program as\n" +
				".icg source, use:\n" +
				"  ic10c decompile -s -o out.icg <file.ic> && ic10c build out.icg",
			ZH: "在保持行为不变的前提下，用更少的行重写现有 IC10 程序：删除注释与\n" +
				"空行、内联 alias/define 符号、把标签改写为绝对行号、删除不可达指令。\n\n" +
				"--keep-defines/--keep-labels 保留对应行；--no-dead-code 保留不可达代码。\n\n" +
				"与 decompile+build 不同，minify 不改动原指令、寄存器分配和设备读写\n" +
				"顺序，因此是安全的变换。若想同时重新优化并以 .icg 维护，请用：\n" +
				"  ic10c decompile -s -o out.icg <file.ic> && ic10c build out.icg",
		},
		Flags: []Flag{
			{Long: "--keep-defines", Desc: text{EN: "keep alias/define lines", ZH: "保留 alias/define 行"}},
			{Long: "--keep-labels", Desc: text{EN: "keep label lines", ZH: "保留标签行"}},
			{Long: "--no-dead-code", Desc: text{EN: "keep unreachable instructions", ZH: "保留不可达指令"}},
			{Short: "-w", Long: "--write", Desc: text{EN: "write the result back to the file", ZH: "将结果写回文件"}},
			{Short: "-o", Long: "--output", Arg: "file", Desc: text{EN: "write the result to a file", ZH: "将结果写入文件"}},
			commonHelp,
		},
		Examples: []string{
			"ic10c minify old.ic",
			"ic10c minify -w old.ic",
			"ic10c minify --keep-defines -o small.ic big.ic",
		},
	},
	{
		Name: "stats", Args: "<file.icg>",
		Summary: text{EN: "report the line/byte/register/stack budget", ZH: "报告行/字节/寄存器/栈预算"},
		Long: text{
			EN: "Compile the file and print how much of the IC10 editor budget it\n" +
				"uses: line count, byte size, longest line, the number of CPU\n" +
				"registers referenced and the stack budget split into the user\n" +
				"region (push/pop and db.stack[]) and the compiler region (data\n" +
				"segment and register spills).",
			ZH: "编译文件并打印它占用的 IC10 编辑器预算：行数、字节数、最长行、\n" +
				"引用到的 CPU 寄存器数量，以及栈预算——分为用户区（push/pop 与\n" +
				"db.stack[]）和编译器区（数据段与寄存器溢出）。",
		},
		Flags:    []Flag{dataLayoutFlag, unsafeFlag, autoTableFlag, dynamicStackFlag, userStackFlag, redundantDeviceWritesFlag, mergeRenamedTailsFlag, commonHelp},
		Examples: []string{"ic10c stats blink.icg", "ic10c stats --user-stack 128 blink.icg"},
	},
	{
		Name: "size", Args: "<file.icg>",
		Summary: text{EN: "break the line budget down by function", ZH: "按函数拆分行预算"},
		Long: text{
			EN: "Compile the file and print how many IC10 lines each source function\n" +
				"contributes. Inlined functions are counted once per call site, so the\n" +
				"largest entries are the best candidates to simplify or outline.\n" +
				"Functions with control flow are attributed exactly; straight-line\n" +
				"inlined code is attributed to its caller.",
			ZH: "编译文件并打印每个源函数贡献了多少 IC10 行。被内联的函数按调用点\n" +
				"累计，因此占比最大的函数最值得简化或外提。\n" +
				"含控制流的函数能精确归属；纯顺序的内联代码会归到调用者。",
		},
		Flags:    []Flag{dataLayoutFlag, unsafeFlag, autoTableFlag, commonHelp},
		Examples: []string{"ic10c size blink.icg"},
	},
	{
		Name: "graph", Args: "[--level source|ir] [--func NAME] [--no-lines] [--full] [-o FILE] <file.icg>",
		Summary: text{EN: "print the control-flow graph as Mermaid", ZH: "输出 Mermaid 控制流图"},
		Long: text{
			EN: "Print the control flow as a Mermaid `flowchart TD`.\n" +
				"--level source (default) draws the source control flow: nodes show the\n" +
				"original statements, conditions and branches, grouped per function.\n" +
				"--level ir draws the compiler's IR basic blocks instead. Paste the output\n" +
				"into a Mermaid renderer, or use the VSCode command \"IC10 Go: Show CFG\".",
			ZH: "以 Mermaid `flowchart TD` 打印控制流。\n" +
				"--level source（默认）画源码控制流：节点是原始语句、条件与分支，按函数分组。\n" +
				"--level ir 改画编译器的 IR 基本块。可粘贴到 Mermaid 渲染器，或用 VSCode\n" +
				"命令 “IC10 Go: Show CFG”。",
		},
		Flags: []Flag{
			{Long: "--level", Arg: "source|ir", Desc: text{EN: "source control flow (default) or IR basic blocks", ZH: "源码控制流（默认）或 IR 基本块"}},
			{Long: "--func", Arg: "NAME", Desc: text{EN: "source level: draw only this function", ZH: "源码级：只画该函数"}},
			{Long: "--no-lines", Desc: text{EN: "hide the source line annotation", ZH: "隐藏源码行号标注"}},
			{Long: "--full", Desc: text{EN: "source: one node per statement; ir: show every instruction", ZH: "源码：每句一个节点；ir：显示全部指令"}},
			{Short: "-o", Long: "--out", Arg: "FILE", Desc: text{EN: "write to FILE instead of stdout", ZH: "写入 FILE 而非标准输出"}},
			dataLayoutFlag, unsafeFlag, autoTableFlag, commonHelp,
		},
		Examples: []string{"ic10c graph blink.icg", "ic10c graph --func main blink.icg", "ic10c graph --level ir blink.icg", "ic10c graph -o blink.mmd blink.icg"},
	},
	{
		Name: "fmt", Args: "[-w] [--no-align] <file.icg|.ic|.ic10>",
		Summary: text{EN: "format .icg / native IC10 source", ZH: "格式化 .icg / 原生 IC10 源码"},
		Long: text{
			EN: "Re-print source in canonical form: .icg is parsed and reprinted;\n" +
				"native .ic/.ic10 files are reflowed (whitespace, labels, aligned\n" +
				"columns). Without -w the result is written to standard output.",
			ZH: "以规范格式重新打印源码：.icg 会解析后重排；原生 .ic/.ic10 会重排\n" +
				"（空白、标签、列对齐）。不加 -w 时结果输出到标准输出。",
		},
		Flags: []Flag{
			{Short: "-w", Long: "--write", Desc: text{
				EN: "write the result back to the file",
				ZH: "将结果写回文件",
			}},
			{Long: "--no-align", Desc: text{
				EN: "native IC10: only normalise whitespace, do not align columns",
				ZH: "原生 IC10：只规范空白，不做列对齐",
			}},
			commonHelp,
		},
		Examples: []string{
			"ic10c fmt blink.icg",
			"ic10c fmt -w blink.icg",
			"ic10c fmt --no-align sorter.ic",
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
				"With --structured the decompiler recovers if/else/for using the\n" +
				"post-dominator tree, and falls back to goto where it cannot.\n\n" +
				"To only shrink the line count while keeping the original\n" +
				"instructions and register usage, use `ic10c minify` instead.",
			ZH: "将 IC10 程序翻译为 .icg 源码。alias 与 define 会被替换，寄存器变为\n" +
				"变量 r0..r15，控制流变为 label/goto/call/ret。不支持的指令会在\n" +
				"stderr 报告，并在源码中作为注释保留。\n\n" +
				"加 --structured 时用后支配树还原 if/else/for；无法还原的部分回退为 goto。\n\n" +
				"若只想压缩行数、保持原指令与寄存器分配不变，请改用 `ic10c minify`。",
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
			EN: "Print the ic10c version, commit, build time, Go version and platform. Use -v/--version for a one-line version.",
			ZH: "打印 ic10c 版本、提交、构建时间、Go 版本与平台信息。用 -v/--version 只打印一行版本号。",
		},
		Flags:    []Flag{commonHelp},
		Examples: []string{"ic10c version", "ic10c -v"},
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
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s:\n", lblNotes.get(l))
	if l == ZH {
		b.WriteString("  minify 压缩现有 IC10 行数（保真）；decompile+build 反编译为\n")
		b.WriteString("  .icg 后重新优化（可维护，尽力保真）。`ic10c help <command>` 看详情。\n")
	} else {
		b.WriteString("  minify shrinks an existing IC10 script (faithful); decompile+build\n")
		b.WriteString("  re-optimises via .icg (maintainable, best-effort). `ic10c help <command>`.\n")
	}
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

// UnknownOption formats an unknown-option error (a token starting with '-').
func UnknownOption(l Lang, name string) string {
	if l == ZH {
		return fmt.Sprintf("ic10c: 未知选项 %q（选项要放在命令之后，例如 `ic10c decompile -s <file.ic>`）", name)
	}
	return fmt.Sprintf("ic10c: unknown option %q (options go after the command, e.g. `ic10c decompile -s <file.ic>`)", name)
}

// IC10Hint suggests decompiling when a .ic/.ic10 file is passed to a command
// that expects .icg source.
func IC10Hint(l Lang, path string) string {
	if l == ZH {
		return fmt.Sprintf("提示：%s 看起来是原始 IC10 脚本；请先反编译为 .icg：ic10c decompile %s", path, path)
	}
	return fmt.Sprintf("hint: %s looks like a raw IC10 script; decompile it first: ic10c decompile %s", path, path)
}

// UnsafeHint warns that --unsafe skipped the data-segment runtime check.
func UnsafeHint(l Lang) string {
	if l == ZH {
		return "警告：--unsafe 跳过数据段的运行时校验，代码更短但依赖外部数据；请确保先运行过 loader，否则读到的是旧数据或 0"
	}
	return "warning: --unsafe skips the data-segment runtime check; the code is smaller but trusts external data; make sure the loader was run, or stale/zero values are read"
}

func writeFlag(b *strings.Builder, label, arg, desc string) {
	l := label
	if arg != "" {
		l += " " + arg
	}
	fmt.Fprintf(b, "  %-22s %s\n", l, desc)
}
