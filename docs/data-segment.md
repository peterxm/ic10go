# 持久栈数据段

> 状态：**已实现**（语法、CLI、VSCode、真机验证均完成）
> 目标：利用 IC10 芯片**持久栈**，把大块常量 / 查表移出 128 行程序预算，
> 让 `.icg` 在行数受限时仍能表达大数据。

---

## 1. 背景与动机

IC10 的程序上限是 **128 行 / 4 KiB**，这是 `.icg` 编译器设计的核心约束。
当前编译器已经做了很多行数优化（寄存器复用、常量内联、CSE、分支融合、
循环不变量外提、尾块合并、寄存器溢出到宿主栈、函数内联/外提等），但仍有两类内容天然占行数：

1. **大查表**：例如一个 17 路 `switch` 查表脚本
   （矿石编号 → 显示哈希 + 比热），展开成比较链要 ~85 行。
2. **大块常量数据**：hash 表、字符串表、配置表等。

而 IC10 有一条被低估的特性：**栈是持久的**。游戏参考文档（第三方资料，仅本地，第 168 行）明确写道：

> 栈内存在逻辑芯片上是**持久**的：即使推送值的代码被移除，栈仍保留那些值。

于是可以把“写数据”的代码与“用数据”的代码分开：

- 先运行一段 **loader**，把数据写进芯片栈；
- 再把芯片里的代码换成 **runtime**（真正运行的程序）；
- 数据仍在栈里，runtime 直接用 `get(db, addr)` 读取。

这样 loader 的指令**不计入 runtime 的 128 行**，相当于给 IC10 增加了一个
**持久数据段 / 常量池**。

---

## 2. 方案概述

编译器新增一种“数据段”输出模式，一次编译产出两段代码：

| 产物 | 作用 | 何时运行 |
|------|------|----------|
| `main.data.ic` | 装载器：把数据表和版本哨兵写入栈 | 安装时运行一次 |
| `main.ic` | 运行时程序：正常逻辑，从栈读数据 | 之后一直运行 |

工作流：

```
1. 把 main.data.ic 贴进 IC 编辑器并运行（写入栈）
2. 把 main.ic 贴进同一个 IC（替换掉 loader）
3. 完成——数据仍在栈中
```

---

## 3. 优势

1. **直接省行数**：大查表从几十行降到几次 `get`。以 17 路表为例，
   loader 里的 17 条数据写入不计入 runtime，runtime 只留索引与读取。
2. **持久**：跨 tick、跨代码替换保留，不需要每轮重建。
3. **免设备、免通道**：不占用设备的可写属性，也不受网络通道
   “每网络 8 个且易失”的限制。
4. **容量大**：每芯片 512 个 float，足以容纳常见查表 / 配置 / hash 表。
5. **与寄存器溢出正交**：溢出从数据段下方（`base-1`）向下增长，编译器自动避让
   （`AllocateReserved`），互不干扰。
6. **可测试**：VM 已有 `Device.Stack` 与 `deviceByID`，只需增加“预置栈”
   与“两段流程”的夹具。

---

## 4. 难点与风险

1. **两步流程易错**
   必须先装数据再跑主程序；忘记安装、装了旧版本、或中途 `clr db` 都会
   读到垃圾。需要工具链（CLI 子命令、VSCode 命令）与清晰文档。

2. **持久性前提需在真机核实**
   掉电、拔芯片、游戏版本 / 存档变更、手动清栈都会丢数据。runtime 必须能
   检测“数据缺失 / 版本不符”。

3. **地址分区冲突**（已解决，见 §5.5）
   同一块栈上有三方：
   - call stack：`sp` 从 0 向上涨（`push`/`pop`/`poke`/`peek`）；
   - spill：编译器溢出从数据段下方 **向下**（`internal/regalloc/regalloc.go:34`）；
   - data 段：默认放栈顶 `512-size`，用 `get(db, addr)` **直接寻址**（不经过 `sp`）。

   `ic10c stats` 把栈分为用户区/编译器区并分别报告用量（见 §5.5）；用户绝对
   地址越界或 `push` 深度超限会编译报错。

4. **收益只在大表**
   IC10 常量本来就能作为立即数内联，小常量不占行。只有**动态索引的表**
   （`get(db, base+idx)`）或大块数据才真正省行数。需要明确的适用边界。

5. **loader 自身也受 128 行限制**（已解决）
   loader 超过 128 行时自动拆成多块（每块 ≤128 行），按序运行（见 §5.6）；
   数据段上限随之提升到编译器区大小。

6. **编译器复杂度**
   需要：数据段 IR、地址分配、loader 代码生成、runtime 读取代码生成，
   以及“哪些常量应进入数据段”的启发式（例如大 `switch` 自动表化）。

7. **测试基建**
   现有 VM 是单机、单栈；需要能**预置栈内容**并模拟“先 loader 后 runtime”
   的测试流程。

8. **可调试性**
   runtime 依赖程序外部状态（栈），出错难定位。需要哨兵与明确的失败行为
   （halt 或报警）。

9. **每张芯片各自安装**
   数据不随代码复制到其它芯片。

10. **优化器安全性**（已增强）
    常量索引的数据读 `T[const]` 直接内联为字面量；其余数据读走 `get`/`peek`，
    已纳入 `redundantLoads`/`globalCSE` 的 CSE，并按地址范围精确失效（写别的
    槽不会清掉缓存，`yield`/`sleep` 保留栈读）。写栈指令（`put`/`putd`/`clr`）
    在 `hasSideEffect` 中标注，避免被误删/重排（`internal/opt/opt.go`）。

---

## 5. 设计

### 5.1 语言层（已实现，语法见 §9）

两种写法都已落地：

```go
// A：显式 data 表
data Recipe = [ -1301215609, 0.0095, ... ]

// B：给 switch 加 table 标记，由编译器自动表化
switch ore table {
case 1: db.Setting = -1301215609; heat = 0.0095
...
}
```

数据表支持：编译期常量元素（数字、`hash("...")`、游戏枚举名）、固定长度、
只读索引 `Recipe[i]`。

### 5.2 编译输出

```
ic10c build main.icg
  -> main.ic          # runtime 到 stdout（≤128 行）
  -> main.data.ic     # loader 自动写到文件（≤128 行，装一次）
  # loader 超过 128 行时自动拆成 main.data.1.ic、main.data.2.ic…（按序运行）
```

实现采用 `--data-out`（runtime 到 stdout、loader 到文件），需要 loader 时**自动
输出**（`--split-data` 兼容保留）；VSCode 的“编译为 IC10”命令自动处理（复制安装
代码 + 预览运行代码）。多芯片时按芯片各写 `<file>.<chip>.ic` 与
`<file>.<chip>.data.ic`，`--chip NAME` 只输出一个（见 [`multichip.md`](multichip.md)）。

### 5.3 寻址与代码生成

- loader：`poke addr value`（本地栈）或 `put db addr value`（设备栈）。
- runtime：优先 `get(db, base+idx)`（**单指令直接寻址**，不碰 `sp`）。
- 固定常量索引可在编译期折叠为具体地址。

### 5.4 版本哨兵（已实现）

- loader 在数据段首槽 `base` 写入版本号；版本由表内容派生（CRC-32，见
  `sema.dataVersion`），所以表一变版本就变，无需手动维护。
- runtime 启动时读该槽并比对，不符则 `jump(9999)` 停机：

  ```ic
  get r0 db <base>
  sne r0 r0 <version>
  beqz r0 4
  j 9999
  ```

- `--no-data-check` / `--unsafe` 跳过校验（哨兵仍写入）。

### 5.5 地址分区（已实现）

真机已确认本地栈与 `db` 栈是**同一块内存**，所以数据段与 `sp` 增长区、寄存器
溢出区共享同一个 512 槽空间。编译器把它显式分成两个区：

- **用户栈** `[0, userLimit-1]`：用户代码可用 `push/pop`、`peek/poke`、
  `db.stack[addr]`、`get/put(db, addr)` 访问。
- **编译器栈** `[userLimit, 511]`：数据段（栈顶）与寄存器溢出槽（数据段下方）。

用户上限默认**固定**为 `--user-stack N`（默认 128，环境变量 `IC10C_USER_STACK`，
VS Code `icg.userStack`）。加 `--dynamic-stack`（环境变量 `IC10C_DYNAMIC_STACK`，
VS Code `icg.dynamicStack`，默认关）改为动态：`userLimit = 512 - size - spills`
（`middle` 布局为 `256`）。

| 布局 | data 段 | 寄存器溢出 | 动态边界 |
|------|---------|------------|----------|
| `top`（默认） | `[512-size, 511]` | 从 `512-size-1` 向下 | `512 - size - spills` |
| `middle` | `[256, 256+size-1]` | 从 `511` 向下 | `256` |

- 数据段首槽是版本哨兵，其后各表依次排布。
- 用户绝对地址（`db.stack[N]`、`poke(N)`、`get/put(db, N)`）落在用户上限及以上
  时**编译报错**；`push` 深度超过上限同样报错；固定模式下数据段 + 溢出放不下
  编译器区也报错。动态栈地址（如 `get(db, r0)`）无法静态验证，只在 `stats`
  报告中标出，不报错。
- `ic10c stats` 分两行输出用户/编译器用量（`stack user` 分子是用户用到的
  槽位**个数**）：
  ```
  stack user   1 / 128 (fixed, max slot 0)
  stack comp  13 @ [499..511] (data 13 + spills 0)
  ```
- 布局细节见 `docs/target-ic10.md` §5.7。

**栈私有 pragma**：用户区的“删/并写入”类优化（常量用户槽提升为寄存器、
成对 `push/pop` 消除、放宽死存储消除）默认在**用户栈可能被后继程序读取**时
关闭。用文件 pragma 声明用户栈只属于本程序：

```go
// icg: private-stack   // 用户栈私有：允许常量用户槽提升为寄存器、消除成对 push/pop
// icg: shared-stack    // 用户栈可能与后继程序共享，保持保守
```

- 默认：**单芯片 → `private-stack`**，含 `chip` 块的多芯片 → `shared-stack`；
  pragma 优先于默认。
- 是**文件级**声明（编译器扫描整份源码取第一个匹配），多芯片时对所有 chip 生效。
- 只影响**芯片自己的持久栈**（`db` / 本地栈）上的“删/并写入”类优化；设备栈
  `d0.stack[...]` 属于共享设备，始终按可观测处理，不受本 pragma 影响。
- 这些优化只在 runtime 行数不增时才采用（编译器比较两种产物）。
- 详见 [`spec.md` §4.6](spec.md#46-栈私有-pragma) 与
  [`architecture.md` §6.8](architecture.md#68-栈私有标记)。

### 5.6 工具链

- `ic10c build --split-data`（兼容保留；现在需要 loader 时会自动输出）
- `ic10c build --data-only`（只生成 / 更新 loader）
- `ic10c run`：在 VM 里**自动先跑一次性 loader**（数据段 + 外提设置）再跑
  runtime，所以数据段程序也能直接 `run`（真机仍需先手动装一次 loader）。
- **Loader 拆分**：loader 超过 128 行时自动切成多块（每块 ≤128 行），
  必须按顺序运行。单块写 `<file>.data.ic`；多块写 `<file>.data.1.ic`、
  `<file>.data.2.ic`…；JSON 里是 `data.loaders[]`。这样数据段可以超过
  128 槽（上限是编译器区大小）。
- VSCode：“IC10 Go: 编译为 IC10” → 一个命令完成：把安装代码复制到
  剪贴板、在旁边预览运行代码，并提示“先跑安装代码、再用运行代码覆盖”；
  多块时逐块复制并提示继续。
- 文档：安装步骤、版本升级、失败行为。

### 5.7 一次性设置外提（复用 loader）

编译器会把**序言里的一次性设备写入**外提到 loader。触发条件：

- runtime 会**超过 128 行**：外提以塞进预算（否则报超行）；或
- 程序**本来就需要 loader**（有 `data` 表）：外提是“顺带”的，不增加安装步骤，
  直接让 runtime 更小。

判定：

- 所在块**不在任何循环内**，且**支配某个循环头**（每次到达循环前恰好执行一次）；
  用支配关系而非直线扫描，所以能跨过数据段哨兵检查这类条件序言。
- 指令是**操作数全为编译期常量**的设备写（`Store`/`StoreSlot`/`StoreDyn`/`Batch`
  的 store 形式）。
- 典型内容：`d0.Mode = …`、`d0.On = 1`、常量 `d4.Setting = …`、
  `batch.writeName(hash, hash("LED"), Mode, DisplayMode.Percent)`。

依据：设备状态持久（同数据段），这些写入只需安装时执行一次。

产物：runtime（≤128 行，去掉外提的写）+ loader（数据段写入 + 外提的设置写入）。
CLI 自动写出 loader（`<file>.data.ic`），`--data-only` 也会带上设置写入；
JSON 接口给出 `data.setup = true`。合并后的 loader 超过 128 行时自动拆成多块
（见 §5.6）。

实现：`internal/opt/setup.go` 的 `SplitSetup`；`pkg/ic10.CompileResult` 在
`generate` 失败（超行/字节/行宽）时、或 `info.DataSize > 0` 时调用它；随后把
「拆分」与「不拆分」两种 runtime 都生成，由候选比较取**行数更短者**，所以拆分
只会减小 runtime。

**开关**：目前**没有**开关，外提是全自动、不可关闭的（现有开关只有 `--unsafe`、
`--auto-table`、`--spill`、`--data-layout`、`--data-access`、`--dynamic-stack`、
`--user-stack`、`--redundant-device-writes`、`--rel-jump`，环境变量
`IC10C_NO_CHECK`/`IC10C_NO_OUTLINE`/`IC10C_NO_OPT`，均与 setup 外提无关）。

> 语义前提：这些常量写被当作**一次性初始化**（设备状态持久）。若循环会改该
> 设备、而序言想每 tick 复位它，外提后就不再每 tick 复位；这种情况目前只能靠
> 改代码避免。

> 没有 `data` 表且 runtime 放得下时**不拆分**：避免无谓的两步安装流程。

---

## 6. 与“跨芯片栈共享内存”方案对比

| 维度 | 跨芯片栈 | 持久栈数据段（本方案） |
|------|----------|------------------------|
| 主要收益 | 通信 / 共享内存 | **省行数 / 扩容量** |
| 与 128 行目标契合 | 弱 | **强** |
| 复杂度 | 高（协议 / 竞态 / 调度） | 中（流程 / 分区 / 哨兵） |
| 通用性 | 中 | 高 |
| 依赖 | 多芯片、接线 / ReferenceId | 单芯片、栈持久性 |

两者可共存：数据段做常量池，跨芯片栈做运行时通信。

---

## 7. 待决问题

1. ~~`db` 栈与本地栈是否同一块内存？~~ **已确认（标准 IC host）：是同一块。**
   真机测试：`poke 50 12345` 后 `get db 50` 读到 `12345`；
   `put db 60 54321` 后本地 `peek 60` 读到 `54321`。
   VM 已同步修正（`internal/vm`：`db.Stack` 与 `m.Stack` 共用底层数组）。
2. ~~替换芯片代码时栈是否保留？~~ **已确认：保留。** 先运行写入程序
   （`1_store.ic`），再整段替换成读取程序（`2_read.ic`），仍读到
   `111/222/333`。
3. **宿主差异（重要）**：IC 芯片必须插在 **IC host** 上才能运行；标准 host
   提供 `d0–d5` 六个设备接口，`db` = 该 host（芯片栈）。
   部分设备自带 host（如空调），此时 `db` 指向**设备本身**
   （`db On = 0` 关闭空调）。**已确认真机不支持**：设备 host 下执行
   `put db 0 111` 报 `MemoryNotWriteable`。
   因此数据段方案**要求标准 IC host**；若要兼容设备 host，需改用本地栈
   （`poke`/`peek`，见 §11 的 `--data-access stack`）。
4. `get(db, addr)` 的越界 / 未初始化行为（未在真机单独验证；loader 未跑时读到 0）。
5. 数据段大小的上限与 loader 分块策略：**已实现自动分块**（loader 超 128 行
   拆成多块按序运行，见 §5.6），数据段上限为编译器区大小
   （`512 - userLimit - 溢出槽`；默认 `userLimit=128` → 384）。
6. ~~哪些常量应自动进入数据段（启发式）~~ **已实现**：`--auto-table`
   （密集整数、≥5 case、纯常量赋值、表 ≤64）。
7. ~~CLI / VSCode 的具体交互~~ **已完成**：`--split-data`/`--data-only`/
   `--data-access`/`--data-layout`/`--unsafe`/`--auto-table` + VSCode
   “编译为 IC10”命令（自动复制安装代码 + 预览运行代码）与 `icg.autoTable` 设置。

---

## 8. 最小验证原型

已在 [`experiments/data-segment/`](../experiments/data-segment/) 完成手工验证
（不改语言），用一个现成的 17 路查表脚本（第三方，本地原型文件未随仓库分发）：

1. 手工拆成 loader（写表 + 哨兵）与 runtime（`get(db, base+idx)`）。
2. 量化结果：

   | 产物 | 行数 |
   |------|------|
   | 原合并端口（loader 内联） | 94 |
   | 拆出的 **runtime** | **60** |
   | 拆出的 **loader**（只跑一次） | 35 |

   runtime **省下 34 行**，剩余预算 34 → 68 行。

3. 闭环测试（`go test ./experiments/data-segment/`）确认：loader 写入后
   `db.Stack[0]` 为 Iron 哈希、哨兵为 1；runtime 在预置栈上读到 Gold 哈希并
   写出 `db.Setting == 226410516`；不跑 loader 时读到 0。

结论：**VM 能预置栈、跨程序 `get(db, addr)` 读取可行、行数收益明显。**

### 真机验证（d0 = LED Display，标准 IC host）

[`experiments/data-segment/ingame/`](../experiments/data-segment/ingame/) 提供 6 段
可粘贴脚本，实测结果与预期**完全一致**：

| 测试 | 步骤 | 预期 / 实测 |
|------|------|-------------|
| A 栈持久 | `1_store.ic` → 覆盖为 `2_read.ic` | 循环 `111/222/333` ✅ |
| B 本地栈 → db | `3_poke.ic` → `4_get.ic` | `12345` ✅（同一块） |
| C db → 本地栈 | `5_put.ic` → `6_peek.ic` | `54321` ✅（同一块） |

即：**栈跨换代码保留，且 `db` 栈 == 本地栈**。数据段方案的核心前提成立。

### 编译器生成的 loader/runtime 真机验证

[`experiments/data-segment/ingame-data/`](../experiments/data-segment/ingame-data/)
用 `data T = [111, 222, 333]` 生成两段并真机运行：

| 步骤 | 内容 | 结果 |
|------|------|------|
| 1 | `1_loader.ic`（`put db 508 <version>` + 509..511 数据） | 运行成功 ✅ |
| 2 | 覆盖为 `2_runtime.ic`（校验哨兵 + `get(db, 509+i)`） | LED 循环 `111/222/333` ✅ |

即编译器生成的两段流程在真机上工作正常。

`switch ... table` 自动表化同样通过真机验证
（[`experiments/data-segment/ingame-switch/`](../experiments/data-segment/ingame-switch/)）：
边界检查 + 自动生成的表布局在标准 IC host 上循环 `111/222/333` 正常。

`--data-access stack`
（[`experiments/data-segment/ingame-stack/`](../experiments/data-segment/ingame-stack/)）
在**标准 IC host 与设备 host（空调）**上分别验证：loader（`poke`）→
runtime（`peek` + `sp` 保存/恢复）都正常循环 `111/222/333`，宿主兼容问题闭环。

### 栈分区（已实现）

栈分为用户区与编译器区（见 §5.5）：编译器区放数据段与溢出槽，用户区放
`push/pop`、`peek/poke`、`db.stack[N]`、`get/put(db, N)`。

- ✅ **绝对地址检查**：用户常量栈地址达到用户上限（`>= userLimit`，默认 128）时
  编译报错；`push` 深度超过上限同样报错。`--dynamic-stack` 时上限为
  `compilerBase = 512 - size - spills`。
- ✅ **报告**：`ic10c stats` / VSCode 状态栏分两行显示 `stack user` 与
  `stack comp`（数据段 + 溢出槽）。
- ⚠️ **动态地址与无界 push**：IC10 程序常用 `get(db, r0)` 与循环内 `push`，
  无法静态验证，只在报告中标记（`stack user unbounded`），不作为编译错误。
- ✅ **溢出避让**：`AllocateReserved` 从数据段下方向下分配；`--data-layout
  middle` 把数据段放在固定槽 `256`（`sema.FixedDataBase`），溢出若会碰到数据段
  则编译报错。默认仍是 `top`（数据段在栈顶、溢出在其下方）。


---

## 9. 语法（已实现）

顶层新增 `data` 表（编译期常量数组），由编译器分配到持久栈：

```go
// 元素只允许编译期常量：数字、hash("...")、游戏枚举名（如 LogicType.Open）
data RecipeDisplay = [
    -1301215609,   // 1 Iron
    -404336834,    // 2 Copper
    226410516,     // 3 Gold
    // ...
]

data RecipeHeat = [
    0.00950100000010010000,  // 1 Iron
    0.00950100000100010000,  // 2 Copper
    // ...
]
```

使用：

```go
func main() {
    // ...
    db.Setting = RecipeDisplay[ore-1]   // 编译为 get(db, baseDisplay + ore - 1)
    heat = RecipeHeat[ore-1]            // 编译为 get(db, baseHeat + ore - 1)
}
```

规则：

- 表名是编译期符号，不占运行期寄存器；索引 `Table[i]` 读栈（1 条 `get`）。
- 索引可为变量；越界不检查（与 IC10 一致），由使用者保证。
- 表**只读**：不允许 `Table[i] = x`（v1 不支持写回数据段）。
- 多表各自分配 base，编译期确定。
- 可选：编译器自动保留一个哨兵槽存数据版本（默认由数据内容派生 CRC），
  runtime 启动时校验；可用 `--no-data-check` 关闭。

> `switch ... table`（§9.1）已实现该自动表化；`--auto-table` 还能自动识别
> 满足条件的普通 `switch`。

### 9.1 实现状态

- ✅ `data` 关键字、AST、parser、sema 常量求值。
- ✅ `Table[i]` → `get(db, base+i)`（`internal/lower`）。
- ✅ 地址分配：默认 `top` 布局，数据段放**栈顶**（`base = 512 - size`），溢出从
  `base-1` 向下；`--data-layout middle` 则放固定槽 `256`，高地址留给溢出，
  溢出与数据段重叠时报错。
- ✅ 版本哨兵：loader 写 `put db <base> <version>`；runtime 校验失败用
  `jump(9999)` 停机（`--no-data-check` 可关）。
- ✅ loader 生成：`ic10.DataLoader` / `ic10c build --data-only`。
- ✅ `switch tag table { ... }` 自动表化：常量 → 常量、连续整数 case、
  各 case 目标一致时，编译器为每个目标生成一张表，lower 为边界检查 +
  `get(db, base + tag - lo)`；可选 `default` 处理越界。
- ✅ `--data-access stack`：loader 用 `poke`，runtime 用 `peek` + `sp` 保存/恢复，
  兼容设备 host（代价是每次读取多 4 条指令）。
- ✅ `--auto-table`（默认关闭）：自动表化满足条件的普通 `switch`（密集整数、
  ≥5 个 case、纯常量赋值、表 ≤64），并对每处表化给出“需先安装 loader”的警告。
- ✅ `data` 表元素支持游戏枚举名（如 `LogicType.Open`），loader 原样写入。
  示例：本地的 airlock 端口用 `data LogicTable` 存逻辑类型（该第三方端口未随
  仓库分发），省去运行时 `poke`。

真机验证：标准 IC host 下 loader → runtime 读表成功；设备 host（空调）
`put db` 报 `MemoryNotWriteable`（见 §11）。

---

## 10. CLI（已实现）

```
ic10c build [flags] <file.icg>

数据段 / loader 相关 flags：
  --split-data           兼容保留（loader 现在需要时会自动输出）
  --data-out <file>      loader 输出路径（默认 <file>.data.ic）
  --data-only            只输出一次性 loader（数据段 + 外提设置；多芯片需 --chip）
  --chip <name>          多芯片：只输出指定芯片（见 multichip.md）
  --no-data-check        不在 runtime 插入版本校验
  --unsafe               不安全：跳过运行时数据校验以进一步压缩代码（会打印警告）
  --data-access <mode>   读取方式：
                           get   （默认）get/put db，需标准 IC host
                           stack poke/peek + sp 保存/恢复，兼容设备 host，较慢
  --data-layout <mode>   数据段位置：
                           top    （默认）栈顶，溢出在其下方
                           middle 固定槽 256，高地址留给溢出
  --auto-table           自动把符合条件的普通 switch 表化（默认关闭；每处会警告）
```

`ic10c stats` 也接受 `--data-layout`、`--dynamic-stack`、`--user-stack`、
`--redundant-device-writes`，并输出数据段范围、用户/编译器栈用量（`stack user`/
`stack comp`）与冲突警告。

示例：

```bash
# 无 data 表时行为不变
ic10c build main.icg

# 有 data 表：runtime 到 stdout，loader 自动写 main.data.ic
ic10c build main.icg > main.ic

# 数据变更后只重装 loader
ic10c build --data-only main.icg > main.data.ic
```

VSCode 扩展：

- 命令 **“IC10 Go: 编译为 IC10”**：若文件含 `data` 表，编译后把安装代码
  复制到剪贴板、在旁边预览运行代码，并提示“先贴入运行安装代码，再用运行
  代码覆盖”。
- 当当前文件含 `data` 表时，状态栏/诊断提示“需要先安装数据段”。

---

## 11. 宿主与兼容

| 宿主 | `db` 指向 | `get/put db` | 数据段可用性 |
|------|-----------|--------------|--------------|
| 标准 IC host | 芯片自身栈 | 可用（真机已验证） | ✅ `--data-access get`（默认） |
| 设备 host（空调等） | 设备本身 | `MemoryNotWriteable` | ✅ `--data-access stack` |

- 默认 `--data-access get` **要求标准 IC host**；设备 host 会报
  `MemoryNotWriteable`（真机确认）。
- `--data-access stack` 用本地 `poke`（loader）/`peek`（runtime，读取任意地址
  先保存 `sp`、把 `sp` 指到 `addr+1`、`peek`、再恢复 `sp`），**兼容设备 host**，
  代价是每次读取多 4 条指令。loader 与 runtime 必须用同一 `--data-access` 编译。
  已在**标准 IC host 与设备 host（空调）**上分别真机验证通过。

---

## 12. 参考

- `Stationeers_IC10_参考文档.md`（第三方资料，仅本地）：栈内存（第 152–168 行）、
  栈遍历、内部栈编程。
- `docs/spec.md` 7.7 设备栈 / 按 id、8.3 栈。
- `internal/regalloc/regalloc.go:34`：溢出槽从 `511 - reserved` 向下。
- `internal/builtin/builtin.go:161-165`：`get/put/getd/putd/clr`。
- `internal/opt/opt.go:133`：`hasSideEffect`（含 `push/pop/poke/put/putd/clr`）。
