# IC10 目标后端说明

> 本文记录 `ic10c` 后端生成 IC10 代码时必须遵守的目标约束、指令映射与内建数据。
> IC10 完整指令参考为第三方资料（`Stationeers_IC10_参考文档.md`），仅本地保留、未随仓库分发。

---

## 1. 目标硬约束

| 约束 | 值 | 说明 |
|------|----|------|
| 最大行数 | 128 | 空行、标签行均计入 |
| 脚本大小 | 4096 字节 | 含所有字符 |
| 单行长度 | 90 字符 | |
| 寄存器 | `r0`–`r15` | 16 个通用 |
| 特殊寄存器 | `ra` | 返回地址，`jal` 写入 |
| 特殊寄存器 | `sp` | 栈指针，最多 512 值 |
| 设备端口 | `d0`–`d5` / `db` | |
| 执行 | 线性 | 每 128 行自动暂停 1 tick |

后端在 emit 前强制校验以上前三项，超限即编译失败。

---

## 2. 寄存器约定

| 寄存器 | 用途 |
|--------|------|
| `r0`–`r15` | 通用分配池 |
| `ra` | 返回地址（仅函数外提时使用） |
| `sp` | 栈指针（溢出/显式栈操作用） |

- 未使用外提时，编译产物不含 `jal` / `j ra`。
- 溢出使用固定栈地址 + `poke`；读取时按栈纪律处理 `sp`。
- 输出不生成 `alias`：直接使用 `r0` 等原名。

### 2.1 初始值与持久性

- **编译器保证先写后读**：寄存器分配器只在寄存器已有定义时才读取它，
  因此 `.icg` 源码无需手动初始化寄存器。
- 函数内 `var x`（不写初值）语义上等于 `0`。当 `x` 只在部分控制流路径被赋值
  时，编译器会在入口补一条 `move rX 0`，保证所有路径都有定义；若所有路径都
  赋值（如纯三元 `x = c ? a : b` 编译为 `select`），这条初始化会被消除。
- 真机 `r0`–`r15`：新芯片初值为 `0`，但**不要依赖**；**跨 tick 保留**，且
  **跨换代码（重新刷写）也保留**——实测（`v0.2.6428.27798`）把 `move r14 14`
  的程序运行后再刷回不写 `r14` 的版本，`r14` 仍是 `14`。芯片的寄存器与栈都随
  存档保存。此行为随游戏版本可能变动。
- 栈同样持久且容量 512，适合放大块数据（`data` 表、`push`/`poke`）。零散状态用
  寄存器也能跨刷写保留；但换新芯片会归 0，所以跨设备/跨新芯片的状态仍应放栈。
- `ic10c run` 的 VM 每次新建，寄存器与栈均从 `0` 开始，不跨运行保留。

---

## 3. 跳转与标签

- **不输出标签行**：标签行同样占用 128 行配额。
- 分支目标使用**绝对行号**，在布局阶段回填。
- 条件分支优先融合比较：

| 源 | 生成 |
|----|------|
| `if a < b { body }` | `bge a b <after>` + body |
| `if a <= b { body }` | `bgt a b <after>` + body |
| `if a == b { body }` | `bne a b <after>` + body |
| `if a != b { body }` | `beq a b <after>` + body |
| `for { body }` | `body` + `j <loop>` |
| `for c { body }` | `<loop>` + 反向条件跳出 + body + `j <loop>` |

> `--rel-jump` 把绝对跳转改为相对跳转（`jr` / `br*`），省字节；相对基准
> （相对当前行还是下一行）需真机验证，默认关闭。近似比较在条件中会融合为
> `bap/bna/bapz/bnaz`；`if cond { call L }` 会融合为单条 `b<cond>al`。

---

## 4. 指令映射表

### 4.1 工具 / 数学 / 位运算

| `.icg` | IC10 |
|--------|------|
| 赋值 | `move` |
| `+ - * / %` | `add sub mul div mod` |
| `abs sgn sqrt exp log pow ceil floor round trunc rand` | 同名指令 |
| `min max clamp lerp` | 同名指令 |
| `sin cos tan asin acos atan atan2` | 同名指令 |
| `& \| ^ ~` | `and or xor not` |
| `logicalNor(a, b)` | `nor` |
| `<< >>` | `sll sra` |
| `== != < <= > >=` | `seq sne slt sle sgt sge` |
| 与常量 0 比较 | 单操作数 `seqz/snez/sltz/slez/sgtz/sgez`（分支用 `beqz/bnez/bltz/blez/bgtz/bgez`） |
| `!` | `seqz` |
| `approx/notApprox/approxZero/notApproxZero`（作为条件） | `bap/bna/bapz/bnaz`（融合） |
| `c ? a : b` | `select` |
| `yield sleep hcf` | 同名指令 |
| `push pop peek poke` | 同名指令 |

### 4.2 设备 / 槽位 / 批量

| `.icg` | IC10 |
|--------|------|
| `d.X`（读） | `l r d X` |
| `d.X = v` | `s d X v` |
| `d.slot[i].X`（读） | `ls r d i X` |
| `d.slot[i].X = v` | `ss d i X v` |
| `d.channel[c][n]`（读） | `l r d:c Channel<n>` |
| `d.channel[c][n] = v` | `s d:c Channel<n> v` |
| `read(d, lt)` | `l r d (r_lt)` |
| `write(d, lt, v)` | `s d (r_lt) v` |
| `readDev(idx, lt)` | `l r drN (r_lt)` |
| `writeDev(idx, lt, v)` | `s drN (r_lt) v` |
| `batch.read` | `lb` |
| `batch.readName` | `lbn` |
| `batch.readSlot` | `lbs` |
| `batch.readNameSlot` | `lbns` |
| `batch.write` | `sb` |
| `batch.writeName` | `sbn` |
| `batch.writeSlot` | `sbs` |
| `isSet(d)` | `sdse` |
| `isUnset(d)` | `sdns` |
| `get / put / clr / rmap` | 同名指令 |
| `dN.stack[addr]` / `id.stack[addr]` | `get` / `put`（device 操作数接受端口 / id / 寄存器） |
| `getd(id, addr)` / `putd(id, addr, v)` | 统一 `get` / `put`（独立的 `getd`/`putd` 已弃用） |
| `clrById(id)` | `clrd` |
| `readById(id, lt)` / `writeById(id, lt, v)` | `ld` / `sd`（按 ReferenceId 读写逻辑类型） |
| `readDevSlot(reg, i, slt)` / `writeDevSlot(reg, i, slt, v)` | `ls drN` / `ss drN`（运行期端口） |
| `readReagent(dev, mode, key)` | `lr` |

### 4.3 分支

| 条件 | 分支指令 |
|------|----------|
| `== != < <= > >=` | `beq bne blt ble bgt bge` |
| `a` 为真 | `bnez` |
| `a` 为假 | `beqz` |
| 设备已/未设置 | `bdse / bdns` |
| 近似比较 | `bap / bna / bapz / bnaz` |

---

## 5. 内建数据

### 5.1 CRC-32 哈希

IC10 的 `HASH()` 与所有 `deviceHash` / `nameHash` 均为字符串的 **CRC-32（IEEE，反射多项式 `0xEDB88320`）**，按**有符号 32 位整数**解释。编译器在编译期计算，直接内联为十进制常量，不生成运行期 `HASH()` 调用。

> **已与游戏核对**（见 `internal/builtin/hash_test.go`）：`"a"`、`"A"`、`"a b"`、`"café"`、`"温度"`、`"Hello, World!"`、`"!@#$%^&*()"` 以及 `StructureSolarPanelDual` / `StructureBattery` 均逐字节吻合，含 UTF-8 非 ASCII。
>
> 注意：游戏内 `HASH("")` 不允许空字符串；`deviceHash` 是游戏内置的（玩家不可改），`nameHash` 才是玩家自定义的。

### 5.2 逻辑类型表

用于设备成员名校验与动态 logicType：

```
On  Open  Mode  Setting  Activate  Temperature  Pressure
Ratio  RatioOxygen  RatioNitrogen  RatioCarbonDioxide
RatioVolatiles  RatioPollutant  RatioWater
Charge  Power  PowerActual  PowerPotential  PowerRequired
SolarAngle  Horizontal  Vertical  HorizontalRatio  VerticalRatio
VelocityMagnitude  VelocityRelativeX/Y/Z
PositionX/Y/Z  ExportCount  ImportCount  Quantity  Reagents
RecipeHash  ReferenceId  PrefabHash  Error  Idle  Lock  Output
PressureExternal  PressureInternal  PressureSetting  Volume
TotalMoles  CompletionRatio  ElevatorLevel  ElevatorSpeed
Filtration  Harvest  Plant  ClearMemory  Maximum
NameHash  ...
```

> 完整表以 `internal/builtin` 为准，可随游戏版本更新。当前已与游戏
> `LogicType` 枚举（280 项，去 `None`）对齐。
> **测试版本：Stationeers Hotfix `v0.2.6428.27798`（2026-08-13）**，游戏更新后需重新核对。

### 5.3 槽位类型表

```
Occupied  OccupantHash  Quantity  Damage  Efficiency  FilterType
Health  Growth  Pressure  Temperature  Charge  ChargeRatio  Class
PressureWaste  PressureAir  MaxQuantity  Mature  ReferenceId  Seeding
FreeSlots  HarvestedHash  LineNumber  Lock  MaturityRatio  Mode
On  Open  PrefabHash  SeedingRatio  SortingClass  TotalSlots  Volume
```

> 当前已与游戏 `LogicSlotType` 枚举（32 项，去 `None`）对齐。
> **测试版本：Stationeers Hotfix `v0.2.6428.27798`（2026-08-13）**。

### 5.4 批量模式

| 名称 | 值 |
|------|----|
| Average | 0 |
| Sum | 1 |
| Minimum | 2 |
| Maximum | 3 |
| Count | 4 |

> 也可用 `LogicBatchMethod.*` 常量。`batch.read/write` 的 mode 实参接受模式名
> 字符串、裸名（`Average` 等）或数字。

### 5.5 枚举常量

编译器识别两类游戏枚举：

- **`LogicType.<成员>`**（如 `LogicType.Open`、`LogicType.Channel0`）：不查表，
  **原样输出**到 IC10，由游戏汇编器解析。因此任何合法成员都能用，也不受内建
  表版本影响。可作为 `read` / `write` / `readDev` / `writeDev` 的逻辑类型实参。
- **未知的 `Enum.Member`**：同样**原样输出并给出 `unknown-enum` 警告**，因此
  游戏更新新增枚举无需改编译器；`raw("...")` 可对任意操作数显式原样输出。
- 需要编译器知道**数值**的枚举（表见 `internal/builtin.EnumConstants`，数值需
  以游戏 Stationpedia 为准）：
  - `SorterInstruction`（低 8 位 OP 码）：`None`/`NOP`(0) / `FilterPrefabHashEquals`(1)
    / `FilterPrefabHashNotEquals`(2) / `FilterSortingClassCompare`(3)
    / `FilterSlotTypeCompare`(4) / `FilterQuantityCompare`(5)
    / `LimitNextExecutionByCount`(6)
  - 条件运算（`Filter*Compare` 的位 8..15）：`ConditionOperation.Equals`(0) /
    `Greater`(1) / `Less`(2) / `NotEquals`(3)，也接受裸名
    `Equals`/`Greater`/`Less`/`NotEquals`
  - `SlotClass.*`（0..43）与 `SortingClass.*`（0..10），如
    `SlotClass.Battery`(14)、`SortingClass.Ores`(9)
  - `LogicReagentMode.Contents`(0) / `Required`(1) / `Recipe`(2)
    / `TotalContents`(3)（旧前缀 `ReagentMode.*` 仍保留）
  - `PrinterInstruction`（8 位 OP 码）：`None`(0) / `StackPointer`(1) /
    `ExecuteRecipe`(2) / `WaitUntilNextValid`(3) / `JumpIfNextInvalid`(4) /
    `JumpToAddress`(5) / `DeviceSetLock`(6) / `EjectReagent`(7) /
    `EjectAllReagents`(8) / `MissingRecipeReagent`(9)
  - `TraderInstruction.*`（Medium Satellite Dish，0..18）
  - 栈容量 / 固定地址（编译器便利常量，非游戏枚举）：`Stack.Size`(512) /
    `SorterStack.Size`(32) / `PrinterStack.Size`(64) / `PrinterStack.StackPointer`(63) /
    `PrinterStack.MissingRecipeReagent`(54)
  - `Color.Blue`(0) / `Gray`(1) / `Green`(2) / `Orange`(3) / `Red`(4) / `Yellow`(5)
    / `White`(6) / `Black`(7) / `Brown`(8) / `Khaki`(9) / `Pink`(10) / `Purple`(11)
    （`LogicType.Color` 设备颜色；>11 视作 Purple，<0 视作 Blue）
  - `PowerMode.Idle`(0) / `Discharged`(1) / `Discharging`(2) / `Charging`(3) / `Charged`(4)
    （区域电源控制器充电状态）
  - `DisplayMode.Default`(0) / `Percent`(1) / `Power`(2) / `Kelvin`(3) / `Celsius`(4)
    / `Meters`(5) / `Credits`(6) / `Seconds`(7) / `Minutes`(8) / `Days`(9) / `String`(10)
    / `Fahrenheit`(11) / `Litres`(12) / `Mol`(13) / `Pa`(14) / `Newtons`(15) / `Degrees`(16)
    （LED 显示器读数模式）
  - `Sound.None`(0) / `Alarm2`(1) … `Alarm1`(45)（扬声器/警报；游戏枚举
    `SoundAlert`，IC10 前缀为 `Sound`）
  - 设备 `Mode` 类枚举：`AirCon.*` / `AirControl.*` / `Vent.*` / `FiltrationMode.*` /
    `ElevatorMode.*` / `RobotMode.*` / `DaylightSensorMode.*` / `SettingDisplayMode.*`
  - 其它：`EntityState.*` / `GasType.*`（31 项） / `HashType.*` / `LogicBatchMethod.*` /
    `LogicSlotType.*`（0..32） / `NodeType.*` / `ReEntryProfile.*` / `RocketMode.*` /
    `ShuttleType.*` / `TransmitterMode.*`

> 数学常量 `pi` / `deg2rad` / `rad2deg` / `epsilon` 按游戏常量**原样输出**，
> 由汇编器解析；`nan` / `pinf` / `ninf` 是特殊字面量。

> 枚举数值核对自 Stationeers 社区 Wiki（Logic Sorter，2026-09-05）与游戏类型导出
> `github.com/Stationeers-ic/ic10`（`src/Defines/consts.ts`）。枚举名与数值随游戏
> 版本变动，更新后需重新核对；未知枚举会原样输出，故轻微变动不会报错。

### 5.6 预制体 / 指令表

除枚举外，编译器/编辑器还带两张游戏数据表（`internal/builtin`，非编译输出）：

- **`Prefabs` / `PrefabByHash`**：1900+ 预制体名 → 显示标题；hash 即 `Hash(name)`。
  用于 `hash("…")` / `HASH("…")` 补全、hash 型实参的预制体补全、数字 hash 反查与 Wiki 文档链接。
- **`IC10Instructions`**：原生 IC10 指令的签名与游戏说明，用于 `.ic`/`.ic10`
  的补全、悬停与未知指令诊断。

> 数据源：游戏预制体列表与指令元数据（见 `Stationeers-ic` / `ic10emu`）；游戏
> 更新后重新核对。

### 5.7 数据段布局

`data` 表放进芯片的持久栈（512 槽）：

| 布局 | 数据段 | 寄存器溢出 | 备注 |
|------|--------|-----------|------|
| `top`（默认） | `[512-size, 511]` | `base-1` 向下 | 数据段与溢出不相交 |
| `middle` | `[256, 256+size-1]` | `511` 向下 | 溢出碰到数据段则报错 |

- 数据段首槽是版本哨兵；其后各表依次排布，`Table[i]` 用
  `get(db, base + i)` 读取。
- `size = 1（哨兵）+ 所有表元素数`（`--unsafe`/`--no-data-check` 仍占哨兵槽）。
- loader 用 `put db addr value`（`--data-access stack` 用 `poke addr value`）写入；
  value 可为数字或游戏枚举名。
- 数据段要求**标准 IC host**；设备 host 需 `--data-access stack`。
- 用户 `push` 增长区与 `poke` 地址必须留在数据段下方；`ic10c stats` 会警告。

详见 [`data-segment.md`](data-segment.md)。

---

## 6. 输出格式

- 每行一条指令，无缩进（或统一无缩进），无注释，无空行。
- 无 `alias` / `define`。
- 常量直接内联为十进制 / `0x` / `0b`。
- 文件末尾单个换行。

> 多芯片（`chip` 块）不改变单芯片的输出格式：每块芯片各自输出一份上述格式的
> 程序（各自 ≤128 行），CLI 按芯片写文件（见 `docs/multichip.md`）。

示例：

```
move r1 0
yield
l r0 d1 Temperature
slt r2 r0 283
select r1 r2 1 r1
sgt r2 r0 296
select r1 r2 0 r1
s d0 On r1
j 1
```

---

## 7. 待 VM 验证的语义细节

- `jr` 相对跳转的基准行（当前行 vs 下一行）。
- 绝对行号是否从 0 开始、空行/标签是否计数。
- `HASH()` 的 CRC-32 变体与编码。
- 位运算的整数宽度与符号处理。
- `select` 对 NaN 的行为。
- 浮点常量在源码中的最大有效位数（double 约 17 位）。
- `poke` / `peek` 对 `sp` 的影响与地址边界。
