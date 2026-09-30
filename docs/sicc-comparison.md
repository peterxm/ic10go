# SICC 对照分析：设备参数外提与行数差距

> 对象：社区项目 [Alan-Chen99/sicc](https://github.com/Alan-Chen99/sicc)（Python 追踪式 IC10 编译器，
> `master` @ `9b5c7f5`，2025-11）。对照版本：`ic10c 0.8.28+42a3d2a`。
> 目的：量化「同一份温控程序，sicc 17 行 / 本项目 20 行」的差距，拆出根因，给出
> **难度 / 风险 / 收益** 评估与推进顺序。本文只做分析，不改代码。

---

## 1. 摘要（TL;DR）

- 差距是真实的，但**不是普通的指令选择/寄存器分配问题**，而是两种**调优策略**在
  「设备端口参数」上的分歧：sicc 用**运行期间接设备操作数 `drN`** + **子程序共享** +
  **尾调用复用 `ra`**；本项目用**内联 + 常量折叠 + 直接端口 `dN`**。
- 该程序里两种策略的取舍恰好偏向 sicc：函数体不算小（9 行），却被调用两次，且两次调用
  只差设备端口。本项目因为「带设备实参的调用强制内联」把 9 行体复制了两遍。
- 差距可拆成三块：**W1 设备参数不能走寄存器（`drN`）**（大头）、**W2 常量实参强制内联**
  （外层决策）、**W3 无尾调用 / `ra` 复用**（1 行）。
- 顺带发现一个**潜在缺陷**：把运行期数值传给设备参数时，本项目会输出非法 IC10
  （`sgt r3 pa.Temperature 297`），只给一条 `unknown enum` 警告，不报错。
- 建议顺序：**I4（修缺陷，小而低风险）→ I1（设备参数 `drN`，收益最大）→ I2（大小模型
  纳入设备/常量实参）→ I3（尾调用，可选）**。整体属于**中等工作量、中风险、中大收益**，
  且本项目已有的**差分/真机回归网**能显著压低风险。

---

## 2. 复现与测量

### 2.1 源程序

`.icg`（等价改写，见附录）：

```go
const (
	PA1 = d0; Valve1 = d1; PA2 = d2; Valve2 = d3
	Target1 = 297; Target2 = 297
)
func controlTemp(pa, valve, target num) {
	needCooling := pa.Temperature > target
	delta := abs(target - pa.Temperature)
	power := 10
	if delta > 5 { power = 100 }
	valve.On = needCooling
	valve.Setting = power
}
func main() {
	for {
		yield()
		controlTemp(PA1, Valve1, Target1)
		controlTemp(PA2, Valve2, Target2)
	}
}
```

### 2.2 两侧产物（实测）

**sicc（17 行 / 190 字节）** — 运行 `python examples/ic11_compare.py` 得到的当前输出：

```
yield
move r1 0
move r0 1
jal 7
move r1 2
move r0 3
move ra 0
l r2 dr1 Temperature
slt r1 297 r2
sub r2 297 r2
abs r2 r2
move r3 10
ble r2 5 14
move r3 100
s dr0 On r1
s dr0 Setting r3
j ra
```

**本项目（20 行 / 249 字节）** — `ic10c build temp_control.icg`：

```
yield
l r0 d0 Temperature
sgt r2 r0 297
sub r0 297 r0
abs r1 r0
move r0 10
ble r1 5 8
move r0 100
s d1 On r2
s d1 Setting r0
l r0 d2 Temperature
sgt r2 r0 297
sub r0 297 r0
abs r1 r0
move r0 10
ble r1 5 17
move r0 100
s d3 On r2
s d3 Setting r0
j 0
```

| 指标 | sicc | ic10go | 差距 |
|---|---|---|---|
| 行数 | 17 | 20 | +3（+17.6%） |
| 字节 | 190 | 249 | +59（+31%） |
| 函数体出现次数 | 1 | 2 | — |

统计：`ic10c stats` → `lines 20/128, bytes 249/4096, registers 3/16, peak live 3/16`。

### 2.3 关键结构差异

- **sicc**：`control_temp` 只发射一次；设备端口作为**运行期参数**放在 `r0/r1`，用
  **间接设备操作数** `dr1` / `dr0` 访问；常量 `297` 折进函数体；`move ra 0` 把返回地址
  指回循环头，最后一次调用**直落**进函数体、`j ra` 收尾 —— 省掉一次 `jal` 与一个回边 `j`。
- **本项目**：两次调用实参全是编译期常量（设备端口 + 297），函数被判定「可折叠」并
  **内联两遍**；回边是普通 `j 0`。

---

## 3. 根因拆解

### W1（主因）设备端口参数不能走寄存器 —— 缺少 `.属性 → drN` 的下沉

本项目把「设备实参」当**编译期符号**。`internal/lower/lower.go:2149`：

```go
if l.outline[id.Name] && !l.inOutlineBody && !l.hasDeviceOrDataArg(call.Args) {
	return l.outlineCall(...)   // 只有实参中没有设备/表时才可能外提
}
return l.inlineCall(...)
```

`hasDeviceOrDataArg` 见 `lower.go:2858`。规范里也明确写了这条（`docs/spec.md` §4.3）：
「带设备 / 表实参的调用**强制内联**（即使该函数原本会被外提为子程序）」。

- 也就是说：只要调用点出现 `PA1`/`Valve1` 这类端口，`controlTemp` **永远不可能被外提**，
  函数体被复制到每个调用点。
- 本项目其实**已经有**运行期设备访问：`readDev`/`writeDev` → `LoadDyn`/`StoreDyn` →
  `drN`（`lower.go:1963`、`docs/target-ic10.md` §指令映射）。缺的是把 `pa.Temperature`
  这类**语法糖**在「`pa` 是运行期端口」时也下沉到 `LoadDyn`，以及在外提调用约定里
  允许用寄存器传端口号。

### W2 常量实参调用强制内联 —— 决策层次

即使 W1 解决，`outlineCall` 里还有一道闸（`lower.go:2623`）：

```go
if l.argsConstant(args) {
	return l.inlineCall(id, fi, args, needResult) // 常量实参一律内联以折叠
}
```

对「常量实参能显著折叠」的函数（如 `(a+5)*3`）这是对的；但对「常量实参只用来选端口、
折叠后体仍然很大」的函数，内联就变成负优化。决策应由**大小模型**（已有 inline/outline
候选比较）统一裁决，而不是一刀切。

### W3 无尾调用 / `ra` 复用

本项目外提体固定以 `j ra` 结尾，`main` 的无限循环固定以 `j <行号>` 收尾（`codegen.go:357`）。
sicc 在**尾位置**的调用上做文章：把返回地址设成「调用之后要去的地方」，让被调体直接
`j ra` 落到目的地，从而把「`jal` + 回边 `j`」并成一条 `move ra`。本例恰好省 1 行。

### D0（潜在缺陷）运行期设备参数会静默产出非法代码

把运行期数值传进设备参数时（`p := Dial.Setting; controlTemp(p, Valve1, 297)`），
本项目输出：

```
warning: unknown enum "pa.Temperature"; emitting verbatim
sgt r3 pa.Temperature 297      # 非法 IC10
```

`pa.Temperature` 被原样吐出，既不是 `dN` 也不是 `drN`，运行时必然出错，而只有**警告**、
没有报错。这是「设备参数只实现了一半」的直接后果：常量走通了，运行期值没有人管。

---

## 4. 改进项（难度 / 风险 / 收益）

约定：难度 / 风险分 **小 / 中 / 大**；收益分 **小 / 中 / 大**（对「省行 + 表达力 + 正确性」综合）。

### I1 设备参数走间接操作数（`drN`）—— 核心项

**做法**：
1. 标记「被当设备用」的参数（出现 `x.Property`、`x.slot[...]`、作为 `readDev/writeDev/
   isSet/put/...` 的设备位等）。
2. 该参数为**编译期端口**时保持现状（直接 `dN`，内联时零成本）。
3. 该参数为**运行期值**时，`x.Property` 下沉为 `LoadDyn/StoreDyn`（IC10 `drN`），
   写端口号到寄存器。`d0..d5`/`db` 需要一张「端口 → 号」的表（传给寄存器）。
4. 外提调用约定：设备参数与普通参数一样在 `jal` 前 `move` 进参数寄存器；被调体用
   `drN` 访问。去掉 `hasDeviceOrDataArg` 对设备实参的一票否决（保留 `data` 表实参的
   否决，或同样评估运行期基址）。

**难度**：中。改动集中在 `internal/lower`（设备参数识别、`LoadDyn/StoreDyn` 下沉、
调用约定）与 `internal/sema`（参数「可作设备」的类型/标记）；`codegen` 的 `drN`
渲染已存在。
**风险**：中。需确认 `drN` 端口编号语义、`db` 的编号、`drN` 占用寄存器带来的压力，
以及真机写序列不变。可用现成的 VM + 真机 A/B 验证。
**收益**：**大**。既修 D0（正确性），又打开「设备 helper 外提」的大门——
本例 20 → 约 18；K 次调用的函数体 B 行，内联是 `B·K`，外提是 `B + 3K`（见 §5），
K≥3 时收益迅速放大（实测 K=3：29 → 约 21）。

### I2 大小模型纳入「设备 / 常量实参」

**做法**：把 I1 后的三种候选一起交给现有 size model 比较：
- 全内联（当前默认，常量折叠）；
- 外提 + **非设备**参数走寄存器，**所有调用点取值相同的**非设备常量参数折进函数体
  （不同则只能走寄存器，或干脆不共享）；
- 不优化版兜底（已有）。

**难度**：中。`PlanOutlines` 只按「调用次数 ≥2 + 可外提」粗判，需要引入「函数体实际行数 ×
调用次数 vs 外提成本」的估算，并与 `argsConstant` 的折叠收益权衡。
**风险**：中-高。这是最容易引入回归的一步（大量小程序依赖常量折叠内联）。缓解：
本项目已有 `TestDifferentialRandom` / `TestDifferentialMultiChip` 优化前后设备写序列对拍。
**收益**：中-大，且**是 I1 生效的前提**（否则常量实参仍会强制内联，本例拿不到 18）。

### I3 尾调用 / `ra` 复用（可选）

**做法**：识别「调用之后紧接着就是回边 `j` / 返回」的尾位置，把 `Call + Jmp` 改写为
「设置 `ra` = 目的地，让被调体 `j ra` 返回目的地」；对无限循环可在循环头前设一次 `ra`。
**难度**：中。
**风险**：中-高。`ra` 只有一份；被调体若内联了别的调用会踩 `ra`（本项目外提体不允许含
`jal`，这条约束必须保持）。收益仅为每处尾调用 1 行。
**收益**：小。本例恰好 1 行（18 → 17）。建议在 I1/I2 落地、确认有真实需求后再做。

### I4 修复 D0：运行期设备参数必须报错（或随 I1 转为合法）

**做法**：若暂不做 I1，遇到「设备参数被绑定运行期值」时**硬报错**并给出
「请用 `readDev`/`writeDev`」的指引；做 I1 后自然变为合法代码。
**难度**：小（一条诊断）。
**风险**：低（把静默误编译变成编译错误，只会更安全）。
**收益**：中（正确性）。**独立可做，建议第一优先。**

---

## 5. 收益测算

设函数体（含 `j ra`）为 `B` 行，被调用 `K` 次，每次调用设置 `A` 个实参。

| 方案 | 总行数模型 | 本例 B=10、K=2、A=3（常量目标 297 折入体）|
|---|---|---|
| 现状：内联 + 折叠（体 9 行/次） | `9K + 2` | 20 |
| I1：外提 + `drN`，目标仍走寄存器 | `B + (A+1)K + 2` | `10+4·2+2 = 20`（无净收益）|
| I1：外提 + `drN` + 常量目标折入体 | `B + A·K + 2` | `10+3·2+2 = 18` |
| I1+I3：再省一次尾调用 | `B + A·K + 1` | 17 |

> 起作用的组合是 **I1 + I2**：外提只有在「设备参数走寄存器、非设备常量折进体」时才比
> 内联短。K 越大越明显：K=3 时内联 `9·3+2=29`（实测 29），外提+折常量约 `10+3·3+2=21`。

**今日可用（不改编译器的）workaround**：把 helper 改写为 `readDev`/`writeDev`，并把目标
参数做成运行期值（否则常量实参会强制内联）。实测：

| 调用次数 K | 内联（默认） | `readDev/writeDev` workaround |
|---|---|---|
| 2 | 20 | 21（更差） |
| 3 | 29 | 25（更优） |

即：**不改编译器时，只有 K≥3 才值得手写 workaround**；这正是 I1/I2 的价值所在。

---

## 6. ROI 与建议排序

| 优先级 | 项 | 难度 | 风险 | 收益 | 说明 |
|---|---|---|---|---|---|
| 1 | **I4** 运行期设备参数报错 | 小 | 低 | 中 | 修正确性缺陷，独立，便宜 |
| 2 | **I1** 设备参数 `drN` + 外提约定 | 中 | 中 | **大** | 打开设备 helper 外提；修 D0 的根治 |
| 3 | **I2** 大小模型纳入设备/常量实参 | 中 | 中-高 | 中-大 | I1 生效的前提；回归风险靠差分测试兜 |
| 4 | **I3** 尾调用 / `ra` 复用 | 中 | 中-高 | 小 | 只为最后 1 行，收益边际，可缓 |

**总体判断**：

- 就**这个基准**而言，20 → 17 需要 I1+I2+I3 全做，投入不小、最后一行收益边际。
- 但 **I1+I2 的通用价值远超该基准**：它把「每个传感器/每台炉子/每个气闸调一次同一个
  helper」这一类**最常见的 IC10 写法**从 `O(K·B)` 压到 `O(B + K)`，并且**顺带修掉一个
  会生成非法指令的缺陷**。这是**值得做**的方向。
- I3 单独看 ROI 一般，建议等 I1/I2 落地后，用真实语料重新测量「尾调用出现频率」再决定。

**不建议**为了追平这一条基准而做的：把「常量实参一律外提」（会毁掉大量依赖折叠的小程序）、
或引入无界序列容器来「重构」这套逻辑（与 `docs/backlog.md`「明确不做」冲突）。

---

## 6.1 推进状态（2026-09-30）

按 §6 的顺序分轮推进，每轮都做**真机验证 + 回归**。

> **真机场景**指 `testdata/bench/ingame/*.json`，每个用 `ic10c testbench run --diff` 在
> **真实芯片 + 内置 VM** 上各跑一遍，统计的是**场景文件数**（场景内还有多个 case）。
> 改动前是 **11** 个；三轮各新增 1 个（`s60_dyndev` / `s61_dyndev_outline` /
> `s62_tailcall`），共 **14** 个。下面每轮的「N/N」是那一轮结束时的场景数。

| 项 | 状态 | 说明 |
|---|---|---|
| **I4** 运行期设备参数 | ✅（被 I1 覆盖） | 不再输出非法 `dev.Setting`，而是合法 `drN` |
| **I1** 设备参数走 `drN` | ✅ Round 1 | `dev.Prop` / `dev.slot[i].Prop` 运行期端口下沉 |
| **I2** 大小模型外提设备 helper | ✅ Round 2 | 目标例 20 → 18；语料 −50 行 |
| **I3** 尾调用 / `ra` 复用 | ✅ Round 3 | 目标例 18 → 17，与 sicc 持平 |
| （新）动态设备读的冗余消除 | ✅ Round 2 | `drN` 读取现参与 CSE（失效保守） |

### Round 1（I1 + I4）

**改动**（`internal/lower/lower.go`）：新增 `dynamicDevice` / `dynamicSlotOf`；在
`lowerDeviceRead` 与赋值目标里，当选择器基址是**运行期**值（局部变量 / 参数 / 数值常量）时
分别发射 `LoadDyn` / `StoreDyn`（逻辑属性）与 `LoadSlot` / `StoreSlot`（槽位，带 `DevPtr`）。
编译期端口（`dN`、`const X = dN`、数值常量端口）不受影响，仍走 `dN`。

**真机验证**（A CHIP：d0=LED、d1=旋钮、d4=逻辑内存；`ic10c testbench`）：

| 场景 | 结果 |
|---|---|
| 旧编译器推 `dst.Setting = dev.Setting` | 芯片报 `IncorrectVariable`，LED 不变（改动前证据） |
| 新编译器同一程序 | 无错误；MEM=4 → LED=4；MEM=1（选 d1 旋钮）→ LED=0 |
| 端口号越界（MEM=7） | 芯片报 `Unknown`（确认寄存器值确实被当端口号） |
| `readDev` 对照写法 | 与本特性产物**逐字节相同** |
| 固化场景 `testdata/bench/ingame/s60_dyndev`（真机 + VM 差分） | 4/4 通过 |
| 全部 ingame 场景（真机 + VM） | 12/12 通过 |

**测试**：`TestDynamicDeviceOperand`（`drN` + `ls drN`，且选择器不泄漏）、
`TestDynamicDeviceNumericConst`（数值常量端口折叠为 `dN`）、`TestVMDynamicDeviceParam`
（VM 语义）。全量 `go test ./...`、`./build.sh assert`（58/58）均绿。

**遗留（Round 2 已处理）**：`drN` 设备读当时**不参与冗余读消除**（`loadKey` 未覆盖
`LoadDyn`），所以用户程序里 `pa.Temperature` 读两次会发两条 `l r drN Temperature`。
Round 2 已补齐（见下）。

### Round 2（I2 + 动态设备读 CSE）

**改动**：

- `internal/lower/lower.go`：去掉 `hasDeviceOrDataArg` 对设备实参的一票否决（改名
  `hasDataArg`，只保留 `data` 表否决）。`outlineCall` 把设备实参按**端口号**写进参数
  寄存器（`devicePortNumber`：`d0…d5 → 0…5`；`db` 无端口号，回退内联），函数体经
  `dynamicDevice` 用 `drN` 访问。新增 `lower.Options.InlineConstArgs`：`true` 时保留
  「常量实参调用内联折叠」的旧特化行为，`false` 时把常量调用也一起外提。
- `pkg/ic10/compiler.go`：候选循环对每个外提计划同时尝试 `InlineConstArgs` 的两种取值，
  取更短者；**每个候选使用独立的诊断袋**——某个候选内部 `ir.Verify` 失败不再让整次编译
  失败（这也是随机差分里 mem2reg + 外提组合暴露出的问题：以前它被「全内联」候选悄悄
  掩盖，现在被正确隔离）。
- `internal/opt/opt.go`：`loadKey` 覆盖 `LoadDyn`（属性读）与带 `DevPtr` 的 `LoadSlot`
  （运行期槽位读）；运行期端口统一登记到伪设备 `dynamicDev = "*"`，任何设备写都使其失效
  （保守但正确）。`size.go`/`graph.go` 同步镜像候选选择。

**实测**：

| 指标 | 旧 | 新 | sicc |
|---|---|---|---|
| 目标例行数 | 20 | **18** | 17 |
| 目标例字节 | 249 | **190** | 190 |
| K=3 设备 helper | 29 | **21** | — |

语料（`testdata/programs` + `ic10code` + `examples`，134 个 `.icg`）：**5320 → 5270 行**
（−50，8 个脚本变短，0 个变长）：气闸控制 92→80、气闸双门 78→73、卫星天线跟踪 95→90、
售货机补给 57→52、sunlightAutomaticLighting 38→30、fabricatorStacker 101→97、
hangarPressurizer 94→89 / hangarPressurizer2 87→81。

**真机验证**：

| 场景 | 结果 |
|---|---|
| `s61_dyndev_outline`（设备 helper 外提，真机 + VM 差分） | 4/4 通过 |
| 全部 ingame 场景（真机 + VM） | 13/13 通过 |
| `s60_dyndev`（Round 1） | 仍 4/4 |

`s61` 的程序用不同设备常量两次调用同一 helper，默认构建**外提**（2 个 `jal`，23 行）而
`IC10C_NO_OUTLINE=1` 全内联（26 行）；真机上按 `d4`/`d0` 输入得到与 VM 一致的
`d0.On`/`d3.On`。

**测试**：`TestOutlineDeviceParams`（外提 + `dr` + 写序列与全内联一致）、
`TestDynamicDeviceReadEliminated`（动态读去重；设备写强制重读）、
`TestFunctionSpecialization`（保留常量调用折叠 + 变量调用外提）。全量 `go test ./...`、
`./build.sh assert`（58/58）均绿。

**唯一剩余**：尾调用 / `ra` 复用（I3），只为最后 1 行（18 → 17）。

### Round 3（I3 尾调用）

**改动**（`internal/opt/opt.go`）：新增 `tailCall` pass。返回块为空、且该块只有一次
`Jmp D` 的 `Call{Target:E, Return:R}` 改写为：在调用块里追加
`StoreSpecial{ra = LabelRef(D)}`，把终结符改成 `Jmp{E}`；R 随之不可达被删除。被调体 E
的 `j ra` 直接返回 D。IR/CFG 早已支持 `ra = <label>; goto sub` 手写调用
（`BuildCFG` 把该 label 作为所有 `JmpRA` 的后继），因此无需新增 IR 节点或调用约定。

**安全性**：只有返回块为空的 `Call` 才改写（有返回值的 outlined 调用会在返回块里拷贝
结果，天然排除），说明调用点不需要 `ra` 回来；外提体已内联其全部调用，体内没有第二个
`jal`，唯一返回地址安全。行数只会持平或减少（紧邻布局省 1 行，否则 `move ra` + `j` 与
原 `jal` + `j` 行数相同）。

**结果**：

| 指标 | Round 2 | Round 3 | sicc |
|---|---|---|---|
| 目标例行数 | 18 | **17** | 17 |
| 目标例字节 | 190 | **190** | 190 |

产物与 sicc 的 17 行一一对应（结构与字节数完全一致）：

```
yield
move r0 0
move r3 1
jal 7
move r0 2
move r3 3
move ra 0
l r0 dr0 Temperature
sgt r2 r0 297
sub r0 297 r0
abs r1 r0
move r0 10
ble r1 5 14
move r0 100
s dr3 On r2
s dr3 Setting r0
j ra
```

语料累计 **5320 → 5268 行（−52，8 个脚本，0 个变长）**（Round 2 为 −50；Round 3 再加
气闸控制 79、气闸双门 72）。

**真机验证**：`testdata/bench/ingame/s62_tailcall`（真机 + VM 差分 4/4）——无限循环里
最后一个设备 helper 调用成为尾调用（`move ra 0` + 直落体内），真机上 `d0.On`/`d3.On`
与 VM 一致。全部 ingame 场景 **14/14**。

**测试**：`TestTailCall`（`move ra` 存在、`jal` 恰 1 个、行数下降、写序列与
`IC10C_NO_OPT=1` 一致）。全量 `go test ./...`、`./build.sh assert`（58/58）均绿。

至此 §4 的 I1–I4 全部完成，目标例与 sicc 持平。

---

## 7. 风险控制与验证

本项目已有的测试资产能显著压低 I1/I2 的风险：

- **差分随机**：`TestDifferentialRandom`（2000 例）与 `TestDifferentialMultiChip`——
  优化版 vs `IC10C_NO_OPT=1` 的设备写序列必须一致。新增 I1/I2 后，应把「设备 helper
  多次调用」纳入随机生成器的语句模板。
- **语料往返**：`ic10code/` 全量反编译→重编译→写序列对比，以及 minify 等价性。
- **真机 A/B**：`ic10c testbench run --diff`（真机 + VM）对比 `drN` 版与 `dN` 版的
  设备写序列，确认间接访问与直接访问可观测行为一致。
- **专门用例**：新增
  - 运行期设备参数 → 必为 `drN`（I1）或必报错（I4）；
  - 设备 helper 被调 K 次：I1+I2 后行数 `≤` 内联版，且写序列一致；
  - `const` 参数目标的折叠路径仍不产生 `drN`（走直接 `dN`）。

落地顺序：**I4 单独一个 PR**（低风险、立即可合）→ **I1 一个 PR**（含 VM/真机验证）
→ **I2 一个 PR**（含差分扩容）→ I3 视测量结果决定。Round 1（I1+I4）、Round 2（I2 + 动态
设备读 CSE）均已完成并通过真机验证（见 §6.1）。

---

## 附录 A：复现命令

```bash
# 本项目
ic10c stats /tmp/temp_control.icg
ic10c build /tmp/temp_control.icg

# sicc（README 示例 examples/ic11_compare.py）
pip install cappa networkx optree ordered-set rich z3-solver
PYTHONPATH=. python examples/ic11_compare.py
```

字节统计：`awk '{n++; b+=length($0)+1} END{print n, b}'`（含行尾换行）。

## 附录 B：对照的 sicc 版本

- 仓库：`git clone https://github.com/Alan-Chen99/sicc`
- 提交：`9b5c7f543e3be6e9187dc55cbac3c8070cb1b116`（2025-11-02，`master`）
- 依赖：Python ≥3.13、`cappa` / `networkx` / `optree` / `ordered-set` / `rich` / `z3-solver`
- 说明：sicc 为 WIP，作者自述「使用前请人工复核输出」；其 `drN` 方案与「不用
  `push`/`pop`、必要时搬 `ra`」是其 README 明确的设计取向。
