# 真机测试：Adv_Airlock_Smol（数据段版）

这份端口用新的 **持久栈数据段** 存逻辑类型列表（`data LogicTable`），
runtime 不再 `poke` 逻辑类型，而是用 `LogicTable[i]` 读。请按下面步骤在游戏里验证。

## 0. 被测对象

- 端口源码：`ic10code/Adv_Airlock_Smol.icg`（63 行，数据段 `503..511`）
- 原始脚本：`ic10code/Adv_Airlock_Smol.ic`
- 生成命令（本目录两份 `.ic` 已生成好）：

  ```bash
  ic10c build --data-only "ic10code/Adv_Airlock_Smol.icg" > 1_loader.ic
  ic10c build             "ic10code/Adv_Airlock_Smol.icg" > 2_runtime.ic
  ```

## 1. 前置条件

- 一个**标准 IC host**（`d0`–`d5`；设备 host 不支持 `put db`，会 `MemoryNotWriteable`）。
- 设备接线（与原始脚本一致）：

  | 端口 | 设备 | 用途 |
  |------|------|------|
  | `d0` | 外门 Exterior Door | `Mode` / `Setting` / `Open` |
  | `d1` | 外通风 Exterior Vent | `Mode` / `On` |
  | `d2` | 内通风 Interior Vent | `Mode` / `On` |
  | `d3` | 内门 Interior Door | `Mode` / `Setting` / `Open` |
  | `d4` | 压力传感器 Sensor | `Pressure` |

> 只验证“数据段两段流程 + 版本校验”的话，可以不接真实气闸：只要 host 有
> `d0`–`d4` 五个接口即可，runtime 不会停机就说明 loader 生效了。

## 2. 步骤

1. 把 `1_loader.ic` 贴进 IC 编辑器并**运行一次**（写入逻辑类型表 + 版本哨兵）。
2. 整段**覆盖**成 `2_runtime.ic`。
3. 观察气闸行为 / 日志。

## 3. 代码

### `1_loader.ic`（运行一次）

```
put db 503 1541576327
put db 504 LogicType.Open
put db 505 LogicType.On
put db 506 LogicType.On
put db 507 LogicType.Open
put db 508 LogicType.Setting
put db 509 0
put db 510 0
put db 511 LogicType.Setting
```

槽位约定：`503` 版本哨兵，`504..511` = `LogicTable[0..7]`
（`0` 表示该槽在运行时跳过）。

### `2_runtime.ic`（覆盖后长期运行）

```
get r0 db 503
sne r0 r0 1541576327
beqz r0 4
j 9999
move r1 0
yield
l r4 d2 Mode
l r3 d4 Pressure
move r6 8
sub r0 r6 1
move r6 r0
add r0 504 r0
get r5 db r0
beq r5 0 17
mod r2 r6 4
l r0 dr2 r5
ins r1 r0 r6 1
bgt r6 0 9
sra r0 r1 4
xor r0 r0 r1
and r6 r0 9
and r1 r1 15
and r0 r1 8
select r0 r0 14 15
and r1 r1 r0
beq r1 0 5
sub r0 r1 1
and r5 r0 r1
bne r5 0 5
and r2 r1 9
move r5 r2
sra r0 r1 3
select r4 r2 r0 r4
bne r6 0 41
bne r5 0 45
sll r5 2 r4
bne r1 r5 38
beq r3 0 41
beq r1 r5 45
select r5 r4 0 100
blt r3 r5 45
move r6 0
select r0 r4 0.5 2
mul r1 r1 r0
j 22
ins r1 r1 4 4
s d0 Mode 1
s d3 Mode 1
s d2 Mode r4
seq r0 r4 0
s d1 Mode r0
move r6 8
sub r0 r6 1
move r6 r0
add r0 504 r0
get r5 db r0
beq r5 0 61
mod r2 r6 4
sra r0 r1 r6
and r0 r0 1
s dr2 r5 r0
bgt r6 0 52
j 5
```

前 4 行是版本校验：读槽 `503`，与 `1541576327` 不符则 `j 9999` 停机。

## 4. 预期结果

| 观察点 | 预期 |
|--------|------|
| 贴入 loader 运行后 | 无报错；IC 栈槽 503 被写入 `1541576327` |
| 覆盖成 runtime 后 | **不停机**（版本校验通过），气闸状态机运行 |
| 状态机运行 | 根据门/通风控制位与 `d4.Pressure` 切换相位，写 `d0/d3.Mode`、`d1/d2.Mode`，并把相位位写回 `d3.Setting`、`d0.Setting`、`d3.Open`、`d2.On`、`d1.On`、`d0.Open` |

对照原脚本 `ic10code/Adv_Airlock_Smol.ic`（或旧端口）应得到相同的设备写入行为。

## 5. 如何单独确认数据段生效

1. **不跑 loader** 直接贴 runtime：应**立刻停机**（`j 9999`），气闸无动作
   —— 说明版本校验在起作用。
2. 跑 loader 后再贴 runtime：不停机 —— 说明 loader 写入的数据被 runtime 读到。
3. （可选）另起一张 IC，用 `get r0 db 503` 读宿主栈，应得到 `1541576327`。

## 6. 常见问题

| 现象 | 原因 / 处理 |
|------|-------------|
| 贴 loader 报 `MemoryNotWriteable` | 芯片插在**设备 host**（空调等）上；换标准 IC host，或用 `--data-access stack` 重新生成两份 |
| runtime 立刻停机、气闸不动 | 没有先跑 loader，或数据被 `clr db`/断电清掉；重跑 loader |
| 行为与旧脚本不一致 | 确认两份 `.ic` 是同一次编译产物（版本号一致），且设备接线正确 |
| 想进一步压缩 runtime | 用 `--unsafe` 生成（跳过版本校验，约省 4 行；此时不跑 loader 会读到 0 而不是停机） |

## 7. 备注

- 数据段放在栈顶（默认 `--data-layout top`）；本脚本没有 `push`/`poke`，不涉及
  栈冲突。`ic10c stats` 会给出 `data slots 503..511 (9 values)`。
- loader 与 runtime 必须来自**同一份源码 + 同一组选项**（否则版本号不符会停机）。
