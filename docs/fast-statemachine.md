# `--fast` 与状态机：从「行数最短」到「tick 预算内最快」（评估与改进方案）

> **TL;DR**：当前 `--fast` 只放宽循环展开，对状态机派发**零作用**，还会在超预算时直接
> 编译失败；`--jump-table` 门控（≥8 稠密 case、体仅 1 条语句）几乎不匹配真实状态机，且
> `ic10c tick` 对 `jr` 的保守建模把跳表程序**误报成超限**。本方案把 `--fast` 重定义为
> 「**在 128 行 / 4 KiB 预算内，最小化两 yield 间最坏指令数**」：P2 用已有的 `internal/tick`
> 做 tick 感知候选评分，P3 加派发转换（case 重排、比较链→二分决策树），P1 先修分析器对
> 跳转表的过估。实测（`testdata/bench/fast/`）：决策树派发把 12 状态机的最坏 tick 从 16 降到
> ≈8–9（**由省复核参照推导的估计，待实测**），case 重排降最坏 tick 2 条、行数 +1，均零语义风险。

> **状态（2026-10-15）**：P0–P4 已落地（提交 `b50b9ab`/`962e22a`/`b97654c`/`3c74cc0`），
> 带叶复核的树实测 worst 16→**9**（行数 +n−1）；实现记录、实测与偏差见 **§8**。

> **复核备注（2026-10-14，`ic10c 0.8.54`/`acbdfa1`）**：本文档**所有实测数字已逐条复现**
> （§2.1/§2.2/§2.4/§2.5 与附录 A，含 `--jump-table` 的 `129` 误报、examples 18 个里恰好 2 个
> 产物不同、`arm.icg` 动态 max 128 / avg 124.0）。复核同时给方案加了 5 处收紧：**P1 的门控必须
> 保持"仍是上界"（§2.4/§4.7）**、**P2 的字典序要含平均轴且诊断路径排除 Fast（§3 P2）**、
> **P0 的回落必须 warning（§3 P0）**、**§6 的阈值改为先实测**（并撤掉自相矛盾的"两轴支配"）、
> 以及**与 `backlog.md` 两处旧记录的关系要写清**（§2.3 区分、P3a 改判）。

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

> 上表全部数字已在 `ic10c 0.8.54`（`acbdfa1`）上**逐字复现**（附录 A 的脚本）。注意两处性质不同：
> `tree<N>.ic` 是**省复核**的手写参照（不是目标形状，带复核的树行数/成本见 §4.2），
> 因此「树化后 worst ≈8–9」是**由参照推导的估计**，P3b 落地时必须实测并回填。

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
  `--fast` 编译时间预估 ×2–3（**估计值，未实测**），可接受；默认模式不受影响。
- **与 `backlog.md` 的「顺带撤回」不是同一件事（须区分）**：backlog 记过一条实测——「默认 vs `--fast`
  （240 程序）：最坏 tick 237 个完全相同、行数 239 个相同 → 没有系统性『省行换 tick』的证据，
  **本条撤回，不做**」。那条否掉的是「**`--fast`（＝多展开）会不会系统性地换 tick**」——实测不会，
  因为展开本来就很少触发（§2.1：examples 只有 2/18 个产物不同）。本节说的是另一件事：
  「**候选选择轴本身**只有 `lines`/`bytes`」（`better()`，已核）。P2 动的是后者，
  **不是把被撤回的想法重提**。

### 2.4 分析器把跳转表（未来的 `jr` 形状）误报成超限

`internal/tick/tick.go` L337–352：寄存器目标 `jr` 连到「所有调用返回点」或干脆只有
`indirect` 标记（L592–596 保守接回边）。codegen 的跳表形状是确定的
`add rX rX 1; jr rX; j <case₀>; … j <caseₙ₋₁>`（`codegen.go` L433 起，表行有 `table` 标记）。
分析器不识别这个 idiom，导致 `--jump-table` 程序的静态报告恒为 `129 EXCEEDS`（§2.2 实测），
`build --score`、LSP 诊断、以及任何**以 tick 成本为准的候选评分都会因此惩罚正确形状**。
先修它，是所有 tick 驱动取舍的前提。

> ⚠ **修的时候必须守住"仍是上界"这条底线。** 现状是**保守但正确**（`jr rX` 解不开 → 接**所有行**
> 为后继）。若改成「`jr rX` 后紧跟 ≥2 条 `j <绝对目标>` 行则后继=这些目标」，等于**假定 `rX` 有界**——
> 而这一点**没有被证明**：手写的 `jr rX`（§3 P1 明确希望它受益）可能越界跳**过**整张表，
> 那部分后继会被删掉，报出的最坏 tick 变成**假的小值**。作为「够不够预算」的判据，
> **低估比高估危险得多**（高估只是拒绝一个本来可用的产物；低估会放行一个真机会爆 tick 的产物）。
>
> 因此门控只能收紧到**可证的 codegen idiom**：同一块内紧邻 `add rX rX 1`（形状可辨）**且**存在
> 支配该 `jr` 的边界检查（codegen 的 `sltz`/`sgt` 那两条）。形状只"像"跳表时**保持 `indirect`**；
> 或者保留一条"越界"保守边（可并成一个虚拟节点），这样即使识别了表也仍非低估。

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

实现要点（避免走偏）：

- Fast 的激进来自 **lowering**（`tryUnrollFor` 的 4/2→8/4），所以「保守档」必须**用非 Fast 参数
  真的再跑一次 lowering**，不能只做「过滤候选」——超预算是 lowering 变大引起的。
- 成本可偷懒：**只在激进档超预算时**才补跑保守档，多数程序不必付这份钱。
- **回落必须出声**：`--fast` 因预算回落到省行候选时给一条 warning（例如
  `--fast 的展开版 6218B > 4096B，已回落到入预算候选`），并在 `--score` / `build --json`
  里给出两轴对照。**静默回落等于 `--fast` 悄悄变成默认**——这正是本项目反复修的「静默」类缺陷。

### P1 — `internal/tick` 识别 `jr` 跳表 idiom（Q4 的分析器半边，小）

建 CFG 时：`jr rX` 之后**连续 ≥2 条 `j <绝对目标>` 行**视为跳转表，
`jr` 的后继 = 这些行的目标（各目标路径含该 `j` 行本身，成本精确）；模式不匹配则维持现状。
手写 IC10（社区固件里也有 `jr 3` 形态）一并受益。补 `tick_test` 单测 + 用
`testdata/bench/fast/var12_1` 的 `--jump-table` 产物加回归（静态应给出 ≤ 动态 max 的诚实上界）。

> ⚠ **门控按 §2.4 的底线收紧**：只有可**证明** `rX` 有界时才把后继缩到表目标——即同块内紧邻
> `add rX rX 1` 且存在支配该 `jr` 的边界检查（`sltz`/`sgt`）。仅"后面跟着 `j` 行"就断定有界，
> 会把越界 `jr` 的后继删掉，把"恒高估 129"变成"恒低估"——**低估会放行真会爆 tick 的产物**。
> 无法证明时**保留 `indirect`**。
> 回归要**两条**：① codegen 跳表产物 → 静态值 ≤ 动态 max（**预期 ≤10**，现状 129）；
> ② 手写越界 `jr rX` → 静态值仍 ≥ 实际（不得被低估）。

### P2 — tick 感知候选评分（Q5，核心）

- `consider()`/`better()` 改为可选评分函数：默认 `(lines, bytes)` 不变；
  **Fast 下** `(必须入预算) → worst tick → 平均（或每轮总 tick）→ lines → bytes` 字典序。
  `tickInfo` 复用 `json.go` 现成实现，按 code 串 memoize。
- **为什么 `worst` 排第一**：worst 一旦越过 tick 边界，"每 tick 一步"就塌成"每 2–3 tick 一步"
  （§1 的动机）。
- **为什么 `平均` 必须在序里**：§2.2 要点 1 正是用"**平均更差**（n=12：9.7 vs 链 8.6）"否决跳表的。
  只按 `worst` 排，会选出"worst 略好、平均明显更差"的形状——即它自己否决的东西。
  两轴都要在 `--score`/`--json` 里可见，便于事后核对。
- 收益自动覆盖所有既有「体积↔速度」权衡：展开深度、内联/外提（`jal`/`j ra` 每调用 +2~3 条执行）、
  LICM（把每圈重复的设备读提出去）、数据读折叠、mem2reg（多 1 条 `get db` vs 常驻寄存器）。
  这些**不需要新优化**，只需要换评分轴——是「平衡」的机制本体。
- 成本控制：Fast 下才跑分析；**诊断路径永不用 Fast 评分**——LSP 的 tick 诊断按默认档算
  （注意：扩展在 `icg.fast` 打开时会把 `--fast` 透传给 build，`editors/vscode/extension.js:419`，
  所以不能默认"编辑器里没有 --fast"）。基准：`go test -bench BenchmarkAnalyze` +
  `--fast` 构建总时延 < 3×默认（语料实测写入 PR）。

### P3 — 状态机派发转换（Q1/Q3/Q4 的生成侧）

- **P3a case 重排（先行、近零风险）**：`lowerSwitch` 生成链前，把非 default case 按
  **估计 handler 成本降序**排列（minimax：最坏 = maxₖ(k 测试 + bodyₖ)）。
  互斥 case 不改变任何可观测写序（单 tick 只执行一个体）。实测 worst −2 / 行数 +1。
  默认模式经 P2 之外的「行数不增才采纳」守卫评估是否开默认（v1 先只在 Fast 开）。

  > **本项修正一条 `backlog.md` 决定**：那里定过「分支布局 / 派发臂重排 / profile-guided 排序」
  > **不做**，理由是「taken 与 not-taken 都是 1 条指令，臂重排只省**平均**、**不改变最坏路径**」。
  > 该推理只在**各 handler 成本相等**时成立：目标是最小化 `maxₖ(k−1 次测试 + bodyₖ)`，
  > 最优解就是把**最贵的 body 放到最前**。实测 `ord_asc12` worst 18 → `ord_desc12` 16（§2.2）。
  > 该条旧决定应据此改判。
- **P3b 决策树派发（主菜）**：新 `internal/opt` pass `balanceDispatch`（或 lower 内构造，实现时定）：
  把「同一 tag、全整数常量 case、每 case 一条融合测试」的比较链重写为二分树
  （节点 `Cmp(Lt/Gt)+Br` 复用现有分支融合，叶子 → 原 body 块 + default）。
  做成 opt pass 的好处：**手写的 if/else-if 链（社区固件常见形态、decompile 产物）同样命中**。
  触发门控：case 数 ≥5 且（`--fast` 或显式 **`// icg: dispatch=tree`**）且经分析器确认
  「最坏 tick 下降 且 在预算内」——`--fast` 下**由 P2 评分器裁决**，转换只当候选；
  带 pragma 时改为**只跑树候选池**（否则行数/字节的大小模型永远选更短的链），
  不适用（<5 个整数常量 case、有 range、有重复值）时发 `dispatch-tree-not-applied` 告警。
  正确性约束见 §4；`var<N>_2` 对照 `tree<N>.ic` 作为形状参照与回归目标。
- **P3c 跳表（降级为遗留项）**：`--jump-table` 保留原样；Fast 不再建议用它
  （实际 n 范围内树深度 ≤5+复核 < 跳表常数 ≈8，且跳表多花 ~n 行、平均更差，见 §2.2）。
  若未来 n 极大再评估放宽单语句体门控（多语句体本身只影响布局安全性论证，不影响表 idiom）。

### P4 — 工具面与文案（小）

- `--fast` 帮助文案与 README：「优先运行速度：展开更多、按每 tick 预算重排状态机派发与
  体积取舍；预算不足时自动退回更省行的候选」。
- **扩展侧的文案也要改**：`editors/vscode/package.nls.json` / `package.nls.zh-cn.json` 的
  `icg.fast.desc` 现在只写「展开更多循环」（`editors/vscode/extension.js:419` 透传 `--fast`）。
- `build --json` / `--score`：Fast 构建给出默认 vs fast 的两轴对照；
  LSP `tick-budget` 诊断在有行数余量时附 quick-fix「尝试 `--fast`」。
- testbench push 的 `--fast` 透传已存在（`cmd/ic10c/testbench.go` L2021）。
- 收尾时更新 `backlog.md`：D2 组追加本方案结论；**并改判两处旧记录**（§2.3 的「顺带撤回」需
  与本方案区分开；P3a 那条「派发臂重排不做」应标为被实测推翻）。

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
7. **分析器改动只收紧、不放松**（P1）：`tick.Analyze` 的返回值是**上界**（"最坏也不会超过"）。
   任何对 `jr` 后继的收窄都必须有**可证**依据（§2.4 的 codegen idiom）；无法证明时保留
   `indirect`。这条是硬约束：**低估会放行真会爆 tick 的产物**，比现状的高估危险。

## 5. 实施切分（建议提交顺序）

| 步 | 内容 | 规模 |
|---|---|---|
| 1 | P0：Fast 预算兜底（含"回落要 warning"、懒补跑保守 lowering） | 小-中（`pkg/ic10`） |
| 2 | P1：`jr` 跳表 idiom 识别（门控到可证 idiom）+ 两条回归（上界成立 / 不再误报） | 小（`internal/tick`） |
| 3 | P2：tick 评分（轴写全、诊断路径排除、memoize）+ 编译耗时基准 | 中（`pkg/ic10`） |
| 4 | P3a：case minimax 重排（Fast 门控）+ 改判 backlog 那条旧决定 | 小（`internal/lower`） |
| 5 | P3b：决策树 pass + 差分测试 + 回填实测行数/成本（替换估计值） | 中-大（`internal/opt`/`lower`） |
| 6 | P4：文案/LSP/JSON + 扩展 nls；文档与 backlog 更新 | 小 |

> 顺序说明：P0 独立且修的是「用户可见的编译失败」，先落地；P1 必须在 P2 之前——否则 P2 的评分
> 会拿被跳表误报污染的 tick 值去裁决，等于用错轴排序。P0 与 P1 都是 P2 的前提。

## 6. 验收标准

> 实测值已于 **2026-10-15** 回填（`ic10c 0.8.54` + 本分支，语料 `testdata/bench/fast/` 与
> `examples/` + `testdata/` + `ic10code/` 共 275 个 `.icg`）。

- `testdata/bench/fast`（默认 → `--fast`，静态 worst tick）：

  | 程序 | 行数 | worst | 降幅 |
  |---|---|---|---|
  | `var8_2` | 34→41 | 12→**8** | −33% |
  | `var12_2` | 50→61 | 16→**9** | −44% |
  | `db8_2` | 33→40 | 12→**8** | −33% |
  | `db12_2` | 49→60 | 16→**9** | −44% |
  | `var8_1` | 23→32 | 10→**7** | −30% |
  | `ord_asc12` | 71→82 | 18→**13** | −28% |

  全部 ≥25% ✓，且都在 128 行预算内（`var12_2` 的 +11 行就是叶复核成本）。
- `var*_1 --jump-table`：静态 **10**（不再 `EXCEEDS` 129），与动态 max **10** 一致；
  手写越界 `jr rX` 仍保守（§4.7 的两条回归均在 `internal/tick/jumptable_test.go`）。
- `气闸控制-四门 --fast`：103 行 / worst **75**（展开收益未回退）；`printer 表驱动 --fast`：**编译成功**
  （118 行）且发 `fast-fallback` 告警，产物与默认一致。
- **默认模式产物逐字节不变** ✓ 实测 **270/270** 与 `v0.8.54`（`acbdfa1`）完全相同。
- **全语料 tick 轴**：269 个可编译程序中 **更好 19 / 相同 250 / 更差 0**，`--fast` 编译失败 0 ✓。
- 编译时间（`testdata/bench/fast` + `examples` 共 28 个程序，单次）：**总耗时 ×2.42**
  （默认 4594ms → `--fast` 11114ms），最慢单例 **`气闸控制-四门` ×2.97（2.9s → 8.5s）**。
  这个代价是**结构性的**：要保住「`--fast` 的每-tick 最坏不劣于默认」这条保证
  （§5 的 pool 结构，`TestFastNeverWorseThanDefaultOnTheTickAxis` 钉住它），
  保守档必须跑完整候选矩阵，所以大致是一倍矩阵起步 + 树档的增量。`--fast` 是 opt-in，可接受但要知情。
- `go test ./...` 全绿；CLI 断言 70/0；`testdata/cli/run.sh` 已含本特性的断言。
- `testdata/server-captures/` 是 **gitignore 的社区捕获**，验收**不依赖** `arm.icg`。
- ⚠ **真机场景 `s30_dispatch_chain/tree` 本轮未复跑**（检查时游戏未启动）。它们只声明
  `d0: LED` / `d4: Memory`（当前机台满足接线预检），下次开游戏应连 `--diff` 补验一次。

## 7. 风险与回退

- **评分把编译时间拉爆** → memoize + 只对「code 与当前最优不同」的候选分析；兜底：Fast 下
  候选数上限。
- **分析器被"修"成低估（最危险的一类）** → §2.4/§4.7 的底线：只收窄到**可证**的 codegen idiom，
  证不出就保留 `indirect`；两条回归分别钉「不再误报」和「上界仍成立」。副作用是手写/反编译
  产物里"长得像跳表但无法证明有界"的 `jr` 仍报保守值——**这是可接受的**（宁可高估）。
- **树化在某些形状行数反升**（叶复核 +1/叶）→ 由评分器自动落选（P2 机制天然兜住），无需额外门控；
  但注意 §6 的阈值必须按**带复核**的树实测，别按省复核的参照值定。
- **分析器 DP 状态爆炸**（`maxDPStates`）→ 已有线性兜底（超限报预算封顶）；评分时把
  「Exceeds 且 indirect」候选排在「精确值」之后（保守正确）。
- **`--fast` 语义变化的兼容性**：`--fast` 的定位从「多展开」变成「tick 优先的取舍」，
  同一程序的 `--fast` 产物会变（默认模式逐字节不变 ✓）。已入库的依赖点只有
  `pkg/ic10/difftest_test.go:32` 的 `{Fast: true}` 选项（它比的是**优化 vs `IC10C_NO_OPT`**
  的写序列，不是 fast vs 默认 → 不受影响 ✓）与扩展的 `icg.fast` 透传，验收按 §6 跑一遍全语料即可。
- **真机语义未知**（本方案不需要新指令语义：`bge/blt/bne` 绝对跳转与融合分支都是现有产物；
  `jr` 已真机验证；无需新的游戏侧验证项——若 P3c 复活跳表再单独真机对拍）。

## 8. 实现记录（2026-10-15，分支 `feat/fast-statemachine-balance`）

### 8.1 落地内容

| 步 | 提交 | 要点 |
|---|---|---|
| P0 | `b50b9ab` | `compileInfo` 候选池抽成 `runPool()`；无候选可编译时以默认展开策略重试并发 **`fast-fallback` warning**（写明超预算原因）。P2 后该兜底并入池维度。 |
| P1 | `962e22a` | `internal/tick` 识别 codegen 跳表 idiom：`sltz/sgt/or/bnez` 界检查 + 紧邻 `add rX rX 1; jr rX` + **K+1 条连续可解析 `j` 行**才收窄后继到表目标（保持上界；界外/带标签空洞/手写 `jr` 维持保守）。回归三条：识别产物静态 10 == 动态 max 10；未证明 `jr` 仍 `indirect`；破洞表不识别。 |
| P2 | `b97654c` | Fast 下候选评分 `(入预算 → worst tick → Exceeds且indirect → ticksPerRound → lines → bytes)`（`tickInfo` 增 `indirect` 字段，按 code memoize）；`--fast` 池 = {激进,保守}×{链,树(门控)} + **未排序保守池**（排序生效时必跑）→ 「fast 的 worst tick ≤ 默认」有构造性保证。默认模式产物逐字节不变（golden + 全量差分绿，pkg/ic10 521s）。 |
| P3a | `b97654c` | `lowerSwitch`：常量 case 按**体估计成本降序**（minimax），marker `DispatchSorted`；range/重复值/非 tag switch 一律回退原链。 |
| P3b | `b97654c` | 二分决策树：整数稠密常量 ≥5 值，序比较节点 + **每叶等值复核**（NaN/小数/±Inf/越界 → default，与链逐值等价）；树根直接落在 switch 所在块（0 额外行）。marker `DispatchTreed`，仅探测命中才加跑树池。 |
| P4 | `3c74cc0` | CLI/扩展 nls 文案重写；`tick-budget` 诊断在有行数余量时附「可试 `--fast`」提示；`testdata/cli/run.sh` 增 6 项 fast 冒烟（编译/树形/worst 下降/树链 40-tick 设备快照一致/回落 exit 0/诊断码）。 |
| 附 | `3c74cc0` | **场景框架补 `reset` 接线**（`Scenario.Reset` 此前声明未用）：游戏侧 `reset` + VM 侧 `vm.Machine.Reset()` 同步生效，tick 相位敏感的多 case 场景才可复现；现有场景未开该字段行为不变。 |

### 8.2 实测（本分支 ic10c，`ic10c build[/--fast] + tick [+--run 40]`）

派发（`testdata/bench/fast/`，格式 行数/静态 worst/动态 max）：

| 程序 | 默认 | `--fast` | Δworst |
|---|---|---|---|
| `var8_2` | 34/12/12 | 41/**8**/8 | −33% |
| `var12_2` | 50/16/16 | 61/**9**/9 | −44% |
| `db8_2`（`db.stack` tag） | 33/12/12 | 40/**8**/8 | −33% |
| `db12_2` | 49/16/16 | 60/**9**/9 | −44% |
| `ord_asc12`（异体 handler） | 71/18/18 | 82/**13**/13 | −28% |
| examples 气闸三兄弟 | 72/26、102/117、77/20 | 74/**23**、103/**75**、94/**15** | −12%/−36%/−25% |
| 其余 15 个 examples | — | 与默认一致（无适用形状） | 0 |

- 带叶复核的树 = §2.2 省复核参照 (7/8) 各 **+1 worst**（8/9），行数增量恰为 **n−1**（`var12_2` +11），
  与 §4.2 模型吻合 → 该节估计值正式回填。
- 跳表（P1 后）：`var12_1 --jump-table` 静态 worst **10**（原 129 误报），≤ 动态 max 10 ✓。
- 回落（P0）：printer 表驱动 `--fast`（激进版 6218B>4096）→ 0.47s 编译成功 + warning ✓。

编译时间（进程级，`time -p ic10c build`）：

| 程序 | 默认 | `--fast` | 倍率 |
|---|---|---|---|
| `var12_2` | 0.02s | 0.04s | 2.0× |
| printer 表驱动 | 0.38s | 0.47s | **1.3×** |
| 气闸控制-四门 | 2.78s | 8.10s | **2.9×**（3 个全量池） |
| `arm.icg`（119 行 + 8 状态 + 同 tick 重派发） | 0.50s | 5.53s | **11×**（5 池 + 每候选分析 40–100ms） |

§6 的「≤3×」在中小型与预算压力程序达成；arm 类（近满行 + 树可触发 + tick 体巨大）超出，
缓解方向记录在 8.4#4。

### 8.3 真机验证（mod 0.5.13 / game 0.2.6428.27798，A CHIP）

- 新场景 `testdata/bench/ingame/s30_dispatch(.icg + chain/tree .json)`：7 态 Memory/LED 状态机，
  逐 case `reset` + 每 tick 一步；链版与 `--fast` 树版各 **5/5**，且 `--diff` 游戏与 VM 全一致
  （含小数 3.5 / 越界 9 / 负数 −1 tag 落 default）。树派发只用既有指令（融合 `bXX r c line`），
  无需新的游戏侧语义验证项。
- `testdata/bench/ingame/run.sh` 全套：**13 通过 / 8 失败**；8 个失败全部是**既有存档接线漂移**
  （`the rig does not match…`），用改动前的二进制复现同样失败，与本分支无关
  （需要时在匹配接线的存档上重跑，或 `--allow-rig-drift`）。
- 测毕已 `pause off` 恢复世界。

### 8.4 偏差与后续（明确记录）

1. **树在 lowering 实现**（`internal/lower/dispatch.go`），未按 §3 P3b 做成 `internal/opt` pass：
   定点幂等性与块编号布局更易控；代价是**手写/反编译的 if/else-if 链暂不覆盖**——后续可加
   `balanceDispatch` opt pass（链形状与 lowering 产物同构）。
2. `build --json` 的「默认 vs fast 两轴对照表」未实现：产物自身两轴已在 `tick`/`score` 字段，
   对照组需再跑一遍默认池外输出；列为后续。
3. LSP「尝试 --fast」quick-fix 降级为 `tick-budget` 诊断文案提示（codeAction 需改扩展 JS，价值低）。
4. arm 类程序 `--fast` 编译 11×：后续做**候选池并行**（池间无共享可变状态的拆分 + 锁住 `consider`）
   与「树池只测主 outline 计划」进一步收窄（本轮已把树池降为精简矩阵、护栏池保持全量）。
5. §4.2 的整数域可证免复核 / `floor` 全局守卫两档未实现（实测叶复核 +n−1 行在预算内可接受）。
6. P3c 跳表原样保留（`--jump-table` 语义、门控未动）；P1 已让它获得诚实的静态度量。

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

---

## 8. 实施状态（2026-10-15）

已按 §5 的顺序落地：**P0**（`compileInfo` 的 `runPool(full)` + 保守档回落 + `fast-fallback` 告警）、
**P1**（`internal/tick` 的跳表 idiom，门控到可证形状）、**P2**（`--fast` 评分轴改为
`cost → indRank → tpr → lines → bytes`；诊断路径不涉及 Fast —— LSP 从不设 `Options.Fast`）、
**P3a/P3b**（`internal/lower/dispatch.go`：常量 case 按体成本降序；≥5 个整数常量 case 降为平衡比较树，
每叶等值复核）、**P4**（帮助/README/扩展 nls/`tick.indirect` 文档；`Scenario.Reset` opt-in 接线，
新真机场景 `s30_dispatch_chain/tree`）。

与方案有两处**收窄**（都不影响验收，但记录在案）：

1. **树的用户开关已补**：`// icg: dispatch=tree`（文件 pragma，与 `private-stack` 同类）在**默认模式**
   下只用树候选池 → 拿到 tick 收益而不受 `--fast` 展开/回落的影响；不适用时发
   `dispatch-tree-not-applied` 告警而不是静默无效。默认模式（无 pragma）依旧逐字节不变 ✓。
   `--fast` 之外的"手写 if/else-if 链树化"仍**不做**（那条链没有 tag 常量集合，v1 不猜）。
2. **`--jump-table` 保持原样**（P3c 的"降级为遗留项"）✓，未扩门控。

另外修了**两处既有缺陷**（不是本方案引入，但被 P2 放大成用户可见症状），它们是同一族：

1. `internal/tick` 的 `detectTrips` 按 map 顺序扫描循环体、返回**第一个**匹配的退出分支 →
   同一输入每次算出不同的 `trips`（worst tick 在 54/66/72/84 间跳）；`--fast` 用它评分 →
   同一源码两次编译产出不同二进制（12 次构建 5:7 分裂）。改为按行号升序扫描。
   回归：`TestAnalyzeIsDeterministic`（`sugar.icg` 30 次一致）。
2. 同族的另一半：`preds()` 返回 **map 顺序**的切片，而 `findInit` 取"第一个找到的写" →
   循环头有两个前置块各写不同常量时，`trips` 在 5/7 之间跳（实测 12 次：10×7 + 2×5）。
   改为 `sort.Ints(out)`。回归：`TestFindInitIsDeterministic`（钻石形 CFG 30 次一致）。

另外补了本轮发现的 CLI 缺陷：`tick`/`lex` 本就能读原始 IC10，却和 `build` 一样提示
「请先反编译」——已去掉（`build`/`stats`/`ast` 保留）。
