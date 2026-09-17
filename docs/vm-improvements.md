# 测试用 IC10 虚拟机（`internal/vm`）改进计划

> 定位：`internal/vm` 是**测试用**的最小 IC10 解释器（同时通过 `ic10c run` 暴露给
> 用户调试）。本轮目标是**更准、更稳**，不改变其「测试用」定位。
>
> 本文记录现状、发现的问题与分阶段计划。P1–P3 已实现，P4–P5 为待办。

---

## 1. 现状

- 入口：`internal/vm/vm.go`（约 1100 行）；`ic10c run` 在 `cmd/ic10c/main.go`。
- 模型：16 个内部寄存器 + `ra`/`sp`、512 槽栈、`d0..d5`/`db` 设备端口、槽位、
  设备栈、试剂、批量读写、通道（以 `d0:0` 命名的伪设备）。
- 多芯片：`World` 同 tick 锁步运行多块芯片，设备按名共享（同一 `dev:conn` 的
  `ChannelN` 互通），`Wire`/`WireBus` 把不同访问点接成同一条网；每芯片保留自己的
  寄存器与栈（`db` 私有）。`ic10c run` 按编译出的 bus 绑定自动接线。
- 指令：编译器可能产出的全部指令 + 社区 `.ic` 语料用到的全部指令（分支经
  `internal/ic10asm.BranchInfo` 动态分发）。
- 用途：`pkg/ic10` 的端到端语义测试、差分测试、`ic10code/` 反编译重编译对比、
  `ic10c run`。

### 已与 emulator 对齐、无需改

- `round`（half-away-from-zero，对应 emulator `f64::round`）。
- `sap` / `sna` / `sapz` / `snaz` 的容差公式（`max(..., ε*8)`）。
- `sll` / `sla` / `sra` / `srl` / `rol` / `ror`、`ext` / `ins`。
- 空批量聚合：`Average`→NaN、`Maximum`→`ninf`、其余→0。
- `alias` / `define` 与位置无关：先收集全部符号再解析操作数，定义在引用之后也能生效（单遍扫描会把未解析的符号误建成同名设备/寄存器）。

---

## 2. 发现的问题

### 2.1 会 panic / 结果错

| # | 问题 | 位置 |
|---|------|------|
| A1 | 越界栈访问直接 panic（`poke 600 1` → `index out of range`） | `execOp` 栈/设备栈各 case |
| A2 | 操作数缺项 panic（各 case 直接 `a[1]`/`a[2]`，无 arity 检查） | `execOp` / `execBranch` |
| A3 | `pi`/`deg2rad`/`rad2deg`/`epsilon` 解析为 0（`Parse` 未灌 `RawConstants`） | `Parse` |
| A4 | `rand` 恒为 0 | `execOp` `rand` |
| A5 | `rmap` 恒为 0 | `execOp` `rmap` |

### 2.2 保真度

| # | 问题 |
|---|------|
| B1 | `LogicType.LineNumber`（含槽位 `LineNumber`）未建模，真机返回当前行号 |
| B2 | `l/s/lr/ls/ss` 对未连接设备自动建并返回 0；真机抛 `DeviceNotFound` |
| B3 | `deviceByID` 找不到时回退 `db`；真机抛 `UnknownDeviceID` |
| B4 | 无 128 行/tick 预算与时钟模型；`Time` 逻辑类型未建模 |
| B5 | 通道以 `d0:0` 伪设备表示，非真正的网络通道（`vm.World` 已让同一 `dev:conn` 的多芯片共享它；仍未建模"不同设备同网络"的拓扑） |

### 2.3 可观测性 / 工具

| # | 问题 |
|---|------|
| C1 | 只有 `OnWrite`，没有 `OnRead` |
| C2 | `ic10c run` 无 `--device name=hash`、槽位初始化、寄存器/栈 dump、`--json`、`--seed` |

### 2.4 测试强度

| # | 问题 |
|---|------|
| D1 | 差分测试只比逻辑写入（+ d0–d2 栈），不比槽位/寄存器/IC 栈/逐步轨迹 |
| D2 | 缺少 NaN/Inf/溢出/异常的 golden 用例 |

---

## 3. 路线图

### P1 健壮性（已实现）

- 操作数 **arity 校验**：`opArity` 表 + 分支按 `TargetIndex` 推导 arity，返回
  `ErrOperandCount` 而非越界 panic。
- **栈越界检查**：`stackAt` 统一边界判定，`poke`/`get`/`put`/`getd`/`putd` 及
  `push`/`pop`/`peek` 返回 `ErrStackOverflow`/`ErrStackUnderflow`。
- 错误 sentinel 便于测试断言：`ErrOperandCount`、`ErrStackOverflow`、
  `ErrStackUnderflow`、`ErrDeviceNotFound`、`ErrUnknownDeviceID`。

### P2 数值完整（已实现）

- `builtin.RawConstants` 由 `map[string]bool` 改为 `map[string]float64`
  （`pi`/`deg2rad`/`rad2deg`/`epsilon`），编译器仍**原样输出名字**（保真），
  VM 用数值解析。

### P3 保真（已实现）

- **`LineNumber`**：`Machine.SelfDevice`（默认 `db`）上的 `LineNumber`（含槽位）
  返回当前行号。
- **确定性 `rand`**：`Machine.Seed` + `SetSeed`，PCG，默认固定种子可复现。
- **`rmap`**：`Machine.Reagents`（试剂 hash → prefab hash），默认空→0。
- **严格设备语义（开关）**：`Machine.Strict`（默认 false，保持宽松）开启时
  `l/s/lr/ls/ss/ld/sd/get/put/...` 对未连接设备返回 `ErrDeviceNotFound`，
  `deviceByID` 找不到返回 `ErrUnknownDeviceID`。

### P4 工具链（待办，未选择）

- `Machine.OnRead` 钩子。
- `ic10c run`：
  - `--device name=hash[,namehash]` 注册可批量匹配的设备；
  - `--set d0.slot[0].On=1` 槽位初始化；
  - `--dump`（寄存器 / 栈 / PC / 时钟）；
  - `--json`（机器可读的设备状态，便于 CI）；
  - `--seed N`（随机种子）。
- `run` 在 VM 报错时打印失败行号/PC（当前错误已带行号，可再补 PC 与上下文）。

### P5 测试强化（待办，未选择）

- 差分测试对比扩展到**寄存器 / 槽位 / IC 栈**（定步或收敛点），并可选逐步轨迹。
- 新增边界 golden 用例：NaN/Inf 传播、除零、位运算边界、栈溢出/下溢、
  严格模式异常。
- 随机程序生成器覆盖 `get/put/clr`、`rand`、`rmap`、`LineNumber`。

---

## 4. 定位演进（待办，未选择）

当前定位是「测试用解释器」。若未来要升级为**一等公民模拟器**，需要：

1. 补齐 P4/P5，并把 `internal/vm` 提升为可对外引用的包（稳定 API + 版本化）。
2. 更完整的设备/网络模型：真实通道网络、`ReferenceId` 网络内解析、设备类型
   属性（`NoStore` 等从设备类型推导而非手工设置）。
3. 128 行/tick 预算、`Time`/`sleep` 时钟、自动 yield 的真实节奏。
4. 独立文档（`docs/vm.md`）与 `ic10c run` 的完整手册。
5. 与真机行为的差分测试（对照 emulator 或游戏内截图/日志）。

> 本轮**不做** P4/P5 与定位演进；它们在此登记，便于后续排期。

---

## 5. 影响面

- 主要改动：`internal/vm/vm.go`。
- 类型调整：`internal/builtin/builtin.go`（`RawConstants`）、
  `internal/lower/lower.go`（一处引用）、`internal/builtin/tables_test.go`。
- 不改变 `ic10c build` 输出。
- 默认行为保持宽松，现有测试不受影响；严格语义需显式开启。
