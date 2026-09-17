# 编译器架构与路线图

> 本文描述 `ic10go` 编译器的内部架构、关键算法与里程碑。
> 语言语法见 [`spec.md`](./spec.md)，IC10 目标细节见 [`target-ic10.md`](./target-ic10.md)。

---

## 1. 目标

将 `.icg` 源码编译为满足 Stationeers IC10 硬约束的机器码：

- ≤ 128 行、≤ 4096 字节、≤ 90 字符/行
- 输出**不可读但高效**：无 alias / define / 注释 / 空行 / 标签
- 基于活跃性的寄存器复用，最小化栈溢出
- 编译期完成一切可完成的工作（常量、哈希、内联）

---

## 2. 编译管线

```
.icg 源码
  │
  ▼
┌─────────┐   ┌─────────┐   ┌─────────┐
│  lexer  │──▶│ parser  │──▶│  AST    │
└─────────┘   └─────────┘   └────┬────┘
                                 ▼
                          ┌─────────────┐
                          │    sema     │  名字解析 / 类型检查 / 内建表校验
                          └──────┬──────┘
                                 ▼
                          ┌─────────────┐
                          │   lower     │  AST → 三地址 IR
                          └──────┬──────┘
                                 ▼
                          ┌─────────────┐
                          │     opt     │  常量折叠/传播/DCE/CSE/内联+外提/...
                          └──────┬──────┘
                                 ▼
                          ┌─────────────┐
                          │  regalloc   │  活跃性 + 图着色 + 合并 + 溢出
                          └──────┬──────┘
                                 ▼
                          ┌─────────────┐
                          │    isel     │  IR → IC10 指令选择
                          └──────┬──────┘
                                 ▼
                          ┌─────────────┐
                          │    asm      │  布局 / 行号回填 / 限额校验
                          └──────┬──────┘
                                 ▼
                           ┌─────────────┐
                           │    emit     │  IC10 文本
                           └─────────────┘
```

**多芯片**：源码可含 `chip 名字 { ... }` 块（见 [`multichip.md`](multichip.md)）。
`parser` 只解析一次；`sema → lower → opt → regalloc → isel → asm → emit` 对**每块
芯片各跑一遍**（顶层 `const`/`data`/`func` 作为公共区合并进每块芯片，chip 内声明可
遮蔽），产物是每芯片一份 IC10 + 各自的一次性 loader。`pkg/ic10.CompileResult` 返回
`Result.Chips`。

---

## 3. 各阶段职责

### 3.1 lexer / parser / ast

- 手写递归下降解析器（错误恢复好、无第三方依赖）。
- 位置信息贯穿所有节点，供诊断与源码映射使用。
- 设备端口 `d0..d5` / `db` 在词法层识别为专用 token。
- 字符串保留原始值，供 `hash()` 与逻辑类型名使用。

### 3.2 sema（语义分析）

- 作用域链与符号表；检测未定义变量、重复声明。
- 顶层声明校验 + **函数体类型检查**：推断并记录表达式类型
  （`Info.ExprTypes` / `VarTypes`），报告未知类型名、同块重复声明、
  非 void 函数缺返回、把 device/data/str/void 当数值用等。
  未定义标识符与未标注（设备/data）参数按 `Any` 处理，保持保守。
- 内建表校验（可开关 `-no-check`）：
  - 设备逻辑类型名合法性
  - 槽位类型名合法性
  - 批量模式名合法性
- 标记常量、内联 / 外提候选函数。
- **多芯片**：`Info.Buses`（`bus` 契约：槽位 → 通道号）与 `Info.BusBindings`
  （本芯片 `use` 的访问点）。每块芯片的 `Info` 都并入顶层公共区。
- sema 报错会短路 lower，因此类型检查与 lower 的诊断不重复。

### 3.3 lower（AST → IR）

- 生成三地址码，虚拟寄存器无限。
- 基本块划分；`if` / `for` / `switch` 展开为分支与块。
- 函数调用点按内联 / 外提策略展开（按体积取短）。
- 生成短路的 `&&` / `||` 分支。

### 3.4 opt（优化）

- pass 注册表 + 固定点迭代；`IC10C_DUMP_PASSES=1` 打印每轮哪个 pass 变化，
  并在每个 pass 后校验 IR。
- 每轮开始/结束用 `ir.Verify` 检查结构不变量（终结符、块引用、可达性）。
- 见第 5 节。

### 3.5 regalloc（寄存器分配）

见第 6 节。

### 3.6 isel（指令选择）

- 把 IR 操作映射为 IC10 指令。
- 比较-分支融合、`select` 化、立即数选择。
- 处理 IC10 操作数限制（如 `select` 4 操作数、`s` 的源可为立即数）。

### 3.7 asm（汇编 / 布局）

- 线性布局，每条指令占 1 行。
- 计算绝对行号，回填所有跳转目标。
- 统计行数 / 字节数 / 单行长度，超限报错。

### 3.8 emit

- 输出纯 IC10 文本，无任何装饰。

---

## 4. IR 设计

采用**非 SSA 三地址码**（程序规模小，数据流迭代足够；后续可升级 SSA）。

```go
type Value interface{} // Const | VReg | Device | Label

type Instr struct {
    Op   Op
    Dst  *VReg
    Args []Value
}

type Block struct {
    Instrs []Instr
    Succs  []*Block
    Preds  []*Block
}
```

- **终结符统一接口**（`internal/ir/term.go`）：每个终结符实现
  `Successors / Uses / Redirect / RewriteUses / Key`，`BuildCFG`、
  opt、regalloc、codegen 均走该接口，不再各自 switch。
- **校验器**（`internal/ir/verify.go`）：`Verify` 检查结构不变量，
  `VerifyReachable` 另查可达性；`Optimize` 前后运行。
- **共享分析**（`internal/ir/analysis.go`）：`DefUse` / `TermUses` /
  `Liveness` / `Successors` / `Preds` / `Dominators` 由 opt 与
  regalloc 共用，避免两份实现分歧。

指令集（示意）：

```
Const    t = k
Move     t = v
BinOp    t = op a b        // add/sub/mul/div/mod/and/or/xor/sll/sra...
UnOp     t = op a          // neg/not/seqz...
Cmp      t = cmp a b
Select   t = select c a b
Load     t = l dev logic
Store    s dev logic v
LoadSlot t = ls dev idx logic
StoreSlot ss dev idx logic v
Batch    ...
Push/Pop/Peek/Poke
Call     // 外提函数的 jal / ra 调用
Yield/Sleep/Hcf
Br       j label
BrCond   b<cmp> a b label
Ret
```

数据流分析（反向）：

- `live-out[b] = ∪ live-in[s]`
- `live-in[b] = use[b] ∪ (live-out[b] \ def[b])`

---

## 5. 优化通道

| 通道 | 作用 |
|------|------|
| 常量折叠 | 编译期计算常量表达式、`hash()` |
| 块内拷贝/常量传播 | 把已知常量、拷贝代入使用点 |
| 全局常量传播 | must 分析，跨基本块传播常量 |
| 代数化简 | `x*1`、`x/1`、`x+0`、`x&0`、`min(x,x)`、`select c x x` 等恒等式 |
| 冗余读消除 | 复用更早的设备/槽位/批量读（写、`yield`/`sleep` 使其失效）；含跨基本块 CSE 与 `put`→`get` 存储转发 |
| 公共子表达式消除 | 复用相同表达式（基于可用表达式的全局 CSE，跨基本块，含设备读、交换律归一） |
| 常量分支折叠 | 条件恒真/恒假或两分支相同 → 跳转，未走分支可删 |
| 循环不变量外提 | 纯计算与设备读提到 preheader（设备读要求循环内无该设备写、无 `yield`/动态写屏障） |
| 尾块合并 | 相同终结符的块共享相同的指令后缀；regalloc 后再按物理寄存器合并一次 |
| 死存储消除 | 覆盖前无读取的芯片栈写（`put`/`poke`，常量地址）；跨基本块用栈槽活跃性分析，栈持久故退出时全部存活 |
| 死代码消除 | 活跃性驱动的纯指令删除、不可达块删除 |
| 小循环展开 | 常量次数、无调用、体小的 `for` 循环展开，使 `Table[i]` 等常量下标折叠为单条 `get` |
| 跳转表派发 | `--jump-table`（默认关闭）：稠密整数 `switch`（≥8 case、简单 case 体）降为 `jr` 计算跳转 + `j` 表，约每 case 省 1 行 |
| `--fast` 模式 | 优先运行速度而非体积：放宽循环展开门控（允许调用、更大体/次数） |
| 内联 / 外提 | 按体积决策：内联调用点，或把多次调用的叶子函数编译成 `jal` 子程序；常量实参调用点始终内联以折叠 |
| 比较-分支融合 | `if a<b` → `bge`，省一条比较 |
| select 化 | 同左值赋常量的 `if-else` → `select` |
| 逻辑化简 | `&&`→`min`、`||`→`max`、`!`→`seqz`（含无副作用的用户函数调用） |

> 尚未实现：强度削弱、以动态指令数为目标的优化。这些不减少静态行数，
> 且对 IC10 的收益有限，暂缓。

> 优化目标函数：**优先减少行数，其次减少字节数**。某些变换（如常量提为 `define`）会增加行但减少字节，由大小模型决策。

---

## 6. 寄存器分配

### 6.1 活跃区间

- 每条指令的 `live-in` / `live-out`。
- 虚拟寄存器 `v` 的区间 = `[首次定义位置, 最后使用位置]`。

### 6.2 图着色（Chaitin-Briggs）

1. 由活跃性构造干涉图（同时在活的两个虚拟寄存器之间连边）。
2. 优先合并拷贝相关的寄存器（union-find，仅在不冲突时），消除 `move`。
3. 简化 / 溢出 / 着色：优先分配到编号最小的空闲物理寄存器 `r0..r15`，
   复用率最高；无法着色时按代价选择溢出。

### 6.3 合并

- 对 `move` 的源/目的，若区间不冲突则合并到同一物理寄存器。

### 6.4 溢出

寄存器压力超过可用寄存器数（16 减去 `reserveRegs` 保留的物理寄存器）时发生。
溢出槽从数据段下方固定高地址向下分配，默认用宿主栈 `get/put db` 读写：

- **`get/put db`（默认）**：每次加载 1 行，且不需要暂存寄存器，因此溢出时仍可
  用满 16 个寄存器。
- **`--spill stack`（回退）**：固定地址 `poke` 写入，读取时恢复 `sp` 后 `peek`
  （每次加载 5 行），并保留 `r15` 作暂存。

分配器按轮迭代：着色失败 → 溢出代价最低的值 → 重新着色，直到收敛。溢出过程
自身产生的短命临时值代价被抬高，避免反复溢出它们（否则不收敛）。

### 6.5 保留寄存器

- `ra`：IC10 返回地址，不参与分配。
- `sp`：栈指针，不参与分配。
- 仅当使用外提（`jal`）时才需要 `ra`。
- `reserveRegs(lo, hi)` 声明的物理寄存器不参与分配，供 `ireg`/`setIreg`
  （`rrN`）间接访问——这是安全使用间接寄存器的前提。

### 6.6 寄存器压力报告

`stats` 输出峰值活跃寄存器数（`peak live`）与溢出槽数（`spills`），便于定位溢出。

---

## 7. 指令选择要点

| 源构造 | 目标 |
|--------|------|
| `a + b` | `add d a b` |
| `if a < b { ... }` | `bge a b Lelse` + body |
| `if c { x = 1 } else { x = 0 }` | `select x c 1 0` |
| `c ? a : b` | `select d c a b` |
| `!a` | `seqz d a` |
| `a && b`（无副作用） | `min d a b` |
| `a \|\| b`（无副作用） | `max d a b` |
| `d0.On` | `l d d0 On` |
| `d0.On = v` | `s d0 On v` |
| `d.slot[i].X` | `ls d d i X` |
| `d.slot[i].X = v` | `ss d i X v` |
| `d.channel[c][n]` | `l d d:c Channel<n>` |
| `d.stack[i]` / `d.stack[i] = v` | `get d i` / `put d i v` |
| `read(d, lt)` / `write(d, lt, v)` | `l r d (r_lt)` / `s d (r_lt) v` |
| `readById(id, lt)` / `writeById(id, lt, v)` | `ld r (id) (r_lt)` / `sd (id) (r_lt) v` |
| `readDev(i, lt)` / `writeDev(i, lt, v)` | `l r drN (r_lt)` / `s drN (r_lt) v` |
| `readDevSlot(i, n, slt)` / `writeDevSlot(i, n, slt, v)` | `ls r drN n slt` / `ss drN n slt v` |
| `ireg(p)` / `setIreg(p, v)` | `move r rrN` / `move rrN r`（`p` 为常量时直接寻址 `rN`，需 `reserveRegs` 覆盖） |
| `batch.read(...)` | `lb ...` |
| `sorter.*` / `printer.*` | 编译期按位段打包为常量（再 `put` 到设备栈） |
| `yield()` | `yield` |

立即数策略：IC10 多数指令允许立即数操作数，常量直接内联，避免额外 `move`。

---

## 8. 内建表

存放于 `internal/builtin`，通过 `go:embed` 加载：

- **逻辑类型表**：`Temperature` `On` `Ratio` ... 用于校验。
- **槽位类型表**：`Occupied` `Mature` ...
- **批量模式表**：`Average`(0) `Sum`(1) `Minimum`(2) `Maximum`(3) `Count`(4)。
- **游戏枚举表**（`EnumConstants`）：`SorterInstruction` / `PrinterInstruction` / `SlotClass` / `GasType` / `LogicSlotType` 等；未知 `Enum.Member` 原样输出。
- **数学常量表**（`RawConstants`）：`pi` / `deg2rad` / `rad2deg` / `epsilon`，原样输出。
- **prefab hash 表**（可选）：常用设备类型名 → CRC-32，支持 `hash("...")` 之外的直接名字。

表的来源可以是手工整理，或后续从游戏数据生成。所有表均可独立更新以适配游戏版本。

---

## 9. 工具链

| 子命令 | 功能 |
|--------|------|
| `ic10c build <file.icg>` | 编译并输出 IC10 到 stdout |
| `ic10c run <file.icg>` | 编译并在内置 VM 中运行（`--steps`/`--set`/`--trace`） |
| `ic10c stats <file.icg>` | 行/字节/寄存器预算报告 |
| `ic10c size <file.icg>` | 按函数拆分行预算（体积剖析） |
| `ic10c fmt [-w] <file.icg>` | 格式化源码（保留注释、`const`/`var` 分组、空行、`data` 表与 `switch ... table`；`-w` 原地写回） |
| `ic10c disasm <file.ic>` | 反汇编旧 `.ic`（解析跳转目标为标签） |
| `ic10c decompile [-s] [-o out.icg] <file.ic>` | 反编译为 `.icg`；`-s` 尝试结构化 |
| `ic10c minify [-w] [-o out] <file.ic>` | 压缩现有 IC10 行数（去注释/内联 define/去标签/去不可达） |
| `ic10c lex <file.icg>` | 打印词法单元（调试） |
| `ic10c ast <file.icg>` | 打印 AST（调试） |
| `ic10c lsp` | 启动 LSP（stdio） |
| `ic10c help [command]` | 帮助（中英双语） |

环境变量：`IC10C_LANG`（输出语言）、`IC10C_NO_CHECK`（关闭 logic type 校验）、`IC10C_NO_OPT`（关闭优化，调试用）、`IC10C_NO_OUTLINE`（关闭函数外提）。

配套：

- VSCode 扩展（TextMate 语法高亮 + 无依赖 LSP 客户端），见 `editors/vscode`
- LSP：诊断、补全（逻辑类型/内建函数/设备端口/预制体，含按调用参数位置的上下文补全）

---

## 10. 目录结构

```
ic10go/
  go.mod
  README.md
  QUICKSTART.md
  docs/
    spec.md
    architecture.md
    target-ic10.md
    data-segment.md
  cmd/ic10c/
    main.go
  internal/
    source/       // 位置、文件、注释
    diag/         // 诊断
    token/
    lexer/
    ast/          // AST + 打印机/格式化
    parser/
    sema/         // 名字解析、常量求值、函数体类型检查
    ir/           // 指令、块、CFG、终结符接口、校验器、共享分析
    lower/        // AST → IR、内联 / 外提
    opt/          // 优化通道
    regalloc/     // 图着色 + 拷贝合并 + 溢出
    codegen/      // 指令选择、布局、绝对行号、限额校验
    builtin/      // logic type / 内建函数 / CRC-32
    ic10asm/      // IC10 文本共享词法（分词/分支/标签）
    disasm/       // IC10 注释清单
    decomp/       // IC10 → .icg 反编译（含结构化）
    lsp/          // 语言服务器
    vm/           // 测试用最小解释器
  pkg/ic10/       // 公开 API（Compile/Format/StatsOf/DataLoader/DataStats/MaxStackDepth）
  experiments/    // 数据段原型与真机测试脚本
  editors/vscode/ // VSCode 扩展
  testdata/
    golden/       // 源 → 期望 IC10
    programs/     // 端到端 .icg 程序
    ic10/         // 真实 .ic 脚本（反编译测试）
```

---

## 11. 里程碑（M0–M6 已实现）

### M0 骨架 ✅
- `go.mod`、`cmd/ic10c` 骨架
- token / lexer / ast / parser（表达式与基础语句）
- 诊断基础设施

### M1 单函数编译 ✅
- `const` / `var` / `:=`
- 算术、比较、位运算
- `if` / `for` / `switch` / `break` / `continue` / `return`
- 设备属性读写、`yield` / `sleep`
- 活跃性分析 + 图着色（Chaitin-Briggs）+ 拷贝合并 + 溢出
- 逆后序布局、绝对行号回填
- 128 行 / 4KiB / 90 字符校验

### M2 优化器 ✅
- 块内拷贝/常量传播、全局常量传播、常量折叠、代数化简
- 冗余设备/槽位/批量读消除（含跨块 CSE、存储转发）、常量分支折叠
- 循环不变量外提（含设备读）、尾块合并、死存储消除
- DCE、全局 CSE（可用表达式）、比较-分支融合、`select` 化、逻辑化简
- 内联 / 外提（按体积取短）、常量实参调用点内联折叠
- 便利语法：`for i := range n` / `for i, v := range Table`、`case lo..hi` 区间、`if`/`switch` 初始化、带标签的 `break`/`continue`
- 单指令比较：与常量 0 比较用 `s*z`/`b*z`；近似比较在条件中融合为 `bap/bna/bapz/bnaz`；`if cond { call L }` 融合为 `b<cond>al`
- 架构加固：终结符统一接口、`ir.Verify` 校验器、`DefUse/Liveness/Dominators` 共享分析、pass 注册表 + 收敛诊断、内建语义单一事实来源（`builtin.Sem`）
- ⏳ 未实现（暂缓）：大小模型与 `define` 决策、强度削弱

### M3 领域特性 ✅
- 槽位 `ls/ss`、通道 `ChannelN`
- 批量 `lb/lbn/lbs/sb/sbn/sbs`
- 栈 `push/pop/peek/poke`、设备栈 `get/put/getd/putd/clr/clrById`（`get/put` 的 device 操作数接受端口 / id / 寄存器）、`dN.stack[addr]` / `id.stack[addr]` 语法糖
- 按 ReferenceId 读写 `readById`/`writeById`（`ld`/`sd`）、运行期端口槽位 `readDevSlot`/`writeDevSlot`（`ls/ss drN`）
- 设备栈指令构建器 `sorter.*` / `printer.*`（字段位宽校验）；未知 `Enum.Member` 原样输出、`raw("...")` 逃生口
- `approx/approxZero/notApprox/notApproxZero/logicalNor/isNotNaN`、`isSet`/`isUnset`/`rmap`/`readReagent`、NaN 支持、`ext/ins/sla/srl/rol/ror`
- 底层控制流：`label/goto/call/ret`、`ra/sp`、`ireg/setIreg`、`jump(expr)`
- 动态 logicType：`read(dev, lt)` / `write(dev, lt, v)`
- 动态设备寄存器：`readDev(idx, lt)` / `writeDev(idx, lt, v)`（IC10 `drN`）、`readReagent(idx, mode, key)`（IC10 `lr r? drN`）
- 相对跳转 `--rel-jump`（`jr`/`br*`，默认关闭）

### M4 工具链 ✅
- `fmt`（保留注释、分组、空行、`data` 表与 `switch ... table`；原生 `.ic`/`.ic10` 重排+列对齐）、`stats`、`disasm`
- `decompile`（IC10 → `.icg`，含 `-s` 结构化）
- `minify`（现有 IC10 → 更少行，语义不变：去注释/内联 define/去标签/去不可达）
- `graph`（控制流图 → Mermaid `flowchart`；默认**源码级**：AST 语句/条件/分支，按函数分组，`--level ir` 为 IR 基本块）
- `run`（编译后在内置 VM 中执行，`--steps`/`--set`/`--trace`）
- LSP（诊断 / 补全 / 格式化 / hover / 定义 / 预制体 / 原生 IC10）、VSCode 扩展

### M5 测试用最小解释器（VM）✅
- 主要用途是测试与开发验证，并经 `ic10c run` 暴露给用户调试
- 实现 IC10 指令解释、寄存器、栈、标签/行号
- mock 设备模型（可脚本化读写逻辑类型）
- 端到端测试：`.icg` → 编译 → VM 执行 → 断言设备状态
- 作为优化器的语义回归基准
- 健壮性：操作数 arity 与栈越界返回错误（不 panic）
- 保真：游戏常量（`pi`/`deg2rad`/…）、`LineNumber`、确定性 `rand`、`rmap`、位置无关的 `alias`/`define`、可选严格设备语义；详见 [`vm-improvements.md`](vm-improvements.md)

### M6 多芯片 ✅
- `chip 名字 { ... }`：一个源文件多块芯片，各编译成独立程序（各自 128 行 / 4 KiB 预算与 loader）
- 顶层 `const`/`data`/`func` 为公共区，chip 内声明可遮蔽；顶层 `main` 与 chip 块互斥
- `bus 名字 { 槽位 }` + 每 chip `use 名字 on dev:conn`：命名网络通道（可多连接跨 8 槽）
- `Bus.槽位` → `l/s <dev>:<conn> ChannelN`；唯一写者 / 被读却无写者 / 未绑定 / 超通道校验
- CLI 按芯片写文件、`--chip`、JSON `chips[]`、`stats` 分组；VM `World` 多芯片锁步并按 bus 自动接线
- LSP 按光标所在 chip 隔离补全/签名；VSCode 编译命令弹芯片选择
- 另：超预算时把一次性设置写入外提到 loader（`internal/opt/setup.go`）；同步新气体比例逻辑类型
- 详见 [`multichip.md`](multichip.md)

---

## 12. 风险与对策

| 风险 | 对策 |
|------|------|
| 128 行 + 4KiB 双约束导致内联爆行 | 内联 / 外提按体积取短、`size` 体积剖析、`stats` 报告 |
| IC10 操作数限制与分配冲突 | 分配器带约束；必要时引入临时寄存器 |
| 位运算是整数语义（double 的坑） | 规范明确，VM 覆盖测试 |
| `ra` / `sp` 管理错误 | 内联为主、外提仅限叶子函数；VM 回归 |
| 游戏版本导致哈希/枚举漂移 | 内建表独立、可更新 |
| NaN / 无穷语义 | 显式内建 `nan/pinf/ninf/isNaN`，VM 测试 |
| 无 VM 时语义验证弱 | M5 最小解释器作为强制测试手段 |

---

## 13. 开发约定

- 所有包提供单元测试；黄金文件放 `testdata/golden`。
- 优化通道必须可通过 flag 单独关闭，便于定位。
- 每次改动跑 `go test ./...` 与 `go vet ./...`。
- 不引入第三方运行时依赖；标准库优先。

---

## 14. 反编译器（`internal/decomp`）

IC10 → `.icg` 的翻译分两步：

1. **扁平翻译**（`Decompile`）：替换 `alias`/`define`，把 `r0..r15` 映射为同名变量（先读后写才发 `var`，其余首次赋值用 `:=`），控制流翻译为 `label`/`goto`/`call`/`ret`，`HASH()/STR()` → `hash()/str()`，`$hex`/`%bin` → `0x`/`0b`，不支持的指令告警并留注释。
   - **操作数校验**：按游戏元数据的操作数种类（`REGISTER`/`DEVICE`/`DEVICE_ID`/`VALUE`/`LOGIC_TYPE`…）检查每个非跳转操作数；未定义设备名、试剂、越界寄存器或非法字符不再生成非法 `.icg`，而是告警并注释掉该行。被注释的指令不参与寄存器声明。
   - **设备操作数**：`d?`/`db`、端口寄存器 `drN`、ReferenceId 寄存器 `r?` 分别映射到 `read`/`readDev`/`readById`（写同理），`sdse/sdns`、`rmap`、`clr` 接受寄存器/id。
   - **标签**：非法字符替换为 `_`，与 `.icg` 关键字冲突的（如 `return`、`for`）加 `_` 后缀；每个跳转目标都登记清理后的标签名，越界/负数目标作为程序末尾停机，未定义的标签目标告警并注释。
2. **结构化**（`DecompileStructured`，`-s`）：
   - 在**分支目标处切分基本块**（保证标签落在正确指令上）。
   - 计算**支配集**与**后支配集**，用最近公共后支配点（LCA）作为 if/else 汇合点。
   - 用 `regionClosed` 校验两分支构成单入口单出口区域；否则回退条件 goto。
   - 循环：回边识别 + 循环体；循环头是 `jal`/`-al` 目标（尾调用状态机）则不结构化；循环内回边到循环头时，只有闩锁块转为 `break`，内部块转为 `continue`。
   - 若无法保证每个基本块恰好发射一次，或产物无法编译，自动回退到扁平形式（`ic10.Decompile` 封装了该策略）。
   - 结构化仍是**尽力而为**：`knownStructuredUnsupported`（`pkg/ic10/corpus_test.go`）列出仍会结构化错误、因而只走扁平形式的脚本。

`ic10code/` 里的真实脚本（作者自有 + 本地保留的第三方脚本，后者缺失时自动跳过）都会做**反编译 → 重编译 → 设备写入序列对比**（`TestIc10CodeRoundTrip`，扁平与结构化各一遍）与 **minify 等价性**（`TestMinifyIc10Code`）。

---

## 15. 持久栈数据段

IC10 的栈是持久的（跨 tick、跨换代码保留），`.icg` 用它当**数据段**：

- **语法**：顶层 `data Name = [ ... ]`；元素为编译期常量（数字、`hash("...")`、
  游戏枚举名如 `LogicType.Open`，渲染成 IC10 字面量）。
- **寻址**：`Table[i]` → `get(db, base + i)`（`internal/lower`），索引可为变量。
- **布局**（`internal/sema.assignData`）：
  - `top`（默认）：数据段在栈顶 `base = 512 - size`，寄存器溢出从 `base-1` 向下；
  - `middle`：数据段固定槽 `256`，溢出从 `511` 向下，重叠则报错。
  版本哨兵占数据段首槽（`--no-data-check`/`--unsafe` 时仍写入，仅 runtime 不校验）。
- **loader**：`internal/codegen` 之外由 `pkg/ic10.DataLoader` 生成，输出
  `put db addr value`（`--data-access stack` 时为 `poke addr value`）。
- **runtime 校验**：`lower.emitDataCheck` 读哨兵，与数据内容派生的版本比对，
  不符则 `jump(9999)` 停机。
- **`switch ... table` / `--auto-table`**：`internal/sema.buildTableSwitch` 把
  「常量 → 常量」的密集整数分支转成数据表，lower 为边界检查 + 查表。
- **冲突检查**：`pkg/ic10.MaxStackDepth` 在 CFG 上静态求 `push` 最大深度；
  `DataStats` 提示 `poke` 越界；`ic10c stats` 汇总。
- 细节与真机验证见 [`data-segment.md`](data-segment.md) 与
  `experiments/data-segment/`。
