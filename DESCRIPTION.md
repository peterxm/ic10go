# ic10go

用 Go 风格语法编写 [Stationeers](https://stationeers.com/) IC10 芯片程序，编译为高效、紧凑的 IC10 代码。

> Write Stationeers IC10 programs in a Go-like language and compile them to compact, optimized IC10.

## 一句话

- **中文**：用 Go 风格语法编写 Stationeers IC10 芯片程序，编译为高效紧凑的 IC10 代码。
- **English**：Write Stationeers IC10 programs in a Go-like language and compile them to compact, optimized IC10.

## 项目简介

`ic10go` 是一个把 **Go 风格语言 `.icg`** 编译为 **Stationeers IC10** 机器码的编译器。IC10 的预算只有 **128 行 / 4 KiB / 每行 90 字符**，手写大型脚本极易超限；`ic10go` 提供现代语言特性（`if` / `for` / `switch`、`for range`、`case lo..hi` 区间、`if`/`switch` 初始化、标签 `break`/`continue`、函数内联/外提、设备属性与槽位、设备栈与分拣/打印栈指令构建器、批量 IO、网络通道、枚举），并通过寄存器分配与复用、常量折叠、CSE、循环不变量外提、分支融合、常量查表内联、精确栈失效、寄存器溢出到宿主栈（`get/put db`）等优化把程序压进预算。

它还实现了**持久栈数据段**：用 `data` 表把大块常量 / 查表放进芯片的持久栈，`switch ... table` 自动表化，从而突破 128 行限制。数据段 loader 超 128 行时自动分块（`.data.1.ic`…，按序运行）；512 槽持久栈显式分为**用户区**与**编译器区**（数据段 + 溢出），用户绝对地址越界会编译报错。单芯片默认 `// icg: private-stack`，可把常量用户栈槽提升为寄存器、消除成对 `push`/`pop`；多芯片或 `// icg: shared-stack` 保持保守。

以及**多芯片**：一个 `.icg` 用 `chip 名字 { ... }` 声明多块芯片（各编译成独立程序、各自 128 行预算与 loader），顶层 `const`/`data`/`func` 为公共区；用 `bus 名字 { 槽位 }` + 每 chip `use 名字 on dev:conn` 声明命名网络通道，编译器校验唯一写者并在内置 VM 里多芯片锁步、按 bus 自动接线。

工具链 `ic10c` 提供 `build` / `run`（内置 VM）/ `fmt` / `decompile` / `minify` / `disasm` / `stats` / `lsp`，并附带 VSCode 扩展（语法与语义高亮、实时诊断、上下文补全、格式化、编译预览）。未知 logic type / 槽位类型 / 枚举成员会给出**拼写建议**；枚举表由 `tools/genenums` 从游戏程序集生成。

## About (English)

`ic10go` compiles a Go-like language (`.icg`) to **Stationeers IC10** machine code. With a hard budget of **128 lines / 4 KiB / 90 chars per line**, large hand-written IC10 scripts are painful; `ic10go` brings modern syntax (`if` / `for` / `switch`, `for range`, `case lo..hi` intervals, `if`/`switch` init statements, labeled `break`/`continue`, inlined/outlined functions, device properties, device stacks and sorter/printer stack-instruction builders, batch IO, channels, enums) plus register allocation, constant folding, CSE, LICM, branch fusion, constant data-read folding, precise stack invalidation and housing-stack spilling (`get/put db`) to fit the budget. A **persistent-stack data segment** (`data` tables, `switch ... table`) moves large tables out of the 128-line program, and its loader is split into chip-sized chunks when needed. The 512-slot stack is partitioned into a user region and a compiler region (data + spills); single-chip programs default to a private stack (`// icg: private-stack`) so constant user slots can live in registers and paired push/pop disappear. **Multi-chip** support lets one `.icg` declare several `chip Name { ... }` programs (each with its own budget and loader, sharing top-level declarations) that talk over `bus Name { slots }` + per-chip `use Name on dev:conn` named network channels. Toolchain: `ic10c` (`build` / `run` with a built-in VM / `fmt` / `decompile` / `minify` / `disasm` / `stats` / `lsp`) and a VSCode extension. Unknown logic types, slot types and enum members get a spelling suggestion, and the enum tables are generated from the game assembly by `tools/genenums`.

## Topics

`stationeers` `ic10` `compiler` `golang` `lsp` `vscode-extension` `game-scripting`

## 免责声明 / Disclaimer

非官方项目，与 RocketWerkz / Stationeers 无任何关联。仓库不包含游戏资料或社区脚本等第三方内容。

Unofficial project, not affiliated with RocketWerkz or Stationeers. The repository does not include third-party content such as game documentation or community scripts.
