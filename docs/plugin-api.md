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
    "loader": "put db 499 -843351598\n...",
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
| `data.loader` | 一次性 loader 的 IC10 代码；`needed=false` 时省略。桥可自动先跑它。 |
| `data.start` / `end` | 数据段占用的栈槽范围。 |
| `data.sentinel` | 版本哨兵槽（= `start`）。 |
| `data.access` | `get`（`put/get db`）或 `stack`（`poke/peek`）。 |
| `data.layout` | `top` 或 `middle`。 |
| `stats` | 行 / 字节 / 最长行 / 引用到的寄存器数。 |
| `limits` | 固定上限（128 / 4096 / 90 / 16）。 |
| `diagnostics[]` | 诊断列表，按源码位置排序。 |

### 一次性设置写入（`data.setup`）

编译器会把**序言里的一次性设备写入**（`Mode`、`On`、常量 `Setting` 等，操作数
全为编译期常量）外提到 loader：设备状态是持久的，这些写入只需在安装时执行一次。
触发条件是 runtime 超过 128 行，或程序本来就需要 loader（有 `data` 表）。

此时 `data.needed = true`、`data.setup = true`，`data.loader` 同时包含数据段写入
（若有）与外提的设置写入；桥接程序应先跑 loader 再写 runtime。runtime 自身仍
≤128 行。

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
| `unknown-logic-type` | warning | 逻辑类型不在内建表中 |
| `unknown-slot-type` | warning | 槽位类型不在内建表中 |
| `unknown-enum` | warning | 未知 `Enum.Member`，原样输出（可用 `raw("...")` 显式原样输出） |
| `no-main` | error | 找不到 `main` 函数 |
| `data-too-large` | error | 数据段超出芯片栈 |
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
