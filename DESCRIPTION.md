# ic10go

用 Go 风格语法编写 [Stationeers](https://stationeers.com/) IC10 芯片程序，编译为高效、紧凑的 IC10 代码。

> Write Stationeers IC10 programs in a Go-like language and compile them to compact, optimized IC10.

## 一句话

- **中文**：用 Go 风格语法编写 Stationeers IC10 芯片程序，编译为高效紧凑的 IC10 代码。
- **English**：Write Stationeers IC10 programs in a Go-like language and compile them to compact, optimized IC10.

## 项目简介

`ic10go` 是一个把 **Go 风格语言 `.icg`** 编译为 **Stationeers IC10** 机器码的编译器。IC10 的预算只有 **128 行 / 4 KiB / 每行 90 字符**，手写大型脚本极易超限；`ic10go` 提供现代语言特性（`if` / `for` / `switch`、`for range`、`case lo..hi` 区间、`if`/`switch` 初始化、标签 `break`/`continue`、函数编译期全内联、设备属性与槽位、批量 IO、网络通道、枚举），并通过寄存器分配与复用、常量折叠、CSE、循环不变量外提、分支融合、寄存器溢出到栈等优化把程序压进预算。

它还实现了**持久栈数据段**：用 `data` 表把大块常量 / 查表放进芯片的持久栈，`switch ... table` 自动表化，从而突破 128 行限制。

工具链 `ic10c` 提供 `build` / `run`（内置 VM）/ `fmt` / `decompile` / `minify` / `disasm` / `stats` / `lsp`，并附带 VSCode 扩展（语法与语义高亮、实时诊断、上下文补全、格式化、编译预览）。

## About (English)

`ic10go` compiles a Go-like language (`.icg`) to **Stationeers IC10** machine code. With a hard budget of **128 lines / 4 KiB / 90 chars per line**, large hand-written IC10 scripts are painful; `ic10go` brings modern syntax (`if` / `for` / `switch`, `for range`, `case lo..hi` intervals, `if`/`switch` init statements, labeled `break`/`continue`, fully inlined functions, device properties, batch IO, channels, enums) plus register allocation, constant folding, CSE, LICM, branch fusion and stack spilling to fit the budget. A **persistent-stack data segment** (`data` tables, `switch ... table`) moves large tables out of the 128-line program. Toolchain: `ic10c` (`build` / `run` with a built-in VM / `fmt` / `decompile` / `minify` / `disasm` / `stats` / `lsp`) and a VSCode extension.

## Topics

`stationeers` `ic10` `compiler` `golang` `lsp` `vscode-extension` `game-scripting`

## 免责声明 / Disclaimer

非官方项目，与 RocketWerkz / Stationeers 无任何关联。仓库不包含游戏资料或社区脚本等第三方内容。

Unofficial project, not affiliated with RocketWerkz or Stationeers. The repository does not include third-party content such as game documentation or community scripts.
