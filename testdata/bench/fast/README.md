# fast 模式状态机基准语料

评估与改进方案见 [`../../../docs/fast-statemachine.md`](../../../docs/fast-statemachine.md)。
再生成：`python3 gen.py`（本目录内）。

| 文件 | 形状 | 用途 |
|---|---|---|
| `var<N>_2.icg` | N 状态、体 2 条语句（设备写 + 转移），tag=循环外变量 | 派发链基线：行数与最坏 tick 随 N 线性增长 |
| `db<N>_2.icg` | 同上，tag=`db.stack[0]`（跨 tick 持久状态） | 与 var 版对照：tag 来源不影响派发成本 |
| `var<N>_1.icg` | N 状态、体仅 1 条赋值 | 唯一能命中 `--jump-table` 的门控形状 |
| `ord_asc<N>.icg` / `ord_desc<N>.icg` | 同一台机器，case 源码序不同（handler 升/降体积） | case 重排收益：worst 18→16（N=12），行数 +1 |
| `tree<N>.ic` | 手写二分决策树版状态机 | `--fast` 期望形状参照：worst 12→7（N=8）、16→8（N=12），行数持平 |

注意：`tree<N>.ic` 的叶子**不带等值复核**，只对整数 tag（0..N-1）与链版语义等价；
自动生成必须保留叶子复核或加守卫（方案文档 §4.2）。
