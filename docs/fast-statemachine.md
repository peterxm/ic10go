# `--fast` 与状态机：从「行数最短」到「tick 预算内最快」（评估与改进方案）

> **TL;DR**：当前 `--fast` 只放宽循环展开，对状态机派发**零作用**，还会在超预算时直接
> 编译失败；`--jump-table` 门控（≥8 稠密 case、体仅 1 条语句）几乎不匹配真实状态机，且
> `ic10c tick` 对 `jr` 的保守建模把跳表程序**误报成超限**。本方案把 `--fast` 重定义为
> 「**在 128 行 / 4 KiB 预算内，最小化两 yield 间最坏指令数**」：P2 用已有的 `internal/tick`
> 做 tick 感知候选评分，P3 加派发转换（case 重排、比较链→二分决策树），P1 先修分析器对
> 跳转表的过估。实测（`testdata/bench/fast/`）：决策树派发把 12 状态机的最坏 tick 从 16 降到
> ≈8–9，case 重排降最坏 tick 2 条、行数 +1，均零语义风险。

关联：[`tick-budget.md`](tick-budget.md)（预算模型）、[`architecture.md`](architecture.md)（size model）、
[`backlog.md`](backlog.md)（D2/E/F 组）、`testdata/bench/fast/`（本评估语料，`python3 gen.py` 再生成）。

---

## 1. 问题

游戏对每块芯片每 tick 最多执行 **128 条**指令，`yield`/`sleep` 提前结束本 tick。
状态机（`switch state` 主循环）的两个结构性成本：

- **派发链是线性的**：默认 lowering 把 switch 编成「每 case 一条融合比较跳转」的顺序链
  （`bne r0 1 <next>` …），第 k 个 case 要跑 k−1 条测试；**最坏 tick = 状态数 + 最大 handler**。
- **最坏路径撑爆预算 → 延迟爆炸**：一个 tick 跑不完一圈就跨 tick 切开，「每 tick 一步」
  变成「每 2–3 tick 一步」，同 tick 重派发的转移时序直接塌掉（实例：`arm.icg`，round≈3 ticks）。

默认模式的一切取舍以**总行数**为准（只在行数不增时采纳优化；候选取最短者）。这与
**每 tick 执行行数**是两个轴：紧凑产物往往用最坏 tick 换行数。用户要的是：`--fast`
模式下针对状态机在两个轴之间取平衡——行数预算有余量时，把行换成派发深度。

## 2. 现状评估

### 2.1 `--fast` 现在是什么

- 定义处只有一处：`internal/lower/lower.go` `tryUnrollFor`（L1075–1105）——展开上限
  `maxTrip/maxBody` 从 4/2 放宽到 8/4，并允许展开含用户函数调用的体。
  `docs/architecture.md` L215 的描述与此一致：「放宽循环展开门控」。
- CLI/库：`--fast` → `ic10.Options.Fast`（`pkg/ic10/compiler.go` L62），其余管线
  （内联/外提、LICM、数据读折叠、跳转表、派发形状）**全部照常按最短行数选**。

实测（`ic10c build [--fast] … ; ic10c tick`）：

| 观察 | 数据 |
|---|---|
| 状态机合成语料上 `--fast` ≡ 默认 | `var<N>_2`/`db<N>_2`（N=3..12）行数与最坏 tick **全等**（派发不受益） |
| examples 语料上 `--fast` 几乎无差异 | 18 个 `.icg` 里只有 2 个产物不同：1 个改善、**1 个编译失败** |
| 改善例：`气闸控制-四门` | 行数 102→103（+1），最坏 tick **117→75**（展开 DOORS 循环消掉环开销） |
| 失败例：`自动打印机产量控制-多机-排料保护-表驱动` | `--fast` 展开后 **6218 字节 > 4096**，`build` 直接报错退出（默认版 118 行编译通过） |

**结论：`--fast` 既不管状态机，也没有预算安全网。**

### 2.2 状态机派发的三种现有形态与成本模型

| 形态 | 触发 | 行数 | 执行派发深度 | 备注 |
|---|---|---|---|---|
| 线性比较链（默认） | 任何 switch | ≈ 1×case 数 | **k**（命中第 k 个 case 需 k−1 次测试） | 分支融合已做到 1 条/测试（`Cmp`+`Br`→`bXX r c line`） |
| `--jump-table` | 带 tag、**≥8 个稠密**整数 case、**每个体仅 1 条赋值/调用**（`lowerJumpTable`，`jumpTableMin=8`） | n 行表 + ~10 行 | 常数 ≈ 8（越界检查 4 + add + jr + 表 1） | `arm.icg`、气闸、绝大多数真实 handler 都**不满足门控** |
| `switch … table` / `--auto-table` | 值查表 | — | — | 数据段**值**表，不改变**控制流**派发 |

实测数据（静态 = `ic10c tick` 最坏分段；动态 = `tick --run 40` VM 实测；语料见 `testdata/bench/fast/`）：

| 程序 | 行数 | 静态 worst tick | 动态 max/avg |
|---|---|---|---|
| `var8_2`（链，8 状态，体 2 条） | 34 | 12 | 12 / 8.2 |
| `var12_2`（链，12 状态） | 50 | **16** | 16 / 9.9 |
| `var12_1` + `--jump-table`（体 1 条才触发） | 44 | **129 = 假警报**（`jr` 保守上界，`indirect`） | 10 / 9.7 |
| `var12_1` 链版对照（同上体形） | 35 | 14 | 14 / 8.6 |
| `tree12.ic`（手写决策树参照，体同 `var12_2`；**叶子省复核**，见 §4.2） | 49 | **8** | 8 / 7.5 |
| `tree8.ic`（对照链版 `var8_2`：34 行 / 静态 12 / 动态 12/8.2） | 33 | **7** | 7 / 6.9 |
| `ord_asc12` → `ord_desc12`（仅 case 源码序不同，大的 handler 提前） | 71→72 | 18→**16** | 18/11.6 → 16/12.1 |

要点：

1. **跳表不划算**：执行派发成本常数 ≈8（越界检查 4 + `add` + `jr` + 表行 1），n≥10 左右才开始
   赢**最坏**路径，但**平均**更差（n=12 动态 avg 9.7 vs 链 8.6：每次派发都付满 8 条）、
   还多吃 ~n 行表；而门控形状（单语句体）在真实状态机上几乎从不满足。
2. **决策树是正解**：二分派发（`bge tag mid → 右子树`，左子树落空续接）深度 ⌈log₂n⌉。
   链的每 case 测试（`bne tag k → next`）**一指令两用**（路由 + 等值复核），树的序比较节点
   不承担复核，所以：**行数持平的前提是 tag 整数域可证**（§4.2）；不可证时每叶补 1 条 `beq`
   （≈ +n−1 行），实测最坏路径仍从 16 降到 ≈9。上表 `tree<N>.ic` 为省复核的参照版。
   树没有间接跳转，分析器可精确建模、静态/动态同步 −40%~−50%。**实际 n 范围（≤~30）内树
   同时优于链与跳表**（深度 ≤5+复核 1 < 跳表常数 8，且不额外吃 n 行表）。
3. **case 排序免费**：minimax 排序（体按估计成本降序）行数 ±1，最坏 tick −2（语料实测），
   语义不变（case 互斥，单 tick 只跑一个体）。

### 2.3 候选评分模型是纯行数驱动的

- `pkg/ic10/compiler.go` `compileInfo`：对 outline×fold×mem2reg×popBank×LICM×globalProp
  的候选矩阵跑完整管线，`consider()`→`better()`（L940）= **行数、字节，先到先赢**。
- 每个「速度换体积」权衡点（LICM 外提设备读、内联省 `jal`/`j ra` 开销、展开）目前**一律按体积选**。
  `气闸控制-四门` 的 117→75 正是展开在「+1 行」处被体积模型拒掉的同族例子——**默认模式里它被
  拒绝是对的**（用户没要速度）；`--fast` 下应反过来按 tick 选。
- 基础设施已在：`pkg/ic10/json.go` `ScoreFor`/`tickInfo` 已经会算每候选同款的静态最坏 tick
  （仅用于展示/`--score`/`build --json`），**只差反馈进选择**。
- 成本：完整 `build`（含数十候选）实测 ~12ms；`tick.Analyze` 对 122 行程序 ~10ms 内含进程启动
  （DP 有 `maxDPStates=200000` 封顶）。评分复用同一 `code` 字符串（memoize 后），
  `--fast` 编译时间预估 ×2–3，可接受；默认模式不受影响。

### 2.4 分析器把跳转表（未来的 `jr` 形状）误报成超限

`internal/tick/tick.go` L337–352：寄存器目标 `jr` 连到「所有调用返回点」或干脆只有
`indirect` 标记（L592–596 保守接回边）。codegen 的跳表形状是确定的
`add rX rX 1; jr rX; j <case₀>; … j <caseₙ₋₁>`（`codegen.go` L433 起，表行有 `table` 标记）。
分析器不识别这个 idiom，导致 `--jump-table` 程序的静态报告恒为 `129 EXCEEDS`（§2.2 实测），
`build --score`、LSP 诊断、以及任何**以 tick 成本为准的候选评分都会因此惩罚正确形状**。
先修它，是所有 tick 驱动取舍的前提。

### 2.5 真实重灾区画像（说明为什么需要整套方案而不是单点补丁）

- `气闸控制-四门`（本仓库 examples）：3 状态、INIT 重体 → worst 117，**handler 主导**，
  派发只有 3 条；`--fast` 的展开已能把它压到 75，但+1 行、且没有兜底。
- `arm.icg`（社区芯片重写，8 状态、同 tick 重派发 `for again`）：119 行，
  静态 round **≈3 ticks**（dispatch 一圈 278 条），动态 30 tick 里 max 128 / avg 124——
  tick 几乎被填满。派发树每轮省 ~4 条 × 同 tick 多轮，再叠加其它 tick 感知取舍才够。
  **单纯重排/树化救不了全局，必须让评分器在所有既有权衡点上按 tick 轴取舍。**

### 2.6 问题清单

| # | 问题 | 证据 |
|---|---|---|
| Q1 | `--fast` 与状态机派发无关 | §2.1 全等表格 |
| Q2 | `--fast` 无预算兜底：超字节/行直接编译失败 | printer 表驱动 6218B>4096B |
| Q3 | 派发线性链：最坏 tick 随状态数线性增长 | §2.2，16@n=12 → 树 8 |
| Q4 | 跳表门控过严 + 成本模型不占优 + 被分析器误报 | §2.2/§2.4 |
| Q5 | 候选评分只看行数，速度轴完全未用 | §2.3 |

## 3. 改进方案

**北极星**：`--fast` = 在 128 行 / 4 KiB / 90 字符预算内，最小化「两 yield 间最坏指令数」，
其次最小化行数/字节；默认模式行为逐字节不变（P3a 是否在默认开启另议，见 §3 P3a）。

### P0 — `--fast` 预算兜底（Q2，小）

`compileInfo` 的候选矩阵在 Fast 下同时跑「保守档」（现有默认参数）与「激进档」：
`generate` 超行/字节的候选**照旧丢弃**而非报错——只要有任一档能编译就出产物，
只有全部超限时才报现有帮助信息（并提示去掉 `--fast`）。改动集中在 `run()`/`consider()`。

### P1 — `internal/tick` 识别 `jr` 跳表 idiom（Q4 的分析器半边，小）

建 CFG 时：`jr rX` 之后**连续 ≥2 条 `j <绝对目标>` 行**视为跳转表，
`jr` 的后继 = 这些行的目标（各目标路径含该 `j` 行本身，成本精确）；模式不匹配则维持现状。
手写 IC10（社区固件里也有 `jr 3` 形态）一并受益。补 `tick_test` 单测 + 用
`testdata/bench/fast/var12_1` 的 `--jump-table` 产物加回归（静态应给出 ≤ 动态 max 的诚实上界）。

### P2 — tick 感知候选评分（Q5，核心）

- `consider()`/`better()` 改为可选评分函数：默认 `(lines, bytes)` 不变；
  **Fast 下** `(必须入预算) → worst tick（tick.Analyze 的 cost，超限候选按 cost=Limit+1）
  → lines → bytes` 字典序。`tickInfo` 复用 `json.go` 现成实现，按 code 串 memoize。
- 收益自动覆盖所有既有「体积↔速度」权衡：展开深度、内联/外提（`jal`/`j ra` 每调用 +2~3 条执行）、
  LICM（把每圈重复的设备读提出去）、数据读折叠、mem2reg（多 1 条 `get db` vs 常驻寄存器）。
  这些**不需要新优化**，只需要换评分轴——是「平衡」的机制本体。
- 成本控制：Fast 下才跑分析；LSP 保持默认模式。基准：`go test -bench BenchmarkAnalyze` +
  `--fast` 构建总时延 < 3×默认（语料实测写入 PR）。

### P3 — 状态机派发转换（Q1/Q3/Q4 的生成侧）

- **P3a case 重排（先行、近零风险）**：`lowerSwitch` 生成链前，把非 default case 按
  **估计 handler 成本降序**排列（minimax：最坏 = maxₖ(k 测试 + bodyₖ)）。
  互斥 case 不改变任何可观测写序（单 tick 只执行一个体）。实测 worst −2 / 行数 +1。
  默认模式经 P2 之外的「行数不增才采纳」守卫评估是否开默认（v1 先只在 Fast 开）。
- **P3b 决策树派发（主菜）**：新 `internal/opt` pass `balanceDispatch`（或 lower 内构造，实现时定）：
  把「同一 tag、全整数常量 case、每 case 一条融合测试」的比较链重写为二分树
  （节点 `Cmp(Lt/Gt)+Br` 复用现有分支融合，叶子 → 原 body 块 + default）。
  做成 opt pass 的好处：**手写的 if/else-if 链（社区固件常见形态、decompile 产物）同样命中**。
  触发门控：case 数 ≥5 且（Fast 或显式 `// icg: dispatch=tree`）且经分析器确认
  「最坏 tick 下降 且 在预算内」——即**由 P2 评分器裁决，转换本身只当候选**。
  正确性约束见 §4；`var<N>_2` 对照 `tree<N>.ic` 作为形状参照与回归目标。
- **P3c 跳表（降级为遗留项）**：`--jump-table` 保留原样；Fast 不再建议用它
  （实际 n 范围内树深度 ≤5+复核 < 跳表常数 ≈8，且跳表多花 ~n 行、平均更差，见 §2.2）。
  若未来 n 极大再评估放宽单语句体门控（多语句体本身只影响布局安全性论证，不影响表 idiom）。

### P4 — 工具面与文案（小）

- `--fast` 帮助文案与 README：「优先运行速度：展开更多、按每 tick 预算重排状态机派发与
  体积取舍；预算不足时自动退回更省行的候选」。
- `build --json` / `--score`：Fast 构建给出默认 vs fast 的两轴对照；
  LSP `tick-budget` 诊断在有行数余量时附 quick-fix「尝试 `--fast`」。
- testbench push 的 `--fast` 透传已存在（`cmd/ic10c/testbench.go` L2021）。

## 4. 正确性与语义约束

1. **case 语义**：Go 语义下 case 互斥、体后统一 `j end`，lowering 已规范化——重排与树化
   只改**测试顺序**，不改可达体集合与每个体的内容；单 tick 内设备写序列不变。
2. **NaN / 小数 tag**：链对「不等任何 case 的值」落 default。树用序比较分边，任何值都会落到
   某个叶子——**叶子必须保留等值复核**（`beq tag v → body`，失败→ default），
   否则 2.5 / NaN / ±Inf 会被错误路由（本仓库手写 `tree<N>.ic` 故意省复核、只覆盖整数，
   作为对照而非目标形状）。**v1 用叶复核**：精确、无额外分析。行数吃紧时的两档替代
   （由 P2 评分器按实测挑选）：
   ① tag 整数域可证（该值的全部写入点都是 case 整数常量，局部状态机通常可证）→ 直接免复核，
   行数持平；`db.stack` 槽位有芯片外写者，**不可证**；
   ② 全局守卫 `floor rT tag; bne rT tag default; bge tag n default`（+3 行、每 tick 多执行 3 条）
   换全部叶子免复核——注意单靠 `floor` 拦不住 ±Inf，必须带范围检查。
3. **多表达式/range case、非整数常量 tag、含 `case` 非稠密**：回退链（P3a 仍可排序）。
   混合形态（部分 range 部分等值）v1 整体回退。
4. **寄存器压力**：树节点共用 tag 寄存器，无新增值；重排可能改活跃区间 → 由 P2 评分兜住
   （溢出行数变多者自然落选）。`--spill` 语义不变。
5. **loader / 数据段契约**：转换只动 runtime 控制流，不动 `data` 表布局与版本哨兵；
   多芯片各 chip 独立编译/评分，行为同单 chip。
6. **验证与差分**：新 pass 产物必须过 `ir.Verify`；`pkg/ic10` 差分测试加「链 vs 树 vs（跳表）」
   逐 tick 设备写对拍（复用 `TestJumpTableEquivalence` 的 VM 对拍框架 + `difftest` 语料），
   含 NaN/小数/负数/超大 tag 的专门用例。

## 5. 实施切分（建议提交顺序）

| 步 | 内容 | 规模 |
|---|---|---|
| 1 | P1：`jr` 跳表 idiom 识别 + 单测/回归 | 小（`internal/tick`） |
| 2 | P0+P2：Fast 候选兜底 + tick 评分（含 memoize、编译耗时基准） | 中（`pkg/ic10`） |
| 3 | P3a：case minimax 重排（Fast 门控） | 小（`internal/lower`） |
| 4 | P3b：决策树 pass + 差分测试 + bench 语料目标值 | 中-大（`internal/opt`/`lower`） |
| 5 | P4 文案/LSP/JSON；文档与 backlog 更新（D2 组追加结论） | 小 |

## 6. 验收标准

- `testdata/bench/fast`：`var8_2`/`var12_2`/`db8_2` 在 `--fast` 下静态 worst tick ≤ 树参照值 +2
  （8 状态 ≤9、12 状态 ≤10，即较默认 −25% 以上）；行数增幅 ≤ N−3（叶复核成本）且不超预算；
  `var*_1 --jump-table` 静态不再 EXCEEDS 误报。
- `气闸控制-四门 --fast`：维持 ≤75（展开收益不回退）；`printer 表驱动 --fast`：**编译成功**
  （回落到入预算候选），产物两轴不劣于默认。
- examples + testdata 全部语料：默认模式产物逐字节不变；`--fast` 不劣于默认（两轴支配比较）。
- 编译时间：`--fast` 构建 ≤3× 默认；`go test ./... -race` 全绿；`ic10c tick --strict` 进 CI 的
  语料清单扩充 fast 列。

## 7. 风险与回退

- **评分把编译时间拉爆** → memoize + 只对「code 与当前最优不同」的候选分析；兜底：Fast 下
  候选数上限。
- **树化在某些形状行数反升**（叶复核 +1/叶）→ 由评分器自动落选（P2 机制天然兜住），无需额外门控。
- **分析器 DP 状态爆炸**（`maxDPStates`）→ 已有线性兜底（超限报预算封顶）；评分时把
  「Exceeds 且 indirect」候选排在「精确值」之后（保守正确）。
- **真机语义未知**（本方案不需要新指令语义：`bge/blt/bne` 绝对跳转与融合分支都是现有产物；
  `jr` 已真机验证；无需新的游戏侧验证项——若 P3c 复活跳表再单独真机对拍）。

## 附录 A — 复现

```bash
cd testdata/bench/fast && python3 gen.py
C=../../../ic10c        # 仓库根二进制（v0.8.54 = acbdfa1），按实际位置调整
for f in var8_2 var12_2 db8_2; do
  for m in "" "--fast" "--jump-table"; do
    $C build $m $f.icg > /tmp/x.ic 2>/dev/null
    printf "%s [%s] lines=%s worst=%s\n" $f "$m" $(wc -l </tmp/x.ic) \
      "$($C tick /tmp/x.ic | grep -oE 'worst-case [0-9]+' | cut -d' ' -f2 | sort -n | tail -1)"
  done
done
$C build var12_2.icg > chain12.ic 2>/dev/null
$C tick --run 40 chain12.ic | grep dynamic   # 链版参照：max 16 / avg 9.9
$C tick --run 40 tree12.ic   | grep dynamic  # 树参照：  max 8  / avg 7.5
$C build --fast ../../../examples/自动打印机产量控制-多机-排料保护-表驱动.icg  # Q2：6218B>4096B
$C build --jump-table var12_1.icg > x.ic && $C tick x.ic | head -4   # Q4：129 假警报
$C stats ../../../examples/气闸控制-四门.icg && $C tick ../../../examples/气闸控制-四门.icg
$C build --fast ../../../examples/气闸控制-四门.icg >/tmp/f.ic && $C tick /tmp/f.ic | head -3  # 117→75
```
