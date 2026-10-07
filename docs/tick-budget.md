# 每 tick 指令预算分析（`ic10c tick`）

> 游戏对一块芯片每 tick 最多执行 **128 条**指令；遇到 `yield` / `sleep` 会提前结束本次
> tick，剩下的指令下一 tick 继续。所以「这个循环每 tick 跑一遍」只有在**一个 tick 内
> 能跑完整圈**时才成立——否则循环会跨 tick 被切开，行为（尤其时序）随之改变。
>
> `ic10c tick` 静态分析编译产物（或手写 IC10），给出每个 `yield`/`sleep` 分段内
> **最坏路径**的指令数，以及程序里每个循环的迭代次数，把「跑不进一个 tick」的地方标出来。

关联：`docs/architecture.md`（IR / codegen）、`internal/vm`（同一条 128 条预算模型）、
`docs/ingame-testbench.md`（在真机上验证）。

---

## 用法

```bash
ic10c tick [--limit N] [--path] [--strict] [--json] <file.icg|file.ic>

ic10c tick printer.icg                 # 编译 .icg 后分析
ic10c tick firmware.ic                 # 原生 IC10 直接分析（不编译）
ic10c tick --path printer.icg          # 打印最坏路径（IC10 行号）
ic10c tick --limit 64 slow.icg         # 换一个预算对照（默认 128）
ic10c tick --run 20 printer.icg        # 再用内置 VM 跑 20 tick，报实测每 tick 指令数
ic10c tick --run 20 --set d4.Setting=1 printer.icg   # --run 前给设备设初值（可重复）
ic10c tick --strict firmware.ic        # 有分段超限则退出码 1（可接 CI）
ic10c tick --json printer.icg          # 机器可读
```

`.icg` 走正常编译（含 `--lib` / `--data-layout` / `--unsafe` / `--auto-table`），
分析的是**编译后的最终 IC10**（已包含寄存器分配、溢出、优化后的真实行数）。
`.ic` / `.ic10`（或其它后缀）按原生 IC10 解析。

### 编译时提醒（`ic10c build --tick-warn`）

`ic10c build --tick-warn` 会对**每个超限的支配循环**在 stderr 打一条警告（诊断 code
`tick-budget`），锚在支配循环的源码行，**不影响退出码**：

```text
examples/打印机控制.icg:25:1: warning: the worst-case path between two yields exceeds the 128-instruction tick budget; the chip resumes mid-loop on the next tick (see `ic10c tick`)
```

**默认关闭**：几乎每个多机 / 长驻控制程序都会命中（语料 153 个 `.icg` 里 **61 个**），默认打印太吵。
该开关传给 `Options.WarnTickBudget`。`build --json` 无论加不加都会在顶层 `tick` 字段给出结构化摘要
（`{limit, cost, exceeds, loops, segments}`，多芯片取最坏的一块），加 `--tick-warn` 时另带这条诊断。
编辑器用自己的分析（见下「编辑器诊断」）；库调用默认关，以免在不显示诊断的场景重复分析。

### 输出

```text
examples/…-表驱动.icg: per-tick budget: limit 128, 122 instructions
  tick segment 0: L1 -> yield L5  worst-case 5 instructions [fits]
  tick segment 1: L6 -> yield L5  worst-case 129 instructions [EXCEEDS 128]
  loop L9..L27 (header L9): body 19 x 8 iterations
  loop L29..L62 (header L29): body 26 x 8 iterations
  worst case: a loop between yields can exceed the budget, so the chip resumes mid-loop on the next tick.
```

- **tick segment**：两个 tick 边界之间的一段。`L6 (src L63) -> yield L5 (src L62)` 表示从第 6 行
  跑到第 5 行的 `yield` 结束本次 tick（括号里是 `.icg` 源码行，`--json` 为 `source`/`barrierSource`）。
  `worst-case` 是这一段任意路径上的最大指令数（≥ 128 就 `EXCEEDS`）。
- **归因**：超限的段尾会附 `<- dominated by loop L29 (src L69) (~182 instr)`，指出是哪个循环
  撑爆的（最坏路径上该循环头执行次数 × 一圈行数的最大值）。
- **loop**：识别到的自然循环：`body` 是**一圈的指令数**（`最快..最慢`，如 `12..26`；两者相同时只给一个数），
  `x N iterations` 是识别出的迭代次数（`?` 表示没识别出常量上界）。
  含 `yield`/`sleep`、会**跨 tick**的循环也会列出（行尾标「跨 tick」）——它正是最该关注的 tick 循环，
  `body` 是它**一圈**的指令数（沿分段边界的续接统计，见下）。
  VSCode 状态栏悬停里也会逐个循环列出「每圈 最快..最慢 条 × N 次」。
- `--path`：`.icg` 给出映射后的**源码行**序列（`sourcePath`），原生 IC10 给出 0 基 IC10 行号。

### 多芯片

`.icg` 里用 `chip` 声明多块芯片时，`tick` 会**逐块分析**并分别输出：

```text
mc.icg chip ChipA: per-tick budget: limit 128, 6 instructions
  tick segment 0: L1 (src L1) -> yield L1 (src L1)  worst-case 1 instructions [fits]
  ...
mc.icg chip ChipB: per-tick budget: limit 128, 7 instructions
  tick segment 1: L2 (src L2) -> (program end)  worst-case 126 instructions [fits]
  loop L4..L7 (header L4 (src L2)): body 4 x 30 iterations
```

`--json` 每块芯片一条文档（带 `chip` 字段）；`build --json` 顶层的 `tick` 取最坏的一块。

### 动态实测（`--run N`）

静态分析给的是「所有路径」的最坏上界；`--run N` 用内置 VM（`internal/vm`，同一套 128 条
tick 模型）跑 N 个 tick，报**实测**的每 tick 指令数。两者互补：静态覆盖所有路径（可能包含
只发生一次的分支），动态只覆盖实际走到的路径，但对动态界循环也是精确的。

```text
examples/…-表驱动.icg: ... worst-case 129 instructions [EXCEEDS 128]
  dynamic: 12 ticks, max 128 instructions/tick, avg 101.1 (limit 128)  (a tick hit the budget: the loop spans ticks)
```

这里 `avg ~101` 对应源码注释里的「约 110 条」（空闲路径），而 `max 128` 说明**确实有 tick
撞到预算被切开**——和静态结论一致。

想跑不同输入，用 `--set name.logic=value`（可重复，也支持 `name.slot[i].logic=value`）在
`--run` 前给设备设初值。

### 编辑器诊断

除了状态栏，LSP 会对**每个超限的支配循环**发一条 **Information** 级诊断（code `tick-budget`），锚在
**支配循环的源码行**，并带上数字与相关位置。同一循环撑爆多个分段时**只报一条**（把各段的
tick 体起点合并在一条里）：

```text
worst case between two yields exceeds 128 instructions, so the tick is cut mid-loop.
tick body starts at source line 63, ends at the next yield (source line 62).
dominant loop at source line 69: body 26 × 8 iterations ≈ 182 instructions.
run `ic10c tick --path` for the worst-case path.
```

- `relatedInformation` 指向**支配循环头**（"dominant loop header (body 26, 8 iterations)"）和
  各 **tick 体起点**（"tick body starts here"），可在 Problems 面板里点跳。
- 多芯片时每块芯片的超限循环各一条。
- 之所以用 Information 而不是 Warning：很多长驻循环本来就该跨 tick，这不是错误，只是提醒。

> `.icg` 与原生 IC10 文本（`.ic`/`.ic10`）都会多一条**总览**诊断（code `tick-loops`），
> 把**所有**循环一次列全（不只看支配的那个），方便直接看「哪几圈、各多少条」。
> 含 `chip` 块的多芯片 `.icg` **每块芯片各一条**（标题带 `[芯片 NAME]`），锚在该芯片第一个循环处：
>
> ```text
> 循环分析 [芯片 A]：共 1 个循环（每圈 最快..最慢 条）：
> 源码 L13：每圈 4 条 × 迭代次数未知（跨 tick）
> ```

> 原生 IC10 若依赖一次性数据 loader（`get db …` 读数据段），VM 里没跑 loader 会提前跳过程序
> （输出会标注 `halted after 1 tick`）。要动态跑这类程序，用 `.icg` 让 `run`/`tick --run` 自动先跑
> loader，或先手动安装数据段。

---

## 编辑器集成（LSP / VSCode）

LSP 每次编译都会把最坏 tick 数放进 `icg/stats`（`tickLimit` / `tickCost` / `tickExceeds`），
VSCode 状态栏显示：

```text
IC10: 122/128 行 · 1572/4096 字节 · … · 每 tick >128
```

鼠标悬停给出说明与 `ic10c tick --path` 提示。这样改代码时就能立刻看到「这个循环还能不能塞进
一个 tick」，不必手动跑命令。

命令面板 / 编辑器右键的 **`IC10 Go: 每 tick 预算与循环`**（`icg.tick`）会跑一次 `ic10c tick --json`，
用 QuickPick 列出每个分段（fits / 超限）和每个循环（每圈最快..最慢、迭代次数、是否跨 tick），
**选中即跳转到对应源码行**——`.icg` 与原生 `.ic`/`.ic10` 都可用。

**`IC10 Go: 打开每 tick 预算面板`**（`icg.tickPanel`）打开一个持久 Webview 面板（Beside 分栏），
按芯片分块（可折叠）显示「分段」表与「循环」表：点击任意一行跳转到源码，**悬停某行会在编辑器里高亮
它的源码范围**（循环按源码起止行，分段按起点）；每个分段可**展开看最坏路径**（路径里每个 `L..` 也可
点击跳转）；顶部有**「只看超限」**开关（刷新后仍保留）和**「导出 Markdown」**（把两份表导出成 Markdown
文档）；保存或切换文件时自动刷新。

---

## 算法

分析对象是**最终发射的 IC10**，`.icg` 与原生 IC10 共用同一条管线：

1. **解析**：`internal/vm.Parse` 解析 IC10（注释、空行、标签、`alias`/`define`、
   `HASH("…")`、符号都在这一步展开），得到按行索引的指令表。
2. **建 CFG**：逐条算后继边。`yield`/`sleep`/`hcf` 是段边界（终结节点）；`j`/`jal`
   取目标（常量或标签）；条件跳转取「目标 + 顺序」两条边；`jr`/寄存器目标按保守处理
   （连到所有可能的返回点，并在结果里标 `indirect`）。目标若落在**标签/空行**（单占一行、
   没有指令），会前移到下一条真实指令——手写 IC10 的 `bgtz rX label` 就靠这一步才能连出回边。
   - **`j ra` 的返回点收窄**：`jal f` / `-al` 把 `ra` 设为返回地址，`move ra r` 改 `ra`。
     对 `ra` 做一遍前向常量传播，`j ra` 只连到该处 `ra` 可能取到的返回点；算不出来才回退到
     「所有返回点」。否则 `jal f` 紧跟的 `move ra r` / `j ra` 续接段会与返回点接出回边，
     凭空造出一个**从不执行**的循环（把本来 fits 的程序报成 `EXCEEDS`）。
   - **被调函数摘要**（`jal` / `-al` 的目标）：先算「入口 → `j ra`」的最坏代价，只接受
     loop-free、无嵌套调用、无 tail-call、且每条路径都返回的函数。调用点改写成一条
     「代价 = 1 + 摘要」的直线指令，`succ` 指向调用后的下一条，函数体不再进入调用者 CFG。
     否则**同一函数被多个调用点**调用时（outline 后的常见形态），`ra` 的并集收窄不了，
     `j ra` 会接出幻影回边、把整段报成「无界」——见 [`backlog.md`](backlog.md) E3（`打印机控制`：
     7 台从「无界」到真实的 267）。
3. **分段**：入口，以及每个 `yield`/`sleep` 之后的指令，各是一个分段起点。分段 = 从起点
   出发、不穿过边界所能到达的节点集合。
4. **最坏路径 DP**：`f(节点, 剩余预算) = 1 + max(后继的 f(·, 剩余-1))`，到边界节点计 1。
   按剩余预算做记忆化——因为每步预算减一，循环会自然终止，**不需要**先知道循环次数
   就能给出「是否会超过预算」。能放进预算的分段给出**精确**值；放不进的分段报 `EXCEEDS`。
5. **循环**：用支配关系找回边，得到自然循环；对每个循环：
   - `body` = 从 header 到 latch 的一圈最坏指令数；`BodyMin` 是最快一圈。
   - 含 `yield`/`sleep` 的循环标 `spans`（跨 tick）：它跨 tick，仍照常列出，`body` 沿
     分段边界的续接（`dsucc`）统计**一圈**行数——这是最该看的 tick 循环。
   - **迭代次数**：若循环只有一个归纳寄存器（`add/sub rX rX k`），且出口分支把它和常量
     比较，就模拟算出一圈有几次；识别不了则为 `?`。已知迭代次数的回边在 DP 里**用满就
     不再走**，所以能正常结束的循环不会被误判超限。
   - 识别不了上界的循环，只靠预算封顶——它的最坏情况本来就是**无界**的，报 `EXCEEDS` 是对的。

---

## 例：多打印机产量控制器

`examples/自动打印机产量控制-多机-排料保护-表驱动.icg` 编译成 122 行，分析结果：

| 分段 / 循环 | 结果 |
|---|---|
| segment 0（入口 → 第一次 `yield`） | 5 条，fits |
| segment 1（主 tick） | **129 = EXCEEDS 128** |
| loop L9..L27（`resolve`，首 tick 解析 ReferenceId） | body 19 × 8 |
| loop L29..L62（`fastStep`，每 tick 每台一遍） | body 26 × 8 |

要点：`fastStep` 的**最坏**一圈是 26 条（走「开口排料」那条分支：读 `Open`→写 `Reagents`→
写堆垛机），8 台 = 208 条，**远超 128**；而**空闲**一圈约十几条，8 台 ~100 条多一点，
这就是源码注释里「约 9 台 ~70 条」的来源。

也就是说：

- **典型路径**（都空闲）能压进一个 tick，程序平时看着正常；
- **最坏路径**（某台正好在开口排料）会在一个 tick 里被切开，`fastStep` 不再保证「每 tick 每台一遍」，
  而是分 2 个 tick 跑完。

这正是这个工具想暴露的东西：设计注释里的「约 110 条」是**平均**，不是**上界**。要真正保证
每 tick 一轮，得缩小 `fastStep` 最坏一圈（例如把「开口排料」分支拆到 `slowStep` 里轮转）。

> 注：segment 1 的最坏路径**也包含首 tick 的 `resolve` 循环**（`resolve` 只在首 tick 跑，
> 之后 `db.stack[BOOT]` 为 0 跳过）。所以 segment 1 的「最坏」是「所有可能路径」的上界，
> 包含首 tick；稳态上界会小一些（但 `fastStep` 本身就 >128，结论不变）。

---

## 局限

- **最坏 + 上界**：给的是「所有可能路径」的最大值，可能包含只发生一次的分支（如首 tick 初始化）。
  要精确到「稳态」，需要用户自己看 `--path` 判断。
- **动态界的循环**：迭代次数由运行时数据决定时无法静态求出，按预算封顶并报超限（其最坏确实无界）。
  只有「单一归纳寄存器 + 常量比较」的计数循环能识别出迭代次数。
- **间接跳转**（`jr rX`、寄存器目标）：目标集合未知，按保守处理并标 `indirect`，结果是上界。
- **只分析运行时程序**：`.icg` 的一次性 loader（`<file>.data.ic`）不在分析范围内——它只在安装时跑一次，
  不参与每 tick 的循环。
- **不含设备 I/O 时间**：只数指令条数，不管设备响应、网络延迟。

---

## 真机验证

在游戏内用 testbench 取芯片当前程序（`ic10c testbench program --chip NAME`）另存为 `.ic`，
再用 `ic10c tick` 分析，得到的就是**该芯片正在跑的那份 IC10** 的每 tick 预算，可与游戏里
「芯片用量」以及实际运行表现对照。多人（含专用服务器）下读取走本地镜像，安全只读。

见 `docs/ingame-testbench.md` 的 `program` / `net` / `device` 命令。
