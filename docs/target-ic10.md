# IC10 目标后端说明

> 本文记录 `ic10c` 后端生成 IC10 代码时必须遵守的目标约束、指令映射与内建数据。
> IC10 完整指令参考见仓库根目录 `Stationeers_IC10_参考文档.md`。

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
| `ra` | 返回地址（全内联后通常不使用） |
| `sp` | 栈指针（溢出/显式栈操作用） |

- 全内联策略下，编译产物一般不含 `jal` / `j ra`。
- 溢出使用固定栈地址 + `poke`；读取时按栈纪律处理 `sp`。
- 输出不生成 `alias`：直接使用 `r0` 等原名。

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

> 是否可用相对跳转 `jr` / `br-` 以缩短字节数，待 VM 验证语义后启用。

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
| `<< >>` | `sll sra` |
| `== != < <= > >=` | `seq sne slt sle sgt sge` |
| `!` | `seqz` |
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
| `batch.read` | `lb` |
| `batch.readName` | `lbn` |
| `batch.readSlot` | `lbs` |
| `batch.readNameSlot` | `lbns` |
| `batch.write` | `sb` |
| `batch.writeName` | `sbn` |
| `batch.writeSlot` | `sbs` |
| `isSet(d)` | `sdse` |
| `isUnset(d)` | `sdns` |
| `get / put / getd / putd / clr / rmap` | 同名指令 |

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
DataNetwork  ...
```

> 完整表以 `internal/builtin` 为准，可随游戏版本更新。

### 5.3 槽位类型表

```
Occupied  OccupantHash  Quantity  Damage  Efficiency  FilterType
Health  Growth  Pressure  Temperature  Charge  ChargeRatio  Class
PressureWaste  PressureAir  MaxQuantity  Mature  ReferenceId  Seeding
```

### 5.4 批量模式

| 名称 | 值 |
|------|----|
| Average | 0 |
| Sum | 1 |
| Minimum | 2 |
| Maximum | 3 |

### 5.5 枚举常量

以下 IC10 枚举由编译器原样输出或编译期解析：

- `LogicType.Channel0` … `LogicType.Channel7`
- `SorterInstruction.*`
- `PrinterInstruction.*`
- `ReagentMode.*`

---

## 6. 输出格式

- 每行一条指令，无缩进（或统一无缩进），无注释，无空行。
- 无 `alias` / `define`。
- 常量直接内联为十进制 / `0x` / `0b`。
- 文件末尾单个换行。

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
