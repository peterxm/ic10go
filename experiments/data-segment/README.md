# 持久栈数据段 — 最小验证原型

对应 [`docs/data-segment.md`](../../docs/data-segment.md) 第 8 节的验证。
用现成的 `Barsiel_s HASH IC Script`（17 路查表）手工拆成 loader + runtime，
量行数、跑通两段流程，并确认 VM 能预置栈、`get(db, addr)` 可跨程序读取。

## 文件

| 文件 | 作用 |
|------|------|
| `barsiel_loader.icg` | 装载器：把 `ore -> (显示哈希, 比热)` 表 + 版本哨兵写入 `db` 栈，运行一次 |
| `barsiel_runtime.icg` | 运行时：原 Barsiel 端口去掉 `loadTable()`，用 `get(db, ...)` 读表 |
| `proto_test.go` | 闭环测试：先跑 loader，再把栈交给 runtime，验证查表结果 |

槽位约定：`0..16` 显示哈希，`17..33` 比热，`34` 版本哨兵。

## 结果

| 产物 | 行数 |
|------|------|
| 原来的合并端口（loader 内联） | 94 |
| 拆出的 **runtime** | **60** |
| 拆出的 **loader**（只跑一次，不计入 runtime 预算） | 35 |

runtime 从 94 → 60 行，**省下 34 行**，剩余预算从 34 行涨到 68 行。

## 闭环测试（`go test ./experiments/data-segment/`）

1. 跑 loader → `db.Stack[0] == -1301215609`（Iron），`db.Stack[34] == 1`（哨兵）。
2. 把栈复制给 runtime，并把 Ingot Dial 设为 3（Gold）。
3. 跑 runtime → `db.Setting == 226410516`（Gold 显示哈希），说明 runtime 成功
   从持久栈读到表。
4. 不跑 loader 时，`db.Setting == 0`，即缺数据会被读到 0。

结论：**VM 能预置栈，跨程序 `get(db, addr)` 读取可行，行数收益明显。**

## 发现 / 待确认

- **真机语义**：VM 把 `db` 的栈（`get`/`put`）与本地栈（`push`/`pop`/`poke`/`peek`）
  建模为两块独立内存。参考文档建议主循环前 `clr db` 清空 `push/pop` 的栈，
  倾向于二者在游戏里是**同一块**。上真机前需要确认这一点，否则 `get(db, addr)`
  的地址分区要重新设计。
- **持久性前提**：需真机确认“换代码后栈仍保留”。
- **版本哨兵**：本原型在槽 34 写了 `1`，runtime 可据此检测数据缺失/过期。
- **地址分区**：本原型用 0..34；实际要避开 call stack（`sp` 增长区）与
  寄存器溢出槽（511 向下，见 `internal/regalloc/regalloc.go:20`）。

## 下一步

确认真机语义后，再定：
1. `.icg` 的数据声明语法（`data` 表 / 标注 `switch` 自动表化）；
2. CLI 输出（`--split-data` / `--data-file`）与 VSCode “安装数据段”命令；
3. 地址分区与版本哨兵约定。
