# Quickstart

本指南带你从零开始用 `.icg` 编写并编译 Stationeers IC10 程序。约 5 分钟。

## 1. 环境要求

- Go 1.27+
- Stationeers 游戏（可选，用于实际粘贴运行）

## 2. 构建

```bash
git clone <repo> ic10go
cd ic10go
go build -o ic10c ./cmd/ic10c
```

或直接运行：

```bash
go run ./cmd/ic10c build examples/hello.icg
```

## 3. 第一个程序

新建 `blink.icg`（仓库内已有同样的 [`examples/hello.icg`](examples/hello.icg)）：

```go
// 每 tick 把电池电量写入 IC 宿主显示屏
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

输出（可粘贴进游戏内 IC10 编辑器）：

```
yield
l r0 d0 Ratio
s db Setting r0
j 0
```

## 4. 常用命令

```bash
ic10c build  <file.icg>        # 编译并输出 IC10 到 stdout
ic10c stats  <file.icg>        # 行/字节/寄存器预算报告
ic10c fmt    [-w] <file.icg>   # 格式化源码（-w 原地写回）
ic10c disasm <file.ic>         # 反汇编注释旧 IC10
ic10c decompile <file.ic>      # 将 IC10 反编译为 .icg（-o 输出到文件）
ic10c lsp                      # 启动语言服务器
ic10c lex / ast <file.icg>     # 调试：打印词法单元 / AST
```

帮助支持中英双语（默认跟随 `$LANG`，也可用 `IC10C_LANG` 或 `-L` 指定）：

```bash
ic10c help              # 总体帮助
ic10c help build        # 单个命令的详细帮助
ic10c build -h          # 同上
ic10c -L en --help      # 强制英文
ic10c -L zh --help      # 强制中文
```

编译到文件：

```bash
./ic10c build blink.icg > blink.ic
```

检查是否超出 IC10 限制：

```bash
./ic10c stats blink.icg
# lines        4 / 128
# bytes       28 / 4096
# max line     9 / 90
# registers    1 / 16
```

## 5. 语法速览

### 常量与变量

```go
const MaxTemp = 296.15
const (
    PreMax = 45000
    PreMin = 40000
)

func main() {
    var on = 0
    t := d1.Temperature   // := 自动推导
    on = 1
    d0.On = on
}
```

### 设备读写

```go
t := d0.Temperature        // l r d0 Temperature
d0.On = t > 300            // s d0 On (...)
m := d2.slot[0].Mature     // ls r d2 0 Mature
d2.slot[0].Harvest = 1     // ss d2 0 Harvest 1
d0.channel[0][3] = t       // s d0:0 Channel3 t
```

### 控制流

```go
if t > MaxTemp {
    d0.On = 0
} else if t < MinTemp {
    d0.On = 1
}

for i := 0; i < 10; i++ {
    sum += i
}

for d0.Activate {
    yield()
}

switch mode {
case 1:
    d1.On = 1
case 2, 3:
    d1.On = 0
default:
    d1.On = -1
}
```

### 函数（编译期全内联，不支持递归）

```go
func clamp(x num, lo num, hi num) num {
    if x < lo { return lo }
    if x > hi { return hi }
    return x
}
```

### 批量 IO

```go
total := batch.read(hash("StructureBattery"), "Charge", "Sum")
avg := batch.read(hash("StructureBattery"), "Ratio", "Average")
batch.writeName(1220484876, hash("Override"), "Open", 1)
```

### 内建函数

```go
yield()
sleep(3)
push(x); y := pop(); z := peek(); poke(0, y)
d2.Mode = rmap(d3, hash("Iron"))
d0.On = isSet(d1)
d0.Setting = approx(x, y, 0.1)
d0.Setting = str("Ready!")
```

完整语法见 [`docs/spec.md`](docs/spec.md)。

## 6. 一个完整例子（滞回温控）

`hysteresis.icg`：

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

编译结果：

```
move r0 0
yield
l r1 d1 Temperature
bge r1 283.15 5
move r0 1
ble r1 296.15 7
move r0 0
s d0 On r0
j 1
```

## 7. 反编译旧 IC10 脚本

把已有的 `.ic` 脚本转成可编辑的 `.icg`：

```bash
ic10c decompile old.ic             # 输出到 stdout
ic10c decompile -o old.icg old.ic  # 输出到文件
```

反编译会：

- 替换 `alias` 与 `define`
- 把 `r0..r15` 变成同名变量
- 用 `label` / `goto` / `call` / `ret` 表达控制流
- 把算术、设备、槽位、批量、栈等映射为对应语法

产物可直接编译回 IC10：

```bash
ic10c build old.icg > old.ic
```

不支持的指令会在 stderr 提示，并在源码里保留为 `// unsupported:` 注释。反编译产物通常行数偏多（含变量声明与标签），可作为迁移起点再手工整理。

## 8. 测试（无需进游戏）

项目自带最小 IC10 解释器，可在测试中执行编译产物并断言设备状态：

```bash
go test ./...
```

例如 `pkg/ic10/vm_test.go` 会编译 `.icg`、在 `internal/vm` 中运行，再检查 `d0.On` 等值。

## 9. 编辑器支持（VSCode）

一键安装扩展（语法高亮 + 错误诊断 + 自动补全）：

```bash
go build -o ic10c ./cmd/ic10c     # 1. 准备编译器
sh editors/vscode/install.sh      # 2. 安装扩展
```

然后按 `Ctrl+Shift+P` → `Developer: Reload Window` 重启 VSCode，用 VSCode 打开本仓库目录并编辑 `.icg` 文件即可。

扩展是纯 JavaScript、无需 npm；它通过 `ic10c lsp` 提供诊断与补全。若找不到 `ic10c`，在设置里填 `icg.serverPath`。

详细说明与常见问题见 [`editors/vscode/README.md`](editors/vscode/README.md)。

## 10. 注意事项

- **128 行 / 4 KiB / 90 字符** 是硬限制，`ic10c build` 会在超限时报错，`stats` 可提前查看。
- 输出**不可读**：不生成 `alias`/`define`/注释/空行/标签，跳转使用绝对行号。
- IC10 常见的**尾调用状态机**（如 `gasHeaters` 循环后 `j greenhouseGasCheck`）请改写为结构化循环，因为函数全内联且不支持递归。
- 寄存器 `r0..r15` 由编译器按活跃区间自动复用，尽量不落栈。

## 11. 下一步

- 语言规范：[`docs/spec.md`](docs/spec.md)
- 编译器架构与里程碑：[`docs/architecture.md`](docs/architecture.md)
- IC10 目标约束与指令映射：[`docs/target-ic10.md`](docs/target-ic10.md)
- IC10 指令参考：[`Stationeers_IC10_参考文档.md`](Stationeers_IC10_参考文档.md)
