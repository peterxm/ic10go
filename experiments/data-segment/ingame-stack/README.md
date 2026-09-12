# 真机测试：--data-access stack（设备 host 兼容）

用本地 `poke`/`peek` 读写数据段，不依赖 `db`，因此**设备 host（空调等）**也能用。
d0 = LED Display（`Setting`）。

## 步骤

1. 贴入 `1_loader.ic` 并运行。
2. 整段覆盖成 `2_runtime.ic`。
3. LED 应每秒循环 `111 → 222 → 333`。

## 生成

```bash
ic10c build --data-access stack --data-only demo.icg > 1_loader.ic
ic10c build --data-access stack             demo.icg > 2_runtime.ic
```

loader 用 `poke`；runtime 读取任意地址时先保存 `sp`、把 `sp` 指到 `addr+1`、
`peek`、再恢复 `sp`。loader 与 runtime 必须用同一 `--data-access` 编译。

> 建议分别在**标准 IC host** 和**设备 host（如空调）**上各测一次：都应正常循环。

## 实测结果

真机通过 ✅：**标准 IC host 与设备 host（空调）**上均正常循环 `111/222/333`，
版本校验正常。宿主兼容问题闭环。
