# `.icg` 语言规范

> 版本：v0.1（草案）
> 目标：用接近 Go 的语法快速编写 Stationeers IC10 程序，由 `ic10c` 编译为**高效、紧凑、不可读**的 IC10 代码。
> 扩展名：`.icg`；模块：`ic10go`；实现语言：Go 1.27。

---

## 1. 设计原则

1. **单一运行时类型**：IC10 只有双精度浮点。`.icg` 的运行时值全部是 `num`（double）。`bool` 是 `0/1` 的语义约定，不是独立类型。
2. **零运行时抽象**：`alias`、`define`、字符串、函数调用等都在编译期消解，不占用宝贵的 128 行。
3. **输出不可读**：编译器自由分配寄存器、使用绝对行号跳转、省略一切注释/空行/标签。
4. **寄存器复用**：基于活跃性分析的图着色（Chaitin-Briggs）分配，同一个物理寄存器在不同时刻可承载不同源变量。
5. **大小感知**：所有优化以"减少行数 / 字节数"为目标，并在超出 IC10 限制时报错。

---

## 2. 词法

### 2.1 注释

```go
// 行注释
/* 块注释 */
```

### 2.2 标识符

```
ident = letter { letter | digit | "_" }
```

### 2.3 关键字

```
const  data  var   func  if    else  for    break  continue
return switch case  default  range
chip   bus   use
true   false nan   pinf  ninf
```

> `table` 是 `switch` 的上下文修饰符（`switch x table { ... }`），不是保留字。
> `chip` / `bus` / `use` 用于多芯片（见 §4.5）。

### 2.4 设备端口

`d0` `d1` `d2` `d3` `d4` `d5` `db` 是**内建设备标识符**，词法层识别，不能在作用域中被遮蔽。

### 2.5 数值

| 形式 | 示例 | 说明 |
|------|------|------|
| 十进制整数 | `123` | |
| 十进制浮点 | `1.5` `1e-3` | |
| 十六进制 | `0xE1B2` | 编译期转为十进制 |
| 二进制 | `0b1010_1010` | `_` 忽略 |
| 温度（摄氏） | `20c` | 编译期转为开尔文 `293.15` |
| 温度（华氏） | `68f` | 编译期转为开尔文 `293.15` |
| 温度（开尔文） | `300k` | 游戏默认单位，`k` 可省略 |
| 压力（帕） | `101325Pa` | 编译期转为千帕 `101.325` |
| 压力（千帕） | `101.3kPa` | 游戏默认单位 |
| 压力（兆帕） | `20.1MPa` | 编译期转为千帕 `20100` |
| 压力（巴） | `1bar` | 编译期转为千帕 `100` |
| 压力（psi） | `14.7psi` | 编译期转为千帕 `101.35…` |
| 功率（瓦） | `1500W` | 游戏默认单位 |
| 功率（千瓦） | `1.5kW` | 编译期转为瓦 `1500` |
| 功率（兆瓦） | `2MW` | 编译期转为瓦 `2e6` |
| 时间（毫秒） | `500ms` | 编译期转为秒 `0.5` |
| 时间（秒） | `90s` | 游戏默认单位 |
| 时间（分/时） | `2min` `1h` | 编译期转为秒 `120` / `3600` |
| 角度（度/弧度） | `180deg` `1rad` | 原样保留，**不换算** |
| 比例（百分比） | `50%` `30pct` | 编译期转为 `0.5` / `0.3` |

> IC10 原生 `$` / `%` 前缀在 `.icg` 中统一写为 `0x` / `0b`。
> 单位后缀只用于十进制字面量，编译期换算为游戏基准单位：
> - **温度** → 开尔文：`c → +273.15`，`f → (x-32)×5/9+273.15`，`k`（大小写均可）不变。
> - **压力** → 千帕：`Pa → ×0.001`，`kPa` 不变，`MPa → ×1000`，`bar → ×100`，`psi → ×6.894757…`。
> - **功率** → 瓦：`W` 不变，`kW → ×1000`，`MW → ×1e6`。
> - **时间** → 秒：`ms → ×0.001`，`s` 不变，`min → ×60`，`h → ×3600`。
> - **比例** → 0..1：`50%` = `0.5`，`30pct` = `0.3`（`%` 同时是取模运算符，见下）。
> - **角度**：`deg` / `rad` 只作可读性标记，**不做换算**（设备角度字段是度，`sin/cos/tan` 用弧度，需要时自行转换）。

> `%` 既可以是百分号后缀，也可以是取模运算符：数字后紧跟 `%` 且其后不是标识符/数字时按百分号（`50%` → `0.5`），否则按取模（`50%2`、`50 % 2`、`a%2` 都是 `%`）。

### 2.6 字符串

双引号字符串**仅存在于编译期**，只能出现在需要编译期常量的位置（`hash()`、逻辑类型名、批量模式名）。运行期没有字符串。

### 2.7 运算符与标点

```
+  -  *  /  %  &  |  ^  ~  <<  >>  ==  !=  <  <=  >  >=  &&  ||  !
=  :=  +=  -=  *=  /=  %=  &=  |=  ^=  <<=  >>=
?  :  (  )  {  }  [  ]  ,  .  ..  ;
```

---

## 3. 类型

| 类型 | 说明 | 运行时表示 |
|------|------|-----------|
| `num` | 数值 | double |
| `bool` | 逻辑值 `true`=1 / `false`=0 | double |
| `device` | 设备端口 `d0`..`d5` / `db` | 编译期符号 |
| `void` | 无返回值 | — |
| `str` | 编译期字符串（不可作变量） | 编译期 |

- 不声明类型的 `:=` 从右值推导。
- 运行时**没有隐式类型转换问题**，一切皆 double。
- 位运算按**整数语义**执行（值先视作 int64 再逐位运算），与 IC10 一致。
- 编译器在 sema 阶段对函数体做**保守类型检查**：报告未知类型名、同块重复声明、
  非 void 函数缺返回值、把 `device`/`data`/`str`/`void` 当数值使用等。未定义
  标识符与参数个数/设备实参等仍由 lower 报告；两类诊断不重复。

---

## 4. 声明

### 4.1 常量

```go
const Pi = 3.14159
const (
    MaxTemp = 296.15
    MinTemp = 283.15
)
```

常量在编译期内联并折叠，不生成 `define`。

也可以给**设备端口**起别名（同样是编译期消解，不占寄存器）：

```go
const (
    sensor  = d0
    battery = d1
    host    = db
)

func main() {
    battery.Setting = sensor.Temperature
    host.Setting = battery.Ratio
}
```

`const NAME = dN`（或 `db`）把 `NAME` 绑定到端口；别名可以再指向别名（`const a = sensor`）。之后 `NAME.Temperature`、`NAME.On = 1`、`NAME.slot[0].X`、`put(NAME, ...)`、`isSet(NAME)` 等都与直接写端口等价。

### 4.2 变量

```go
var on = 0
t := d1.Temperature
```

- 顶层只允许 `const` 与 `data`（v0.1 不允许可变全局变量）。
- 函数内 `var` / `:=` 为局部变量，由寄存器分配器映射到物理寄存器，必要时溢出到栈。
- `var x`（不写初值）等价于 `0`。若 `x` 只在部分路径被赋值，编译器会在入口补
  `move rX 0`；全部路径都赋值时该初始化被消除。分配器保证先写后读，源码无需
  手动初始化寄存器。
- 真机寄存器跨 tick、跨换代码保留（新芯片初值为 0），因此也可用于跨刷写保留状态；
  大块数据仍建议放持久栈（见 §4.4）。

### 4.3 函数

```go
func clampTemp(x num) num {
    if x < MinTemp { return MinTemp }
    if x > MaxTemp { return MaxTemp }
    return x
}
```

- **编译期展开**：函数是编译期抽象，会被内联或编译成 `jal` 子程序，因此**不支持递归**。
- 无函数重载、无闭包、无多返回值。
- `return` 可省略类型（推导）。
- **参数可以是设备或 `data` 表**：调用点按实参类型绑定，同一个函数可用不同设备 /
  表调用（每次都内联，因为设备端口与数据段基址是编译期符号，不能放进寄存器）：

```go
func occupied(dev, first, last) num {
    n := 0
    for i := first; i < last; i++ { n += dev.slot[i].Occupied }
    return n
}

func loadRoute(route) {
    for i := 0; i < 12; i++ { put(db, i, route[i]) }
}

func main() {
    if occupied(d0, 2, 102) > 0 { loadRoute(MyTable) }
}
```

  带设备 / 表实参的调用**强制内联**（即使该函数原本会被外提为子程序）。

### 4.4 数据表（持久栈数据段）

顶层 `data` 声明一个**编译期常量数组**，由编译器放进 IC 的持久栈：

```go
data RecipeDisplay = [ -1301215609, -404336834, 226410516 ]
data RecipeHeat    = [ 0.009501, 0.009502, 0.009503 ]
```

- 元素只能是编译期常量：数字、`hash("...")`，或游戏枚举名
  （如 `LogicType.Open`，原样写入 loader，由游戏汇编器解析）。
- `Table[i]` 读栈：编译为 `get(db, base + i)`（1 条指令），`i` 可为变量。
- 表**只读**，越界不检查（与 IC10 一致）。
- 编译器把数据段放在栈顶、寄存器溢出区（511 向下）之上，并写一个版本哨兵；
  runtime 启动时校验，缺失/过期则停机。
- 数据本身由一次性的 **loader** 写入（`ic10c build` 自动写出 `<file>.data.ic`；
  `--data-only` 只生成 loader）。
  默认用 `get/put db`，要求芯片插在**标准 IC host** 上；设备 host 用
  `--data-access stack`（本地 `poke`/`peek`）编译。
- 数据段占用栈顶槽位；`push`/`poke` 必须留在其下方。`ic10c stats` 会静态分析
  `push` 最大深度（无界或达到数据段时报错/警告），并在源码用了 `poke` 时提醒。
  默认 `--data-layout top`；`--data-layout middle` 把数据段放到固定槽 `256`、
  高地址留给寄存器溢出。
- 默认 runtime 会校验数据段版本（缺失/过期则停机）；`--no-data-check` 或
  `--unsafe` 可跳过校验以缩短代码（`--unsafe` 会在 CLI 打印警告）。

```go
db.Setting = RecipeDisplay[ore-1]   // -> get(db, baseDisplay + ore - 1)
heat       = RecipeHeat[ore-1]
```

详见 [`data-segment.md`](data-segment.md)。

### 4.5 多芯片（`chip` 块）

一个 `.icg` 文件可声明多块芯片，每块各自编译成独立的 IC10 程序（各自 128 行 /
4 KiB 预算、各自的一次性 loader）：

```go
const Shared = 33.3          // 顶层声明 = 公共区，所有 chip 可见
func helper(x) num { return x + 1 }

chip control {
    const Kp = 0.8           // chip 内声明可遮蔽顶层同名
    func main() { for { yield(); d0.Setting = helper(Kp) } }
}
chip display {
    func main() { for { yield(); d1.On = Shared } }
}
```

- 每个 `chip` 必须恰好有一个 `main`（无参数、无返回值）。
- 顶层 `main` 与 `chip` 块**不能混用**。
- 顶层 `const`/`data`/`func` 被每个用到的 chip **各编译一份**；`data` 表各自进
  自己 chip 的 loader。
- 芯片间通信见 [`multichip.md`](multichip.md)（`bus` / 网络通道）。

`bus` 声明一组命名网络通道；每个 chip 用 `use` 声明自己的访问点：

```go
bus Display {
    o2Pressure num
    mixOut     num
}
chip control {
    use Display on db:0
    func main() { for { yield(); Display.o2Pressure = d1.Pressure } }
}
chip display {
    use Display on d2:1
    func main() { for { yield(); d0.Setting = Display.o2Pressure } }
}
```

- `use Bus on dev:conn[, dev:conn ...]`：访问点（可多连接），`dev` 为端口
  （`db`/`d0..d5`）或设备别名，**只允许编译期常量**。槽位 `i` → 第 `i/8` 条绑定的
  `Channel(i%8)`。
- `Bus.slot` 写 → `s <dev>:<conn> ChannelN`，读 → `l r <dev>:<conn> ChannelN`。
- 每个槽位至多一个写者；**被读却没有写者** → 报错。不做握手，由用户保证时序。
- 同一条绑定上的芯片必须落在同一条电缆网络（否则读到 `NaN`），跨网络需桥接设备。
- 槽位类型 `num` / `bool` / `str`（`str` 是数值编码，接收端 LED 用
  `DisplayMode.String` 显示）。

CLI：`ic10c build x.icg` 会为每个 chip 写 `<file>.<chip>.ic`（及各自的
`.data.ic`）；`ic10c build --chip NAME x.icg` 只输出指定 chip。`--json` 的
`chips[]` 列出每块芯片。

---

## 5. 语句

### 5.1 赋值

```go
x = expr
x += expr        // x = x + expr，同理 -= *= /= %= &= |= ^= <<= >>=
d0.On = expr     // 设备写入
d2.slot[0].Harvest = 1
```

### 5.2 条件

```go
if cond { ... }
if cond { ... } else { ... }
if cond { ... } else if cond2 { ... } else { ... }
if x := expr; cond { ... }        // 带初始化语句，x 的作用域限于该 if
```

编译期优化：若两分支仅对**同一左值**赋常量且无副作用，折叠为 `select`（1 行）。

### 5.3 循环

```go
for { ... }                       // 无限循环
for cond { ... }                  // while
for i := 0; i < 10; i++ { ... }   // 三段式
for i := range 10 { ... }         // i = 0..9
for i := range Table { ... }      // 遍历 data 表的下标
for i, v := range Table { ... }   // v = Table[i]
```

- `range` 后接整数（编译期常量或运行时值，表示 `0..n-1`）或 `data` 表（长度编译期已知）。
- `break` / `continue` 可带外层循环标签：`break Outer` / `continue Outer`（标签写在循环前的 `label Outer:`）。
- 建议在长循环内显式调用 `yield()`。

### 5.4 switch

```go
switch x {
case 0:
    ...
case 1, 2:
    ...
default:
    ...
}

switch x := expr; x {       // 带初始化语句
case 1..5:                  // 闭区间（含上下界）
    ...
case 6, 7:
    ...
}
```

编译为比较链或跳转表（视情况）。`case lo..hi` 是闭区间，lower 为两次边界比较；区间 case 会让该 `switch` 退出 `--auto-table` / `--jump-table`（它们要求稠密单值）。

加 `table` 标记可把「常量 → 常量」的多路分支自动放进数据段（省行数）：

```go
switch ore table {
case 1: db.Setting = -1301215609; heat = 0.0095
case 2: db.Setting = -404336834;  heat = 0.0095
case 3: db.Setting = 226410516;   heat = 0.0095
}
```

约束：case 值必须是**连续整数**（`lo..hi` 无空缺），每个 case 体是若干
`目标 = 常量` 赋值，且各 case 的目标一致。编译为一次边界检查 + 每个目标
一条 `get(db, base + tag - lo)`；可选 `default` 处理越界。见
[`data-segment.md`](data-segment.md)。

`--auto-table`（默认关闭）会让编译器自动把**符合上述条件**的普通 `switch`
表化（≥5 个 case、表大小 ≤64），并对每处表化给出警告提示需先安装 loader。

### 5.5 返回

```go
return
return expr
```

### 5.6 底层控制流（反编译用）

高层语法（`if`/`for`/函数）之外，`.icg` 还提供一组底层原语，用于表达任意 IC10 控制流，主要由 `ic10c decompile` 生成：

```go
label start:            // 跳转目标（编译为位置，不占额外行）
    ...
    goto start          // 无条件跳转
    call routine        // 设置 ra 并跳转（IC10 jal）
    ret                 // 跳转到 ra（IC10 j ra）
```

- `label Name:` 只是标记一个位置，不生成标签行。
- `goto Name` / `call Name` 解析为绝对行号。
- `ra` 与 `sp` 是预定义的特殊寄存器，可直接读写：`ra = 0`、`x := ra`。
- `ireg(ptr)` 读取 `ptr` 指向的寄存器，`setIreg(ptr, v)` 写入（对应 IC10 的 `rrN`）。
- `jump(expr)` 执行计算跳转（对应 IC10 的 `j r0`），用于反编译无法静态解析的跳转。

> 这些原语会让产物更难优化，建议仅在迁移旧脚本时使用；正常开发请用高层语法。

---

## 6. 表达式

### 6.1 运算符优先级（高 → 低）

```
一元:      !  ~  -  +
乘除模:    *  /  %
加减:      +  -
移位:      <<  >>
位与:      &
位异或:    ^
位或:      |
比较:      ==  !=  <  <=  >  >=
逻辑与:    &&
逻辑或:    ||
三元:      ? :
```

### 6.2 语义映射

| `.icg` | IC10 | 备注 |
|--------|------|------|
| `a + b` | `add` | |
| `a - b` | `sub` | |
| `a * b` | `mul` | |
| `a / b` | `div` | |
| `a % b` | `mod` | 符号跟随 `b` |
| `-a` | `sub 0 a` | |
| `a == b` | `seq` / 融合 `beq` | 分支语境融合 |
| `a != b` | `sne` / `bne` | |
| `a < b` | `slt` / `blt` | |
| `a <= b` | `sle` / `ble` | |
| `a > b` | `sgt` / `bgt` | |
| `a >= b` | `sge` / `bge` | |
| `!a` | `seqz` | |
| `a & b` | `and` | |
| `a \| b` | `or` | |
| `a ^ b` | `xor` | |
| `~a` | `not` | |
| `a << b` | `sll` | 逻辑左移 |
| `a >> b` | `sra` | 算术右移（保符号） |
| `a && b` | `min` | 仅当两侧无副作用 |
| `a \|\| b` | `max` | 仅当两侧无副作用 |
| `c ? a : b` | `select` | |
| `true` / `false` | `1` / `0` | |

> `&&` / `||` 若含副作用（函数调用、设备读写），则按短路语义生成分支。

---

## 7. 设备与逻辑类型

### 7.1 属性读写

```go
t := d1.Temperature     // l r d1 Temperature
d0.On = 1               // s d0 On 1
d0.On = t > 300         // s d0 On (t>300)
```

- `d0.On` 出现在**右值**位置 → load；出现在**赋值左侧** → store。
- 逻辑类型名（`Temperature` `On` `Ratio` `Setting` ...）是设备成员名，由内建表校验（可关闭）。

### 7.2 槽位

```go
m := d2.slot[0].Mature          // ls r d2 0 Mature
d2.slot[0].Harvest = 1          // ss d2 0 Harvest 1
```

槽位索引可为变量或常量。槽位类型（`Occupied` `Mature` `Quantity` ...）由内建表校验。

### 7.3 网络通道

```go
x := d0.channel[0][0]           // l r d0:0 Channel0
d0.channel[0][2] = x            // s d0:0 Channel2 x
```

- 第一个下标是连接号（connection），第二个是通道号 `0..7`。
- 通道号必须是编译期常量（用于生成 `ChannelN` 枚举名）。

### 7.4 动态逻辑类型

当逻辑类型需要在运行时决定时使用底层函数：

```go
lt := someNumber
v := read(d0, lt)               // l r d0 (r_lt)
write(d0, lt, v)                // s d0 (r_lt) v
```

设备端口本身也可在运行时选择（对应 IC10 的 `drN`）：

```go
idx := someNumber
v := readDev(idx, lt)           // l r drN (r_lt)
writeDev(idx, lt, v)            // s drN (r_lt) v
```

`LogicType.X`（如 `LogicType.Open`）作为游戏枚举名原样输出，可作为 `read`/`write`/`readDev`/`writeDev` 的逻辑类型实参。

### 7.5 设备状态查询

```go
if isSet(d0) { ... }            // sdse
if isUnset(d0) { ... }          // sdns
if isLoadValid(d0, "Temperature") { ... }   // 设备支持读取该 logicType（bdnvl 取反）
if isStoreValid(d0, "On") { ... }           // 设备支持写入该 logicType（bdnvs 取反）
```

> `isLoadValid` / `isStoreValid` 只能在 `if` / `for` 条件中使用（IC10 无对应的置寄存器指令）。

### 7.6 批量 IO

```go
avg   := batch.read(hash("StructureBattery"), "Ratio", "Average")   // lb
val   := batch.readName(hash("StructureBattery"), hash("Bank 1"), "Ratio", "Maximum")  // lbn
q     := batch.readSlot(hash("StructureBattery"), 0, "ChargeRatio", "Sum")             // lbs
batch.write(hash("StructureBattery"), "On", 1)                       // sb
batch.writeName(hash("StructureBattery"), hash("Bank 1"), "On", 1)   // sbn
batch.writeSlot(hash("StructureBattery"), 0, "ChargeRatio", 1)       // sbs
```

- 批量模式字符串：`"Average"`(0) `"Sum"`(1) `"Minimum"`(2) `"Maximum"`(3) `"Count"`(4)；
  也可用裸名（`Sum`）或 `LogicBatchMethod.*` 常量。
- `hash("...")` 在编译期计算 CRC-32。

### 7.7 设备栈 / 按 id

```go
v := d0.stack[addr]    // get：读取设备栈（语法糖）
d0.stack[addr] = v     // put：写入设备栈（语法糖）
id := d1.ReferenceId
id.stack[0] = v        // 设备操作数也可为 id / 寄存器（生成统一 get/put）

get(dev, addr)         // get：dev 可为端口 dN/db、设备 id 或保存 id 的寄存器
put(dev, addr, value)  // put：同上
getd(id, addr)         // 等价 get，按设备 id
putd(id, addr, value)  // 等价 put，按设备 id
clr(d0)                // clr
clrById(id)            // clrd：按设备 id 清空
rmap(d0, reagentHash)  // rmap
readReagent(d0, LogicReagentMode.Contents, key)  // lr：读取反应物
```

> 游戏的 `get` / `put` 的 device 操作数是 `d?|r?|id`，因此 `.icg` 的 `get`/`put`
> 也接受设备 id 或保存 id 的寄存器；`getd`/`putd` 只是按 id 的别名，编译器统一
> 生成 `get` / `put`（独立的 `getd`/`putd` 指令已弃用）。
> `dN.stack[addr]` / `id.stack[addr]` 与 `dN.slot[i].X` 一样是语法糖。

按 ReferenceId 读写逻辑类型（IC10 `ld` / `sd`），以及运行期选择设备端口的槽位
读写（IC10 `ls drN` / `ss drN`）：

```go
v := readById(id, LogicType.Temperature)   // ld：按 id 读逻辑类型
writeById(id, LogicType.On, 1)             // sd：按 id 写逻辑类型
ptr := d2.Setting                           // 端口号在寄存器里
n := readDevSlot(ptr, 0, Occupied)          // ls r? drN i slt
writeDevSlot(ptr, 1, On, 1)                 // ss drN i slt r?
```

> `read`/`write` 用端口，`readById`/`writeById` 用 ReferenceId，`readDev`/`writeDev`
> 用寄存器里的端口号（`drN`），`readDevSlot`/`writeDevSlot` 是 `drN` 的槽位版本。
> 设备栈地址/容量常量：`Stack.Size`(512)、`SorterStack.Size`(32)、
> `PrinterStack.Size`(64)、`PrinterStack.StackPointer`(63)、
> `PrinterStack.MissingRecipeReagent`(54)。

> 在标准 IC host 上，`db` 的栈就是芯片自身的栈：`get/put(db, addr)` 与
> `push/pop/poke/peek` 访问同一块内存（真机已验证）。栈是**持久**的，跨代码
> 替换保留，可当作数据段使用（见 [`data-segment.md`](data-segment.md)）。
> 注意部分设备自带 host（如空调），此时 `db` 指向设备本身而非芯片栈。

---

## 8. 内建函数

### 8.1 控制

| 函数 | 说明 |
|------|------|
| `yield()` | 暂停 1 tick |
| `sleep(sec)` | 暂停 `sec` 秒 |
| `hcf()` | 停机并起火（慎用） |

### 8.2 数学

`abs sgn sqrt exp log pow ceil floor round trunc rand min max clamp lerp`
以及三角 `sin cos tan asin acos atan atan2`。

位运算：`& | ^ ~ << >>`（映射 `and or xor not sll sra`），以及
`logicalNor(a, b)`（`nor`）、`sla/srl/rol/ror`、`ext/ins`。

### 8.3 栈

```go
push(x)          // sp+1
v := pop()       // sp-1
v := peek()      // 不改 sp
poke(addr, v)
```

### 8.4 近似比较

```go
approx(a, b, tol)      // sap
approxZero(a, tol)     // sapz
notApprox(a, b, tol)   // sna
notApproxZero(a, tol)  // snaz
```

在 `if` / `for` 条件中直接使用上述调用会融合为单条近似分支
`bap` / `bna` / `bapz` / `bnaz`（`!approx(...)` 同样融合）。

### 8.5 特殊值

| 名称 | 说明 |
|------|------|
| `nan` | 安静 NaN |
| `pinf` / `ninf` | 正/负无穷 |
| `isNaN(x)` | `snan` |
| `isNotNaN(x)` | `snanz` |

### 8.6 编译期

```go
hash("StructureBattery")   // CRC-32，编译期常量
str("Ready!")              // 显示字符串，输出为 STR("Ready!")
raw("Equals")              // 原样输出该 IC10 操作数（游戏枚举/关键字逃生口）
```

> `raw("...")` 把字符串原样写进 IC10，用于编译器还不认识的游戏常量或汇编器
> 关键字；未知的 `Enum.Member` 也会**原样输出并告警**，因此游戏更新新增枚举
> 无需改编译器（已知枚举仍优先用内建表里的数值）。

### 8.7 设备栈指令构建器

分拣器 / 打印机的栈指令是按位段打包的整数。`sorter.*` / `printer.*` 构建器
按手册的字段布局打包（常量参数会折叠成单个数字）：

```go
put(d0, 0, sorter.filterPrefabHash(hash("ItemIronOre")))       // hash << 8 | 1
put(d0, 1, sorter.filterPrefabHashNotEquals(hash("ItemGold"))) // hash << 8 | 2
put(d0, 2, sorter.filterSortingClass(Equals, SortingClass.Ores))   // class<<16 | op<<8 | 3
put(d0, 3, sorter.filterSlotType(Greater, SlotClass.Battery))      // class<<16 | op<<8 | 4
put(d0, 4, sorter.filterQuantity(Less, 10))                        // qty<<16   | op<<8 | 5
put(d0, 5, sorter.limitNextExecutionByCount(5))                    // count<<8  | 6

put(d1, 0, printer.none())                            // 0
put(d1, 1, printer.stackPointer(7))                   // index<<8 | 1（仅栈地址 63）
put(d1, 2, printer.executeRecipe(50, hash("ItemCableCoil"))) // qty<<8 | hash<<16 | 2
put(d1, 3, printer.waitUntilNextValid())              // 3
put(d1, 4, printer.jumpIfNextInvalid(3))              // addr<<8 | 4
put(d1, 5, printer.jumpToAddress(10))                 // addr<<8 | 5
put(d1, 6, printer.deviceSetLock(1))                  // lock<<8 | 6
put(d1, 7, printer.ejectReagent(hash("Iron")))        // hash<<8 | 7
put(d1, 8, printer.ejectAllReagents())                // 8
put(d1, 9, printer.missingRecipeReagent(2, hash("Iron"))) // ceil<<8 | hash<<16 | 9
```

> `sorter` / `printer` 是命名空间（与 `batch` 一样），不是设备；设备仍用 `dN`/`db`
> 或 `const myPrinter = d1` 这样的别名。构建器参数可以是运行期值（会生成
> `sll`/`or`）；**常量参数会做位宽校验**（如 `printer.executeRecipe` 的 quantity
> 只有 8 位，超出报错而不是串到 hash 字段）。字段布局取自游戏内 Stationpedia
> 的 `PrinterInstruction`：`StackPointer` 仅用于栈地址 63，`MissingRecipeReagent`
> 仅用于 54..62，其余用于 0..53。

条件运算取 `Equals` / `Greater` / `Less` / `NotEquals`（0/1/2/3）；分类/槽位/数量
常量取 `SortingClass.*` / `SlotClass.*`。分拣器 `Mode`：`All`(0) / `Any`(1) /
`None`(2)。

### 8.8 底层

| 函数 | 说明 |
|------|------|
| `ireg(ptr)` | 读取 `ptr` 指向的寄存器（IC10 `rrN`） |
| `setIreg(ptr, v)` | 写入 `ptr` 指向的寄存器 |
| `sla/srl/rol/ror(a,b)` | 移位/旋转 |
| `ext(src,off,len)` / `ins(field,off,len)` | 位域提取/插入 |

> `ins` 是**读改写**：`x = ins(field, off, len)` 把 `field` 的低 `len` 位插到 `x` 的 `off` 处，保留 `x` 的其余位，因此 `x` 必须已有值（IC10 的 `ins dst field off len` 语义）。

> `ins` 在**稳定版**游戏里参数顺序有 bug（实际为 `offset length field`）；用 `ic10c build --stable-ins` 生成稳定版顺序（默认是文档顺序，即 beta 版）。

---

## 9. 编译期求值

以下内容在编译期完成，不占运行期指令：

- 常量折叠（算术、比较、位运算、逻辑）
- `hash()` 的 CRC-32
- 批量模式名 → 数字
- 逻辑类型名 → IC10 标识符
- 死代码消除、常量传播、公共子表达式消除
- 设备成员合法性校验（可关闭）

---

## 10. 代码生成语义

### 10.1 跳转与标签

- **不生成标签行**。所有分支使用**绝对数字行号**，在布局完成后回填。
- 条件分支尽量融合比较：`if a < b { ... }` 生成 `bge a b <else>` 而非 `slt` + `beqz`。

### 10.2 寄存器分配

- 反向数据流计算活跃性，构造干涉图。
- **图着色（Chaitin-Briggs）**分配到 `r0..r15`，优先复用空闲寄存器。
- **拷贝合并**（union-find，仅在不冲突时合并）消除 `move`。
- 寄存器不足时**溢出到 IC10 栈**：固定高地址槽（511 起）用 `poke` 写入，读取时保存/恢复 `sp` 后 `peek`；最高寄存器保留作溢出加载暂存。
- `ra` 与 `sp` 不参与通用分配。

### 10.3 示例

源码：

```go
func main() {
    var on = 0
    for {
        yield()
        t := d1.Temperature
        if t < 283 { on = 1 }
        if t > 296 { on = 0 }
        d0.On = on
    }
}
```

编译产物（示意，具体寄存器以分配器为准）：

```
move r1 0
yield
l r0 d1 Temperature
bge r0 283 5
move r1 1
ble r0 296 7
move r1 0
s d0 On r1
j 1
```

---

## 11. 限制与校验

`ic10c` 在输出前强制校验：

| 约束 | 值 | 超限处理 |
|------|----|---------|
| 行数 | ≤ 128 | 报错，列出各函数行数占比与建议 |
| 字节数 | ≤ 4096 | 报错 |
| 单行长度 | ≤ 90 | 报错 |

`stats` 子命令可在编译前输出行/字节预算报告。

---

## 12. 完整示例

```go
// 氧气过滤灌装：维持过滤器出口压力
const PreMax = 45000
const PreMin = 40000

func main() {
    for {
        yield()
        ventControl()
        filterControl()
    }
}

func ventControl() {
    d0.Mode = 1
    p := d0.PressureOutput
    var target = PreMin
    if p > PreMax { target = PreMax }
    d0.On = p < target
}

func filterControl() {
    p := db.PressureOutput
    db.Mode = p < 19000
}
```

编译产物（示意）：

```
move r1 45000
yield
s d0 Mode 1
l r0 d0 PressureOutput
ble r0 45000 5
slt r0 r0 r1
s d0 On r0
l r0 db PressureOutput
slt r0 r0 19000
s db Mode r0
j 1
```

---

## 13. 与 Go 的差异速览

| Go 特性 | `.icg` |
|---------|--------|
| 多类型 / 结构体 / 接口 | 仅 `num` / `bool` / `device` |
| 字符串 | 仅编译期 |
| 指针 / 切片 / map | 无 |
| goroutine / channel | 无（`channel` 是设备网络通道，含义不同） |
| 多返回值 | 无 |
| 递归 | 不支持（编译期展开 / 外提） |
| 包 / import | 无（单文件） |
| GC / 内存 | 无（寄存器 + 栈） |
| `for range` | 支持：`for i := range n`、`for i, v := range Table` |
