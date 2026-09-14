# ic10go

用 Go 风格语法编写 [Stationeers](https://stationeers.com/) IC10 芯片程序，编译为**高效、紧凑、不可读**的 IC10 代码。

- 语言：`.icg`（见 [`docs/spec.md`](docs/spec.md)）
- 编译器：`ic10c`
- 实现：Go 1.27
- 不改动游戏：产物为可粘贴进 IC10 编辑器的纯文本

## 特点

- **寄存器复用**：活跃性分析 + 图着色（Chaitin-Briggs）+ 拷贝合并；寄存器不足时自动溢出到 IC10 栈。
- **面向 128 行 / 4 KiB 约束**：不生成 `alias` / `define` / 注释 / 空行 / 标签，跳转用绝对行号。
- **现代语法**：`:=`、`if/for/switch`、`for range`（含遍历 `data` 表）、`case lo..hi` 区间、`if/switch` 初始化语句、带标签的 `break/continue`、函数（编译期内联 / 外提，按体积决策）、设备属性 `d0.On`、槽位 `d0.slot[i].X`、批量 IO、通道。
- **持久栈数据段**：`data` 表把大块常量 / 查表放进芯片持久栈，`switch ... table` 自动表化，突破 128 行预算。
- **编译期求值**：常量折叠、`hash()` 的 CRC-32、单位字面量（温度 `20c`/`68f`→K、压力 `20.1MPa`/`101.3kPa`→kPa）、逻辑类型校验。
- **快速开发工具链**：`build` / `run` / `fmt` / `disasm` / `decompile` / `stats` / `lsp`，以及内置最小解释器。

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
bge r0 283.15 5
move r1 1
ble r0 296.15 7
move r1 0
s d0 On r1
j 1
```

## 状态

**M0 已完成**：lexer / parser / AST / 诊断 / CLI 骨架。
**M1 已完成**：sema（名字解析、常量求值）、lower（AST→三地址 IR、内联 / 外提）、regalloc（活跃性 + 图着色 + 拷贝合并 + 溢出，寄存器复用）、codegen（指令选择、空块消除、逆后序布局、绝对行号、限额校验）。
**M2 已完成**：IR 优化器（块内拷贝/常量传播、全局常量传播、常量折叠、代数化简、全局 CSE（可用表达式，含跨基本块设备读 CSE）、select 转换、冗余设备/槽位/批量读消除、存储转发、常量分支折叠、循环不变量外提（含设备读）、尾块合并、死存储消除、活跃性死代码消除、不可达块删除）、比较-分支融合、`&&`/`||`→`min`/`max`（含纯函数）、**函数外提 / 特化**（内联与 `jal` 按体积取短，常量实参调用点内联折叠）。**便利语法**：`for i := range n` / `for i, v := range Table`、`case lo..hi` 区间、`if`/`switch` 初始化语句、带标签的 `break`/`continue`（`label Outer:`）。
**M3 已完成**：批量 IO（`batch.read/readName/readSlot/readNameSlot/write/writeName/writeSlot`）、网络通道 `d.channel[conn][ch]`、栈 `push/pop/peek/poke`、设备栈 `get/put/getd/putd/clr/clrById`、`isSet/isUnset/rmap/readReagent`、`approx/approxZero/notApprox/notApproxZero/logicalNor/isNotNaN`、`str("...")` 显示字符串、动态 logicType `read/write`、动态设备寄存器 `readDev/writeDev`（IC10 `drN`）、`LogicType.X` 枚举名透传、补充 logic type、**持久栈数据段**（`data` 表 / `switch ... table` / loader+runtime 两段流程 / 版本哨兵 / `--data-access` / `--data-layout` / `--unsafe` / `--auto-table`）。
**M5 已完成**：测试用最小 IC10 解释器 `internal/vm`（寄存器 / 栈 / 设备 / 槽位 / 通道 / 批量 / 分支 / 标签与绝对行号），配套端到端语义测试与常量折叠差分测试；并经 `ic10c run` 暴露给用户调试。
**M4 已完成**：`ic10c stats`（行/字节/寄存器预算）、`ic10c fmt`（格式化，支持 `-w`，保留注释/分组/空行/`data` 表）、`ic10c disasm`（旧 IC10 反汇编注释）、`ic10c decompile`（IC10 → `.icg`，支持 `-s` 结构化）、`ic10c minify`（压缩现有 IC10 行数）、`ic10c run`（内置 VM 执行）、`ic10c lsp`（诊断 / 上下文补全 / 格式化 / hover / 定义 / 大纲 / 折叠 / 引用 / 重命名 / 参数提示 / 快速修复 / 语义高亮 / 预算内联）、VSCode 扩展（`.icg` 与 `.ic`/`.ic10` 支持、片段、编译预览并自动处理数据段安装代码、VM 运行、反编译/压缩/注释命令）。

当前可用：

```
ic10c build  <file.icg>       # 编译为 IC10 并输出到 stdout
ic10c build --json <file.icg> # 输出机器可读的 JSON（代码/数据段 loader/统计/诊断）
ic10c build --split-data [--data-out FILE] [--data-access get|stack] \
            [--data-layout top|middle] [--unsafe] [--auto-table] [--jump-table] \
            [--fast] [--rel-jump] <file.icg>
                              # 同时输出持久栈数据段 loader（默认 <file>.data.ic）
ic10c build --data-only <file.icg>  # 只输出数据段 loader
ic10c run    <file.icg>       # 编译并在内置 VM 中运行（--steps/--set/--trace）
ic10c stats  [--data-layout top|middle] [--unsafe] [--auto-table] <file.icg>
                              # 行 / 字节 / 寄存器预算 + 数据段 / 栈冲突警告
ic10c size   <file.icg>       # 按函数拆分行预算（找最占行数的函数）
ic10c fmt    [-w] <file.icg>  # 格式化源码
ic10c disasm <file.ic>        # 反汇编注释旧 IC10
ic10c decompile <file.ic>     # 将 IC10 反编译为 .icg 源码（-o 输出到文件，-s 结构化）
ic10c minify <file.ic>        # 压缩现有 IC10 的行数（去注释/内联/去不可达）
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

> 提示：IC10 里常见的「尾调用跳转」状态机（如 `gasHeaters` 循环后 `j greenhouseGasCheck`）在 `.icg` 中请改写为结构化循环，因为函数不支持递归（编译期展开 / 外提）。
>
> 寄存器压力超过 16 时，编译器会自动把多余的值**溢出到 IC10 栈**（固定高地址槽 + 暂存寄存器），而不是报错。未知 logic type 会给出**警告**（可用 `IC10C_NO_CHECK=1` 关闭）。
>
> 反编译：`ic10c decompile` 会替换 `alias`/`define`、用 `:=` 声明首次写入的寄存器、用 `label`/`goto`/`call`/`ret` 表达控制流；配合寄存器拷贝合并，`ic10code/` 里的真实脚本都能反编译并在 128 行内重新编译（含最复杂的 Furnace，148→121 行）。加 `-s/--structured` 会基于后支配树还原 `if`/`else`/`for`；结构化失败时自动回退到 goto 形式。

## 测试

```
go test ./...
```

- golden：`testdata/programs/*.icg` → `testdata/golden/*.ic`（用 `go test ./pkg/ic10 -update` 更新）
- VM 端到端：编译后在 `internal/vm` 中执行并断言设备状态
- 差分/随机：`TestDifferentialRandom` 随机生成 2000 个 `.icg`（含 `const`、函数、`if/else`、`for`/`range`、`switch`（含区间 case）、`break/continue`（含标签）、`label`/`goto`/`call`/`ret`、栈、批量、槽位、动态 `read/write`、`ins/ext`、近似比较），分别用优化与 `IC10C_NO_OPT` 编译（并覆盖 `--stable-ins`/`--jump-table`/`--fast`/`--rel-jump` 与 `data` 表），在 VM 中运行并对比设备写入序列（优化的回归安全网）
- 覆盖：寄存器复用、比较融合、select、死代码消除、批量聚合、栈、通道、槽位、真实脚本 `solar_tracker`
- 工具：`fmt` 幂等性、`stats`、`disasm`、LSP 诊断与补全
- 真实脚本：`ic10code/` 下的每个 `.ic`/`.ic10` 都做**反编译→重编译→设备写入序列对比**（`TestIc10CodeRoundTrip`）与 **minify 等价性**（`TestMinifyIc10Code`）；第三方脚本仅本地保留，缺失时自动跳过
- 数据段：`data` 表端到端（loader→runtime、版本哨兵、`--data-access stack`、`--data-layout middle`、`--auto-table`），见 `pkg/ic10/data_test.go` 与 `experiments/data-segment/`

## 文档

| 文档 | 内容 |
|------|------|
| [`TUTORIAL.md`](TUTORIAL.md) | 新手详细教程（从零到部署） |
| [`QUICKSTART.md`](QUICKSTART.md) | 5 分钟上手 |
| [`docs/spec.md`](docs/spec.md) | `.icg` 语言规范 |
| [`docs/architecture.md`](docs/architecture.md) | 编译器架构与里程碑 M0–M5 |
| [`docs/target-ic10.md`](docs/target-ic10.md) | IC10 目标约束、指令映射与内建数据 |
| [`docs/data-segment.md`](docs/data-segment.md) | 持久栈数据段：`data` 表 / loader+runtime / 布局 / 宿主兼容 |
| [`docs/plugin-api.md`](docs/plugin-api.md) | `build --json` 机器接口：字段、诊断 code、桥接流程 |
| [`docs/ingame-test-plan.md`](docs/ingame-test-plan.md) | 真机测试方案（新内建 / 优化 / `--rel-jump` 验证） |

> IC10 指令完整参考（`Stationeers_IC10_参考文档.md`）为第三方资料，仅本地保留、未随仓库分发。

## 路线图

- **M0** 骨架：lexer / parser / AST
- **M1** 单函数编译：寄存器分配 + 代码生成 + 限额校验
- **M2** 优化器：常量折叠 / DCE / CSE / 内联与外提 / 分支融合
- **M3** 领域特性：槽位 / 批量 / 通道 / 栈
- **M4** 工具链：`fmt` / `disasm` / `decompile` / `run` / `stats` / LSP / VSCode 扩展
- **M5** 测试用最小解释器（VM）
