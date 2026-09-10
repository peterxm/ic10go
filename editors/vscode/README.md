# IC10 Go — VSCode 扩展

为 `.icg` 提供语法高亮、括号/注释配置、错误诊断、上下文感知补全，以及"编译预览 / 内置 VM 运行"命令。

扩展用**纯 JavaScript** 编写，无需 `npm install`，只要本机有 `ic10c` 可执行文件即可。

界面文案（命令、设置说明）跟随 VSCode 的显示语言，**内置简体中文**（`package.nls.zh-cn.json`）：把 VSCode 语言设为中文即可看到中文命令名与设置说明。

## 一、安装（推荐，一条命令）

在仓库根目录执行：

```bash
sh editors/vscode/install.sh
```

它会把这个扩展复制到 `~/.vscode/extensions/ic10go.icg-<版本>/`（版本读取自 `package.json`）。

然后**重启 VSCode**（或按 `Ctrl+Shift+P` 输入 `Developer: Reload Window`）。

## 二、准备 `ic10c`

扩展需要 `ic10c` 来提供诊断与补全。编译它：

```bash
go build -o ic10c ./cmd/ic10c
```

扩展按以下顺序查找 `ic10c`（找到即用）：

1. VSCode 设置 `icg.serverPath`（绝对路径）
2. 工作区文件夹，及其**各级父目录**里的 `ic10c`
3. 当前打开文件所在目录，及其**各级父目录**里的 `ic10c`
4. `$GOPATH/bin`、`~/go/bin`、`~/bin`、`/usr/local/bin`
5. 系统 `PATH`

所以最省事的做法（任选其一）：

- 用 VSCode 打开本仓库目录，并在根目录构建 `ic10c`；或
- 把 `ic10c` 放进 `PATH` 上的目录，例如：

  ```bash
  go build -o ~/.local/bin/ic10c ./cmd/ic10c
  ```

如果还是找不到，会弹出提示，点 **Open Settings** 在 `icg.serverPath` 填入绝对路径即可。

## 三、验证

1. 新建 `demo.icg`：

   ```go
   func main() {
       for {
           yield()
           d0.On = d1.Temperature > 300
       }
   }
   ```

2. 应看到语法高亮。
3. 故意写错，例如 `x := foo`，编辑器应报 `undefined variable "foo"`。
4. 输入 `d0.` 应补全 logic type；输入 `batch.` 应补全批量方法；输入你定义的函数名也会被补全。
5. 命令面板执行 `IC10 Go: Compile to IC10`，旁边会打开编译产物，输出面板显示行/字节预算。

## 四、手动安装（不使用脚本）

把 `editors/vscode` 整个目录复制到：

```
~/.vscode/extensions/ic10go.icg-0.5.3/
```

目录里必须包含 `package.json`、`extension.js`、`language-configuration.json`、`syntaxes/` 和 `snippets/`，然后重启 VSCode。

## 五、功能与命令

| 功能 | 说明 |
|------|------|
| 语法高亮 | `.icg` 用专用语法；`.ic`/`.ic10` 有 IC10 汇编高亮 |
| 语义高亮 | 语义 token（函数/变量/设备/logic type/内建/枚举），比 TextMate 更准 |
| 注释/括号 | `//`、`/* */`、自动闭合 |
| 诊断 | 打开/编辑时实时显示编译错误（含 128 行 / 4KiB / 90 字符超限） |
| 补全 | **上下文感知**：`d0.`→logic type、`batch.`→批量方法、`SorterInstruction.`→枚举成员；并补全当前文件的函数/常量/变量/标签、内建、设备端口；补全项带签名与文档 |
| 片段 | `main`、`hyst`、`batchread`、`batchwrite`、`func`、`const`、`slotread` 等 |
| 大纲 / 折叠 | 文档符号（函数、常量、变量、标签）+ 代码折叠 |
| 查找引用 / 重命名 | `Shift+F12` 查找引用；`F2` 重命名（单文件内所有引用） |
| 参数提示 | 函数调用时显示参数签名，并高亮当前参数 |
| 快速修复 | 未知 logic/slot type 的"你是不是想写…"；缺少 `main` 时一键补上 |
| 预算内联 | 文件末尾显示 `IC10: 行/字节/寄存器` 预算（inlay hint） |
| 格式化 | `Shift+Alt+F`（或保存时）调用 `ic10c` 的格式化 |
| 悬停 | 悬停显示内建函数**签名 + 用法说明**、逻辑类型含义、关键字与底层原语说明（中英随显示语言） |
| 跳转定义 | `F12` / `Ctrl+点击` 跳到函数、常量、变量、标签定义 |
| 命令 | 见下 |

命令（命令面板 / 右键菜单）：

| 命令 | 说明 |
|------|------|
| `IC10 Go: Compile to IC10` | 编译 `.icg`，旁边预览 IC10 产物 + 行/字节预算 |
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

## 七、常见问题

- **没有高亮**：确认文件扩展名是 `.icg`，且已重启 VSCode；检查扩展是否出现在扩展列表（搜索 `IC10 Go`）。
- **没有诊断/补全**：多半是找不到 `ic10c`。按上面第二节设置 `icg.serverPath`，或在工作区根目录构建 `ic10c`。
- **改了编译器后行为没变**：运行命令面板里的 `IC10 Go: Restart Language Server`。
- **查看日志**：输出面板（`Ctrl+Shift+U`）选择 `IC10 Go`。

## 八、打包成 .vsix（可选）

如果要用 `code --install-extension` 或分发给别人：

```bash
cd editors/vscode
npx @vscode/vsce package --allow-missing-repository
# 生成 icg-0.5.3.vsix
code --install-extension icg-0.5.3.vsix
```

`vsce` 需要联网下载，首次使用会慢一些。
