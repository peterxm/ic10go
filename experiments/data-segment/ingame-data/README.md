# 真机测试：data 表 + loader/runtime

验证编译器生成的**持久栈数据段**两段流程。d0 = LED Display（`Setting`）。

## 步骤

1. 把 `1_loader.ic` 贴进 IC 编辑器并运行（写入数据 + 版本哨兵）。
2. 整段**覆盖**成 `2_runtime.ic`。
3. LED 应每秒循环显示 `111 → 222 → 333`。

## 预期

- `2_runtime.ic` 启动时先读哨兵槽 508 校验版本，通过后才进入循环。
- `T[i]` 用变量索引，编译为 `get(db, 509 + i)`。
- 若第 1 步没做（或数据被清），runtime 会因版本不符而停机（`j 9999`），
  LED 不变化。

## 源码 / 生成

`demo.icg` 是源文件；两份 `.ic` 由：

```bash
ic10c build --data-only demo.icg > 1_loader.ic
ic10c build             demo.icg > 2_runtime.ic
```

生成。数据段布局：槽 508 哨兵，509..511 = `T[0..2]`。

> 仅标准 IC host 支持（设备 host 的 `put db` 报 `MemoryNotWriteable`）。

## 实测结果

真机通过 ✅：先跑 `1_loader.ic`，再覆盖成 `2_runtime.ic`，LED 循环
`111 → 222 → 333`，版本校验正常。

> 已知问题（暂不处理）：数据段在栈顶，可能与大量 `push`/`poke` 冲突，
> 见 [`docs/data-segment.md`](../../../docs/data-segment.md) 的「已知问题 / 待办」。
