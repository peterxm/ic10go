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
| 拆出的 **runtime**（手写 loader/runtime） | **60** |
| 拆出的 **loader**（只跑一次，不计入 runtime 预算） | 35 |
| `data` 语法版 runtime（`barsiel_datatable.icg`，含版本校验） | **65** |

runtime 从 94 → 60/65 行，**省下约 30 行**，剩余预算从 34 行涨到 63 行。

`barsiel_datatable.icg` 演示了新语法：两个 `data` 表，runtime 用
`RecipeDisplay[ore-1]` / `RecipeHeat[ore-1]` 读表，loader 由
`ic10c build --split-data` 生成（35 行，含版本哨兵）。

## 闭环测试（`go test ./experiments/data-segment/`）

1. 跑 loader → `db.Stack[0] == -1301215609`（Iron），`db.Stack[34] == 1`（哨兵）。
2. 把栈复制给 runtime，并把 Ingot Dial 设为 3（Gold）。
3. 跑 runtime → `db.Setting == 226410516`（Gold 显示哈希），说明 runtime 成功
   从持久栈读到表。
4. 不跑 loader 时，`db.Setting == 0`，即缺数据会被读到 0。

结论：**VM 能预置栈，跨程序 `get(db, addr)` 读取可行，行数收益明显。**

## 发现 / 结论（含真机验证）

- **栈持久**：真机确认，换掉代码后栈仍保留（`ingame/1_store.ic` → `2_read.ic`
  读到 `111/222/333`）。
- **`db` 栈 == 本地栈**：真机确认（标准 IC host）。
  `poke 50` → `get db 50` = `12345`；`put db 60` → `peek 60` = `54321`。
  VM 已同步修正（`db.Stack` 与 `m.Stack` 共用底层数组）。
- **宿主差异**：IC 芯片必须插在 **IC host** 上才能运行；标准 host 提供
  `d0–d5`。部分设备自带 host（如空调），此时 `db` 指向设备本身
  （`db On = 0` 关闭空调）；**设备 host 下真机已确认不支持**：`put db 0 111`
  报 `MemoryNotWriteable`。数据段方案应要求标准 IC host，或改用本地栈
  （`poke`/`peek`）以兼容。
- **版本哨兵**：本原型在槽 34 写了 `1`，runtime 可据此检测数据缺失/过期。
- **地址分区**：数据段与本地栈共享同一空间，要避开 `sp` 增长区与寄存器
  溢出槽（511 向下，见 `internal/regalloc/regalloc.go:20`）。

## 真机测试目录

| 目录 | 内容 |
|------|------|
| `ingame/` | 栈持久性、`poke`↔`get db` 语义（6 段） |
| `ingame-data/` | `data` 表 loader/runtime 两段流程 |
| `ingame-switch/` | `switch ... table` 自动表化 |
| `ingame-stack/` | `--data-access stack`（兼容设备 host） |
| `ingame-airlock/` | **Adv_Airlock_Smol 数据段端口**（详细步骤见该目录 README） |

## 下一步

已全部落地：`.icg` 的 `data` 表语法、`switch ... table`、`--split-data`/
`--data-only`/`--data-access`/`--data-layout`/`--unsafe`/`--auto-table`、
VSCode “安装数据段”命令、地址分区与版本哨兵约定。
