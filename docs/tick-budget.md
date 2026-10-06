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
ic10c tick --strict firmware.ic        # 有分段超限则退出码 1（可接 CI）
ic10c tick --json printer.icg          # 机器可读
```

`.icg` 走正常编译（含 `--lib` / `--data-layout` / `--unsafe` / `--auto-table`），
分析的是**编译后的最终 IC10**（已包含寄存器分配、溢出、优化后的真实行数）。
`.ic` / `.ic10`（或其它后缀）按原生 IC10 解析。

### 输出

```text
examples/…-表驱动.icg: per-tick budget: limit 128, 122 instructions
  tick segment 0: L1 -> yield L5  worst-case 5 instructions [fits]
  tick segment 1: L6 -> yield L5  worst-case 129 instructions [EXCEEDS 128]
  loop L9..L27 (header L9): body 19 x 8 iterations
  loop L29..L62 (header L29): body 26 x 8 iterations
  worst case: a loop between yields can exceed the budget, so the chip resumes mid-loop on the next tick.
```

- **tick segment**：两个 tick 边界之间的一段。`L6 -> yield L5` 表示从第 6 行跑到第 5 行的
  `yield` 结束本次 tick。`worst-case` 是这一段任意路径上的最大指令数（≥ 128 就 `EXCEEDS`）。
- **loop**：识别到的自然循环：`body` 是一圈的最坏指令数，`x N iterations` 是识别出的
  迭代次数（`?` 表示没识别出常量上界）。
- `--path` 给出实际的最坏指令序列（IC10 行号，0 基），可直接对着反汇编/产物看是哪几行。

---

## 算法

分析对象是**最终发射的 IC10**，`.icg` 与原生 IC10 共用同一条管线：

1. **解析**：`internal/vm.Parse` 解析 IC10（注释、空行、标签、`alias`/`define`、
   `HASH("…")`、符号都在这一步展开），得到按行索引的指令表。
2. **建 CFG**：逐条算后继边。`yield`/`sleep`/`hcf` 是段边界（终结节点）；`j`/`jal`
   取目标（常量或标签）；条件跳转取「目标 + 顺序」两条边；`jr`/寄存器目标按保守处理
   （连到所有可能的返回点，并在结果里标 `indirect`）。
3. **分段**：入口，以及每个 `yield`/`sleep` 之后的指令，各是一个分段起点。分段 = 从起点
   出发、不穿过边界所能到达的节点集合。
4. **最坏路径 DP**：`f(节点, 剩余预算) = 1 + max(后继的 f(·, 剩余-1))`，到边界节点计 1。
   按剩余预算做记忆化——因为每步预算减一，循环会自然终止，**不需要**先知道循环次数
   就能给出「是否会超过预算」。能放进预算的分段给出**精确**值；放不进的分段报 `EXCEEDS`。
5. **循环**：用支配关系找回边，得到自然循环；对每个循环：
   - `body` = 从 header 到 latch 的一圈最坏指令数；
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
