# 特殊寄存器 / select 的操作数折叠

> 题目：把 `ic10code/reoreotest.ic10`（92 行）反编译成 `.icg` 再编译回来，产物却涨到
> 97 行。找出多出来的行，把「单次使用的读」直接折叠进消费者（设备写 / 内建 / `select`），
> 并让 `select` 也能直接写 `sp`/`ra`。
>
> 数据来自 `ic10c 0.8.23` 之后的工作树；可跑示例见
> [`testdata/bench/ingame/`](../testdata/bench/ingame/)。

---

## 1. 现象

| 阶段 | 行数 |
|---|---:|
| 原版 `reoreotest.ic10` | 92 |
| `ic10c decompile reoreotest.ic10` → `.icg` | 106 |
| `ic10c build` 该 `.icg`（改动前） | 97（+5） |

多出来的行主要来自「先 `move rX sp` 再用 `rX`」这类中转拷贝——而 IC10 里 `sp`/`ra`
本就能直接当源操作数，设备写、`poke`、`select` 也都接受普通寄存器/特殊寄存器/间接寄存器。

> `ic10code/reoreotest.*` 是**本地语料**（被 `.gitignore` 的 `/ic10code/**` 忽略），
> 因此上表的具体数字只在本机可复现；同样的往返命令对任意脚本都适用，见 §7。

---

## 2. 三处改动

### A. 读折叠扩展到更多消费者（`codegen.foldLoadOperand`）

原先只认算术消费者 `u = <load>; d = u op b`。现在消费者可以是任何把该寄存器当普通源操作数
的指令：设备写 `s` / `ss` / `sd`、内建 `poke`/`put`/…、`select`。约束不变：临时寄存器
**全局单次使用**、读必须**紧邻其消费者**、且消费者**确实读它**。

```asm
u = ireg(rrP)      ; u = sp
s db Setting u     ->  s db Setting sp
poke u v           ->  poke sp v
select d c u e     ->  select d c sp e
```

### B. `select` 也能直接写 sp/ra（`codegen.specialComputeText`）

```asm
t = select c a b   ->  select sp c a b
sp = t
```

`<op>` 现在支持二元、一元（`neg`/`not`/`seqz`）和 `select`，前提仍然是临时寄存器全局只有
这一处使用。注意：这只在 `select` 与其 `sp` 存储在**同一个基本块**内才触发（见 §4）。

### C. `select` 化放宽（`opt.selectConvert`）

原来要求两个分支都恰好一条赋值。现在：

- 分支可以是**拷贝链** `t = x; dst = t`，解析成 `x`。中间寄存器必须只在本分支内使用，
  否则拒绝——否则删掉拷贝后，分支外的读者会拿到未定义值。
- 分支可以是**空**：值原样直通合并目标 `dst`。

这来自 `x ? y : 20` 这类三元表达式：一个分支的值本来就已经在 `dst` 里，拷贝被传播消掉后
就剩一个空分支。

---

## 3. 结果

### 3.1 反编译往返（`.ic10` → 反编译 `.icg` → 重编译）

**92 → 97 → 95**。两处真实的折叠：

```asm
; 改动前                 ; 改动后
move r0 sp                 s db Setting sp
s db Setting r0
```

```asm
; 改动前                 ; 改动后
move r1 sp                 poke sp r0
poke r1 r0
```

其余差值只是分支行号随行数 -2 平移。

### 3.2 手写端口 `reoreotest.icg`

```bash
ic10c build ic10code/reoreotest.icg | wc -l    # 100 → 98 → 97
```

91 行源码编译成 **97** 行（原始 `.ic10` 是 92 行）。开头两行现在与原始一致：

```asm
select sp r5 r5 20     ; 原始第 3 行就是 select sp r14 r14 20
s db Setting sp        ; 原始第 4 行
```

---

## 4. 跨块（fall-through）折叠

B 的前提是 `select` 与写 `sp` 在同一个基本块。手写端口里 `sp = saved != 0 ? saved : 20`
的 `select` 在循环头块、`move sp r0` 在下一个块——两者在输出里相邻，只因为中间的 `j` 被当作
fall-through 省掉了。codegen 现在在**渲染阶段**跨过这个边界折叠：当块 `b` 以无条件 `j` 跳到
布局上的**下一个块**、且该后继只有 `b` 一个前驱时，把后继的指令拼进折叠窗口。跳转被省掉、又
没有别的分支进入后继，拼接在语义上就是一条直线，于是 `select ...; move sp ...` 折成
`select sp ...`。

这是纯渲染折叠（不动 IR/CFG），比在优化器里合并基本块安全得多。`mergeBlocks` 试过：随机差分
立刻找出 CFG 被破坏的用例（返回块被折进调用返回块、跳转目标悬空），所以没启用。「后继单前驱」
这条约束也不能省——否则有别的分支跳到后继的第一条指令，拼进去会多执行前一条指令。

---

## 5. 位级语义对齐（手写 `.icg` vs 原始 `.ic10`）

手写端口的 `adjust()` 原本写：

```go
throttle := max(total-110, 0) + (total > 0 ? 10 : 0)
```

而原始 `.ic10` 是先减 10（作为 CombustionLimiter 写出）、**再**减 100：

```asm
86: sub r8 r8 10        ; total - 10
87: s r15 CombustionLimiter r8
88: sub r8 r8 100       ; (total - 10) - 100
```

当设备值涨到 ~1.8e16（ULP = 2）时，`(total-10)-100` 与 `total-110` 会差 1 ULP。逐条比对
写序列（16 个种子）只在 seed 1 的 `db.Throttle` 上暴露一次：

```
orig=db.Throttle=1.8014398509481982e+16
port=db.Throttle=1.8014398509481984e+16
```

修法是让 `.icg` 保留原始的两步减法（`combustionLimiter := total - 10` 写出后再 `- 100`）。
改完后 16 个种子的写序列**逐字节一致**，剩余 +5 行则来自源码结构（条件调用 `blez; jal; move`
对比原始 `bgtzal`，以及状态机用重派发 `j` 代替原始的 fall-through）。

---

## 6. 正确性

- 单元测试：`internal/codegen` 的 `TestFoldSpecialIntoDeviceStore` / `…Poke` / `…Select`
  / `…SlotStore`、`TestFoldSpecialArithSelect`、跨块折叠的 `TestFoldAcrossFallthroughBlock`
  与反例 `TestNoFoldAcrossSharedBlock`；`internal/opt` 的
  `TestSelectConvertCopyChain` / `…PassthroughBranch` / `…KeepsLiveIntermediate`。
- 语料往返 `TestIc10CodeRoundTrip*`：设备写序列一致。
- 差分 `TestDifferential*`：优化产物 vs `IC10C_NO_OPT`，随机程序对照。
- 真机 `sh testdata/bench/ingame/run.sh`：**10/10**（真机 + 内置 VM 差分），其中
  `s16_special_fold.json` 专门走跨块折叠（`sp = ...? ... : ...; d0.Setting = sp`）。

---

## 7. 端口测试口径的修正

`pkg/ic10/ports_test.go` 原来把两个脚本各跑固定**步数**再比较「可达设备状态集合」。移除指令
后端口更快，同样步数里写事件更多，集合就成了超集而误报——但逐条比对**写序列**其实完全一致。
所以改成：各自跑到固定步数，记录**每次写之后**的状态，再按**两边共同的写次数**截断比较。
这样口径与「可观测设备写序列」一致，不再因为端口更快就判失败。

---

## 8. 复现

`reoreotest.*` 是本地语料，别人 clone 不到；换成任意脚本走同样的往返即可。仓库自带的
可跟踪语料（如 `ic10code/氧气过滤灌装.ic`）可以直接试：

```bash
# 反编译 → 重编译（用本地 reoreotest 时，实测 106 行 → 95 行；改动前 97 行）
ic10c decompile <x>.ic > x.icg
ic10c build x.icg | wc -l

# 手写端口（本地）：100 → 98 → 97 行
ic10c build ic10code/reoreotest.icg | wc -l

# 用仓库自带的已跟踪语料
ic10c decompile "ic10code/氧气过滤灌装.ic" > /tmp/x.icg   # 21 行
ic10c build /tmp/x.icg | wc -l                            # 15 行

# 正确性回归（不依赖本地语料）
go test ./internal/codegen ./internal/opt ./pkg/ic10
sh testdata/bench/ingame/run.sh
```

## 9. 相关

- codegen 折叠总览：[`architecture.md`](architecture.md) §7。
- 端口 / 差分 / 真机测试：[`ingame-testbench.md`](ingame-testbench.md)。
- `select` 的其它用法（信号反转）：[`signal-invert.md`](signal-invert.md)。
