# 真机测试：switch ... table 自动表化

验证 `switch i table { ... }` 编译成数据段查表。d0 = LED Display（`Setting`）。

## 步骤

1. 贴入 `1_loader.ic` 并运行（写入表 + 版本哨兵）。
2. 整段覆盖成 `2_runtime.ic`。
3. LED 应每秒循环 `111 → 222 → 333`。

## 生成

```bash
ic10c build --data-only demo.icg > 1_loader.ic
ic10c build             demo.icg > 2_runtime.ic
```

`demo.icg` 的 `switch i table` 三个 case 各给 `d0.Setting` 赋一个常量；
编译器生成一张表（槽 509..511），runtime 做边界检查后
`get(db, 509 + i)`。

> 仅标准 IC host 支持（设备 host 的 `put db` 报 `MemoryNotWriteable`）。
