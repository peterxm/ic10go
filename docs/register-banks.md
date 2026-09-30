# 间接寄存器（`rrN`）与「寄存器当数组」

> 题目：社区服务器存档里那块 `StructureCircuitHousing`（118）用 `rrN` 把 4 个太阳能板
> prefab hash 放在 `r0..r3`、用一个变量当索引进而遍历。挖清楚这种写法在 `.icg` 里怎么表达、
> 编译器在「编译期」能替我们做多少，以及反编译器怎么还原它。

---

## 1. `StructureCircuitHousing` 在做什么

原脚本 74 行，是个太阳能板追踪器。开头把 4 个 prefab hash 塞进寄存器：

```
move r0 -2045627372   # StructureSolarPanel
move r1 -539224550    # StructureSolarPanelDual
move r2 -934345724    # StructureSolarPanelReinforced
move r3 -1545574413   # StructureSolarPanelDualReinforced
alias list r4
...
loop:
brne list 4 3         # list(0..3) 走完？
move list 0
j start
L70:
sb rr4 Horizontal horizontal   # 批量写「list 指向的那块板」
sb rr4 Vertical vertical
add list list 1
j loop
```

`rr4` = **以 r4 的值为编号**的那个寄存器。`list` 恒在 `[0,3]`，所以 `rr4` 依次指向
`r0/r1/r2/r3`——这是一张**放在寄存器里的表**，索引在 `list` 里。`sb rr4 …` 则按表项里的
prefab hash 批量写设备。

要点：`list`（r4）是**索引寄存器**，`r0..r3` 是**被索引的目标寄存器**。两者角色不同。

---

## 2. `.icg` 里的表达

语法的低层入口是三个内建（见 [`spec.md`](spec.md) §内建）：

| 内建 | 作用 |
|---|---|
| `reserveRegs(lo, hi)` | 声明物理寄存器 `lo..hi` 归间接访问专用，分配器不碰 |
| `ireg(ptr)` | 读 `ptr` 指向的寄存器（IC10 `rrN`，`ptr` 是**寄存器号**） |
| `setIreg(ptr, v)` | 写 `ptr` 指向的寄存器 |

118 的手写端口长这样：

```icg
func main() {
    reserveRegs(0, 3)
    setIreg(0, hash("StructureSolarPanel"))
    setIreg(1, hash("StructureSolarPanelDual"))
    setIreg(2, hash("StructureSolarPanelReinforced"))
    setIreg(3, hash("StructureSolarPanelDualReinforced"))
    var list = 0
    ...
    batch.write(ireg(list), "Horizontal", horizontal)
    ...
}
```

`list` 仍是普通变量（编译器随便给它分寄存器），`r0..r3` 被钉成物理寄存器。

---

## 3. 编译期能替我们做到什么

这部分答案是：**已经做得不错，而且是分层的**。

**（a）常量指针直接折叠成物理寄存器（根本不用 `rrN`）。**

```icg
reserveRegs(0, 3)
setIreg(0, 7)              ->  move r0 7
d1.Setting = ireg(3)       ->  s d1 Setting r3
setIreg(2, ireg(1) + 1)    ->  add r2 r1 1
```

**（b）动态指针才发 `rrN`，且被操作数折叠压回一行。**

```icg
reserveRegs(0, 3)
i := d0.Setting
if i < 0 || i >= 4 { return }
d1.Setting = ireg(i)
```
```
l r6 d0 Setting
sltz r5 r6
sge r4 r6 4
max r4 r5 r4
bnez r4 6
s d1 Setting rr6      # 一次设备写，没有中间拷贝
```

即 D6 那套折叠（见 [`special-reg-operands.md`](special-reg-operands.md)）直接让间接读当
源操作数、间接写当目标操作数，`t = ireg(p); use(t)` 不再多一行。

**（c）反编译器负责把 `rrN` 还原成 `reserveRegs` + `ireg`/`setIreg`。**

`internal/decomp/bank.go` 做一个小型抽象解释，求出「运行期指针可能命中的寄存器集合」，
把这段保持为物理寄存器。它的**起始状态现在按 IC10 语义取全 0**（`r0..r15` 与 `sp` 都是 0），
所以像 118 这种「索引从不赋初值、直接用」的表也能定界（得到 `reserveRegs(0, 3)`）。指针
真正未知（来自设备读等）时按设计放弃并报 warning，而不是静默产出可能读错寄存器的代码。

> 这也是 118 之前的坑：分析器把「未写过的寄存器」当未知，于是 `list` 未知 → 无法定界 →
> 回退到变量翻译，产物把 `rr4` 编译成 `rr6`（`list` 恰好被分到 r6）——测试却因为
> `portSetup` 没给设备 prefab hash 而**空过**。端口测试对 `sb`（按 hash 批量写）是盲区，
> 这点见 §5。

---

## 4. 能不能让编译器**自动**把小的 `data` 表换成寄存器组？

直觉上很诱人：小表直接放进寄存器，连数据段都不用装。实测同一段逻辑两种写法：

**A：`data` 常量表（持久栈 + 哨兵校验）**

```icg
data T = [hash("A"), hash("B"), hash("C"), hash("D")]
func main() {
  for {
    i := d0.Setting
    if i < 0 || i >= 4 { continue }
    batch.write(T[i], "On", 1)
  }
}
```
```
get r0 db 507                # 数据段哨兵
bne r0 390122420 129
l r2 d0 Setting
sltz r1 r2
sge r0 r2 4
max r0 r1 r0
bnez r0 2
add r0 508 r2                # base + i
get r0 db r0                 # 读表
sb r0 On 1
j 2
```

**B：寄存器组（`reserveRegs` + `ireg`，自包含）**

```
move r0 -740712821           # 表内容
move r1 1255198513
move r2 1037565863
move r3 -1548523004
l r6 d0 Setting
sltz r5 r6
sge r4 r6 4
max r4 r5 r4
bnez r4 4
move r4 rr6                  # 取表项
sb r4 On 1
j 4
```

运行期行数 **11 vs 12**，基本持平。但差别在别处：

| | `data` 表（A） | 寄存器组（B） |
|---|---|---|
| 需要额外的安装步骤 | 是（loader 芯片或跑一次安装代码） | 否 |
| 持久栈占用 | 有 | 无 |
| 寄存器占用 | 无 | `hi-lo+1` 个 + 取表项的临时 |
| 读表一条指令 | `get db addr`（1） | `move t rrP`（1，已折叠） |
| 越界安全 | `get` 越界读栈 | **无**，`rrN` 越界会读到任意寄存器 |

结论：**不值得做成默认优化**。寄存器只有 16 个，是比栈更稀缺的资源；自动降级很可能把
热循环的变量挤到栈上，得不偿失。可行且稳妥的做法是「显式选择、编译器只做折叠」——也就是
现状：`data` 用于常数表、需要自包含的小表就手写 `reserveRegs` + `ireg`，编译器在编译期把
常量指针折成物理寄存器、动态指针折成一条 `rrN`。若以后要做自动化，建议只在「表很小
（≤4）且该芯片没有其他寄存器压力」时作为一个**可选** flag（不改变默认行数取向）。

---

## 5. 回归语料与盲区

- `ic10code/github/gamechips/indirectRegsHousing.{ic,icg}`：118 的端口，现可正确还原成
  `reserveRegs(0, 3)` + `setIreg(0..3, hash(...))`，通过端口等价 / 往返（flat + structured）
  / minify 三项测试。
- **盲区**：`TestIc10CodePorts` 的 `portSetup` 不给设备 `Hash`/`ReferenceId`，因此
  `sb rrN …`（按 hash 批量写）在原脚本和端口里都匹配不到设备，比较是空过的。要真正覆盖
  这类程序的接口，需要按 prefab hash 建一批设备（或改用 `TestIc10CodeRoundTripStack` 那样
  比较宿主栈）。这一条留给后续。
