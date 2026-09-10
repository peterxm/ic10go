# ic10go

用 Go 风格语法编写 [Stationeers](https://stationeers.com/) IC10 芯片程序，编译为**高效、紧凑、不可读**的 IC10 代码。

- 语言：`.icg`（见 [`docs/spec.md`](docs/spec.md)）
- 编译器：`ic10c`
- 实现：Go 1.27
- 不改动游戏：产物为可粘贴进 IC10 编辑器的纯文本

## 特点

- **寄存器复用**：基于活跃性分析的线性扫描分配，`r0` 可在不同时刻承载不同变量；尽量不落栈。
- **面向 128 行 / 4 KiB 约束**：不生成 `alias` / `define` / 注释 / 空行 / 标签，跳转用绝对行号。
- **现代语法**：`:=`、`if/for/switch`、函数（编译期全内联）、设备属性 `d0.On`、槽位 `d0.slot[i].X`、批量 IO、通道。
- **编译期求值**：常量折叠、`hash()` 的 CRC-32、逻辑类型校验。
- **快速开发工具链**：`build` / `fmt` / `disasm` / `stats` / `lsp`，以及测试用最小解释器。

## 示例

```go
const MaxTemp = 296.15
const MinTemp = 283.15

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

编译（示意）：

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

## 状态

**M0 已完成**：lexer / parser / AST / 诊断 / CLI 骨架。
**M1 已完成**：sema（名字解析、常量求值）、lower（AST→三地址 IR、全内联）、regalloc（活跃性 + 图着色，寄存器复用）、codegen（指令选择、空块消除、逆后序布局、绝对行号、限额校验）。
**M2 已完成**：IR 优化器（块内拷贝/常量传播、常量折叠、局部 CSE、select 转换、活跃性死代码消除、不可达块删除）、比较-分支融合、`&&`/`||`→`min`/`max`（纯操作数）。
**M3 已完成**：批量 IO（`batch.read/readName/readSlot/readNameSlot/write/writeName/writeSlot`）、网络通道 `d.channel[conn][ch]`、栈 `push/pop/peek/poke`、设备栈 `get/put/getd/putd/clr`、`isSet/isUnset/rmap`、`approx/approxZero`、`str("...")` 显示字符串、补充 logic type。
**M5 已完成**：测试用最小 IC10 解释器 `internal/vm`（寄存器 / 栈 / 设备 / 槽位 / 通道 / 批量 / 分支 / 标签与绝对行号），配套端到端语义测试与常量折叠差分测试。
**M4 已完成**：`ic10c stats`（行/字节/寄存器预算）、`ic10c fmt`（格式化，支持 `-w`）、`ic10c disasm`（旧 IC10 反汇编注释）、`ic10c lsp`（诊断 + 补全）、VSCode TextMate 语法高亮。

当前可用：

```
ic10c build  <file.icg>       # 编译为 IC10 并输出到 stdout
ic10c stats  <file.icg>       # 行 / 字节 / 寄存器预算报告
ic10c fmt    [-w] <file.icg>  # 格式化源码
ic10c disasm <file.ic>        # 反汇编注释旧 IC10
ic10c decompile <file.ic>     # 将 IC10 反编译为 .icg 源码（-o 输出到文件）
ic10c lsp                     # 启动语言服务器（stdio）
ic10c lex    <file.icg>       # 打印词法单元
ic10c ast    <file.icg>       # 打印 AST
ic10c help   [command]        # 帮助（中英双语，-L en|zh 切换）
```

帮助默认跟随 `$LANG`，可用 `IC10C_LANG` 或全局选项 `-L/--lang en|zh` 覆盖。

编辑器：安装 VSCode 扩展获得语法高亮、诊断与补全：

```bash
sh editors/vscode/install.sh   # 安装到 ~/.vscode/extensions
```

详见 [`editors/vscode/README.md`](editors/vscode/README.md)。

> 提示：IC10 里常见的「尾调用跳转」状态机（如 `gasHeaters` 循环后 `j greenhouseGasCheck`）在 `.icg` 中请改写为结构化循环，因为函数是全内联且不支持递归。
>
> 寄存器压力超过 16 时，编译器会自动把多余的值**溢出到 IC10 栈**（固定高地址槽 + 暂存寄存器），而不是报错。未知 logic type 会给出**警告**（可用 `IC10C_NO_CHECK=1` 关闭）。

## 测试

```
go test ./...
```

- golden：`testdata/programs/*.icg` → `testdata/golden/*.ic`（用 `go test ./pkg/ic10 -update` 更新）
- VM 端到端：编译后在 `internal/vm` 中执行并断言设备状态
- 覆盖：寄存器复用、比较融合、select、死代码消除、批量聚合、栈、通道、槽位、真实脚本 `solar_tracker`
- 工具：`fmt` 幂等性、`stats`、`disasm`、LSP 诊断与补全

## 文档

| 文档 | 内容 |
|------|------|
| [`docs/spec.md`](docs/spec.md) | `.icg` 语言规范 |
| [`docs/architecture.md`](docs/architecture.md) | 编译器架构与里程碑 M0–M5 |
| [`docs/target-ic10.md`](docs/target-ic10.md) | IC10 目标约束、指令映射与内建数据 |
| [`Stationeers_IC10_参考文档.md`](Stationeers_IC10_参考文档.md) | IC10 指令完整参考 |

## 路线图

- **M0** 骨架：lexer / parser / AST
- **M1** 单函数编译：寄存器分配 + 代码生成 + 限额校验
- **M2** 优化器：常量折叠 / DCE / CSE / 内联 / 分支融合
- **M3** 领域特性：槽位 / 批量 / 通道 / 栈
- **M4** 工具链：`fmt` / `disasm` / `stats` / LSP / 语法高亮
- **M5** 测试用最小解释器（VM）
