# IC10 Go — VSCode 扩展

为 `.icg` 提供语法高亮、括号/注释配置、错误诊断与自动补全。

扩展用**纯 JavaScript** 编写，无需 `npm install`，只要本机有 `ic10c` 可执行文件即可。

## 一、安装（推荐，一条命令）

在仓库根目录执行：

```bash
sh editors/vscode/install.sh
```

它会把这个扩展复制到 `~/.vscode/extensions/ic10go.icg-0.2.0`。

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
4. 输入 `batch.` 或 `Temperature` 时应有补全。

## 四、手动安装（不使用脚本）

把 `editors/vscode` 整个目录复制到：

```
~/.vscode/extensions/ic10go.icg-0.2.0/
```

目录里必须包含 `package.json`、`extension.js`、`language-configuration.json` 和 `syntaxes/`，然后重启 VSCode。

## 五、功能与命令

| 功能 | 说明 |
|------|------|
| 语法高亮 | 关键字、字符串、数字、设备端口、逻辑类型、内建函数 |
| 注释/括号 | `//`、`/* */`、自动闭合 |
| 诊断 | 打开/编辑时实时显示编译错误 |
| 补全 | 内建函数、逻辑类型、槽位类型、设备端口 |
| 命令 | `IC10 Go: Restart Language Server`（重启语言服务器） |

## 六、常见问题

- **没有高亮**：确认文件扩展名是 `.icg`，且已重启 VSCode；检查扩展是否出现在扩展列表（搜索 `IC10 Go`）。
- **没有诊断/补全**：多半是找不到 `ic10c`。按上面第二节设置 `icg.serverPath`，或在工作区根目录构建 `ic10c`。
- **改了编译器后行为没变**：运行命令面板里的 `IC10 Go: Restart Language Server`。
- **查看日志**：输出面板（`Ctrl+Shift+U`）选择 `IC10 Go`。

## 七、打包成 .vsix（可选）

如果要用 `code --install-extension` 或分发给别人：

```bash
cd editors/vscode
npx @vscode/vsce package --allow-missing-repository
# 生成 icg-0.2.0.vsix
code --install-extension icg-0.2.0.vsix
```

`vsce` 需要联网下载，首次使用会慢一些。
