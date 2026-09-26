# 游戏内测试台（testbench）：mod + Go harness + VSCode

> 目标：把 [`ingame-test-plan.md`](ingame-test-plan.md) 里「编译 → 手动粘贴 → 拧旋钮 →
> 看 LED → 记表」的真机验证，变成**可脚本化、可回归、可在编辑器里一键跑**的自动化流程。
> 同时给日常 `.icg` 开发提供「把代码推到游戏里的芯片、实时看寄存器 / 栈 / 设备」的能力。

本文是方案（设计）文档。实现分 M1–M5，见 §9。

---

## 1. 现状与可复用件

| 资产 | 位置 | 复用方式 |
|---|---|---|
| 无 patch 的游戏内导出器 | `tools/ingame-exporter/` | mod 的 csproj / `About.xml` / 编译安装流程直接照抄 |
| `build --json` 机器接口 | `docs/plugin-api.md` | `lines` + `data.loaders[]` 就是要推给芯片的内容 |
| 内置 VM | `internal/vm/` | 同一场景先在 VM 跑，再在真机跑，做差分 |
| 纯 Go ECMA-335 reader | `tools/genenums/main.go` | 抽出 `tools/dumpgameapi`，游戏更新后一键核对字段 |
| VSCode 扩展 | `editors/vscode/` | 复用 `execCli()`、输出面板、`withTempFile()`，新增 bench 客户端与视图 |
| 真机测试用例 | `docs/ingame-test-plan.md` | 直接转成场景文件，形成回归集 |

### 1.1 环境（本机已确认）

- Stationeers 由 Proton 运行：`~/.local/share/Steam/steamapps/common/Stationeers`。
- **BepInEx 5.4.23.5 + StationeersLaunchPad 0.5.1 已安装**；
  mods 目录：`.../compatdata/544550/pfx/drive_c/users/steamuser/Documents/My Games/Stationeers/mods/`。
- Proton 共用主机网络栈，mod 监听 `127.0.0.1:7800`，宿主机的 `ic10c` / VSCode **loopback 直连**。
- 需一次性安装 **.NET SDK**（本机尚无）才能编译 mod。

### 1.2 已确认的游戏 API（从 `Assembly-CSharp.dll` 读出）

真实芯片 `Assets.Scripts.Objects.Electrical.ProgrammableChip`：

| 成员 | 签名 | 用途 |
|---|---|---|
| `Execute` | `public void Execute(int)` | 单步入口（参数为指令预算；正常 tick 为 128） |
| `ReadMemory` | `public double ReadMemory(int)` | 读栈 / 内存槽 |
| `WriteMemory` / `ClearMemory` | public | 写 / 清内存 |
| `GetStackSize` | `public int` | 栈大小 |
| `SetSourceCode` / `GetSourceCode` | public | 设置 / 读取程序 |
| `get_LineNumber` | `public double` | 当前 PC / 行号 |
| `Reset` | `public void` | 复位 |
| `_Registers` | `private readonly double[]` | r0–r15 / ra / sp（反射读） |
| `_Stack` | `private readonly double[]` | 512 槽栈（反射读，或用 `ReadMemory`） |
| `_StackPointerIndex` / `_ReturnAddressIndex` / `_executeIndex` | private int | sp / ra / PC（反射读） |

- 全集/单步：`Assets.Scripts.Objects.Electrical.CircuitHolders` 的 `AllCircuitHolders` / `Execute()`。
- 主板：`ProgrammableChipMotherboard.InputFinished(string)` / `SetSourceCode` / `GetSourceCode`。
- 端口：`ICircuitHolder.GetLogicableFromIndex(int)` / `GetLogicableFromId` / `GetLogicBindings`。
- 暂停：`WorldManager.IsGamePaused`。

> 结论：**推代码、读寄存器 / 栈、手动单步**都有公开方法兜底，只有 `_Registers`/`_Stack`
> 需要反射。这也是本方案把「确定性单步」从高风险降为中风险的原因。

---

## 2. 总体架构

```text
┌───────────────────────────── 宿主机（Linux） ─────────────────────────────┐
│                                                                            │
│   editors/vscode  ──(net)──┐                                               │
│                           ▼                                                │
│   ic10c testbench ──► internal/testbench ──(NDJSON/TCP 127.0.0.1:7800)     │
│        │                        │                                          │
│        └─ build --json          └─ 复用 internal/vm 做差分                  │
│                                                                            │
└──────────────────────────────────┬─────────────────────────────────────────┘
                                   │ loopback（Proton 与宿主共用网络栈）
┌──────────────────────────────────▼─────────────────────────────────────────┐
│  游戏内 mod  tools/ingame-testbench（BepInEx + LaunchPad）                 │
│   BenchServer(TcpListener, NDJSON) ──► 主线程队列 ──► 游戏 API              │
│   chip 定位 / push / set / get / state / run / watch                       │
└────────────────────────────────────────────────────────────────────────────┘
```

- **编译器永远在宿主机**（mod 不内嵌 ic10c）；mod 只负责「把编译好的行写进芯片 + 跑 + 读状态」。
- **协议是唯一契约**：三方（mod / harness / VSCode）都只说 §4 的 NDJSON。

---

## 3. 目录结构

```text
tools/ingame-testbench/          # C# mod（StationeersLaunchPad 加载，无 Harmony）
  About/About.xml
  Ic10GoTestbench.csproj
  README.md
  TestbenchPlugin.cs             # 入口、配置(host/port/autoload/autoloadDelay)、主线程队列
  BenchServer.cs                 # TcpListener + NDJSON 编解码 + 连接管理
  BenchCommands.cs               # 命令实现（ping/chip.*/push/state/set/step/ports/pause/run/watch/world.*）
  GameApi.cs                     # 游戏 API 封装（含反射读私有字段 / 私有属性）
tools/dumpgameapi/main.go        # 从 Assembly-CSharp.dll 核对 API（-check）
internal/clr/clr.go              # 共享的 ECMA-335 reader（genenums + dumpgameapi）
internal/testbench/              # Go 客户端 + 场景模型
  client.go                      # NDJSON 客户端（请求/响应/事件）
  scenario.go                    # 场景 schema + 断言 + Runner
  client_test.go                 # 假服务器单元测试
cmd/ic10c/testbench.go           # ic10c testbench 子命令（含与 internal/vm 的差分）
editors/vscode/bench.js          # VSCode 侧客户端 + 视图 + 面板（无 npm 依赖）
editors/vscode/extension.js      # 注册命令 / 激活
editors/vscode/package.json      # commands / views / configuration 贡献点
testdata/bench/                  # 回归场景（counter / mem / ac / link + 对应 *.json）
```

---

## 4. 协议（NDJSON over TCP）

传输：TCP，UTF-8，**每行一个 JSON 对象**（`\n` 分隔）。mod 是服务端，harness / VSCode 是客户端。
地址默认 `127.0.0.1:7800`。

### 4.1 消息

```jsonc
// 连接后服务端先发（无 id）
{"event":"hello","mod":"ic10go-testbench","version":"0.1.0","gameVersion":"0.2.x","addr":"127.0.0.1:7800"}

// 请求
{"id":1,"cmd":"ping","args":{}}

// 成功
{"id":1,"ok":true,"result":{...}}

// 失败（code 稳定、不本地化）
{"id":1,"ok":false,"error":{"code":"no-chip","message":"no chip selected"}}

// 事件（无 id，仅订阅后发送）
{"event":"state","seq":42,"state":{...}}
```

### 4.2 命令

| cmd | args | result |
|---|---|---|
| `ping` | `{}` | `{version, gameVersion, paused, chips}` |
| `chip.list` | `{}` | `[{id, name, prefab, line, lines, registers}]` |
| `chip.select` | `{target:{id?|name?|index?}}` | `{chip}` |
| `push` | `{code, loaders?:[string], reset?:bool}` | `{chip, lines, loaders, compileError?}` |
| `state` | `{include?:["registers","stack","devices","program","errors"]}` | `state`（见 §4.3） |
| `set` | `{writes:[{port,logic,slot?,value}], force?:bool}` | `{applied:n}` |
| `get` | `{reads:[{port,logic,slot?}]}` | `{values:[...]}` |
| `run` | `{ticks:n, mode?:"step"|"realtime"}` | `{ticks, line}` |
| `step` | `{ticks:n}` (alias of `run`) | `{ticks, line}` |
| `ports` | `{chip?}` | diagnostic: `Devices[]`, ids, labels, lookups |
| `reset` | `{}` | `{ok:true}` |
| `pause` | `{on:bool}` | `{paused:bool}` |
| `watch` | `{on:bool, include?:[...], all?:bool}` | `{watching:bool}` |
| `world.saves` | `{}` | `{saves:[name,...]}` |
| `world.load` | `{save:name}` | `{result:"..."}` |
| `world.state` | `{}` | `{state, world, paused}` |

> `state` also carries `paused` so a UI can show Pause/Resume. Save loading uses the
> game's own `loadgame` console command (`Util.Commands.CommandLine`).

> 端口设备用 `CircuitHousing.Devices[portIndex]`（已确认）；`ICircuitHolder.GetLogicableFromIndex`
> 返回的是 `CableNetwork`，不是物理设备。`set` 默认校验 `CanLogicWrite`，`force:true` 跳过。

错误 code：`bad-request`、`no-chip`、`no-device`、`unknown-logic`、`compile-error`、
`not-paused`（`mode:"step"` 需先暂停）、`internal`。

### 4.3 state 结构

```jsonc
{
  "chip": {"id":3,"name":"TestChip"},
  "registers": {"r0":1,"r1":0,"ra":0,"sp":4},   // 仅 include 时出现
  "stack": {"size":512,"sp":4,"values":{"0":10,"1":20,"2":30,"3":40}},
  "pc": 12, "line": 13,
  "devices": [
    {"port":"d0","prefab":"StructureLogicDisplay","logic":{"Setting":105}},
    {"port":"d1","prefab":"StructureLogicDial","logic":{"Setting":10}}
  ],
  "program": {"lines":37,"current":13},
  "errors": {"code":"","line":-1}
}
```

- 栈默认只回 `0..sp`（外加少量上下文），避免一次传 512 个值；`--all` 可要全量。
- `registers` 里 `r0..r15`、`ra`、`sp` 分别来自 `_Registers[0..15]`、`_Registers[16]`、
  `_Registers[17]`（读不到时用 `_ReturnAddressIndex`/`_StackPointerIndex` 兜底）。

---

## 5. 游戏内 mod 设计

- **入口**：照抄 exporter——`OnLoaded(List<Assembly>)` + `Awake()` 兜底，`_initialized` 去重。
- **配置**：`Documents/My Games/Stationeers/ic10go/testbench.json` 的 `host` / `port` /
  `autoload`（启动后自动载入的存档名）/ `autoloadDelay`（秒，默认 90）。载入走
  `LoadHelper.LoadGame(path, station)`（与主菜单「载入最新」同路径），**不是** `loadgame`
  控制台命令（后者参数是世界 id，如 `Mars2`）。`autoloadDelay` 必须晚于
  `GameManager.Start`，否则会崩。
- **不需要 Harmony**（v1）：只用公开 API + 反射读私有字段；`Execute(int)` 用于单步。
  若真机发现暂停时游戏仍自行 tick 导致重复执行，再在 M5 加 Harmony 门控。
- **线程**：`BenchServer` 在后台线程收请求，入队；`TestbenchPlugin.Update()` 在主线程出队执行，
  结果回写并发回。所有游戏 API 只在主线程调用。
- **chip 定位**：遍历 `CircuitHolders.AllCircuitHolders`（`ICircuitHolder`），拿 `ProgrammableChip`；
  名字用挂载的 `CircuitHousing` / `Thing` 的昵称或预制体名。`chip.select` 设默认。
- **push**：`Reset()` → `SetSourceCode(code)`（主板则 `InputFinished`）→ 先逐个跑 `loaders`
  （每块写完 `Execute` 到结束）→ 再写 runtime。
- **set/get**：`GetLogicableFromIndex(port)` → 按 logic 名读 / 写；logic 名到 `LogicType`
  用内置表（与编译器同一份枚举）。槽位走 `slot` 参数。
- **state**：反射读 `_Registers` / `_Stack` / `_StackPointerIndex` / `_ReturnAddressIndex`，
  `get_LineNumber()`，`GetSourceCode()`；设备值经 `ICircuitHolder.GetLogicBindings()` 或逐端口读。
- **run**：`mode:"step"` 时确认 `WorldManager.IsGamePaused`，循环 `chip.Execute(128)` N 次；
  `mode:"realtime"` 时确保未暂停，按 tick 计数等待。
- **watch**：在 `Update()` 里按节流周期发 `state` 事件。

---

## 6. Go harness 设计（`ic10c testbench`）

```text
ic10c testbench list                       # 列出芯片
ic10c testbench ping                        # 连接自检
ic10c testbench push <file.icg> [--chip N] [--as NAME]  # 编译 + 上传（多芯片用 --as 选块；自动先跑 loader）
ic10c testbench state [--chip N] [--all] [--json]
ic10c testbench set d1.Setting=10 [...]      # 设置输入（--force 跳过只读校验）
ic10c testbench step [N]                     # 推进芯片 N 个 tick（暂停下）
ic10c testbench ports                        # 诊断：端口/设备接线映射
ic10c testbench pause [on|off]               # 暂停 / 恢复游戏（确定性）
ic10c testbench saves                        # 列出存档
ic10c testbench load <name>                  # 载入存档（游戏 loadgame 命令）
ic10c testbench world                        # 游戏状态 / 当前世界 / 暂停
ic10c testbench run <scenario.json> [--diff] # 跑场景并断言
ic10c testbench watch [--json]               # 持续打印 state 事件
```

连接地址：`--addr 127.0.0.1:7800` 或环境变量 `IC10_BENCH_ADDR`。

### 6.1 场景文件（JSON）

```jsonc
{
  "program": "rel.icg",
  "args": ["--rel-jump"],              // 传给 ic10c build
  "chip": "TestChip",                  // 可选：选择芯片
  "ports": {"d0":"LED","d1":"Dial"},   // 可选：喂给 VM 差分
  "reset": true,
  "cases": [
    {
      "name": "knob 0..2",
      "set": {"d1.Setting": 0},
      "run": 5,
      "expect": {"d0.Setting": 105},
      "expectReg": {"r0": 105}         // 可选
    }
  ]
}
```

### 6.2 差分（真机 vs VM）

`--diff`：同一份编译产物 + 同一 `ports` 映射，在 `internal/vm` 里跑相同 `set/run`，
逐 case 比较 `expect` 项与设备写入序列。用途有二：

1. 验证真机与 VM 语义一致（发现 VM 偏差）；
2. 验证优化不改变行为（回归安全网）。

### 6.3 退出码

`0` 全通过；`1` 有断言失败；`2` 连接 / 编译 / IO 错误。输出人读摘要 + `--json` 机器格式。

---

## 7. VSCode 扩展设计（易用 + 美观）

目标：**零 npm 依赖**（与仓库现状一致，用内置 `net`），观感完全用 VS Code 原生主题变量，
在浅色 / 深色 / 高对比下都好看。

### 7.1 信息架构

```text
Activity Bar「IC10」
└─ Testbench 视图（TreeView）
   ├─ ● Connected  127.0.0.1:7800          （绿=已连接，灰=断开；点击连接/断开）
   ├─ TestChip        d0 LED · d1 Dial     （选中芯片）
   │  ├─ Registers    r0..r15, ra, sp
   │  ├─ Stack        sp 高亮，0..sp
   │  └─ Devices      d0.Setting = 105
   └─ LED             d0                     （同网络其它可逻辑设备，可选）
```

- 状态栏项（右下）：`$(circuit-board) IC10: TestChip`，已连接绿色；点击弹 QuickPick
  （Connect / Push / Open Panel / Refresh）。
- 编辑器标题栏（`.icg`）：`$(cloud-upload)` 上传、`$(beaker)` 跑测试台。
- 图标统一用 codicon（`$(circuit-board)`/`$(symbol-number)`/`$(database)`/`$(server-process)`），
  颜色用 `ThemeIcon` + `ThemeColor`（`testing.iconPassed` / `charts.orange` / `descriptionForeground`）。

### 7.2 命令

| 命令 | 标题 | 入口 |
|---|---|---|
| `icg.bench.connect` | IC10: Connect to Game | 视图 / 状态栏 |
| `icg.bench.disconnect` | IC10: Disconnect | 视图 |
| `icg.bench.push` | IC10: Upload to Game | 标题栏、`Ctrl+Alt+U` |
| `icg.bench.refresh` | IC10: Refresh State | 视图标题 |
| `icg.bench.watch` | IC10: Toggle Live Updates | 视图标题 |
| `icg.bench.runScenario` | IC10: Run Testbench | 标题栏、`Ctrl+Alt+T` |
| `icg.bench.openPanel` | IC10: Open Chip Panel | 状态栏 / 命令面板 |
| `icg.bench.pause` | IC10: Pause / Resume Game | 视图标题、面板按钮 |
| `icg.bench.loadSave` | IC10: Load Save | 视图标题（快速选择存档） |
| `icg.bench.selectChip` | IC10: Select Chip | 树（点击某块 host） |
| `icg.bench.setDevice` | IC10: Set Device Value | 树（点击某个 logic 值） |

### 7.3 Webview「Chip State」面板

- 顶栏：芯片名 + 连接圆点 + `Pause | Watch | Refresh`（`Pause` 在实时更新旁，暂停时变 `Resume` 并高亮）。
- **Registers**：网格（r0–r15 / ra / sp），等宽数字；值变化时短暂高亮（绿色淡出）。
- **Stack**：默认**折叠**（`<details>`），展开后显示全部 512 槽，`sp` 行加色条。
- **Devices**：每个设备一张**可折叠卡片**（`db` + `d0..d5`，空端口灰显 `empty`），带绑定标签与该设备的 logic 数量；展开看全部 logic，避免 `db` 那种几十条一次铺开。点击树里的 logic 可改输入。
- **Program**：当前行 + `line/total`。
- 全部用 `var(--vscode-*)` 变量、`--vscode-editorWidget-border` 描边、`--vscode-textCodeBlock-background`
  底色，无第三方 CSS；禁用内联脚本，`webview.cspSource` 白名单。

### 7.4 配置

| 设置 | 默认 | 说明 |
|---|---|---|
| `icg.bench.host` | `127.0.0.1` | mod 地址 |
| `icg.bench.port` | `7800` | mod 端口 |
| `icg.bench.autoConnect` | `true` | 激活时自动连 |
| `icg.bench.refreshInterval` | `0` | 自动刷新毫秒（0=手动） |
| `icg.bench.watchOnOpen` | `false` | 打开面板即开实时 |

### 7.5 交互细节（易用性）

- 未连接时视图显示一个 `$(plug)` 项，点击即连；连接失败给出可点击「重试 / 查看设置」。
- `push` 成功用 `setStatusBarMessage` 显示 `✓ 37/128 lines`；编译失败复用现有诊断路径。
- `runScenario` 结果用 Webview 报告（每 case ✓/✗ + 期望/实际）。
- 所有错误走已有的 `IC10 Go` 输出面板。
- `Pause` 按钮走游戏自身暂停流程，恢复后输入/光标正常，不用再按 F1。
- 「Load Save」从 `world.saves` 快速选择并调用 `LoadHelper.LoadGame`；mod 侧 `autoload` 可启动即进存档。

---

## 8. 版本兼容策略

- 游戏更新后用 `tools/dumpgameapi` 重新核对 `ProgrammableChip` / `ProgrammableChipMotherboard` /
  `CircuitHolders` 的字段与方法；名称变化在编译期或启动期立刻暴露。
- mod 启动时把关键 API 探测结果写日志；`ping.result` 带 `gameVersion`，harness 可据此告警。
- 反射读私有字段集中在 `GameApi.cs`，出问题只改一处。

---

## 9. 里程碑与进展

| 里程碑 | 内容 | 状态 |
|---|---|---|
| **M1 探针** | `dumpgameapi`；mod 起服务 + `ping`/`chip.list`/`push`/`state` | ✅ |
| **M2 完整 state** | 寄存器 / 栈 / PC / 错误 / 设备（含 `db` 宿主） | ✅ 真机验证 |
| **M3 Harness** | 场景 schema + `testbench run` + `internal/vm` 差分 | ✅（差分按 tick/yield 建模） |
| **M4 VSCode** | 命令 + 状态栏 + TreeView + Webview 面板 + 配置 | ✅ |
| **M5 增强** | 暂停下单步（`Execute`）、`pause`、`world.*`（列存档/载入/autoload） | ✅ |
| **M6 待办** | 多芯片场景文件（一个 scenario 驱动多块芯片）、场景自动 spawn | ⏳ |

---

## 10. 风险

| 风险 | 级别 | 缓解 |
|---|---|---|
| 暂停时游戏仍自行 tick，单步重复执行 | 中 | 先只做 `set/run/read`；M5 用 Harmony 门控 |
| 私有字段名随游戏更新变化 | 低 | `dumpgameapi` + `GameApi.cs` 单点 |
| 设备 logic 名 → enum 映射不全 | 低 | 复用 `internal/builtin` 同一份表 |
| mod 在 Proton 下绑定失败 | 低 | 配置可改 host/port；回退写文件模式 |
| 用固定存档，场景不可移植 | 低 | v1 明确接受；M5 再 spawn |
| 编译 mod 需 .NET SDK | 低 | 一次性 `dotnet-install.sh`（走代理） |

---

## 11. 真机验证记录（2026-09-26）

在本机 Proton（BepInEx 5.4.23.5 + StationeersLaunchPad 0.5.1，游戏 `0.2.6428.27798`）
上用两个 IC 封装（`A CHIP` / `B CHIP`，d0=LED、d1=Logic Dial、d5 互连）验证：

| 项 | 结果 |
|---|---|
| mod 加载 / 监听 127.0.0.1:7800（Proton 共用主机网络） | ✅ |
| `ping` / `chip.list` / `state`（寄存器/栈/PC/设备） | ✅ |
| 从封装取芯片：`CircuitHousing.Devices[port]`（`GetLogicableFromIndex` 返回 `CableNetwork`，不可用） | ✅ |
| `push` 单芯片（`testdata/bench/counter.icg`） | ✅ |
| `pause on` + `set d0.Setting=100` + `step N` + 读回（100→110） | ✅ |
| 场景 `run` 断言成功；`--diff` 与 VM 一致（VM 已按 tick/yield 建模） | ✅ |
| 多芯片：`push --as A/--as B`，A↔B 走 d5 通道，锁步 8 tick 后 B 的 LED = 7 | ✅ |
| `WorldManager.SetGamePause(true)` 确实停住逻辑 | ✅ |
| 可写输入 `Logic Memory`（d3）：`set d3.Setting=42` 读回 42，程序镜像到 d0 | ✅ |
| 设备 host（空调）：`db` 伪端口读到空调自身逻辑，`set db.On=1` 生效 | ✅ |
| `state` 全量栈（`all:true`）：512 槽 | ✅ |
| 暂停恢复可操作性：改用 `InputSourceCode.PauseGameToggle`（反射），不再直接写 `IsGamePaused` | ✅（用户确认） |
| 自动载入存档：`testbench.json` 的 `autoload`+`autoloadDelay`，走 `LoadHelper.LoadGame`，真机免手动进存档 | ✅ |
| 芯片放在槽位里的 host（AdvancedSuit/HardSuit）：从 `ChipSlot.Occupant` 解析，`state`/`push` 可用（`Slot.Get()` 有歧义） | ✅ |
| 固定端口绑定（宇航服/平板）：`GetLogicableFromIndex` 返回绑定设备（`db`/`d0..d5` = SUIT/HELMET/BACKPACK/…），不再误用 `Devices[]` 顺序；`state` 带 `binding` 标签 | ✅ |
| 场景 `mem.json`（memory→LED，两个 case）+ `--diff` | ✅ 真机与 VM 都通过 |
| 按钮 `Logic Button`（d2）：`Setting` 只读，随物理按下变化 | 只读（观察用） |

**已知限制**

- **物理输入设备不能用 `set` 驱动**：Logic Dial 的 `Setting`、Logic Button 的 `Setting`
  由物理旋钮/按键决定，写入不报错但读回不变。测试输入请用 **Logic Memory**（`Setting` r/w，
  已真机验证）、**Logic Switch**，或芯片间 **bus 通道**（见 `testdata/bench/link.icg`）。
  LED 等输出设备的 `Setting` 写入正常。`set --force` 可跳过 `CanLogicWrite` 校验。
- `state.registers` 里 `r0..r15` 来自 `_Registers`；`ra`/`sp` 在数组长度恰好 18 时取
  `_Registers[16]/[17]`，否则回退到 `_ReturnAddressIndex`/`_StackPointerIndex`。
  真机上 `_Registers` 长度为 18、`_StackPointerIndex` 为栈指针；字段语义已在
  `GameApi.BuildState` 注释中记录，后续按需修正标签。
- **设备 host**（如空调）：程序用 `db` 控制宿主本身。`state` 会列出 `db` 设备；`set db.On=1`
  等生效。给设备 host 推送带 `data` 表的程序要用 `--data-access stack`。
- **暂停/恢复**走游戏自己的 `InputSourceCode.PauseGameToggle(bool)`（社区 IC10 编辑器同款），
  避免只写 `WorldManager.IsGamePaused` 导致恢复后输入卡死。
- **多芯片**：`chip.list` 返回全部 host；`push --as` 选编译块；多芯片场景文件尚未实现。
