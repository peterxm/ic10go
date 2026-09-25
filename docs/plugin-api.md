# 插件 / 机器接口（`build --json`）

给游戏内编辑器插件（例如 BepInEx mod）或任何外部工具用的稳定、机器可读接口。
第一阶段只提供一次性命令；后续会加常驻 NDJSON 模式（`ic10c serve`）。

```
ic10c build --json <file.icg>
```

- stdout 是**单个 JSON 文档**（紧凑格式，一行）。
- stdout 在任何情况下都是合法 JSON，包括文件读不到、源码报错、超出 IC10 限额。
- 退出码：`0` = 编译成功；`1` = 源码有 error 或超出限额；`2` = 调用 / IO 错误。
  桥接程序应以 `ok` 与 `diagnostics` 为准，不要依赖退出码语义。

## 文档结构

```json
{
  "apiVersion": 1,
  "ok": true,
  "code": "yield\nmove r2 0\n...",
  "lines": ["yield", "move r2 0", "..."],
  "data": {
    "needed": true,
    "setup": true,
    "loader": "put db 499 -843351598\n...",
    "loaders": ["put db 499 ...\n", "put db 600 ...\n"],
    "start": 499,
    "end": 511,
    "sentinel": 499,
    "access": "get",
    "layout": "top"
  },
  "stats":  { "lines": 53, "bytes": 1004, "maxLine": 35, "regs": 10 },
  "limits": { "lines": 128, "bytes": 4096, "maxLine": 90, "regs": 16 },
  "diagnostics": [
    {
      "severity": "warning",
      "code": "unknown-logic-type",
      "file": "airlock.icg",
      "range": {
        "start": { "line": 74, "col": 5, "offset": 123 },
        "end":   { "line": 74, "col": 5, "offset": 123 }
      },
      "message": "unknown logic type \"Bogus\""
    }
  ]
}
```

### 字段

| 字段 | 说明 |
|------|------|
| `apiVersion` | 接口版本；破坏性变更才 +1。桥接程序启动时应校验。 |
| `ok` | 是否编译成功。`false` 时 `code` 为空，`diagnostics` 必含 error。 |
| `code` | 编译产物原文。 |
| `lines` | 产物按行拆分。**直接写芯片请用这个**，避免尾换行 / CRLF 歧义。 |
| `data.needed` | 是否需要先运行一次性 loader（数据段和/或外提的设置写入）。 |
| `data.setup` | loader 里是否包含**外提的一次性设置写入**（`Mode`/`On`/常量 `Setting` 等，见下文）。 |
| `data.loader` | 一次性 loader 的完整 IC10 代码；`needed=false` 时省略。 |
| `data.loaders` | loader 拆成的**分块**（每块 ≤128 行，按序运行）；仅当 loader 超过 128 行时给出，单块时省略（用 `loader`）。 |
| `data.start` / `end` | 数据段占用的栈槽范围。 |
| `data.sentinel` | 版本哨兵槽（= `start`）。 |
| `data.access` | `get`（`put/get db`）或 `stack`（`poke/peek`）。 |
| `data.layout` | `top` 或 `middle`。 |
| `stats` | 行 / 字节 / 最长行 / 引用到的寄存器数。 |
| `limits` | 编译器校验所用的 IC10 上限（默认 128 / 4096 / 90，寄存器恒为 16）。可用 `--max-lines` / `--max-bytes` / `--max-line` 或 `IC10C_MAX_LINES` / `IC10C_MAX_BYTES` / `IC10C_MAX_LINE` 覆盖，以跟随游戏变化；这里的值就是本次构建实际生效的值。 |
| `diagnostics[]` | 诊断列表，按源码位置排序。 |

### 一次性设置写入（`data.setup`）

编译器会把**序言里的一次性设备写入**（`Mode`、`On`、常量 `Setting` 等，操作数
全为编译期常量）外提到 loader：设备状态是持久的，这些写入只需在安装时执行一次。
触发条件是 runtime 超过 128 行，或程序本来就需要 loader（有 `data` 表）。

此时 `data.needed = true`、`data.setup = true`，`data.loader` 同时包含数据段写入
（若有）与外提的设置写入；桥接程序应先跑 loader 再写 runtime。runtime 自身仍
≤128 行。loader 超过 128 行时改用 `data.loaders[]` 分块，按序全部运行。

外提是**全自动、不可关闭**的（无对应开关），并会与「不拆分」的产物比较、取
runtime 更短者，所以拆分只会减小 runtime。语义前提：这些常量写被视为一次性
初始化；若循环会改该设备、序言想每 tick 复位，则外提会改变行为。

### 栈私有 / 共享 pragma

`// icg: private-stack` / `// icg: shared-stack`（文件 pragma，见
[`spec.md` §4.6](spec.md#46-栈私有-pragma)）决定用户栈能否做“删/并写入”类优化
（常量用户槽提升为寄存器、消除成对 `push/pop`、放宽死存储消除）。**单芯片默认
`private-stack`**，含 `chip` 块的多芯片默认 `shared-stack`；pragma 优先于默认。

对桥接程序是透明的：它只改变优化强度与 `stats` 的栈用量，不新增诊断 `code`，
也不改变 `data.*` 字段的语义。用户栈地址落在编译器区的 `stack-overlap` 判定与它
无关（始终按分区检查）。

### 诊断

| 字段 | 说明 |
|------|------|
| `severity` | `error` / `warning` / `note`。 |
| `code` | 稳定、不本地化的标识符，可空。见下表。 |
| `file` | 源文件名。 |
| `range.start` / `range.end` | 1-based 行列 + 0-based 字节偏移。当编译器未记录 span 结束时 `end == start`。 |
| `message` | 面向人的文本，跟随 `-L en\|zh`。 |

已定义的稳定 `code`：

| code | 级别 | 含义 |
|------|------|------|
| `unknown-logic-type` | warning | 逻辑类型不在内建表中；`message` 附带最接近的拼写建议（拼错时） |
| `unknown-slot-type` | warning | 槽位类型不在内建表中；`message` 附带拼写建议 |
| `unknown-enum` | warning | 未知 `Enum.Member`，原样输出；`message` 附带同组最接近的拼写建议（可用 `raw("...")` 显式原样输出） |
| `loop-without-yield` | warning | 无 `yield()`/`sleep()` 的无条件 `for {}`；提示加 `yield()` 以免循环空转 |
| `no-main` | error | 找不到 `main` 函数 |
| `data-too-large` | error | 数据段超出芯片栈 |
| `stack-overlap` | error | 用户栈地址落在编译器区（数据段/溢出）或 `push` 深度超过用户上限 |
| `stack-compiler-overflow` | error | 固定用户区下，数据段 + 溢出放不下编译器区 |
| `stack-addr-range` | error | 栈地址超出 `[0, 511]` |
| `codegen-error` | error | 代码生成失败（例如超出 128 行 / 4096 字节 / 90 字符） |
| `io-error` | error | 读文件失败（`--json` 下也会输出 JSON） |

## 桥接建议

```text
1. ic10c build --json <file.icg>
2. 若 !ok：用 diagnostics[].range 画波浪线，结束。
3. 若 data.needed：先把 data.loader 跑一次（写进芯片并执行）。
4. 把 lines 逐行写进芯片。
5. 用 stats / limits 画预算条。
```

## 兼容性

- 字段只增不减；`apiVersion` 不匹配时桥应降级或提示升级。
- `message` 可能变化或本地化；**不要**用 `message` 做逻辑判断，用 `code`。
- `lines` / `code` 的内容由优化器决定，不保证跨版本稳定；请按「源码 → 产物」一次性使用。
