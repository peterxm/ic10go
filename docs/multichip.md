# 多芯片支持

> 状态：**P1–P4 已实现**（`chip` 块 / 顶层公共区 / `bus` 通道糖 / 每芯片管线 /
> CLI `--chip` / JSON `chips[]` / VM `World` 锁步 / 编辑器）。
> 定位：**编译期语法糖 + 多程序输出**。`chip` / `bus` 全部降级成现有 IC10
> 通道读写（`s/l <dev>:<conn> ChannelN`），**运行时没有新指令**；VM 的多芯片
> 只是测试设施。
> 关联：`docs/spec.md` §4.5 与 §7.3、`docs/data-segment.md`、`docs/vm-improvements.md` §B5。

---

## 1. 目标与非目标

**目标**

1. 一个 `.icg` 源文件可声明**多块芯片**，每块各自编译成独立的 IC10 程序（各自
   128 行 / 4 KiB 预算、各自持久栈 loader）。
2. 用 `bus` 声明芯片间共享数据，编译器**自动推断收发**并生成通道读写。
3. VM 支持多芯片**同 tick 锁步** + 真实网络通道，便于本地验证。

**非目标**

- 不新增 IC10 指令；不改真机语义。
- 不做运行时握手 / 版本校验（行数宝贵，由用户保证读写时序）。
- 不做自动"该不该拆分"的启发式；拆不拆、拆到哪条网络由用户决定。
- 不做打包（`ins/ext` 压值进单通道）——留作将来。

---

## 2. 语法

### 2.1 顶层公共区

顶层 `const` / `data` / `func` 即"公共区"：所有 `chip` 都能引用，**各芯片各编译
一份**（`const` 内联；`data` 只在该芯片用到时才进它的 loader）。

```go
const Target = 33.3
data Recipe = [ -1301215609, 0.0095 ]

func clamp(x, lo, hi) { ... }   // 公共函数，各芯片各自内联/外提
```

### 2.2 `bus` 声明（契约）

顶层 `bus` 只声明**契约**：槽位名字与顺序（→ 通道号）。**访问点由每次访问决定**
（默认用 chip 的 `use`，可用 `[dev][conn]` 内联覆盖，见 §2.4）。

```go
bus Display {
    o2Pressure num
    mixOut     num
    mixError   num
}
```

- 槽位 `i` → `Channel{i}`；**bus 最多 8 槽**（一条连接 8 个通道）。超 8 请拆成多个 bus。
- 槽位类型：`num` / `bool` / `str`（见 §2.6）。

### 2.3 `chip` 块

```go
chip control {
    const Kp = 0.8          // chip 内可遮蔽顶层同名
    func main() { ... }
}

chip display {
    func main() { ... }
}
```

- 每块 `chip`：独立作用域、独立 `main`、独立预算、独立 loader。
- 无 `chip` 块时保持现状（顶层 `main` = 单芯片）。
- **不允许**顶层 `main` 与 `chip` 块混用（报错）。

### 2.4 访问总线（默认访问点 + 内联覆盖）

`use Bus on dev:conn` 给本 chip 一个**默认访问点**；`Bus.slot` 用它。每次访问也可用
`Bus.slot[dev][conn]` **内联覆盖**（`dev` 为端口 `db`/`d0..d5` 或设备别名，`conn`
为编译期字面量）：

```go
chip control {
    use Display on db:0
    func main() {
        for { yield()
            Display.o2Pressure = d1.Pressure       // 默认 -> s db:0 Channel0
            Display.mixOut[d5][1] = 1              // 覆盖 -> s d5:1 Channel1
        }
    }
}
chip display {
    func main() {                                  // 无 use，全部内联
        for { yield()
            d0.Setting = Display.o2Pressure[d1][0] // l r0 d1:0 Channel0
        }
    }
}
```

- 通道号恒等于**槽位下标**；`use` / 内联只决定**访问点**。
- 没有 `use` 且没写 `[dev][conn]` → 报错。
- **接线约束**：同一槽位的收发双方必须落在同一条电缆网络（否则读到 `NaN`）；跨网络
  需桥接设备。编译器不校验接线，由用户保证；`ic10c run` 会按槽位自动把它们接成同一网络。

### 2.5 文法（增量）

```
File    = { TopDecl }
TopDecl = ConstDecl | DataDecl | FuncDecl | BusDecl | ChipDecl
BusDecl = "bus" Ident "{" { Slot } "}"
UseDecl = "use" Ident "on" ConnLit { "," ConnLit }
ConnLit = Ident ":" Int              // db:0, d0:1 (device port or alias)
Slot    = Ident Type                 // num / bool / str
ChipDecl= "chip" Ident "{" { ConstDecl | DataDecl | FuncDecl | UseDecl } "}"
```

### 2.6 槽位类型

| 类型 | 底层表示 | 过通道 |
|------|----------|--------|
| `num` | float64 | ✅ 直接 |
| `bool` | 0/1（float64） | ✅ 直接 |
| `str("...")` | `STR("...")` 的**数值编码**（见下） | ✅ 直接（短串已验证） |

编译器把 `str("...")` 降成 `ir.Const{Raw: "STR(\"...\")"}`（`internal/lower/lower.go`），
但**真机实测**：`STR("AB")` 的数值是 `16706 = 0x4142`（`'A'<<8 | 'B'`），
`STR` 值能通过通道传递，接收端 LED 用 `DisplayMode.String` 会解回 `AB`
（见 `experiments/multichip-str/` T3）。

**结论**：`str` 槽位按 `num` 一样走通道即可；接收端显示时把 LED 设为
`DisplayMode.String`。唯一注意点：**显示模式要匹配**——同一个数值，`Mode 10`
(String) 解字符串、`Mode 0`(Default) 显示数字，用错会显示 `*` 等。

> 待补：长字符串 / 非 ASCII（`STR("HELLO")`、`STR("温度")`）是否仍是可解回的
> 数值编码（`experiments/multichip-str/` T3b）。若长串退化为 hash 且显示端解不回，
> 再考虑"传编号 + 消费方表"（原方案 B）。

---

## 3. 语义

### 3.1 作用域

| 名字 | 可见范围 | 冲突规则 |
|------|----------|----------|
| 顶层 `const`/`data`/`func` | 所有 chip | chip 内同名**遮蔽** |
| chip 内 `const`/`data`/`func` | 本 chip | 本 chip 内重名报错 |
| `bus` 槽位 | 所有 chip | 全局唯一；重名报错 |

- 每个 chip 必须恰好有一个 `main`（无参数、无返回值），否则报错。
- 顶层 `main` 与 `chip` 块互斥。

### 3.2 生产者 / 消费者推断

对每个 bus 槽位，扫描所有 chip：

- **写**该槽位的 chip = producer；**读**的 = consumer。
- 约束（按**芯片**计数，同一芯片多处写只算一次）：
  - ≥2 个写者 → 报错 `written by multiple chips`；
  - 有读者但**没有写者** → 报错 `read but never written`；
  - 既无读者也无写者的槽位 → 允许（未使用）。
- 生成（用该次访问的**访问点**：内联 `[dev][conn]` 优先，否则 chip 的 `use` 默认）：
  - producer：在赋值处发 `s <dev>:<conn> ChannelN <value>`。
  - consumer：在读取处收 `l <reg> <dev>:<conn> ChannelN`。
- **不做握手**：消费者可能读到 `NaN`（通道默认值）或旧值；由用户保证时序。

### 3.3 通道分配

- 槽位 `i` → `Channel{i}`（`i` 即槽位在 bus 中的下标）。
- bus 最多 8 槽；超过 → 报错，提示拆成多个 bus：

  ```
  bus "Display" has 9 slots; a network connection has only 8 channels — split it into another bus
  ```

### 3.4 与现有语法的关系

| 新写法 | 等价的现有写法 |
|--------|----------------|
| `Display.slot = v`（chip 内 `use Display on db:0`） | `db.channel[0][i] = v` |
| `Display.slot[d5][1] = v` | `d5.channel[1][i] = v` |

即 `bus` 是"命名 + 自动编号 + 自动收发"的糖；访问点默认来自 `use`，可按次用
`[dev][conn]` 覆盖。

---

## 4. 容量与限制

| 项 | 值 |
|----|----|
| 每连接通道数 | 8 |
| 每通道载荷 | 1 个 float64 |
| 每 `bus` 槽位 | ≤ 8 |
| 槽位类型 | `num` / `bool` / `str`（`str` 是数值编码，见 §2.6） |
| 超 8 的办法 | 拆成多个 `bus` |
| 通道持久性 | **易失**（改接线 / 退出世界清空），默认 `NaN` |

典型显示场景：显示芯片**自读源设备**（零通道）；只有主芯片的中间量（PID 输出、
误差等）才需要发，通常 < 8 个，单 bus 够用。

---

## 5. 编译模型与产物

### 5.1 每芯片独立管线

```
parse（一次）
  └─ 顶层公共区 + 各 chip 块
        ├─ chip control: sema.Check → lower → opt → codegen → Result{Code, Loader, Data}
        └─ chip display: 同上
```

- 复用现有 `CompileResult` 管线（`pkg/ic10/compiler.go`），只是每芯片跑一次。
- 每芯片的 `data` 段/loader 独立（`data-segment.md` 机制原样复用）。

### 5.2 API 形状（拟）

```go
type ChipResult struct {
    Name   string   // "control"
    Code   string
    Loader string   // 该芯片的一次性 loader（可空）
}

type Result struct {
    Code   string       // 单芯片时 = Chips[0].Code（向后兼容）
    Loader string
    Chips  []ChipResult // 多芯片；单芯片时长度 1
}
```

JSON（`build --json`）新增：

```json
{
  "ok": true,
  "chips": [
    { "name": "control", "code": "...", "lines": ["..."], "loader": "...", "data": { ... } },
    { "name": "display", "code": "...", "lines": ["..."] }
  ]
}
```

- 单芯片（无 `chip` 块）时保持现有顶层 `code` / `data` 字段不变；`chips` 也给出。
- `apiVersion` 不变（只增字段）。

---

## 6. 模块改动

| 模块 | 改动 | 量 |
|------|------|----|
| `internal/token` | 可能新增 `bus` / `chip` 关键字 | 小 |
| `internal/ast` | `BusDecl` / `ChipDecl` / `BusSlot`；printer 支持 | 中 |
| `internal/parser` | 解析 `bus` / `chip`；`dev:conn` 字面量 | 中 |
| `internal/sema` | 公共区 + 每 chip `Info`；bus 符号表；producer/consumer 检查；作用域/遮蔽 | 中 |
| `internal/lower` | `Bus.slot` → `ir.Load/Store{Dev:"db:0", Logic:"ChannelN"}`；按 chip 选 `main`/作用域 | 中 |
| `pkg/ic10` | `CompileResult` 多芯片；`ChipResult`；`data.go` 按芯片 | 中 |
| `pkg/ic10/json.go` | `chips[]` | 小 |
| `cmd/ic10c` | `build --chip NAME`；默认写 `<file>.<chip>.ic` + `<file>.<chip>.data.ic`；`stats/size` 分组 | 小 |
| `internal/lsp` | 按光标所在 chip 诊断/补全；bus 槽位补全；跨芯片符号隔离 | 中 |
| `editors/vscode` | "编译为 IC10" 选芯片（逐块预览/复制） | 中 |
| `internal/vm` | `World` 多芯片锁步 + 真实网络通道 | 大 |

---

## 7. VM 多芯片（`vm.World`）

### 7.1 模型

```go
type World struct {
    Chips   []*Machine           // 各自寄存器/栈/程序
    Devices map[string]*Device   // 共享设备
    Nets    map[int]*Network     // 网络：8 个通道
    wire    map[string]int       // "chip0:db:0" -> netID
}
```

- **锁步**：每 tick，每块芯片执行一条指令（或执行到 `yield` 暂停）。
- **网络**：`(chip, device, connection)` 经 `wire` 解析到网络；`s/l d:c ChannelN`
  读写该网络第 N 槽。
- 现状通道是 `d0:0` 伪设备（`vm-improvements.md` B5），本次替换为真实网络。

### 7.2 接线来源

- 测试：显式传 `wire`（如 `chip0.db:0 ↔ chip1.db:0`）。
- `ic10c run`：默认把所有芯片的 `db:0` 接成同一条网络（够跑通 bus），可用
  `--wire` 覆盖。

---

## 8. CLI / LSP / VSCode

- `ic10c build x.icg`：多芯片时默认每个芯片各写一份
  `<file>.<chip>.ic`（+ 各自的 `.data.ic`），并把概览打到 stderr。
- `ic10c build --chip control x.icg`：只输出 `control` 的 runtime 到 stdout。
- `ic10c stats/size`：按芯片分组显示行/字节/寄存器预算；**各芯片分别 ≤128 /
  4 KiB**（不做合计）。栈预算（`stack user`/`stack comp`）目前只在单芯片时报告。
- 多芯片默认 **`shared-stack`**（用户栈保守，不做寄存器提升）。在文件里写一次
  `// icg: private-stack` 即对本文件**所有 chip** 启用栈私有优化（pragma 是
  文件级的，各 chip 栈独立）。详见 [`spec.md` §4.6](spec.md#46-栈私有-pragma)。
- LSP：文档内多个 chip，按光标位置选 chip 做诊断/补全；`bus` 槽位有补全。
- VSCode："编译为 IC10" 弹出芯片选择，或逐芯片预览/复制安装代码。

---

## 9. 测试与真机验证

**单元**

- bus 通道分配（0..7、>8 报错）。
- producer/consumer 推断（唯一写者、多写者报错、**无写者报错**）。
- 作用域/遮蔽、顶层与 chip 内同名。
- 顶层 `main` 与 `chip` 混用报错。

**端到端（VM）**

- 一个"控制 + 显示"两芯片样例，`World` 锁步跑通，断言显示芯片读到主芯片发的值。
- 通道未写时读到 `NaN`（默认值），验证"无握手"行为。

**真机**

- 最小两芯片样例：`db:0` 同网络，A `s db:0 Channel0`，B `l db:0 Channel0`，确认互通。
- 验证 `bus` 生成的 `s/l` 与手写 `d.channel` 等价。

---

## 10. 分阶段

0. **P0 `str` 真机验证**：确认 `STR("...")` 能否过网络通道（决定 `str` 槽位做法）。
   脚本与步骤见 [`experiments/multichip-str/`](../experiments/multichip-str/README.md)。
1. ✅ **P1 多程序拆分**（已实现）：`chip` 块 + 公共区 + 每芯片管线 + CLI `--chip` /
   JSON `chips[]`。不做 bus → 显示芯片可自读设备，立即解决 fuel mixer 行数问题。
2. ✅ **P2 `bus` 糖**（已实现）：`bus Name { slot type ... }`（≤8 槽，槽位下标即通道）
   + 每 chip `use Name on dev:conn` 默认访问点、`Name.slot[dev][conn]` 内联覆盖 →
   `l/s dev:conn ChannelN`；唯一写者 / 多写者 / 被读却无写者 / 超 8 槽 / 未绑定诊断。
3. ✅ **P3 VM `World`**（已实现）：`vm.World` 多芯片锁步，设备按名共享（同一
   `dev:conn` 的 `ChannelN` 互通），每芯片保留自己的寄存器与栈；`ic10c run` 跑
   多芯片程序并打印世界设备。见 `internal/vm` 的 `World` / `Machine.Step`。
4. ✅ **P4 编辑器**：LSP 按**光标所在 chip** 隔离补全与签名（只给公共区 + 本 chip 的
   声明，`declsAtOffset`）；`Bus.` 补全槽位；语义高亮 `chip`/`bus`/`use`；VSCode
   编译命令弹芯片选择、状态栏显示芯片数。

---

## 11. 已定 / 待决

**已定**（2026-09-16 评审）

1. ✅ `bus` 槽位类型做 `num` / `bool` / `str("...")` 三种（`str` 的传输方式见下）。
2. ✅ bus 连接 `on db:0` 的语义：`db` = 各芯片自己的 host；接线约束写进文档。
3. ✅ 无写者的槽位 → **报错**。
4. ✅ 多芯片 `stats` = **各芯片分别 ≤128**。
5. ✅ `ic10c run` 默认把所有芯片 `db:0` 接同一网络（`--wire` 可覆盖）。
6. ✅ `bus` 连接**显式指定**（`on db:0`）。
7. ✅ chip 内声明**允许遮蔽**顶层同名。
8. ✅ 顶层 `data` 表被多芯片用到时，**各自一份 loader**。
9. ✅ `str("...")` 走通道：真机 T3 证实 `STR` 是数值编码，**按 `num` 处理**
   （接收端用 `DisplayMode.String` 显示）。

**待决**

1. 长字符串 / 非 ASCII 的 `STR`（T3b）：若仍可解回则无需特殊处理；若退化为
   hash，再考虑"传编号 + 消费方表"。

---

## 12. 完整示例：拆分 fuel mixer 的显示

```go
// 顶层公共区
const TargetO2 = 33.3
const PresTarget = 550.0

// 显示总线：主芯片发中间量，显示芯片收
bus Display {
    mixOut   num
    mixError num
}

chip control {
    use Display on db:0
    func main() {
        // ... 现有 PID / 安全 / 温度前馈 ...
        for {
            yield()
            // ... 计算 mixOut、e ...
            d4.Setting = mixOut
            Display.mixOut   = mixOut      // s db:0 Channel0 r
            Display.mixError = e           // s db:0 Channel1 r
        }
    }
}

chip display {
    use Display on d2:1                // 本芯片的接入点（可为桥接设备的口）
    func main() {
        for {
            yield()
            // 自读设备，零通信
            led(hash("LED氧气压力"), d1.Pressure)
            led(hash("LED输出压力"), d5.Pressure)
            led(hash("LED输出氧气比例"), d5.RatioOxygen)
            // 主芯片的中间量，走总线
            led(hash("LEDPID输出"), Display.mixOut)
            led(hash("LED误差"), Display.mixError)
        }
    }
}
```

效果：`control` 只多 2 行发送，删掉整个 LED 段（约 16 行）；`display` 独立 128 行
预算。`ic10c run` 会按 bus 自动把两块芯片的访问点接成同一条网，直接可跑。

---

## 13. 不用 `chip` / `bus`：手写跨芯片通信

`chip` / `bus` 只是**编译期语法糖 + 多程序输出**（§1）。不写多芯片语法也有三种手写方式——
**通道**（`d.channel[c][n]` ≈ `l/s d:c ChannelN`）、**宿主栈**（`get/put(dN, i)` 读另一块外壳的栈）、
**设备逻辑当共享寄存器**（`sbn`/`lbn` 按名字读写某台设备的 Logic，§13.3）——但都要自己补上编译器
替你做的事。按踩坑概率排：

### 13.1 栈通信：`private-stack` 会删掉"读不回"的栈写（务必 `// icg: shared-stack`）

没有 `chip` 块的单芯片 `.icg` 默认 **`private-stack`**：用户槽可提升为寄存器（mem2reg）、成对
`push`/`pop` 消除、**放宽死存储消除**。于是 `db.stack[i] = v`（本意是给别的芯片 `get/put(dN, i)`
读）会被当成死存储**删掉**：

```go
func main() { for { yield(); db.stack[5] = 42; d0.Setting = 1 } }
```

```text
默认产物：              （没有 put/poke —— 写没了）
// icg: shared-stack：  put db 5 42
```

**规则**：手写单芯片程序若向宿主栈发布数据，**必须自己加 `// icg: shared-stack`**（`chip`/`bus`
会自动设；pragma 是文件级的，见 [`spec.md` §4.6](spec.md#46-栈私有-pragma) 与
[`data-segment.md`](data-segment.md)「栈私有 pragma」）。反编译产物总带这个 pragma，正是为此。

### 13.2 栈通信：和对方编译器抢同一段栈

栈 512 槽分两区：**用户区** `[0, userLimit-1]`（默认 128）、**编译器区** `[userLimit, 511]`
（数据段在顶部、溢出槽在其下，见 [`data-segment.md`](data-segment.md) §5.5）。

`get/put(dN, i)` 读/写的是**那块外壳的整段栈**——也就是对方芯片的同一块内存，**含它的数据段与
溢出区**。所以要**手工分区**：别写进对方编译器区；两边用户区边界要一致（对方若用
`--dynamic-stack`，边界会挪到编译器区正下方）。`db.stack[N]` 的越界检查只针对**本程序**，
管不到你从别的芯片去读对方。

对比：`bus` 走通道、不占栈，天然没有这个问题。

### 13.3 第三种方式：把设备逻辑当共享寄存器

手边找不到合适的通道 / 栈时，还可以**把某台设备的某个 Logic 当共享寄存器**：用
`sbn <预制体> HASH("<设备名>") <Logic> v` 按「类型 + 名字」写、`lbn ... <Logic>` 读。真机实例
（社区服务器那套炉子的两芯片，`Furnace IC` ⇄ `Hash IC`）就是**互读对方 IC 外壳的 `Setting`**
（`2037291645` = `StructureCircuitHousingCompact`）传值：

```text
Hash IC 选配方： s db Setting <recipeHash>                      # 写自己外壳 Setting
Furnace IC 读：  lbn r12 2037291645 HASH("Hash IC")  Setting    # 按名字读回配方 hash
Hash IC 确认：   sbn   2037291645 HASH("Furnace IC") Setting r2 # 写对方外壳 Setting
Furnace IC 读：  l r0 db Setting                                # 读自己外壳，解码 r2
```

- 优点：不占通道（不受每连接 8 通道限制）、不占栈（没有 §13.1 / §13.2 的问题）；外壳 `Setting`
  就是个普通可读写 Logic，`sbn`/`lbn` 按名字寻址即可。
- 缺点：和其它手写方式一样**无握手 / 无版本校验**（§13.6）；要保证两端指的是**同一台设备**
  （类型 + 名字都对得上，否则读默认值）；而且拿逻辑当寄存器用会**和该设备的正常用途打架**
  （外壳 `Setting` 也可能被别的程序 / 玩家改，没有独占保证）。
- `bus` 就是把这套路固化下来：**专用通道 + 编译期契约**，不必借设备的 `Setting`。

### 13.4 没有编译期检查

`bus` 会做的推断/校验，手写通信一个都没有：

| 检查 | `bus` | 手写（channel / 栈 / 设备逻辑） |
|---|---|---|
| ≥2 个写者 | 报错 `written by multiple chips` | 无（静默取最后一次写） |
| 有读无写 | 报错 `read but never written` | 无（读到 `NaN`） |
| 槽位 → 通道号 | 自动编号（下标 = `ChannelN`） | 自己数，易撞号/错位 |
| >8 槽 / 无访问点 | 报错 | 无 |
| 类型 | `num` / `bool` / `str` 声明 + 赋值检查 | 无（只有一个 float64） |

错了都是**编译通过、运行时才现**（表现为读到 `NaN` / 旧值 / 错值）；同一网络上通道号撞了也无人提醒。

### 13.5 其它

- **公共区复制**：多芯片一个文件里顶层 `const`/`data`/`func` 共享（各芯片各编译一份）；裸写法把
  每个芯片拆成独立文件，要复制常量/协议，改一处漏一处。
- **每芯片 loader**：每个用到 `data` 段的芯片各有一个一次性 loader，要按顺序装/跑。
- **传设备引用**：通道只传数字；接收端 `readById`，坏 id 会让芯片 `Error=1` **停机**。
- **本地验证**：`vm.World` / `WireBus` 只对**一次编译出的多芯片程序**自动接线（`run --diff`
  端到端，§7）。裸多文件时 `ic10c run` 一次只跑一块，另一块要**手工 `--set` 模拟**
  （例见 [`chips/交易天线接线图.md`](chips/交易天线接线图.md) 的「纯 VM 复现」）。

### 13.6 两边都一样（多芯片也没解决）

- 不做握手 / 版本校验：消费者可能读到 `NaN`（通道默认）或旧值，时序自己保证（§1 非目标）。
- 通道**易失**：改接线 / 退出世界清空（§4）。
- 不校验物理接线：同一槽位的收发必须落在同一根电缆网络，否则读 `NaN`（§2.4）。

### 13.7 建议

- **纯 channel / 设备逻辑**：协议简单、两边自己对齐即可，主要缺的是**编译期检查**。
- **栈通信**：务必 `// icg: shared-stack`，并手工规划两侧的栈区间。
- 想省心：直接用 `chip` / `bus`（自动 shared-stack、自动收发 + 校验、一次输出、VM 可端到端测）。
