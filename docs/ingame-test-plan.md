# 真机测试方案（新内建 / 优化 / `--rel-jump` 验证）

> 目标：在 Stationeers 真机上验证近期新增的 IC10 能力与代码生成优化。
> 测试版本：`v0.2.6428.27798`（或更新）。所有程序都可用 `ic10c build` 编译后
> 粘贴进 IC10 编辑器。

## 0. 准备

| 端口 | 设备 | 用途 |
|------|------|------|
| `d0` | LED Display | 输出（显示 `Setting`） |
| `d1` | Logic Dial | 输入（旋转改变 `Setting`） |
| `d2` | 备用 | 部分测试使用 |

- 芯片插在**标准 IC host** 上（`d0`–`d5`）。
- 用 `ic10c build [flags] file.icg` 编译，把输出整段贴进 IC 编辑器。
- 每项测试：先贴程序、观察 LED；再改变 `d1` 旋钮，核对表格里的期望值。
- 建议先跑「第 1 节」确认基础语义没退化，再跑优化相关测试。

---

## 1. 默认编译：综合语义回归（range / 区间 / if-init / 标签 break）

```go
func main() {
    for {
        yield()
        x := d1.Setting
        var acc = 0
        for i := range 3 { acc += i }
        switch x {
        case 0..2: acc += 100
        case 3..5: acc += 200
        default: acc += 300
        }
        label Outer:
        for i := 0; i < 3; i++ {
            for j := 0; j < 3; j++ {
                if j == 1 { continue Outer }
                if i == 2 { break Outer }
                acc += 1
            }
        }
        d0.Setting = acc
    }
}
```

编译：`ic10c build test.icg`

| `d1` 旋钮 | 期望 LED |
|---|---|
| 0 ~ 2 | 105 |
| 3 ~ 5 | 205 |
| 其它 | 305 |

---

## 2. 新内建

### 2.1 `logicalNor`（IC10 `nor`）

```go
func main() {
    for {
        yield()
        d0.Setting = logicalNor(d1.Setting, 0)
    }
}
```

| `d1` | `logicalNor(a,0)` = `~(a\|0)` = `~a` |
|---|---|
| 0 | -1 |
| 1 | -2 |
| 5 | -6 |

### 2.2 反向近似比较与 `isNotNaN`

```go
func main() {
    for {
        yield()
        a := d1.Setting
        b := 5
        // 依次显示三个结果，每 tick 轮换
        d0.Setting = notApprox(a, b, 0.001)
        yield()
        d0.Setting = notApproxZero(a, 0.001)
        yield()
        d0.Setting = isNotNaN(a)
    }
}
```

| `d1` | `notApprox(a,5)` | `notApproxZero(a)` | `isNotNaN(a)` |
|---|---|---|---|
| 0 | 1 | 0 | 1 |
| 5 | 0 | 1 | 1 |
| 6 | 1 | 1 | 1 |

### 2.3 `clrById`（IC10 `clrd`，可选）

需要一个带栈且可读 `ReferenceId` 的设备（如 Logic Sorter）接 `d2`。

```go
func main() {
    for {
        yield()
        id := d2.ReferenceId
        putd(id, 10, 123)
        before := getd(id, 10)
        clrById(id)
        after := getd(id, 10)
        d0.Setting = before * 1000 + after
    }
}
```

期望 LED：`123000`（清空前读到 123，清空后读到 0）。

> 编译产物为 `put <id> 10 123` / `clrd <id>` / `get <id> 10 0`：`getd`/`putd`
> 在游戏里已弃用（编辑器划线），编译器改用统一的 `get`/`put`（其 device 操作数
> 现在接受设备 id）。`clrd` 仍是当前指令。

### 2.4 `readReagent`（IC10 `lr`，可选）

需要一个含反应物的设备（熔炉、气体混合器等）接 `d2`。`key` 用试剂 hash：

```go
func main() {
    for {
        yield()
        d0.Setting = readReagent(d2, LogicReagentMode.Contents, hash("Oxygen"))
    }
}
```

期望：LED 显示该设备中氧气的含量（具体数值取决于设备状态）。
> 若设备无该试剂，读数为 0。此测试主要确认不报错、能读取。
> `LogicReagentMode` 可取 `Contents`(0) / `Required`(1) / `Recipe`(2) / `TotalContents`(3)。

### 2.5 `readById` / `writeById`（IC10 `ld` / `sd`，可选）

用同一个带 `ReferenceId` 的设备接 `d2`（如 Logic Sorter）。

```go
func main() {
    for {
        yield()
        id := d2.ReferenceId
        writeById(id, LogicType.On, 1)          // sd <id> On 1
        v := readById(id, LogicType.On)          // ld <id> On
        d2.stack[0] = v                          // put d2 0 v
        d0.Setting = d2.stack[0]                 // get d2 0
    }
}
```

期望 LED：`1`（写入并读回 On，再经设备栈往返）。真机上确认 `ld` / `sd` 不报
`DeviceNotFound`，且 `d2.stack[...]` 生成的 `get`/`put` 正常。

### 2.6 游戏枚举与常量（可选）

用一个支持 `Mode` 的设备接 `d0`（如空调），若干电池接数据口。

```go
func main() {
    for {
        yield()
        d0.Mode = AirCon.Cold              // s d0 Mode 0
        d0.Setting = pi                    // 原样输出 pi
        d1.Setting = GasType.Oxygen        // s d1 Setting 1
        d2.Setting = batch.read(hash("StructureBattery"), "Ratio", "Count")
    }
}
```

期望：`d0.Mode` 变为 Cold；`d2.Setting` 显示网络内电池**数量**（Count 模式）；
`pi` / `GasType.Oxygen` 均不报未知标识符。真机上确认这些枚举名被汇编器接受
（未知枚举会原样输出，若名字有误游戏会报错）。

---

## 3. 代码生成优化（应与第 1 节行为一致）

以下程序会触发零比较单指令、近似分支融合、条件调用融合。若行为与手写等价
版本一致，即通过。

### 3.1 零比较 + 近似分支融合

```go
func main() {
    for {
        yield()
        x := d1.Setting
        var n = 0
        if x > 0 { n += 1 }
        if x < 0 { n += 2 }
        if x == 0 { n += 4 }
        if approx(x, 10, 0.001) { n += 10 }
        if notApprox(x, 10, 0.001) { n += 20 }
        if approxZero(x, 0.001) { n += 40 }
        d0.Setting = n
    }
}
```

| `d1` | 期望 LED |
|---|---|
| 10 | 1+10 = 11 |
| -3 | 2+20 = 22 |
| 0 | 4+20+40 = 64 |
| 7 | 1+20 = 21 |

### 3.2 条件调用融合（`b<cond>al`）

```go
func main() {
    var n = 0
    for {
        yield()
        n = 0
        if d1.Setting > 0 { call inc }
        if d1.Setting > 10 { call inc }
        d0.Setting = n
    }
    label inc:
    n += 1
    ret
}
```

| `d1` | 期望 LED |
|---|---|
| ≤ 0 | 0 |
| 1 ~ 10 | 1 |
| > 10 | 2 |

### 3.3 栈私有 / 共享标记（`// icg: private-stack` / `// icg: shared-stack`）

单芯片默认 `private-stack`：常量用户槽会被提升为寄存器（mem2reg）、成对
`push/pop` 被消除。寄存器与栈一样跨 tick 保留，所以跨 tick 状态必须一致。
下面这个程序用 `db.stack[0]` 做计数器，分别按 private / shared 编译，逐 tick
行为应完全相同：

```go
// 单芯片默认 private-stack；复制一份在开头加 // icg: shared-stack 对比
func main() {
    for {
        yield()
        db.stack[0] = db.stack[0] + 1
        d0.Setting = db.stack[0]
    }
}
```

| tick | 期望 LED |
|---|---|
| 1 | 1 |
| 2 | 2 |
| 3 | 3 |

- private 版会把槽位放进寄存器（本例 4 行 vs shared 6 行）；两版逐 tick 读数
  一致即通过。
- 注意：`ic10c run --steps N` 按**指令数**而非 tick 计，两版每 tick 的指令数
  不同，固定步数下读数会不同；真机按 tick 观察，或只在同一步数下比较“是否在
  递增”。
- 设备栈 `d2.stack[...]` 不受 pragma 影响（属于共享设备），可另接 Logic Sorter
  复核。详见 [`spec.md` §4.6](spec.md#46-栈私有-pragma)。

---

## 4. 相对跳转 `--rel-jump`（重点验证）

相对跳转的「基准行」需真机确认（当前 VM 按**相对跳转指令自身所在行**计算）。
若基准不符，程序会跳错行甚至死循环。请把 `--rel-jump` 版本与默认版本对比。

> 注意：`--rel-jump` 现在**逐条选择**——只有相对形式（`jr`/`br*`）严格更短时才
> 使用，否则保持绝对。小程序的绝对行号本来就短，两个版本可能**完全一致**
> （即没有用到相对跳转）。要真正验证基准，需要一个绝对目标 ≥100、且相对偏移
> 更短的程序（这样产物里会出现 `br*`/`jr`）。

### 4.1 前向 / 后向 / 条件跳转

源码（`rel.icg`）：

```go
func main() {
    for {
        yield()
        x := d1.Setting
        var n = 0
        if x > 5 { goto big }
        n = 1
        goto cont
        label big:
        n = 2
        label cont:
        for i := 0; i < 4; i++ {
            if i == 2 { continue }
            n += 10
        }
        d0.Setting = n
    }
}
```

编译两种版本：

```bash
ic10c build           rel.icg > rel.abs.ic   # 默认（绝对跳转）
ic10c build --rel-jump rel.icg > rel.rel.ic  # 相对跳转
```

分别贴入芯片观察：

| `d1` | 期望 LED |
|---|---|
| ≤ 5 | 31 |
| > 5 | 32 |

- 若产物里出现了 `br*`/`jr`，且 `--rel-jump` 版本与默认版本行为一致 → 相对基准正确。
- 若两个版本完全一致 → 本程序没用到相对跳转（都被逐条比较淘汰），换一个
  绝对目标 ≥100 的程序再试。
- 若显示其它值、卡死或 LED 不变 → 相对基准有偏差，记录实际现象。

### 4.2 跳转表 + 相对跳转

```go
func main() {
    for {
        yield()
        switch d1.Setting {
        case 0: d0.Setting = 10
        case 1: d0.Setting = 20
        case 2: d0.Setting = 30
        case 3: d0.Setting = 40
        case 4: d0.Setting = 50
        case 5: d0.Setting = 60
        case 6: d0.Setting = 70
        case 7: d0.Setting = 80
        case 8: d0.Setting = 90
        default: d0.Setting = 0
        }
    }
}
```

```bash
ic10c build --jump-table --rel-jump jt.icg > jt.rel.ic
```

| `d1` | 期望 LED |
|---|---|
| 0 ~ 8 | `(n+1)*10` |
| 其它 | 0 |

> 该程序同时使用 `jr`（跳转表派发）与相对 `j`，能验证相对基准。

### 4.3 大循环压力

```go
func main() {
    var n = 0
    for {
        yield()
        for i := 0; i < 20; i++ {
            n += 1
        }
        d0.Setting = n
    }
}
```

```bash
ic10c build --rel-jump loop.icg > loop.rel.ic
```

期望：LED 每秒稳定增加 20（不停机、不跳错）。连续观察 30 秒。

---

## 5. 结果记录

| 测试 | 期望 | 实测 | 通过 |
|---|---|---|---|
| 1 综合语义 | 105 / 205 / 305 | | ☐ |
| 2.1 logicalNor | -1 / -2 / -6 | | ☐ |
| 2.2 反向近似 | 见 2.2 表 | | ☐ |
| 2.3 clrById | 123000 | | ☐ |
| 2.4 readReagent | 不报错、可读 | | ☐ |
| 3.1 零比较/近似分支 | 11 / 22 / 44 / 21 | | ☐ |
| 3.2 条件调用 | 0 / 1 / 2 | | ☐ |
| 4.1 相对跳转 | 与默认一致 | | ☐ |
| 4.2 跳转表 + 相对 | (n+1)*10 | | ☐ |
| 4.3 大循环 | 每秒 +20 | | ☐ |

## 6. 反馈要点

若某项失败，请记录：
1. 使用的 `ic10c` 版本（`ic10c version`）。
2. 编译命令与完整产物（尤其 4.x 的相对跳转产物）。
3. `d1` 旋钮值、实际 LED 显示、是否卡死。
4. 与默认（绝对跳转）版本的差异。

> 提示：`ic10c stats <file.icg>` 可先确认行数/字节数未超限；
> `ic10c run` 可在本地 VM 里预跑（但 VM 的相对基准未必等于真机，故 4.x 必须真机验证）。
