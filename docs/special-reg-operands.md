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

`reoreotest.ic10` 往返：**92 → 97 → 95**。两处真实的折叠：

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

---

## 4. 为什么还差 3 行：B 的「同一个基本块」前提

`sp = savedSP != 0 ? savedSP : 20` 产生的 `select` 落在循环头块，而写 `sp` 的
`move sp r0` 在下一个块；codegen 逐块折叠，跨块就不触发——尽管两者在最终输出里是相邻两行
（中间的 `j` 被当作 fall-through 省掉了）。

要让 B 在 `reoreotest` 生效，需要在优化器里把「单前驱、以无条件跳转相连」的块合并成一个块
（`mergeBlocks`）。这个 pass 实现过，但随机差分测试立刻找出 CFG 被破坏的用例（返回块被折进
调用返回块、跳转目标悬空等），因此**没有启用**。`select sp` 的收益是 1 行，不值得冒这个风险；
`.ic10` 原版的 `select sp r14 r14 20` 仍是我们想要的形态，等块合并稳妥后再补。

---

## 5. 正确性

- 单元测试：`internal/codegen` 的 `TestFoldSpecialIntoDeviceStore` / `…Poke` / `…Select`
  / `…SlotStore`、`TestFoldSpecialArithSelect`；`internal/opt` 的
  `TestSelectConvertCopyChain` / `…PassthroughBranch` / `…KeepsLiveIntermediate`。
- 语料往返 `TestIc10CodeRoundTrip*`：设备写序列一致。
- 差分 `TestDifferential*`：优化产物 vs `IC10C_NO_OPT`，随机程序对照。
- 真机 `sh testdata/bench/ingame/run.sh`：9/9（真机 + 内置 VM 差分）。

---

## 6. 端口测试口径的修正

`pkg/ic10/ports_test.go` 原来把两个脚本各跑固定**步数**再比较「可达设备状态集合」。移除指令
后端口更快，同样步数里写事件更多，集合就成了超集而误报——但逐条比对**写序列**其实完全一致。
所以改成：各自跑到固定步数，记录**每次写之后**的状态，再按**两边共同的写次数**截断比较。
这样口径与「可观测设备写序列」一致，不再因为端口更快就判失败。

---

## 7. 复现

`reoreotest.*` 是本地语料，别人 clone 不到；换成任意脚本走同样的往返即可。仓库自带的
可跟踪语料（如 `ic10code/氧气过滤灌装.ic`）可以直接试：

```bash
# 反编译 → 重编译（用本地 reoreotest 时，实测 106 行 → 95 行；改动前 97 行）
ic10c decompile <x>.ic > x.icg
ic10c build x.icg | wc -l

# 用仓库自带的已跟踪语料
ic10c decompile "ic10code/氧气过滤灌装.ic" > /tmp/x.icg   # 21 行
ic10c build /tmp/x.icg | wc -l                            # 15 行

# 正确性回归（不依赖本地语料）
go test ./internal/codegen ./internal/opt ./pkg/ic10
sh testdata/bench/ingame/run.sh
```

## 8. 相关

- codegen 折叠总览：[`architecture.md`](architecture.md) §7。
- 端口 / 差分 / 真机测试：[`ingame-testbench.md`](ingame-testbench.md)。
- `select` 的其它用法（信号反转）：[`signal-invert.md`](signal-invert.md)。
