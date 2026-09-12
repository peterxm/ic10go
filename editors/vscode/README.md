# IC10 Go — VSCode 扩展

为 **`.icg`**（ic10c 的类 Go 语言）提供完整的编辑体验，也为 **`.ic` / `.ic10`**（原生 IC10 汇编）提供高亮与工具命令。

扩展用**纯 JavaScript** 编写，**无需 `npm install`**：它启动 `ic10c lsp` 并与之通信，所有分析（诊断、补全、文档、语义高亮等）都由编译器完成，所以扩展本身很薄。

> 界面文案与函数文档**跟随 VSCode 显示语言**，内置**简体中文**。

## 一、特性一览

| 能力 | 说明 |
|------|------|
| 语法高亮 | `.icg` 专用语法；`.ic`/`.ic10` 有 IC10 汇编语法 |
| 语义高亮 | 语义 token：函数 / 变量 / 设备端口 / logic type / 内建 / 枚举 |
| 诊断 | 打开、编辑时实时显示编译错误，**含 128 行 / 4KiB / 90 字符超限** |
| 补全 | **上下文感知** + 签名与文档（见下） |
| 悬停文档 | 内建函数**签名 + 用法说明**、逻辑类型含义、关键字与底层原语 |
| 大纲 / 折叠 | 文档符号（函数、常量、变量、标签）+ 代码折叠 |
| 跳转定义 | `F12` / `Ctrl+点击` |
| 查找引用 / 重命名 | `Shift+F12` / `F2`（单文件内所有引用） |
| 参数提示 | 函数调用时显示签名，高亮当前参数 |
| 快速修复 | 未知 logic/slot type 的"你是不是想写…"；缺 `main` 一键补上 |
| 预算常驻 | **状态栏**实时显示当前 `.icg` 的 `行/字节/行长/寄存器` 预算（含数据段 `data a..b`；点击即编译）；文件末尾另有 inlay hint |
| 格式化 | `Shift+Alt+F`（或保存时）调用 `ic10c fmt` |
| 数据段 | 命令 **“IC10 Go: 编译为 IC10”** 会自动识别 `data` 表：把一次性安装代码复制到剪贴板、并在旁边打开运行代码；`data` 表 / `switch ... table` 有语法高亮与补全 |
| 片段 | `main`、`hyst`、`batchread`、`batchwrite`、`readlt`、`writelt`、`readdev`、`writedev`、`data`、`switchtable`、`func`、`const`、`slotread` 等 |

**上下文感知补全**：
- `d0.` → 该端口的 logic type；`d0.slot[0].` → 槽位属性
- `batch.` → `read` / `readName` / `readSlot` / `write` …
- `SorterInstruction.` / `LogicType.` → 枚举成员
- 普通位置 → 当前文件的函数 / 常量 / 变量 / 标签 + 内建 + 设备端口

**文档**（悬停与补全项）示例：
- `sqrt` → `sqrt(x)` — 平方根 / Square root
- `clamp` → `clamp(x, lo, hi)` — 把 x 限制在 [lo, hi]
- `Temperature` → 温度（开尔文）
- `func` → 全内联，不支持递归

## 二、安装（推荐，一条命令）

在仓库根目录执行：

```bash
sh editors/vscode/install.sh
```

它会把扩展复制到 `~/.vscode/extensions/ic10go.icg-<版本>/`（版本读取自 `package.json`），并带上语法、片段与语言包。

然后按 `Ctrl+Shift+P` → **Developer: Reload Window** 重启 VSCode。

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

命令面板（`Ctrl+Shift+P`）或编辑器右键菜单：

| 命令 | 说明 |
|------|------|
| `IC10 Go: Compile to IC10 (data loader included)` | 编译 `.icg`，旁边预览 IC10 产物 + 行/字节预算。若程序含 `data` 表，自动把「安装代码」复制到剪贴板并提示两步操作（先运行安装代码，再用预览中的运行代码覆盖） |
| `IC10 Go: Run in VM` | 在内置 VM 中运行 `.icg` 并显示设备状态 |
| `IC10 Go: Decompile IC10 to .icg` | 把 `.ic`/`.ic10` 反编译为 `.icg`（结构化） |
| `IC10 Go: Minify IC10` | 压缩 `.ic`/`.ic10` 行数 |
| `IC10 Go: Annotate IC10 (disasm)` | 给 `.ic`/`.ic10` 加跳转目标注释 |
| `IC10 Go: Restart Language Server` | 重启语言服务器 |

## 六、设置

| 设置 | 默认 | 说明 |
|------|------|------|
| `icg.serverPath` | `""` | `ic10c` 可执行文件路径；留空则自动查找 |
| `icg.stableIns` | `false` | 编译/运行时加 `--stable-ins`（稳定版 `ins` 参数顺序） |
| `icg.noCheck` | `false` | 关闭设备 logic type 校验（`IC10C_NO_CHECK`） |
| `icg.autoTable` | `false` | 编译时加 `--auto-table`：自动表化符合条件的常量 `switch`（需先跑安装代码） |

## 七、文件类型

| 扩展名 | 语言 | 说明 |
|--------|------|------|
| `.icg` | `icg` | ic10c 源码，拥有全部语言功能 |
| `.ic` / `.ic10` | `ic10` | 原生 IC10 汇编，高亮 + 反编译/压缩/注释命令 |

## 八、常见问题

- **没有高亮**：确认扩展出现在扩展列表（搜索 `IC10 Go`），并已重启 VSCode。
- **没有诊断/补全/悬停**：多半是找不到 `ic10c`。设置 `icg.serverPath`，或在仓库根目录构建 `ic10c`。
- **悬停一直显示"正在加载"**：旧版扩展/编译器的已知问题，已在 0.5.4 修复——请更新 `ic10c` 与扩展后重载。
- **含 `data` 表的程序停机 / 行为不对**：数据段要先安装——点“IC10 Go: 编译为 IC10”，扩展会把「安装代码」复制到剪贴板：先贴入 IC 运行一次，再用旁边预览里的运行代码覆盖；设备 host（空调等）需改用 `--data-access stack` 生成。
- **始终报 `no main function found`**：确认文件确实有 `func main()`；若文件带 UTF-8 BOM，旧版编译器会解析失败（0.5.4 起已支持 BOM）。
- **改了编译器后行为没变**：运行 `IC10 Go: Restart Language Server`。
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
