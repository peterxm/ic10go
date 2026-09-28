# 信号反转（0↔1）的编译研究

> 题目：把一路信号取反——`0 → 1`、`1 → 0`。用几种等价写法编译，观察 `ic10c` 的优化器
> （比较折叠、`select` 转换、分支融合）如何降级与取舍。
>
> 数据来自 `ic10c 0.8.23`。测试台：`A CHIP`，`d4` = Logic Memory（可写输入）、
> `d0` = LED（输出）。可跑示例见 [`testdata/bench/not.json`](../testdata/bench/not.json)。

---

## 1. 推荐写法：`!x` → 单条 `seqz`

```go
func main() {
	for {
		yield()
		d0.Setting = !d4.Setting
	}
}
```

产物（5 行 / 53 字节 / 1 寄存器）：

```asm
yield
l r0 d4 Setting
seqz r0 r0        ; r0 = (r0 == 0) ? 1 : 0
s d0 Setting r0
j 0
```

`!x` 被降成单条 `seqz`：没有分支、没有额外寄存器。

---

## 2. 各种写法的产物对比

| 写法 | 关键指令 | 行 | 字节 | 备注 |
|---|---|--:|--:|---|
| `!x` | `seqz r0 r0` | 5 | 53 | **推荐**，布尔反转 |
| `x == 0`（作表达式赋值） | `seqz r0 r0` | 5 | 53 | 同上 |
| `var v; if x==0 {v=1} else {v=0}; store v` | `select r0 r0 0 1` | 5 | 59 | if/else → `select`，再常量折叠 |
| `if x==0 { s d0 1 } else { s d0 0 }` | `bnez` + 两条 `s` | 7 | 70 | 分支未合并（副作用） |
| `1 - x` | `sub r0 1 r0` | 5 | 54 | 仅对 0/1 正确 |
| `logicalNor(x, 0)` | `nor r0 r0 0` | 5 | 54 | 按位取反（0→-1，1→-2），**非**布尔反转 |

（都是 `yield` + `l` + 计算 + `s` + `j` 的骨架；最长行 ≤ 16/90。）

---

## 3. 逐条分析

### 3.1 `!x` / `x == 0` → `seqz`

```asm
l r0 d4 Setting
seqz r0 r0
```

`seqz`（set-if-equal-zero）本身就是“`x == 0` 的结果”，值域正好是 `{0,1}`。所以布尔反转
不需要任何分支。`d0.Setting = x == 0` 得到完全相同的产物——`== 0` 的比较结果直接就是
想要的 1/0，优化器不会再多做一次布尔化。

### 3.2 if/else 赋值 → `select`（select 转换 + 常量折叠）

```go
var v = 0
if d4.Setting == 0 { v = 1 } else { v = 0 }
d0.Setting = v
```

```asm
l r0 d4 Setting
select r0 r0 0 1
```

两个分支都只是给**同一个局部变量**赋值。优化器先做 **select 转换**（把控制流合并成
一条 `select`），再把条件 `x == 0` 与常量分支一起折叠，得到 `select r0 r0 0 1`
（`r0 = (r0 != 0) ? 0 : 1`）。

### 3.3 直接向设备写值的 if/else → 保留分支

```go
if d4.Setting == 0 { d0.Setting = 1 } else { d0.Setting = 0 }
```

```asm
l r0 d4 Setting
bnez r0 5
s d0 Setting 1
j 0
s d0 Setting 0
j 0
```

两个分支各自是**对外可观测的设备写**。优化器不会把设备写挪进 `select`（那会改变可观测
的写序列，也可能对不支持该逻辑的设备出错），所以保留 `bnez` 跳转——比“算好再写”多 2 行。

**结论**：想让 if/else 合进 `select`，就让分支只计算**纯值**（写局部变量），最后再统一写设备。

### 3.4 语义陷阱：`1 - x` 与 `logicalNor`

- `d0.Setting = 1 - d4.Setting` → `sub r0 1 r0`。只对 `{0,1}` 成立：输入 `5` 会得到 `-4`。
- `d0.Setting = logicalNor(d4.Setting, 0)` → `nor r0 r0 0`。IC10 的 `nor` 是**按位** NOR：
  `~(x | 0) = ~x`，所以 `0 → -1`、`1 → -2`。要**布尔**反转用 `!x`（`seqz`）；要**按位**
  取反用 `not` / `nor`。

---

## 4. 真机验证

[`testdata/bench/not.json`](../testdata/bench/not.json)：

```json
{
  "program": "not.icg",
  "chip": { "name": "A CHIP" },
  "ports": { "d0": "LED", "d4": "Memory" },
  "cases": [
    { "name": "0 -> 1", "set": { "d4.Setting": 0 }, "run": 2, "expect": { "d0.Setting": 1 } },
    { "name": "1 -> 0", "set": { "d4.Setting": 1 }, "run": 2, "expect": { "d0.Setting": 0 } },
    { "name": "5 -> 0", "set": { "d4.Setting": 5 }, "run": 2, "expect": { "d0.Setting": 0 } }
  ]
}
```

```bash
ic10c testbench run testdata/bench/not.json --diff
# VM + 真机： 0→1 ✓  1→0 ✓  5→0 ✓
```

---

## 5. 结论

1. **布尔反转就用 `!x`（或 `x == 0`）**：一条 `seqz`，最少字节。
2. **按条件在两个值里选**：写成 `var v; if/else { v = ... }`，优化器会转 `select`；
   直接在分支里做**设备写**不会合并。
3. 用 `1 - x` / `logicalNor` 之前先确认**定义域与位语义**——它们不是布尔反转。

---

## 6. 复现

```bash
ic10c build not.icg          # 看产物
ic10c stats not.icg          # 行 / 字节 / 寄存器预算
ic10c build --json not.icg   # 机器可读（chips[]/stats/诊断）
ic10c disasm old.ic          # 对既有 IC10 反汇编注释
```

## 7. 相关

- 优化器 pass 列表（常量折叠 / CSE / **select 转换** / **比较-分支融合** / 强度削减）：
  [`architecture.md`](architecture.md) 的 M2。
- IC10 目标约束与指令映射：[`target-ic10.md`](target-ic10.md)。
- 更多可跑场景与 `--diff` 差分：[`ingame-testbench.md`](ingame-testbench.md)。
