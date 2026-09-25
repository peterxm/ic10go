# IC10 Go — VSCode 扩展

为 **`.icg`**（ic10c 的类 Go 语言）提供完整的编辑体验，也为 **`.ic` / `.ic10`**（原生 IC10 汇编）提供高亮与工具命令。

扩展用**纯 JavaScript** 编写，**无需 `npm install`**：它启动 `ic10c lsp` 并与之通信，所有分析（诊断、补全、文档、语义高亮等）都由编译器完成，所以扩展本身很薄。

> 界面文案与函数文档**跟随 VSCode 显示语言**，内置**简体中文**。

## 一、特性一览

| 能力 | 说明 |
|------|------|
| 语法高亮 | `.icg` 专用语法；`.ic`/`.ic10` 有 IC10 汇编语法 |
| 语义高亮 | 语义 token：函数 / 变量 / 设备端口 / logic type / 内建 / 枚举 |
| 诊断 | 打开、编辑时实时显示编译错误，**含 128 行 / 4KiB / 90 字符超限**；`.ic`/`.ic10` 检查编辑器上限、未知指令与重复标签 |
| 补全 | **上下文感知** + 签名与文档（见下）；`.ic`/`.ic10` 补全指令 / 逻辑类型 / 批量模式 |
| 悬停文档 | 内建函数**签名 + 用法说明**、逻辑类型含义、枚举成员（如 `Color.Purple = 11`、`DisplayMode.Percent`）、关键字与底层原语、**变量/表达式的类型**；`.ic`/`.ic10` 显示指令说明；**预制体名 / 数字 hash 反查** |
| 预制体 | `hash("…")` / `HASH("…")` 内补全 1900+ 预制体名；**hash 型实参处**（`batch.*` 的 typeHash、`sorter.filterPrefabHash`、`printer.executeRecipe` 等）直接选预制体名即插入 `hash("…")`；悬停数字 hash 显示预制体名与标题 |
| 大纲 / 折叠 | 文档符号（函数、常量、变量、标签）+ 代码折叠；`.ic`/`.ic10` 显示标签大纲 |
| 跳转定义 | `F12` / `Ctrl+点击`；符号若声明在 `import` 的文件里，会跳到那个文件 |
| 查找引用 / 重命名 | `Shift+F12` / `F2`（单文件内所有引用） |
| 同名高亮 | 光标处标识符的所有出现位置高亮 |
| 智能扩选 | `Shift+Alt+→` 从标识符扩到行、代码块、整个文件 |
| 跨文件符号 | `Ctrl+T` 搜索**工作区内所有 `.icg`**（含未打开）的函数 / 常量 / 变量 |
| 参数提示 | 函数调用时显示签名，高亮当前参数 |
| 快速修复 | 未知 logic/slot type 与枚举成员的"你是不是想写…"（标为 preferred）；找不到的 `import` 一键**创建该文件**；缺 `main` 一键补上 |
| 类型提示 | 变量声明后显示 inlay hint 类型（`: num` / `: bool` / `: device` / `: data`） |
| 颜色 | `Color.Red` 等枚举成员显示颜色色块，点选可换色 |
| 代码透镜 | 每个函数上方显示 **Compile to IC10** |
| 文档链接 | 函数调用与 `data` 表引用可点击跳到声明行；`import "…"` 的路径可点击打开目标文件；`hash("…")` / `HASH("…")` / 数字预制体 hash 可点击打开社区 Wiki |
| 预算常驻 | **状态栏**实时显示当前 `.icg` 的 `行/字节/行长/寄存器/栈` 预算（含数据段 `data a..b`、用户栈 `stack 个数/上限`、一次性装载器 `loader N` 行；悬停显示用户/编译器分区、最高槽位与 `push`/手动 `db.stack[]`/数据段/溢出槽；点击即编译）；文件末尾另有 inlay hint |
| 栈边界 | 用户栈上限默认固定为 `icg.userStack`（默认 128），用户槽位越界编译报错；开启 `icg.dynamicStack` 后按编译器实际数据段/溢出占用动态确定上限 |
| 栈私有 | 单芯片默认 `// icg: private-stack`：允许把常量用户栈槽提升为寄存器、消除成对 `push`/`pop`，让 runtime 更短；多芯片默认 `// icg: shared-stack`（保守）。文件里写 pragma 可覆盖 |
| 格式化 | `Shift+Alt+F`；`[icg]` 默认**保存时格式化**（可在设置中关闭）；`.ic`/`.ic10` 重排空白并对齐指令列 |
| 数据段 | 命令 **“IC10 Go: 编译为 IC10”** 会自动识别 `data` 表：把一次性「安装代码」复制到剪贴板、并在旁边打开运行代码；`data` 表 / `switch ... table` 有语法高亮与补全。编译器还会把序言里的一次性设置写入（`Mode`/`On`/常量 `Setting`）自动外提到同一份安装代码：**超 128 行时**用来塞进预算，**本来就有 `data` 表时**顺带复用、让 runtime 更小。安装代码超过 128 行时自动拆成多块，扩展会逐块复制并提示继续 |
| 多芯片 | 一个 `.icg` 用 `chip 名字 { ... }` 声明多块芯片（各自 128 行预算 / loader），用 `bus 名字 { 槽位 }` + `use 名字 on dev:conn`（默认访问点）或 `Bus.槽位[dev][conn]`（内联覆盖）声明通道；`chip`/`bus`/`use` 有语法高亮、语义高亮与片段；`Bus.` 补全槽位、悬停显示通道号。「编译为 IC10」多芯片时弹出芯片选择，再预览/复制该芯片的运行代码与安装代码；状态栏显示芯片数与各芯片预算的较大值 |
| 片段 | `main`、`hyst`、`pid`（软件 PID）、`piddev`（硬件 PID 控制器配置）、`batchread`、`batchwrite`、`readlt`、`writelt`、`readdev`、`writedev`、`readbyid`、`writebyid`、`readdevslot`、`stackread`、`stackwrite`、`sorterfilter`、`printerexec`、`data`、`datagen`（编译期表推导）、`import`、`func2`（多返回值）、`multi`（多重赋值）、`constfn`（编译期常量函数）、`ternary`/`?:`（三目）、`str`（显示字符串）、`strcat`（编译期字符串拼接）、`hash`（预制体哈希）、`switchtable`、`chip`（多芯片）、`bus`（命名通道）、`func`、`const`、`slotread` 等 |
| 新语法 | `import "路径"` **跟随导入**：被导入文件的函数/常量在诊断、补全、跳转里解析；多返回值 `func f() (num, num)` + `x, y := f()`；`data T = [expr for i in lo..hi]` 编译期表推导；`const K = helper(3)` 纯函数折叠、`hash("a" + "b")` 编译期字符串——均有高亮、语义高亮、诊断与片段 |
| 导入补全 | 在 `import "` 后补全当前目录与子目录的 `.icg` 文件；改动磁盘上任意 `.icg` 会重新分析打开的文档，因此编辑被导入文件会刷新导入它的文件；被导入文件里的错误会标在**该文件**上（而不是导入它的文件），`import` 路径可悬停查看解析到的文件、可点击打开 |

**上下文感知补全**：
- `import "` → 当前目录与子目录里的 `.icg` 文件
- `d0.` → 该端口的 logic type；`d0.slot[0].` → 槽位属性
- `all(Prefab).` → **该预制体自己的属性**（标注 `r`/`rw`/`w`；目录来自游戏内扫描，见仓库 `docs/device-catalog.md`）
- `batch.` → `read` / `readName` / `readSlot` / `write` …
- `sorter.` / `printer.` → 设备栈指令构建器（`filterSortingClass`、`executeRecipe` …）
- `SorterInstruction.` / `PrinterInstruction.` / `TraderInstruction.` / `ConditionOperation.` / `LogicReagentMode.` / `SorterStack.` / `PrinterStack.` / `LogicType.` / `LogicSlotType.` / `LogicBatchMethod.` / `DisplayMode.` / `Sound.` / `Color.` / `PowerMode.` / `AirCon.` / `GasType.` / `RobotMode.` / `ShuttleType.` 等 → 枚举成员（补全项由 `EnumConstants` 自动派生）
- 调用实参按位置 → `batch.*` 的 typeHash 补预制体（插入 `hash("…")`）、logic 补 logic type、mode 补批量模式；`sorter.filterPrefabHash` / `printer.executeRecipe` / `printer.ejectReagent` / `rmap` / `readReagent` 的 hash 实参补预制体；`read`/`write`/`readById`/`writeById`/`readDev`/`writeDev` 的 logic 实参补 logic type
- 原生 IC10（`.ic`/`.ic10`）按操作数类型 → `sb`/`sbn`/`lb`/`lbn`/`lbs`/`lbns` 的 `DEVICE_TYPE` 补预制体（插入 `HASH("…")`）、`LOGIC_TYPE` / `BATCH_MODE` / `SLOT_LOGIC_TYPE` 补对应名字
- 普通位置 → 当前文件的函数 / 常量 / 变量 / 标签 + 内建 + 设备端口

**文档**（悬停与补全项）示例：
- `sqrt` → `sqrt(x)` — 平方根 / Square root
- `clamp` → `clamp(x, lo, hi)` — 把 x 限制在 [lo, hi]
- `Temperature` → 温度（开尔文）
- `func` → 编译期内联 / 外提，不支持递归

## 二、安装（推荐，一条命令）

在仓库根目录执行：

```bash
sh editors/vscode/install.sh
```

它优先用 `code` CLI 安装 `.vsix`（会同步更新 VSCode 的扩展注册表，避免旧版本
指向已删除目录）；`code` 不可用时才回退为复制到
`~/.vscode/extensions/ic10go.icg-<版本>/`（版本读取自 `package.json`）。

然后按 `Ctrl+Shift+P` → **Developer: Reload Window** 重启 VSCode。

> 回退复制**不会**更新注册表：若之前装过旧版本，请先在扩展视图卸载
> `ic10go.icg`，或用 `code --install-extension <vsix> --force` 安装。

## 三、准备 `ic10c`

扩展需要 `ic10c` 来提供全部语言功能。编译它：

```bash
go build -o ic10c ./cmd/ic10c
```

扩展按以下顺序查找 `ic10c`（找到即用）：

1. 设置 `icg.serverPath`（绝对路径）
2. 工作区文件夹及其**各级父目录**里的 `ic10c`
3. 当前文件所在目录及其**各级父目录**里的 `ic10c`
4. `$GOPATH/bin`、`~/go/bin`、`~/bin`、`/usr/local/bin`
5. 系统 `PATH`

最省事（任选其一）：用 VSCode 打开本仓库并在根目录构建 `ic10c`；或把它放进 `PATH`：

```bash
go build -o ~/.local/bin/ic10c ./cmd/ic10c
```

找不到时会弹提示，点 **Open Settings / 打开设置** 填 `icg.serverPath` 即可。

## 四、验证

新建 `demo.icg`：

```go
func main() {
    for {
        yield()
        d0.On = d1.Temperature > 300
    }
}
```

1. 应有语法高亮。
2. 故意写错（如 `x := foo`）应报 `undefined variable "foo"`。
3. 输入 `d0.` 补全 logic type；输入 `batch.` 补全批量方法；悬停 `sqrt` / `Temperature` 看文档。
4. 命令面板执行 `IC10 Go: 编译为 IC10`，旁边打开产物，输出面板显示预算。

## 五、命令

命令面板（`Ctrl+Shift+P`）或编辑器右键菜单（`.icg` 内可用快捷键 `Ctrl+Alt+B` 编译、`Ctrl+Alt+R` 运行）：

| 命令 | 说明 |
|------|------|
| `IC10 Go: Compile to IC10 (data loader included)` | 编译 `.icg`，旁边预览 IC10 产物 + 行/字节预算。若程序含 `data` 表，自动把「安装代码」复制到剪贴板并提示两步操作（先运行安装代码，再用预览中的运行代码覆盖） |
| `IC10 Go: Run in VM` | 在内置 VM 中运行 `.icg` 并显示设备状态 |
| `IC10 Go: Decompile IC10 to .icg` | 把 `.ic`/`.ic10` 反编译为 `.icg`（结构化） |
| `IC10 Go: Minify IC10` | 压缩 `.ic`/`.ic10` 行数 |
| `IC10 Go: Annotate IC10 (disasm)` | 给 `.ic`/`.ic10` 加跳转目标注释 |
| `IC10 Go: Show CFG (Mermaid)` | 在 Markdown 预览中显示 `.icg` 的**源码级控制流图**（Mermaid，按函数分组；条件/分支/循环/break/continue 一目了然） |
| `IC10 Go: Restart Language Server` | 重启语言服务器 |

## 六、设置

| 设置 | 默认 | 说明 |
|------|------|------|
| `icg.serverPath` | `""` | `ic10c` 可执行文件路径；留空则自动查找 |
| `icg.stableIns` | `false` | 编译/运行时加 `--stable-ins`（稳定版 `ins` 参数顺序） |
| `icg.noCheck` | `false` | 关闭设备 logic type 校验（`IC10C_NO_CHECK`） |
| `icg.autoTable` | `false` | 编译时加 `--auto-table`：自动表化符合条件的常量 `switch`（需先跑安装代码） |
| `icg.jumpTable` | `false` | 编译时加 `--jump-table`：把稠密整数 `switch` 降为计算跳转表 |
| `icg.relJump` | `false` | 编译时加 `--rel-jump`：生成相对跳转（`jr` / `br*`）以省字节（需真机验证） |
| `icg.fast` | `false` | 编译时加 `--fast`：优先速度而非体积（展开更多循环） |
| `icg.unsafe` | `false` | 编译时加 `--unsafe`：跳过数据段运行时校验以进一步压缩代码 |
| `icg.dataLayout` | `top` | 数据段位置 `--data-layout`：`top`（默认）或 `middle` |
| `icg.dataAccess` | `get` | 数据段安装代码访问芯片栈的方式：`get`（标准 IC host）/ `stack`（设备 host，如空调） |
| `icg.maxLines` | `128` | IC10 行数上限（`IC10C_MAX_LINES`）；游戏上限变化时上调 |
| `icg.maxBytes` | `4096` | IC10 字节数上限（`IC10C_MAX_BYTES`） |
| `icg.maxLine` | `90` | IC10 每行字符数上限（`IC10C_MAX_LINE`） |
| `icg.dynamicStack` | `false` | 用户栈上限按编译器实际数据段/溢出占用动态计算，而非 `icg.userStack` 固定值 |
| `icg.userStack` | `128` | 固定的用户栈大小；用户使用达到或超过它的槽位会在编译时报错 |
| `icg.redundantDeviceWrites` | `false` | 删除重复的同值常量设备写（省行，但改变可观测写序列） |
| `icg.mergeRenamedTails` | `false` | 在可证明安全时额外合并寄存器分配不同但结构相同的尾块（实验性） |
| `icg.libDirs` | `[]` | 额外的 `import` 搜索目录（在导入文件所在目录之后查找）；相对路径以第一个工作区目录为基准。同时作用于语言服务器与编译/运行 |
| `icg.trace.server` | `off` | 设为 `messages` 时在 `IC10 Go` 输出通道记录与语言服务器之间的消息 |
| `icg.runSteps` | `0` | VM 运行步数上限（`--steps`）；`0` 用编译器默认 1000 |
| `icg.runTrace` | `false` | VM 运行时打印每条执行的指令（`--trace`） |
| `icg.runSet` | `[]` | VM 运行前设置设备值，每项一条，如 `d0.Temperature=350`（可重复 `--set`） |

## 七、文件类型

| 扩展名 | 语言 | 说明 |
|--------|------|------|
| `.icg` | `icg` | ic10c 源码，拥有全部语言功能 |
| `.ic` / `.ic10` | `ic10` | 原生 IC10 汇编，高亮 + 标签大纲 + 编辑器上限诊断 + 反编译/压缩/注释命令 |

## 八、常见问题

- **没有高亮**：确认扩展出现在扩展列表（搜索 `IC10 Go`），并已重启 VSCode。
- **没有诊断/补全/悬停**：多半是找不到 `ic10c`。设置 `icg.serverPath`，或在仓库根目录构建 `ic10c`。
- **悬停一直显示"正在加载"**：旧版扩展/编译器的已知问题，已在 0.5.4 修复——请更新 `ic10c` 与扩展后重载。
- **含 `data` 表的程序停机 / 行为不对**：数据段要先安装——点“IC10 Go: 编译为 IC10”，扩展会把「安装代码」复制到剪贴板：先贴入 IC 运行一次，再用旁边预览里的运行代码覆盖；设备 host（空调等）需改用 `--data-access stack` 生成。
- **始终报 `no main function found`**：确认文件确实有 `func main()`；若文件带 UTF-8 BOM，旧版编译器会解析失败（0.5.4 起已支持 BOM）。
- **改了编译器后行为没变**：扩展会在启动时比较运行中的 server 版本与磁盘上的 `ic10c`，不一致会自动重启；也可手动运行 `IC10 Go: Restart Language Server`。
- **查看日志**：输出面板（`Ctrl+Shift+U`）选择 `IC10 Go`。

## 九、工作原理

扩展启动 `ic10c lsp`（一个最小的 Language Server），通过 stdio 上的 JSON-RPC 通信：

- 文档同步：打开/关闭 + **全量**变更（防抖 150ms）
- 服务器提供：`publishDiagnostics`、补全、格式化、hover、定义、大纲、折叠、引用、重命名、参数提示、快速修复、语义 token、inlay hint
- 扩展把这些注册成 VSCode 的对应 Provider

因此**编译器一更新，编辑器能力随之更新**，无需改扩展。

## 十、打包成 .vsix（可选）

```bash
cd editors/vscode
npx @vscode/vsce package --allow-missing-repository
# 生成 icg-<版本>.vsix
code --install-extension icg-<版本>.vsix
```

`vsce` 需要联网下载，首次会慢一些。

## 十一、手动安装

把 `editors/vscode` 整个目录复制到：

```
~/.vscode/extensions/ic10go.icg-<版本>/
```

目录需包含 `package.json`、`package.nls.json`、`package.nls.zh-cn.json`、`extension.js`、`language-configuration.json`、`syntaxes/`、`snippets/`，然后重启 VSCode。
