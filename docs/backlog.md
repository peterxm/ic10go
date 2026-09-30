# 改进清单（backlog）

> **`.icg` 语法已冻结**（自 `ic10c 0.8.23`，见 [`spec.md`](spec.md)）：本清单只收
> **实现层**的改进（内建、优化、工具、测试），不引入新关键字/语法形式，也不做破坏性改动。
> 需要新语法的想法一律不做。

围绕 **"零成本表达力"** 与 **"把程序塞进 128 行 / 4 KiB"** 两条主线整理。原则：

- 任何新语法/内建都必须**在编译期消解**，不产生运行期对象（否则负预算）。
- 优化以**减少行数**为先、字数为次；默认行为应尽量取更短者，而不是靠用户开 flag。
- **不做**无界序列容器（list/数组 + 查询 DSL）：见文末「明确不做」。

优先级：**P1 > P2 > P3 > P4 > P5 > P6**。状态用 ✅ 已实现 / 🚧 进行中 / ⬜ 待做。

> ✅ **寄存器分配：几乎保留全部寄存器时的指数爆炸（已修，2026-09）**。`reserveRegs(0, 15)`
> 配稍大程序（例：`solverLarge` 改写版 2.4 KB）原来会让编译 **>120s 不返回**。
>
> 根因（实测每轮数据）：palette 为空 ⇒ 每个值都判为 spill ⇒ `spiller` 重写函数、为
> load/store 临时量再引入新寄存器 ⇒ 下一轮 spill 集**翻倍**（110 → 400 → 800 → 1600 → …），
> 64 轮上限对应约 2⁶⁴ 条指令，等价于永不返回。前端 0.56ms、降级 0.10ms，全部时间都在
> `regalloc`。
>
> 修法：分配器加**发散检测**——当上一轮已需 spill ≥32 个值、且本轮 ≥2 倍时立即报
> `register allocation did not converge`（并带可操作提示），另加 20000 条指令的兜底预算；
> 小规模翻倍（1→2、2→5）是正常的，不触发。修后该用例 **0.03s** 报错。
> 反编译器侧另有 `bankMaxRange = 12`，保证自身产物不会落入这段。

> ✅ **不可达前驱污染支配集（已修，2026-10）**。`ir.Dominators` 把**不可达块**也当成前驱
> 求交，而不可达块的支配集只有自身，于是可达块的真实支配者被抹掉。后果之一：`licm` 里
> 判断「定义块是否支配其所有使用」的 `dominatesAllUses` 误判，把一个定义提升到某个使用点
> 之前，随后 `dce` 按活跃性把它删掉，产出内部错误「块 N 使用寄存器 R，但它从未被定义」。
> 触发样例（`data` 表用循环变量索引 + 循环内 `continue` + 无 `yield`）：
> `for { i := d0.Setting; if i < 0 || i >= 3 { continue }; d1.Setting = T[i] }`。
> 修法：`Dominators` 先算 Entry 可达集，求交时跳过不可达前驱（不可达块支配集取自身）。
> 回归：`TestDominatorsIgnoreUnreachablePreds`、`TestDataTableIndexInLoopWithContinue`。
>
> 同轮修：`Function.NewBlock` 原来用 `len(Blocks)` 当块 ID，块被删除后再新建会**复用 ID**；
> 而终结符 `Key()` 用块 ID，两个不同块会被当成同一个（CSE / 合并类 pass 的隐患）。改为
> 单调计数器分配。回归：`TestNewBlockUniqueIDs`。

> ✅ **标签当值（已修，2026-09）**。标签用作值应取它的行号（游戏与 VM 都已验证：注释行、
> 空行都计数）。原来在**分支条件**里会失败——生成的 IC10 留着占位符（控制字符
> `\x01L5\x01`）。根因：标签的块在布局时没有自己的行（它只是个跳转到下一块的空块），
> 旧代码只对**已布局**的块做占位符替换，于是遍历不到它。
>
> 修法：`codegen` 的 `labelLine` 沿该块的 `Jmp`/`Goto` 链找到第一条真正发射的行——这正是
> IC10 里「标签取下一行」的含义；找不到才报 internal error（不再输出控制字符）。反编译器
> 随之**取消了「只在赋值形式翻译」的限制**，`roboroutine`/`baseFriend`/`dirciroutine`
> 的告警**全部归零**，语料往返（写序列 + 用户栈）全绿。

> ✅ **编译器：共享栈上被覆盖的写被误删（已修，2026-09）**。`deadStores` 判「该槽之后没被读
> 过就直接删」，但**共享栈在每个 tick 边界都可能被别的芯片看到**——芯片跑满指令预算就被打断，
> 中间值照样会暴露。于是反编译产物里这种形状会丢掉一条可达的写：
>
> ```icg
> // icg: shared-stack
> var r0 = d0.Setting      // 编译期未知；运行时 r0 = 1
> var r1 = 1
> label L:
> if r0 < r1 { goto M }
> put(db, 0, 2)            // 运行时走这里
> goto L
> label M:
> put(db, 0, 3)
> ```
>
> **真机实测**（芯片 2，原件 299 条 `put` 效果）：修复前重编译版 **0 条**（整段程序等于空转），
> 修复后 299 条全部一致（0 差异）。`IC10C_NO_OPT=1` 时也有那条写。
>
> 修法：`deadStores` 只在 `fn.PrivateStack` 时才把 store 判死（私有栈确实没人能看见）。
> 语料行数**零影响**（这个优化本来也没在这些共享程序上触发）。反编译器现在给产物加
> `shared-stack`，所以这条约束默认生效。

> ✅ **间接寄存器组（已修，2026-09）**：反编译器现在分析运行期指针能命中的寄存器集合，
> 把这段范围保持为**物理寄存器**（发 `reserveRegs(lo, hi)`，访问写成 `ireg(N)` /
> `setIreg(N, v)`），与手写端口的做法一致。分析（`internal/decomp/bank.go`）是对解析后
> 指令表的小型抽象解释：寄存器取已知整数或未知，`sp` 当作 16 号寄存器跟踪（所以计数器
> 驱动的循环会终止），条件未知的分支两条边都走；**任何**动态间接访问的指针未知就放弃并
> 报一条 warning，而不是静默产出可能错的代码。起始状态按 IC10 语义取**全 0**
> （`r0..r15` 与 `sp` 都是 0），所以只靠初值 0 当索引的寄存器表也能定界——例如
> `indirectRegsHousing`（寄存器里放 4 个太阳能板 prefab hash，`list` 从不赋初值直接用）
> 现在会还原成 `reserveRegs(0, 3)` + `setIreg(0..3, hash(...))`。间接寄存器 / 寄存器组的
> 完整分析（编译期折叠、能否自动降级、端口测试盲区）见
> [`register-banks.md`](register-banks.md)。
>
> 配套的**代码生成折叠**（`internal/codegen`）把这种寄存器访问压回一行：
> `t = <op>; rrP = t` → `<op> rrP ...`（间接目标本来就是合法目标操作数），
> `u = ireg(rrP); d = u op b` → `d = rrP op b`（间接读是合法源操作数）。
>
> **结果**：`traderSolver{,RAW}` 126 行、`solverLarge{,RAW}` 106 行，
> **`knownUnsupported` 现在为空**——四个求解器都纳入写序列 + 用户栈往返测试，真机逐写
> A/B（`solverLarge` 去等待分支后 95 vs 88 效果）**0 差异**。
>
> `Barsiel_s Furnace` 的指针在某条路径上确实未知（`r5` 在到达 `trunc rr5 r0` 前可能来自
> 设备读），按设计报 warning。
>
> 已修（同轮）：`shared-stack` 静默删写、VM 的 `define` + `HASH` 解析、
> round-trip 测试盲区（见下）。
> - **round-trip 测试盲区**：以前只比较**设备写**，而这些程序的接口是**宿主栈**，
>   全 0 输入下输出退化、比较空过。新增 `TestIc10CodeRoundTripStack`：预填宿主栈、
>   跑到**稳态**、比较用户区 `[0,128)`（编译器的 data/spill 区在其上方，重编译可以合法
>   地不去动它）。
> - **`shared-stack` 静默错编译**：单芯片程序默认 `private-stack`，会把「自己从不读的
>   宿主栈写」当死存储删掉——而 `db` 栈是给别的芯片读的接口。`put db 0 42` 单独编译
>   得到**空程序**，`dirciStackloader.ic10` 被删到 **1 行**。反编译器现在总是给产物加
>   `// icg: shared-stack`（全语料代价仅 +23 行）。注意：**手写** `.icg` 若向宿主栈发布
>   数据，仍需自己写 `// icg: shared-stack`。
> - **VM 的 `define` + `HASH` 解析**：`define A HASH("x")` 后使用 `A` 会解析成字符串而
>   不是数字（`resolve` 只在初始符号上试 `HASH(...)`），于是原程序在 VM 里读到 0。
>   已修（每步符号展开都重试）。

---

## 优先级总表

| 优先级 | 项 | 主题 | 影响 | 工作量 | 状态 |
|---|---|---|---|---|---|
| **P1** | A1 | `const` / `data` 可调用**纯用户函数** | 省行 + 表达力 | 中 | ✅ |
| **P1** | A2 | 补齐可折叠的**纯内建** | 省行 | 小 | ✅ |
| **P2** | B1 | `import` / 多文件 / 本地库 | 库的载体 | 中 | ✅ |
| **P3** | C3 | 编译期**循环/生成器**（生成 `data`） | 去重 + 省行 | 中 | ✅ |
| **P3** | A4 | `data` 表可由编译期函数生成 | 省行 | 中 | ✅ |
| **P4** | D2 | 统一 **size model**，把 opt-in 优化变自动 | 省行 | 中 | 🚧 |
| **P4** | D3 | 放宽 **outline**（多层/非叶子） | 省行 | 中-大 | ✅ |
| **P4** | D4 | 特殊寄存器（`sp`/`ra`）算术**两地址折叠** | 省行 | 小 | ✅ |
| **P4** | D5 | **空分支清理**（目标即下一行） | 省行 | 小 | ✅ |
| **P4** | D6 | 间接寄存器操作数折叠（`rrN` 目标/源） | 省行 | 小 | ✅ |
| **P5** | B3 | 小记录**多返回值**（非容器） | 表达力 | 中-大 | ✅ |
| **P6** | A3 | 编译期字符串（拼接 + hash） | 表达力 | 中 | ✅ |

---

## P1 — 编译期求值：从"常量折叠"升级为"编译期执行"

### A1 `const` / `data` 可调用纯用户函数 ✅

**问题**：`const X = helper(3)` 直接报 "not a compile-time expression"，即使 `helper` 是纯函数。求值器（`internal/sema/sema.go` 的 `Eval`/`evalCall`）只认内建。

**方案**：把 `const` / `data` 的求值**推迟到声明收集之后**（此时函数表已完整），用一个能解释纯用户函数的求值器：

- 纯函数判定（调用图闭包）：函数体不含设备访问、无副作用内建、无对非纯函数的调用、不读 `data` 表。
- 解释器支持：局部变量、`if/else`、`for`、`return`、赋值、算术/比较/三元、内建调用、用户函数调用。
- **步数预算**防止 `for {}` 不终止；超预算即判定"不是编译期表达式"并报错。
- 设备/data 参数、递归（语言本就不允许）一律不参与编译期求值。

**验收**：
- `const K = triple(3)` 折叠为 `9`，产物无运行期指令。
- `data T = [f(0), f(1), f(2)]` 可由纯函数生成。
- 非纯函数（含 `yield`/设备读/`push`）在 `const` 中仍报错。
- 不终止的 `const` 在步数预算内报错而非挂死。

### A2 补齐可折叠的纯内建 ✅

**问题**：`evalCall` 只折叠 `abs/sqrt/floor/ceil/round/trunc/pow/min/max/hash`；`const S = sin(0.5)`、`clamp(...)`、`sgn(...)` 失败。

**方案**：把以下纯内建加入编译期求值：
`sgn sin cos tan asin acos atan atan2 exp log clamp lerp isNaN`（`log` 为自然对数，与 IC10 的 `log` 一致）。

**验收**：上述函数在 `const` / `data` 中可用并折叠；`rand` 永不折叠。

---

## P2 — `import` / 多文件 / 本地库 ✅

**问题**：`.icg` 单文件编译，`const`/`func`/`data` 库只能整段复制粘贴，没有"标准库"。

**方案（已实现）**：顶层 `import "路径"`，相对导入者目录解析、省略扩展名补 `.icg`；
被导入文件只能声明 `const`/`data`/`func`（无 `chip`/`bus`/`use`/`main`）；菱形与循环
导入按文件去重；节点保留各自文件位置。**LSP/VSCode 也会跟随导入**（相对当前文件）。
库搜索目录由 API `Options.LibDirs` 提供，CLI 暴露为可重复的 `--lib DIR`，
VSCode 为设置 `icg.libDirs`（同时作用于语言服务器与编译/运行）。

**验收**：两个文件共享 `const`/`func` 并折叠进产物；缺失导入报错并指向正确行；
循环导入正常合并；被导入文件声明 `main` 报错。

---

## P3 — 编译期生成 ✅

### C3 编译期循环 / 生成器 ✅

**方案（已实现）**：`data` 支持**范围推导** `data T = [ expr for i in lo..hi ]`，在编译期
展开成整张表；元素表达式可调用纯函数，上下界必须是编译期常量整数，长度受 512 槽持久
栈限制。`for i in range` 形式之外（如展开重复语句块）暂未提供。

### A4 `data` 表由编译期函数生成 ✅

由 P1 的编译期执行直接得到：`data T = [f(0), f(1)]` 与推导形式都可用。

---

## P4 — 优化器默认强度 🚧

### D2 统一 size model 🚧
编译器本就会对 **inline/outline、data-read 折叠、mem2reg** 试多方案并取更短的 runtime；
本次又给 outline 增加了**单个函数**候选（整套外提不如只提一个时能选到）。另在**优化版
接近/超出 128 行或存在溢出**时，与**整体未优化**版比对取更短者（见
[`architecture.md`](architecture.md) §5）。其余 opt-in 开关**不适合默认开启**，原因如下，故保持手动：

- `--fast`：以运行速度为目标（多展开），不是省行。
- `--jump-table`：依赖 `jr` 的真机语义（与 `--rel-jump` 同类，需要真机验证）。
- `--auto-table`：会把 switch 放进数据段，**要求先跑 loader**，改变部署契约。
- `--redundant-device-writes`：改变可观测的设备写序列。
- `--merge-renamed-tails`：实验性，会改变寄存器使用。

### D3 放宽 outline：非叶子（D3a）✅

**已实现 D3a**（2026-09）。`PlanOutlines`/`outlinable` 现在允许**非叶子**函数：外提时其
函数体内所有调用都**内联**，因此外提体内不含 `jal`，IC10 唯一的 `ra` 依然安全。低层
`label`/`goto`/`call`/`ret` 改用**传递**的 `computeLabeledFuncs` 排除（只看函数自身会漏掉
“f 调用含 `call` 的 g”）。实现点：

- `internal/lower/outline.go`：`outlinable` 去掉 `leaf` 约束，改用传递 `labeled` 集合；非
  多返回值、语句数 ≥2 不变。
- `internal/lower/lower.go`：新增 `inOutlineBody`——`lowerOutlined` 期间置位，
  `lowerCallExpr` 不再走 `outlineCall`，于是体内全部内联。
- size model 不变（仍含“完全不外提”候选），**行数只减不增**；`IC10C_NO_OUTLINE` 可整体关闭。

**实测**：
- 现有语料仍为 **0** 收益（127 个 `.icg`，非叶子且被调 ≥2 的函数 = 0；叶子且被调 ≥2 = 16）。
- 合成场景 `experiments/outline-d3/`（`step` 非叶子、调 `clamp`、被调 3 次）：**53 → 28 行**，
  寄存器 **8 → 2**，VM 写序列一致。
- 真机场景 `testdata/bench/ingame/s50_nonleaf_outline.json`（A CHIP）：默认 **25 行 / 2 个
  `jal`** vs `IC10C_NO_OUTLINE=1` **39 行**；两种构建都通过 `testbench run --diff`（真机 +
  VM，3/3）。
- 测试：`internal/lower` `TestPlanOutlinesNonLeaf`（叶子候选 + 传递 labeled 排除）、
  `pkg/ic10` `TestOutlineNonLeaf`（行数下降 + 写序列一致）。

D3b（真正嵌套外提、保存 `ra`）未做，也不再需要：D3a 已覆盖“非叶子被多处复用”，且无
`ra` 风险。

**触发条件**：`import`（P2）催生的“分层库 + 非叶子 helper 多处复用”。两者都可作为候选
方案由 size model 取更短者，行数上不会变差。

### D4 特殊寄存器算术两地址折叠 ✅

**问题**：`sp`/`ra` 不参与通用分配，但可以像普通寄存器一样作为算术目标。编译器把
`sp = sp - 46` 降级成"临时寄存器 + 回写"：

```asm
move r0 sp
sub r0 r0 46
move sp r0
```

3 行，而 IC10 允许直接写 `sub sp sp 46`（`--spill stack` 的存取路径本就发
`add sp sp 1`）。

**方案**：`internal/codegen` 在**行号回填之前**做一次纯渲染折叠（不改 IR，因此对同一
`fn` 反复调用 `generate` 是安全的），识别两种形态：

- `t = a op b; sp = t` → `op sp a b`（2 行变 1 行）
- `t1 = sp; t2 = t1 op b; sp = t2` → `op sp sp b`（3 行变 1 行）

只在**临时寄存器/被读的特殊寄存器全局仅此一次使用**时折叠；`Bin`/`Un`/`Select` 都覆盖。
减行后跳转目标由现有布局重算。（`Select` 只在 `select` 与其 `sp` 存储在同一个基本块时触发，
见 [`special-reg-operands.md`](special-reg-operands.md)。）

**实测**（122 个语料脚本，反编译→重编译）：共省 **56 行**，8 个脚本变短
（`roboroutine` 114→101、`Vending_Machine_Controller` 112→100、`baseFriend` 82→71、
`dirciroutine` 108→102、`tugetest`/`printers` 123→118）。真机逐写 A/B（芯片
13/47/6/45）**0 差异**，其中芯片 13 的产物确实含 `sub sp sp 46` 等折叠行。

### D5 空分支清理 ✅

**问题**：产物里存在目标就是下一行的分支/跳转（`j 47`、`bne r3 2 61` 等）。两支落到
同一行，指令是空操作。

**方案**：`internal/codegen` 在行号回填前删除这类行，迭代到不动点（删行可能让别的分支
的目标变到相邻行）。**跳转表条目**（`j <case>`，按索引寻址，删了会错位）与**调用**
（`jal`、`b<cond>al`，必须真正执行）显式排除；`line` 结构新增 `table` 标记。

**实测**：语料里清掉 8 处，共省 8 行；真机逐写 A/B 同 D4 一样 **0 差异**。

### D6 间接寄存器操作数折叠 ✅

**问题**：IC10 的 `rrN` 既能作目标也能作源操作数，但 `.icg` 的 `setIreg(ptr, t)` / `t := ireg(ptr)`
先用临时寄存器再搬。反编译「间接寄存器组」程序时这类往返很多（`traderSolver` 里 13 处）。

**方案**：`internal/codegen` 在行号回填前折叠（都要求临时寄存器全局单次使用）：

- `t = <op>; rrP = t` → `<op> rrP ...`（跨过 lowerer 产生的一次拷贝也算）
- `u = ireg(rrP); d = u op b` → `d = rrP op b`（读必须紧邻消费者）
- `u = sp; d = u op b` → `d = sp op b`

消费者不限于算术：单次使用的读还可以直接喂给设备写（`s` / `ss` / `sd`）、内建
（`poke`/`put`/…）和 `select`——`u = sp; s db Setting u` → `s db Setting sp`、
`u = sp; poke u v` → `poke sp v`。另外 `select` 也支持写 `sp`/`ra`
（`t = select c a b; sp = t` → `select sp c a b`）。细节与 `reoreotest` 的 92→95 案例见
[`special-reg-operands.md`](special-reg-operands.md)。

**实测**：语料 5511 → **5490 行**（6 个脚本变短）。其中 `traderSolver` 139 → **126 行**，
`knownUnsupported` 因此清空；真机逐写 A/B（`solverLarge`，去掉等待分支以便确定）
**0 差异**。

### D7 设备参数外提（drN）🚧

**问题**：带设备实参的调用**强制内联**（`hasDeviceOrDataArg`），设备 helper 被调 K 次就
复制 K 遍。对照 [sicc](https://github.com/Alan-Chen99/sicc) 的温控示例：同样语义 17 行 vs
本项目 20 行，差距主要来自「设备端口走运行期寄存器 + 子程序只发一次」。
分析见 [`sicc-comparison.md`](sicc-comparison.md)。

**已实现（Round 1，2026-09）**：运行期设备端口。`dev.Prop` / `dev.slot[i].Prop` 在 `dev`
是运行期值（局部变量 / 参数 / 数值常量）时下沉为 IC10 的寄存器选择设备操作数 `drN`
（复用 `readDev`/`writeDev` 的 `LoadDyn`/`StoreDyn`、`readDevSlot`/`writeDevSlot` 的
`LoadSlot`/`StoreSlot`）。编译期端口仍走 `dN`，零开销。**顺带修复**：以前把运行期值传给
设备参数会静默输出非法的 `dev.Setting`（仅警告），现在输出合法 `drN`。

真机验证：`testdata/bench/ingame/s60_dyndev`（真机 + VM 差分 4/4）。

**已实现（Round 2，2026-09）**：设备 helper 外提 + 动态设备读去重。

- **外提决策**：去掉 `hasDeviceOrDataArg` 对设备实参的一票否决。外提时设备实参按端口号
  写进参数寄存器，函数体用 `drN`；`data` 表实参仍强制内联，`db` 实参也回退内联（无数
  值端口）。常量实参调用点有两种候选——**保持内联折叠**（旧行为）与**一起外提**（常量
  在共享体里经全局常量传播折叠）——由大小模型取更短者（`lower.Options.InlineConstArgs`
  + 编译器候选循环）。
- **`drN` 冗余读消除**：`loadKey` 覆盖 `LoadDyn`（属性读）与带 `DevPtr` 的 `LoadSlot`
  （运行期槽位读）。运行期端口注册到伪设备 `"*"`，任何设备写都使其失效（保守但正确）。
- **候选诊断隔离**：一个候选变体内部的 `ir.Verify` 失败不再污染整次编译；大小模型丢弃
  它并保留可用的候选（顺带让随机差分里此前被掩盖的 mem2reg + 外提组合不再让编译失败）。

**实测**：目标例（sicc 温控）**20 → 18 行 / 249 → 190 字节**（sicc 17 行 / 190 字节，
只差尾调用）；语料 `testdata/programs`+`ic10code`+`examples` 共 134 个 `.icg`，
**5320 → 5270 行（−50，8 个脚本变短，0 个变长）**。真机 + VM：新增
`testdata/bench/ingame/s61_dyndev_outline` 4/4，全部 ingame 场景 13/13。全量
`go test ./...`、`./build.sh assert`（58/58）绿；`TestFunctionSpecialization` 仍保留
「常量调用折叠 + 变量调用外提」的语义。

**已实现（Round 3，2026-09）**：尾调用 / `ra` 复用。返回块只做一次跳转的 `Call` 在
`internal/opt` 的 `tailCall` pass 里改写为「`ra = <目的地>` + `Jmp <被调体>」；被调体的
`j ra` 直接回到目的地。被调体紧邻布局时跳转被消掉，每处省 1 行。IR/CFG 早已支持
`ra = <label>; goto sub` 手写调用（`BuildCFG` 会把该 label 作为所有 `JmpRA` 的后继），
所以无需新增 IR 节点。安全性：外提体已内联其全部调用，体内无第二个 `jal`；且只有
返回块为空的 `Call` 才改写（有返回值的调用返回块含拷贝，天然排除）。

**实测**：目标例 **18 → 17 行 / 190 字节**，与 sicc **完全持平**。语料累计
`5320 → 5268 行`（−52；气闸控制 92→79、气闸双门 78→72）。真机 + VM：
`testdata/bench/ingame/s62_tailcall`（真机 + VM 差分 4/4），全部 ingame 场景 **14/14**。
测试 `TestTailCall`（断言 `move ra` 存在、`jal` 恰 1 个、写序列与 `IC10C_NO_OPT=1` 一致）。
全量 `go test ./...`、`./build.sh assert`（58/58）绿。

**现状**：D7（运行期设备端口、设备 helper 外提、`drN` 冗余读消除、尾调用）已全部实现。

---

## P5 — 小记录多返回值（非容器）✅

**方案（已实现）**：`func f() (num, num) { return a, b }` + `x, y := f()`（也支持
`x, y = f()`）。多返回值函数**始终内联**，结果映射到独立虚拟寄存器，不引入调用约定或
返回地址（`ra`）风险；也因此不能被外提、不能当普通表达式、不能出现在 `const`/`data`。
与"不做序列容器"的边界：固定小记录映射到寄存器，无界序列要占栈槽。

---

## P6 — 编译期字符串 ✅

**方案（已实现）**：`hash` / `str` / `raw` 的实参支持**编译期字符串拼接**
（`"A" + "B"`，可嵌套），在编译期折叠：`hash("Structure" + "GasSensor")` 得到与
`hash("StructureGasSensor")` 相同的 CRC-32，`str("Re" + "ady!")` 输出 `STR("Ready!")`。
更完整的文本库（查找/切片/parse）未做。

---

## 明确不做

- **新语法**：`.icg` 语法已冻结（见 [`spec.md`](spec.md)），不再新增关键字/语法形式，也不做破坏性改动；本清单只收实现层改进。
- **无界序列容器 / LINQ 查询 DSL**：在 128 行预算下是负资产（物化要占栈槽、Blocking 阶段代价高）。真正省行的"查询"形态（网络批量聚合、常量表折叠）已由 `batch.*` 与 `data` 段提供。

---

> 相关：语言规范 [`spec.md`](spec.md)、目标与内建表 [`target-ic10.md`](target-ic10.md)、优化通道 [`architecture.md`](architecture.md) §5。
