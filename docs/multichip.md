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

顶层 `bus` 只声明**契约**：槽位名字与顺序（→ `Channel0..`）。**访问点由每个 chip
用 `use` 指定**（§2.4）。

```go
bus Display {
    o2Pressure num
    mixOut     num
    mixError   num
}
```

- 槽位按声明顺序映射到 `Channel0..`；**每条连接 8 个通道**。
- 槽位类型：`num` / `bool` / `str`（见 §2.6）。
- 一个 bus 可跨多条连接（`8 × 连接数` 个通道），槽位 `i` 落在第 `i/8` 条连接的
  `Channel(i%8)`。

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

### 2.4 访问总线（`use` 访问点）

每个用到 bus 的 chip 声明自己的**访问点**（可多个连接，按槽位顺序分段）：

```go
chip control {
    use Display on db:0            // 本芯片经 db:0 接入
    func main() { for { yield(); Display.o2Pressure = d1.Pressure } }
}
chip display {
    use Display on d2:1            // 本芯片经 d2:1（如桥接设备的某个口）接入
    func main() { for { yield(); d0.Setting = Display.o2Pressure } }
}
```

- `use Bus on dev:conn[, dev:conn ...]`：`dev` 可为端口（`db`/`d0..d5`）或**设备
  别名**（`const Mem = d2`），只允许编译期常量（不允许 `drN`）。
- 槽位 `i` → 第 `i/8` 条绑定的 `Channel(i%8)`：`s/l <dev>:<conn> ChannelN`。
- 与现有 `d.channel[conn][ch]` 降到**同一组 IR**。
- **接线约束**：同一条绑定的所有芯片必须落在同一条电缆网络，否则读到 `NaN`
  （No Available Network）；跨网络需桥接设备。编译器不校验接线，由用户保证。

> 相比最初"在 bus 上写死一个 `db:0`"，把访问点拆到每个 chip 更贴近现实接线。

### 2.5 文法（增量）

```
File    = { TopDecl }
TopDecl = ConstDecl | DataDecl | FuncDecl | BusDecl | ChipDecl
BusDecl = "bus" Ident "on" ConnLit "{" { Slot } "}"
ConnLit = Ident ":" Int              // db:0, d0:1
Slot    = Ident "num"
ChipDecl= "chip" Ident "{" { ConstDecl | DataDecl | FuncDecl } "}"
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
- 生成（用**本芯片的 `use` 绑定**）：
  - producer：在赋值处发 `s <dev>:<conn> ChannelN <value>`。
  - consumer：在读取处收 `l <reg> <dev>:<conn> ChannelN`。
- **不做握手**：消费者可能读到 `NaN`（通道默认值）或旧值；由用户保证时序。

### 3.3 通道分配

- 槽位 `i` → 第 `i/8` 条绑定的 `Channel(i%8)`。
- 芯片绑定 `n` 条连接即有 `8n` 个通道；若 bus 槽位数超过 `8n` → 报错，提示加连接：

  ```
  bus "Display" has 9 slots but only 8 channel(s) bound (8 per connection); add more connections
  ```

### 3.4 与现有语法的关系

| 新写法 | 等价的现有写法 |
|--------|----------------|
| `Display.slot = v`（chip 内 `use Display on db:0`） | `db.channel[0][i] = v` |
| `x := Display.slot` | `x := db.channel[0][i]` |

即 `bus` 是"命名 + 自动编号 + 自动收发"的糖；`use` 决定每芯片的访问点。

---

## 4. 容量与限制

| 项 | 值 |
|----|----|
| 每连接通道数 | 8 |
| 每通道载荷 | 1 个 float64 |
| 每 `bus` 槽位 | ≤ `8 ×` 该芯片绑定的连接数 |
| 槽位类型 | `num` / `bool` / `str`（`str` 是数值编码，见 §2.6） |
| 超 8 的办法 | `use` 里加连接：`use Display on a:0, b:1`（按槽位顺序分段） |
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
  4 KiB**（不做合计）。
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
2. ✅ **P2 `bus` 糖**（已实现）：`bus Name on dev:conn { slot type ... }` + `Bus.slot`
   读写 → `l/s dev:conn ChannelN`；唯一写者 / 多写者 / 无写者 / >8 槽诊断。
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
