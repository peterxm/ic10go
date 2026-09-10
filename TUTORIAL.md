# ic10go 新手教程

> 面向**完全没写过 IC10**、但会一点编程的玩家。读完你能独立写出、调试并部署 Stationeers 的 IC10 程序。
>
> 配套阅读：[Quickstart](QUICKSTART.md)（5 分钟上手）、[语言规范](docs/spec.md)（完整语法）、[架构说明](docs/architecture.md)（编译器内部）。

---

## 目录

1. [IC10 是什么](#1-ic10-是什么)
2. [为什么要用 .icg / ic10go](#2-为什么要用-icg--ic10go)
3. [安装](#3-安装)
4. [第一个程序](#4-第一个程序)
5. [建立正确的心智模型](#5-建立正确的心智模型)
6. [基础语法](#6-基础语法)
7. [设备操作](#7-设备操作)
8. [常用配方](#8-常用配方)
9. [调试与验证](#9-调试与验证)
10. [迁移已有的 IC10 脚本](#10-迁移已有的-ic10-脚本)
11. [限制、性能与最佳实践](#11-限制性能与最佳实践)
12. [常见错误排查](#12-常见错误排查)
13. [速查表](#13-速查表)
14. [下一步](#14-下一步)

---

## 1. IC10 是什么

IC10 是 Stationeers 里的**可编程芯片**。你把芯片插进一个设备（控制台、显示器等），用游戏内的
编辑器写一段小汇编，芯片就会每个游戏 tick 执行一遍，读取和写入同网络上的设备。

写 IC10 前要先认识三样东西：

**① 设备端口（引脚）**

芯片有 6 个数据口 `d0`~`d5` 和一个宿主口 `db`。你在游戏里用数据线把设备接到这些口上，
程序里就用 `d0`、`d1`…… 指代它们。

```
d0  →  电池
d1  →  温度传感器
db  →  芯片所插的宿主设备（比如显示屏）
```

**② 逻辑属性（logicType）**

每个设备都有一组可读写的属性，比如电池有 `Charge`（电量）、`Ratio`（百分比）、`On`（开关），
传感器有 `Temperature`、`Pressure`、`Activate`…… 程序里写成 `d0.Charge`、`d1.Temperature`。

**③ 硬限制**

| 约束 | 上限 |
|------|------|
| 行数 | **128 行** |
| 字节数 | **4096 字节** |
| 每行长度 | **90 字符** |
| 通用寄存器 | 16 个（`r0`~`r15`） |

这些限制就是 ic10go 存在的理由：它帮你把高层代码压进这 128 行里。

---

## 2. 为什么要用 .icg / ic10go

原生 IC10 是**汇编**，写起来很痛苦：

```
# 原生 IC10：温度高于 296 就关，低于 283 就开
l r0 d1 Temperature
bgt r0 296 5
l r1 d0 On
beqz r1 7
...
```

痛点：跳转要手算行号、没有变量名、没有函数、没有 `if`/`for`、寄存器要自己分配。

`.icg` 是 ic10go 提供的一门**类 Go 语言**，编译成上面那种紧凑 IC10：

```go
const (
    MaxTemp = 296.15
    MinTemp = 283.15
)

func main() {
    var on = 0
    for {
        yield()
        t := d1.Temperature
        if t < MinTemp { on = 1 }
        if t > MaxTemp { on = 0 }
        d0.On = on
    }
}
```

编译后：

```
move r1 0
yield
l r0 d1 Temperature
bge r0 283.15 5
move r1 1
ble r0 296.15 7
move r1 0
s d0 On r1
j 1
```

编译器自动做了：变量→寄存器、`if`→条件跳转、`for`→回跳、常量内联、寄存器复用、
绝对行号回填。你只管写逻辑。

---

## 3. 安装

需要 **Go 1.27+**。

```bash
git clone <repo> ic10go
cd ic10go
go build -o ic10c ./cmd/ic10c
```

把 `ic10c` 放到 PATH 里（可选）：

```bash
cp ic10c ~/.local/bin/
```

验证：

```bash
./ic10c version
./ic10c help
```

> 不想编译也行，用 `go run ./cmd/ic10c ...` 代替 `./ic10c ...`。

---

## 4. 第一个程序

新建 `blink.icg`：

```go
// 每 tick 把电池电量写入芯片宿主显示屏
func main() {
    for {
        yield()
        db.Setting = d0.Ratio
    }
}
```

编译：

```bash
./ic10c build blink.icg
```

输出：

```
yield
l r0 d0 Ratio
s db Setting r0
j 0
```

逐行解释：

| 输出 | 含义 |
|------|------|
| `yield` | 暂停到下一个 tick |
| `l r0 d0 Ratio` | 从 `d0` 读取 `Ratio` 到寄存器 `r0` |
| `s db Setting r0` | 把 `r0` 写到 `db` 的 `Setting` |
| `j 0` | 无条件跳回第 0 行（循环） |

在游戏里：把这段贴进 IC10 编辑器，把电池接到 `d0`，芯片插到有屏幕的设备上，就能看到电量。

**先本地跑一下**，不用进游戏：

```bash
./ic10c run --set d0.Ratio=0.75 blink.icg
# 输出：db.Setting = 0.75
```

---

## 5. 建立正确的心智模型

理解下面几点，写 `.icg` 会顺很多。

### 5.1 运行时只有一种类型：数字

IC10 内部全是双精度浮点。`.icg` 的 `num` 和 `bool` 在运行时**都是数字**，
`true` 就是 `1`，`false` 就是 `0`。所以：

```go
d0.On = t > 300      // 比较结果 0/1 直接写进设备，合法
if d0.Activate { }   // 0 为假，非 0 为真
```

不需要区分 `int`/`float`/`bool`，也不用管类型转换。

### 5.2 字符串只在编译期存在

运行时没有字符串。字符串只能出现在需要**编译期常量**的地方，比如
`hash("StructureBattery")`、逻辑类型名、批量模式名。`str("Ready!")` 是特例，
它会生成 IC10 的显示字符串指令。

### 5.3 函数会被全部内联

`.icg` 的函数只是**编译期抽象**，编译后不存在函数边界。因此：

- **不支持递归**（编译器会报错）。
- 函数没有运行开销，可以放心拆小函数让代码更好读。

### 5.4 输出不可读是故意的

编译器不生成 `alias`/`define`/注释/空行/标签，跳转用绝对行号，寄存器按需复用。
这是为了省下宝贵的行数——**不要试图手改编译产物**，要改就改 `.icg`。

---

## 6. 基础语法

### 6.1 注释

```go
// 行注释
/* 块注释 */
```

### 6.2 常量 `const`

常量在编译期内联并折叠，**不占运行期行数**。

```go
const Pi = 3.14159
const solarPanel = -539224550   // 设备类型哈希，见第 7.4 节

const (
    PreMax = 45000
    PreMin = 40000
)
```

顶层只允许 `const`（v0.1 没有可变全局变量）。

### 6.3 变量 `var` / `:=`

```go
func main() {
    var on = 0        // 显式声明
    t := d1.Temperature   // := 自动推导
    on = 1            // 赋值
    on += 1           // 复合赋值：+= -= *= /= %= &= |= ^= <<= >>=
}
```

变量是函数内的**局部变量**，由寄存器分配器映射到 `r0`~`r15`，必要时溢出到栈。
你不需要手动管理寄存器。

### 6.4 运算符

```go
+  -  *  /  %          // 算术（% 的符号跟随除数）
&  |  ^  ~  <<  >>    // 位运算（整数语义）
== != <  <= >  >=     // 比较，结果是 0/1
&& || !               // 逻辑
c ? a : b             // 三元
```

例子：

```go
d2.Mode = x&3 | 1
y := sqrt(abs(-16)) + min(3, 5) * 2
d0.On = (t > 300) && (p < 100)
```

> `&&` / `||` 两侧无副作用时会编译成 `min`/`max`（1 行）；有副作用（函数调用、设备读写）
> 时按短路语义生成分支。

### 6.5 条件 `if` / `else`

```go
if t > MaxTemp {
    d0.On = 0
} else if t < MinTemp {
    d0.On = 1
} else {
    d0.On = 2
}
```

编译期优化：若两个分支只对**同一个左值**赋常量且无副作用，会折叠成 `select`（1 行）。

### 6.6 循环 `for`

```go
for {                 // 无限循环
    yield()
}

for d0.Activate {     // while
    yield()
}

for i := 0; i < 10; i++ {   // 三段式
    sum += i
}
```

- 支持 `break` / `continue`。
- **长循环里一定要 `yield()`**，否则芯片会在一个 tick 内空转（见第 11 节）。

### 6.7 `switch`

```go
switch mode {
case 1:
    d1.On = 1
case 2, 3:
    d1.On = 0
default:
    d1.On = -1
}
```

### 6.8 函数

```go
func clamp(x num, lo num, hi num) num {
    if x < lo { return lo }
    if x > hi { return hi }
    return x
}
```

- 参数和返回类型写 `num`（或 `bool`）。
- `return` 可以省略类型（自动推导）。
- **全内联、不支持递归**。
- 函数让代码可读，编译后没有额外开销。

---

## 7. 设备操作

### 7.1 读写属性

同一个 `d0.On`，在等号右边是**读**，在等号左边是**写**：

```go
t := d0.Temperature     // 读：l r d0 Temperature
d0.On = 1               // 写：s d0 On 1
d0.On = t > 300         // 比较结果直接写
```

逻辑类型名（`Temperature` `On` `Ratio` `Setting` `Mode` `Activate` `Open` …）
由内建表校验，写错了编译器会**警告**。

### 7.2 槽位 `slot`

机器内部的物品槽：

```go
m := d2.slot[0].Mature      // 读槽位属性
d2.slot[0].Harvest = 1      // 写槽位属性
q := d2.slot[0].Quantity
```

槽位索引可以是常量或变量。槽位属性有 `Occupied` `Mature` `Quantity` `Damage`
`OccupantHash` `Efficiency` `FilterType` 等。

### 7.3 网络通道 `channel`

设备之间的逻辑网络通道：

```go
x := d0.channel[0][0]       // 第一个下标=连接号，第二个=通道号 0..7
d0.channel[0][2] = x + 1
```

> 通道号必须是**编译期常量**（用于生成 `ChannelN`）。

### 7.4 批量 IO（多设备聚合）

当同类型的多个设备接在**同一个数据口**上时，批量指令可以一次读取/写入它们，
常用模式有 `Average` `Sum` `Minimum` `Maximum`。

```go
const batteries = -400115994   // "StructureBattery" 的类型哈希

total := batch.read(batteries, "Charge", "Sum")        // 所有电池电量之和
avg   := batch.read(batteries, "Ratio", "Average")     // 平均百分比
batch.write(batteries, "On", 1)                        // 全部开启
```

- `batch.read(类型哈希, 逻辑类型, 模式)`
- `batch.write(类型哈希, 逻辑类型, 值)`

按名字/槽位筛选：

```go
v := batch.readName(typeHash, nameHash, "Pressure", "Sum")
q := batch.readSlot(typeHash, 0, "Charge", "Maximum")
batch.writeName(typeHash, nameHash, "On", 1)
batch.writeSlot(typeHash, 0, "ChargeRatio", 1)
```

**怎么得到类型哈希？** 用 `hash("英文类型名")`：

```go
batch.read(hash("StructureBattery"), "Charge", "Sum")
```

`hash()` 在编译期算 CRC-32，直接内联成常量（例如 `hash("StructureBattery")` = `-400115994`），
不产生运行期调用。

### 7.5 动态逻辑类型

一般写 `d0.Temperature` 就够了；但如果逻辑类型要在运行时决定：

```go
lt := d0.Setting          // 运行时值
v := read(d1, lt)         // 读 d1 的 lt 属性
write(d2, lt, v)          // 写 d2 的 lt 属性
```

### 7.6 设备有效性查询

```go
if isSet(d0) { ... }                          // d0 是否已设置（连上了设备）
if isUnset(d0) { ... }
if isLoadValid(d0, "Temperature") { ... }     // d0 是否支持读 Temperature
if isStoreValid(d0, "On") { ... }             // d0 是否支持写 On
```

> `isLoadValid` / `isStoreValid` 只能用在 `if` / `for` 条件里。

### 7.7 设备栈与按 id 访问

```go
v := get(d0, addr)          // 读取设备内存地址
put(d0, addr, value)        // 写入
v = getd(id, addr)          // 按设备 id 而非端口
putd(id, addr, value)
clr(d0)                     // 清空设备
d2.Mode = rmap(d3, hash("Iron"))   // 反向映射试剂
```

---

## 8. 常用配方

下面每个都是**可编译的完整程序**，可以直接拿去改。

### 8.1 滞回温控（开/关带死区）

```go
const (
    MaxTemp = 296.15
    MinTemp = 283.15
)

func main() {
    var on = 0
    for {
        yield()
        t := d1.Temperature
        if t < MinTemp { on = 1 }
        if t > MaxTemp { on = 0 }
        d0.On = on
    }
}
```

滞回（hysteresis）的关键：**用上一 tick 的 `on` 状态**决定目标，避免在阈值附近抖动。

### 8.2 太阳能追日

```go
const solarPanel = -539224550   // "StructureSolarPanelDual"

func main() {
    for {
        yield()
        batch.write(solarPanel, "Vertical", 90-d0.Vertical)
        batch.write(solarPanel, "Horizontal", d0.Horizontal-90)
    }
}
```

`d0` 接日光传感器，`Vertical`/`Horizontal` 是传感器的朝向角度；批量写会把网络上的
双轴太阳能板一起调角。

### 8.3 电池监控 + 夜间照明

```go
const (
    batteries = -400115994   // Station Battery
    lights    = 797794350    // Wall Light (Long)
)

func main() {
    for {
        yield()
        d1.Setting = batch.read(batteries, "Charge", "Sum")
        d2.Setting = batch.read(batteries, "Ratio", "Average")
        batch.write(lights, "On", d0.Activate == 0)   // 没太阳就开灯
    }
}
```

`d0` 接日光传感器，`Activate` 为 1 表示有阳光；`== 0` 取反用于夜间开灯。

### 8.4 用函数拆逻辑

```go
const (
    PreMax = 45000
    PreMin = 40000
)

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
    target := d0.On != 0 ? PreMax : PreMin
    d0.On = p < target
}

func filterControl() {
    p := db.PressureOutput
    db.Mode = p < 19000
}
```

### 8.5 槽位采集

```go
func main() {
    for {
        yield()
        if d2.slot[0].Mature {
            d2.slot[0].Harvest = 1
        }
        d0.Setting = d2.slot[0].Quantity
    }
}
```

### 8.6 字符串显示与哈希

```go
func main() {
    d0.Setting = hash("StructureBattery")   // 编译期常量，-400115994
    d1.Setting = str("Ready!")              // 显示字符串
}
```

---

## 9. 调试与验证

### 9.1 预算报告 `stats`

在进游戏前确认没有超限：

```bash
./ic10c stats blink.icg
# lines        4 / 128
# bytes       28 / 4096
# max line     9 / 90
# registers    1 / 16
```

### 9.2 内置 VM `run`

不用进游戏就能跑：

```bash
# 预置设备值，跑 500 步
./ic10c run --steps 500 --set d0.Temperature=350 ctrl.icg

# 打印每条执行的指令（排查跳转/循环）
./ic10c run --steps 20 --trace blink.icg
```

`--set` 可重复，格式是 `端口.属性=值`。

### 9.3 看中间产物

```bash
./ic10c ast blink.icg     # 打印语法树
./ic10c lex blink.icg     # 打印词法单元
./ic10c build blink.icg   # 打印 IC10
```

### 9.4 写测试

项目自带 VM，可在测试里执行编译产物并断言设备状态（见 `pkg/ic10/vm_test.go`）。
适合给复杂逻辑做回归。

---

## 10. 迁移已有的 IC10 脚本

### 10.1 反编译 `decompile`

把现成的 `.ic`/`.ic10` 变成可编辑的 `.icg`：

```bash
./ic10c decompile old.ic              # 输出到 stdout
./ic10c decompile -s -o old.icg old.ic  # 结构化，写到文件
```

- `alias` / `define` 会被替换；
- 寄存器变成变量；
- 控制流变成 `label`/`goto`/`call`/`ret`，加 `-s` 时尝试还原 `if`/`else`/`for`；
- 不支持的指令会在 stderr 警告并保留为 `// unsupported:` 注释。

反编译产物可以直接编译回 IC10：

```bash
./ic10c build old.icg > new.ic
```

### 10.2 压缩行数 `minify`

如果你只想**让现有脚本行数变少**、不想改变它的算法：

```bash
./ic10c minify old.ic          # 输出到 stdout
./ic10c minify -w old.ic       # 原地写回
./ic10c minify -o small.ic big.ic
```

`minify` 做的是**汇编级结构清理**：去注释空行、内联 `define`、把标签换成绝对行号、
删除不可达指令。它**不改动原指令、寄存器分配和设备读写顺序**，所以安全。

### 10.3 两者的区别

| | `minify` | `decompile -s` → `build` |
|--|--|--|
| 变换 | 汇编级结构清理 | 反编译重建 + 重新优化 |
| 指令/寄存器 | 原样不动 | 可能重排、合并、换寄存器 |
| 语义 | 基本可视为等价 | 尽力而为，不保证 |
| 产物 | 仍是 IC10 | 中间 `.icg` 可读可改 |
| 适合 | 只省行、要保真 | 想迁移维护、愿接受重优化 |

---

## 11. 限制、性能与最佳实践

**硬限制**：128 行 / 4096 字节 / 每行 90 字符。超了 `build` 会报错，`stats` 可提前看。

**`yield()` 的用法**：芯片每 tick 会执行一批指令。主循环里放一个 `yield()`，
让程序每 tick 只跑一遍、其余时间休眠，既省电又不会过热。需要等待时用 `sleep(秒)`。

```go
for {
    yield()          // 每 tick 一遍
    ...
}
```

**减少行数的技巧**：

- 把重复值写成 `const`（编译期内联，不占行）。
- 用 `batch.*` 一条指令处理多个同类设备。
- 用 `hash("...")` 而非手抄魔法数字。
- 别在循环里重复读取不变的值。

**别做的事**：

- 手改编译产物（改 `.icg` 重新编译）。
- 写递归（不支持）。
- 期望有数组/字符串/指针/多返回值（没有）。

---

## 12. 常见错误排查

| 现象 | 原因 / 解决 |
|------|------|
| `missing main function` | 必须有 `func main()` |
| `recursion is not supported` | 把递归改成循环 |
| `unknown logic type "xxx"` | 逻辑类型拼写错误；可临时用 `IC10C_NO_CHECK=1` 关闭校验 |
| 编译报超出 128 行 | 用 `stats` 看占比，拆小逻辑、用 `batch`、减少重复 |
| 脚本在游戏里行为不对 | 先用 `./ic10c run --trace` 本地复现；再核对端口/逻辑类型 |
| `hash` 值对不上 | `hash()` 输出**有符号 int32**，如 `-400115994` |

环境变量：

- `IC10C_LANG=en|zh`：切换帮助语言。
- `IC10C_NO_CHECK=1`：关闭逻辑类型校验（调试用）。
- `IC10C_NO_OPT=1`：关闭优化器（对比排查用）。

---

## 13. 速查表

```go
// 常量 / 变量
const A = 1
var x = 0
y := d0.Ratio

// 设备读写
t := d1.Temperature
d0.On = t > 300
m := d2.slot[0].Mature
d2.slot[0].Harvest = 1
d0.channel[0][2] = t

// 批量
batch.read(hash("StructureBattery"), "Charge", "Sum")
batch.write(hash("StructureBattery"), "On", 1)
batch.readName(typeHash, nameHash, "Pressure", "Sum")
batch.writeSlot(typeHash, 0, "ChargeRatio", 1)

// 控制流
if cond { } else if c2 { } else { }
for { break }
for cond { }
for i := 0; i < 10; i++ { }
switch x { case 1: ... default: ... }

// 函数（内联，无递归）
func f(a num, b num) num { return a + b }

// 内建
yield()  sleep(3)  hcf()
abs sgn sqrt exp log pow ceil floor round trunc rand min max clamp lerp
sin cos tan asin acos atan atan2
push(x)  y := pop()  z := peek()  poke(addr, v)
approx(a, b, tol)  approxZero(a, tol)
nan  pinf  ninf  isNaN(x)
hash("...")  str("...")
read(dev, lt)  write(dev, lt, v)
isSet(dev)  isUnset(dev)  isLoadValid(dev, "lt")  isStoreValid(dev, "lt")
rol(a, b)  ror(a, b)  sla(a, b)  srl(a, b)   // 旋转 / 移位（位运算，整数语义）
```

常用命令：

```bash
ic10c build   f.icg          # 编译
ic10c run     f.icg          # 内置 VM 运行
ic10c stats   f.icg          # 预算报告
ic10c fmt -w  f.icg          # 格式化
ic10c decompile -s f.ic      # 反编译为 .icg
ic10c minify  f.ic           # 压缩行数
ic10c lsp                    # 语言服务器
```

---

## 14. 下一步

- 语言细节：[`docs/spec.md`](docs/spec.md)
- IC10 指令与约束：[`docs/target-ic10.md`](docs/target-ic10.md)
- 编译器架构：[`docs/architecture.md`](docs/architecture.md)
- 真实脚本示例：[`ic10code/`](ic10code/)（17 个手工改写并测试过的 `.icg`）
- 编辑器支持：[`editors/vscode/`](editors/vscode/)（语法高亮 + 诊断 + 补全）

最好的学习方式：打开 `ic10code/` 里一个真实脚本，对照它同目录的原始 `.ic`，
再用 `./ic10c build` 看编译结果。
