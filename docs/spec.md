# `.icg` 语言规范

> 版本：v0.1（草案）
> 目标：用接近 Go 的语法快速编写 Stationeers IC10 程序，由 `ic10c` 编译为**高效、紧凑、不可读**的 IC10 代码。
> 扩展名：`.icg`；模块：`ic10go`；实现语言：Go 1.27。

---

## 1. 设计原则

1. **单一运行时类型**：IC10 只有双精度浮点。`.icg` 的运行时值全部是 `num`（double）。`bool` 是 `0/1` 的语义约定，不是独立类型。
2. **零运行时抽象**：`alias`、`define`、字符串、函数调用等都在编译期消解，不占用宝贵的 128 行。
3. **输出不可读**：编译器自由分配寄存器、使用绝对行号跳转、省略一切注释/空行/标签。
4. **寄存器复用**：基于活跃性分析的线性扫描分配，同一个物理寄存器在不同时刻可承载不同源变量。
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
const  var   func  if    else  for    break  continue
return switch case  default
true   false nan   pinf  ninf
```

### 2.4 设备端口

`d0` `d1` `d2` `d3` `d4` `d5` `db` 是**内建设备标识符**，词法层识别，不能在作用域中被遮蔽。

### 2.5 数值

| 形式 | 示例 | 说明 |
|------|------|------|
| 十进制整数 | `123` | |
| 十进制浮点 | `1.5` `1e-3` | |
| 十六进制 | `0xE1B2` | 编译期转为十进制 |
| 二进制 | `0b1010_1010` | `_` 忽略 |

> IC10 原生 `$` / `%` 前缀在 `.icg` 中统一写为 `0x` / `0b`。

### 2.6 字符串

双引号字符串**仅存在于编译期**，只能出现在需要编译期常量的位置（`hash()`、逻辑类型名、批量模式名）。运行期没有字符串。

### 2.7 运算符与标点

```
+  -  *  /  %  &  |  ^  ~  <<  >>  ==  !=  <  <=  >  >=  &&  ||  !
=  :=  +=  -=  *=  /=  %=  &=  |=  ^=  <<=  >>=
?  :  (  )  {  }  [  ]  ,  .  ;
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

### 4.2 变量

```go
var on = 0
t := d1.Temperature
```

- 顶层只允许 `const`（v0.1 不允许可变全局变量）。
- 函数内 `var` / `:=` 为局部变量，由寄存器分配器映射到物理寄存器，必要时溢出到栈。

### 4.3 函数

```go
func clampTemp(x num) num {
    if x < MinTemp { return MinTemp }
    if x > MaxTemp { return MaxTemp }
    return x
}
```

- **全内联**：函数是编译期抽象，编译产物中不存在函数边界，因此**不支持递归**。
- 无函数重载、无闭包、无多返回值。
- `return` 可省略类型（推导）。

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
```

编译期优化：若两分支仅对**同一左值**赋常量且无副作用，折叠为 `select`（1 行）。

### 5.3 循环

```go
for { ... }                       // 无限循环
for cond { ... }                  // while
for i := 0; i < 10; i++ { ... }   // 三段式
```

- `break` / `continue`。
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
```

编译为比较链或跳转表（视情况）。

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

### 7.5 设备状态查询

```go
if isSet(d0) { ... }            // sdse
if isUnset(d0) { ... }          // sdns
```

### 7.6 批量 IO

```go
avg   := batch.read(hash("StructureBattery"), "Ratio", "Average")   // lb
val   := batch.readName(hash("StructureBattery"), hash("Bank 1"), "Ratio", "Maximum")  // lbn
q     := batch.readSlot(hash("StructureBattery"), 0, "ChargeRatio", "Sum")             // lbs
batch.write(hash("StructureBattery"), "On", 1)                       // sb
batch.writeName(hash("StructureBattery"), hash("Bank 1"), "On", 1)   // sbn
batch.writeSlot(hash("StructureBattery"), 0, "ChargeRatio", 1)       // sbs
```

- 批量模式字符串：`"Average"`(0) `"Sum"`(1) `"Minimum"`(2) `"Maximum"`(3)。
- `hash("...")` 在编译期计算 CRC-32。

### 7.7 设备栈 / 按 id

```go
get(d0, addr)          // get
put(d0, addr, value)   // put
getd(id, addr)         // getd
putd(id, addr, value)  // putd
clr(d0)                // clr
rmap(d0, reagentHash)  // rmap
```

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
```

### 8.5 特殊值

| 名称 | 说明 |
|------|------|
| `nan` | 安静 NaN |
| `pinf` / `ninf` | 正/负无穷 |
| `isNaN(x)` | `snan` |

### 8.6 编译期

```go
hash("StructureBattery")   // CRC-32，编译期常量
str("Ready!")              // 显示字符串，输出为 STR("Ready!")
```

### 8.7 底层

| 函数 | 说明 |
|------|------|
| `ireg(ptr)` | 读取 `ptr` 指向的寄存器（IC10 `rrN`） |
| `setIreg(ptr, v)` | 写入 `ptr` 指向的寄存器 |
| `sla/srl/rol/ror(a,b)` | 移位/旋转 |
| `ext(src,off,len)` / `ins(field,off,len)` | 位域提取/插入 |

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

- 反向数据流计算活跃性，构造活跃区间。
- 线性扫描分配到 `r0..r15`，优先复用空闲的最小编号寄存器。
- 拷贝合并消除 `move`。
- 溢出策略（按优先级）：
  1. 常量重物化（不占寄存器）
  2. 廉价表达式重算
  3. 固定栈地址 + `poke` / 恢复 `sp` 后 `peek`
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
slt r2 r0 283
select r1 r2 1 r1
sgt r2 r0 296
select r1 r2 0 r1
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
yield
s d0 Mode 1
l r0 d0 PressureOutput
sgt r1 r0 45000
select r2 r1 45000 40000
slt r1 r0 r2
s d0 On r1
l r0 db PressureOutput
slt r1 r0 19000
s db Mode r1
j 0
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
| 递归 | 不支持（全内联） |
| 包 / import | 无（单文件） |
| GC / 内存 | 无（寄存器 + 栈） |
| `for range` | 无 |
