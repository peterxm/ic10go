# 改进清单（backlog）

> **`.icg` 语法已冻结**（自 `ic10c 0.8.23`，见 [`spec.md`](spec.md)）：本清单只收
> **实现层**的改进（内建、优化、工具、测试），不引入新关键字/语法形式，也不做破坏性改动。
> 需要新语法的想法一律不做。

围绕 **"零成本表达力"** 与 **"把程序塞进 128 行 / 4 KiB"** 两条主线整理。原则：

- 任何新语法/内建都必须**在编译期消解**，不产生运行期对象（否则负预算）。
- 优化以**减少行数**为先、字数为次；默认行为应尽量取更短者，而不是靠用户开 flag。
- **不做**无界序列容器（list/数组 + 查询 DSL）：见文末「明确不做」。

优先级：**P1 > P2 > P3 > P4 > P5 > P6**。状态用 ✅ 已实现 / 🚧 进行中 / ⬜ 待做。

> ✅ **VSCode 直传原始 IC10 + 上传尾部空行（2026-10，mod 0.4.6 · 扩展 0.7.40）**。以前「上传」只认
> `.icg`（`activeICG` 要求 `languageId === 'icg'`），打开 `.ic10` 会报「请先打开一个 .icg 文件」；现在
> `.ic10`/`.ic`（IC10）走 `activeProgram()` 里的 **raw 分支**，把文件内容**原样**经 `testbench push`
> 上传（不编译）。另外编译产物以 `\n` 结尾，`SetSourceCode` 会把它当成**空的最后一行**（芯片显示 31 行
> 而实际 30 行指令）；现在 CLI/扩展上传前 `TrimRight("\r\n")`，mod 的 `Push` 也兜底裁剪（含 loader）。

> ✅ **`readById`/`writeById` 改发 `l`/`s`（去掉弃用的 `ld`/`sd`）（2026-10）**。游戏的 IC10 现在支持把
> **ReferenceId 直接当设备操作数**（`l r0 r4 Setting` / `l r0 704 Setting`），旧的 `ld`/`sd` 已弃用。
> 真机（0.2.6428）实测：按 id 的 `l`/`s` 与 `ld`/`sd` 在读、写、缺 id 报错上**完全等价**、字节数相同。
> 编译器默认改发 `l`/`s`；`--legacy-by-id`（CLI，及 VSCode `icg.legacyById`）可回退到 `ld`/`sd` 兼容老版本。
> 反编译仍把 `ld`/`sd` 还原成 `readById`/`writeById`，且新的 `l`/`s` 按 id 也能还原；`readReagentById`（`lr`）
> 与 `readByIdSlot`/`writeByIdSlot`（`ls`/`ss`）本就是现代写法，未动。

> ✅ **反编译：未知值操作数不再丢行；运行期 id+logic（已修，2026-10）**。真机脚本暴露两处：
> ① 不认识的**值操作数**（游戏特殊寄存器 `rgas`、试剂名等）会让**整行被丢**（改行为）；现在改
> `raw("...")` 原样保留并告警，寄存器照常参与分配。② `l rN rM` / `s rN rM v`（设备 id 在寄存器、
> 逻辑类型也在寄存器）被译成 `read`/`write`（要求编译期设备）而重编译报错；现在用
> `readById`/`writeById`（发 `ld`/`sd`）。回归：`TestDecompileUnknownValuePreserved` /
> `TestDecompileDynamicLogicOnIDRegister`。间接寄存器组的 bank 分析起始状态改为按 IC10 语义
> 取**全 0**（见下文「间接寄存器组」），`indirectRegsHousing` 得以正确还原。

> ✅ **测试台定位 / 显示 / 筛选（2026-10，mod 0.3.x · 扩展 0.7.33）**。在社区存档里找一块芯片
> 原来很痛苦（`id` 重启会变、名字大量重复），于是：
> - `chip.list` 每条带 **世界坐标 `pos`**、**程序指纹 `fp`**（源码归一化后 SHA‑1 前 4 字节，重启不变）
>   与 **通电 `powered`/`power`**；
> - `ic10c testbench locate` 支持**按源码**（`--program FILE`，指纹匹配）、**按坐标**
>   （`--at X Y Z [--range N]`：精确=三轴四舍五入忽略小数，大致=N 格内按距离排序）、按名字/索引；
> - 游戏内 **HUD 罗盘**（`hud` / 右键 **Track in Game**）：自身 `X/Y/Z` + 朝向/俯仰、追踪目标的
>   距离/方向词/箭头；`F8` 开关、`F9` 清目标、`hud --offset N` 校准。朝向对齐 StationeersUIMod 的
>   `HeadingDeg = CameraController.CurrentCamera.eulerAngles.y + 180`（用错相机会差 180°）。
>   目标也可是**玩家**（`players` 经 `Brain.PlayerBrains` 列出**在线/离线**玩家 / `hud --player NAME`，在线者位置每帧实时读）。
>   修了 HUD 一处格式符 bug（`{0,+0.0;…}` 逗号当了对齐符）——它每帧抛 `FormatException`，一次会话刷了
>   2.6 万行日志并拖住主线程，正是「下不了代码 / 定位报错」的元凶；现在出错只记一条、连续 30 次自动关 HUD。
> - VSCode：树按「**有芯片·有代码 / 有芯片·无代码 / 无芯片**」分三组、组内名字降序；标题栏
>   **按坐标找**与**只看身边 ±4**；右键 host **Track in Game**。
> - 另修 CLI：`--chip 26` 现在按**索引**解析（原来当名字 → `no chip named "26"`）。

> ✅ **测试台：只要世界里还有实体就能追踪（2026-10，mod 0.4.2 · 扩展 0.7.36）**。之前 VSCode
> 按 `online` 判断，离线玩家直接提示「无法追踪」，可他们的角色（或尸体袋）其实还在世界里、`players`
> 也显示着距离。现在：
> - `players` 每行新增 `trackable`（世界里能找到实体）与 `body`（是尸体袋）；离线但角色还在的行标
>   「离线 · 角色在」，尸体袋标「尸体袋」，都能被 HUD 指过去（HUD 目标由 `Human` 泛化为 `Thing`）；
> - 尸体袋的 `Brain` 是私有成员，用反射（带显示名兜底）关联到所属玩家；
> - VSCode 只在**确实没有实体**时才拒；`ic10c testbench players` 同步显示。

> ✅ **切存档后 VSCode 列不出芯片（2026-10，mod 0.4.5 · 扩展 0.7.38）**。mod 把「当前选中的芯片」按
> `ReferenceId` 缓存在 `_selection`；这个 id 属于**上一个世界**。从线上模式 / 旧存档切到本地存档后，
> 任何不带显式 chip 的命令（`state`/`program`/…）都去解析那个旧 id → `no-chip: no chip with
> ReferenceId …`；扩展的刷新把这条错误误判成「当前世界没有可编程芯片」，于是树里空空的（`players`
> 是另一条命令，所以人物照常）。修法：mod 解析不到缓存的选中项时**清掉它并回退到默认芯片**；
> 扩展在 `no-chip` 但 `chip.list` 非空时不再判定「没有芯片」，只当「还没选」，并把失效的 pinned
> 选择持久化清除。`ic10c testbench state` 现在切档后也能直接工作。

> ✅ **HUD 箭头指反 180°（2026-10，mod 0.4.3）**。罗盘朝向用的是 `eulerAngles.y + 180`（对齐站内 HUD），
> 但箭头把同一个 `yaw` 直接和目标方位角相减，于是**天然差 180°**：提示「正前」时目标其实在身后，
> 跟着走只会越来越远（实测 200 m → 450 m）。现在箭头改用摄像机的**世界朝向** `atan2(forward.x, forward.z)`
> 与目标方位角求相对角，纯几何、与罗盘约定无关；`hud --offset` 只校准朝向数字，不再影响箭头。

> ✅ **HUD 布局 + 玩家列表排序（2026-10，mod 0.4.4 · 扩展 0.7.36）**。追踪芯片时名字太长会把「距离/方向词」
> 挤出 220px 的面板（只剩名字和高差）；现在名字单独一行并按像素宽度截断（`Fit`），距离/方向、高差各占一行。
> `players` 排序从「按距离」改为**你 → 在线 → 离线(角色在) → 尸体袋 → 无实体**、组内按名字（再按距离）；
> 扩展的 QuickPick 同样再排一遍，老 mod 也能得到这个顺序。另加 `tools/ingame-testbench/deploy.sh`：
> 构建并安装，**游戏在跑时拒绝覆盖 dll**（避免 Mono 惰性 JIT 的 `method has zero rva`）。

> ✅ **寄存器分配：几乎保留全部寄存器时的指数爆炸（已修，2026-09）**。`reserveRegs(0, 15)`
> 配稍大程序（例：`solverLarge` 改写版 2.4 KB）原来会让编译 **>120s 不返回**。
>
> 根因（实测每轮数据）：palette 为空 ⇒ 每个值都判为 spill ⇒ `spiller` 重写函数、为
> load/store 临时量再引入新寄存器 ⇒ 下一轮 spill 集**翻倍**（110 → 400 → 800 → 1600 → …），
> 64 轮上限对应约 2⁶⁴ 条指令，等价于永不返回。前端 0.56ms、降级 0.10ms，全部时间都在
> `regalloc`。
>
> 修法：分配器加**发散检测**——当上一轮已需 spill ≥32 个值、且本轮 ≥2 倍时立即报
> `register allocation did not converge`（并带可操作提示），另加 20000 条指令的兜底预算；
> 小规模翻倍（1→2、2→5）是正常的，不触发。修后该用例 **0.03s** 报错。
> 反编译器侧另有 `bankMaxRange = 12`，保证自身产物不会落入这段。

> ✅ **不可达前驱污染支配集（已修，2026-10）**。`ir.Dominators` 把**不可达块**也当成前驱
> 求交，而不可达块的支配集只有自身，于是可达块的真实支配者被抹掉。后果之一：`licm` 里
> 判断「定义块是否支配其所有使用」的 `dominatesAllUses` 误判，把一个定义提升到某个使用点
> 之前，随后 `dce` 按活跃性把它删掉，产出内部错误「块 N 使用寄存器 R，但它从未被定义」。
> 触发样例（`data` 表用循环变量索引 + 循环内 `continue` + 无 `yield`）：
> `for { i := d0.Setting; if i < 0 || i >= 3 { continue }; d1.Setting = T[i] }`。
> 修法：`Dominators` 先算 Entry 可达集，求交时跳过不可达前驱（不可达块支配集取自身）。
> 回归：`TestDominatorsIgnoreUnreachablePreds`、`TestDataTableIndexInLoopWithContinue`。
>
> 同轮修：`Function.NewBlock` 原来用 `len(Blocks)` 当块 ID，块被删除后再新建会**复用 ID**；
> 而终结符 `Key()` 用块 ID，两个不同块会被当成同一个（CSE / 合并类 pass 的隐患）。改为
> 单调计数器分配。回归：`TestNewBlockUniqueIDs`。

> ✅ **标签当值（已修，2026-09）**。标签用作值应取它的行号（游戏与 VM 都已验证：注释行、
> 空行都计数）。原来在**分支条件**里会失败——生成的 IC10 留着占位符（控制字符
> `\x01L5\x01`）。根因：标签的块在布局时没有自己的行（它只是个跳转到下一块的空块），
> 旧代码只对**已布局**的块做占位符替换，于是遍历不到它。
>
> 修法：`codegen` 的 `labelLine` 沿该块的 `Jmp`/`Goto` 链找到第一条真正发射的行——这正是
> IC10 里「标签取下一行」的含义；找不到才报 internal error（不再输出控制字符）。反编译器
> 随之**取消了「只在赋值形式翻译」的限制**，`roboroutine`/`baseFriend`/`dirciroutine`
> 的告警**全部归零**，语料往返（写序列 + 用户栈）全绿。

> ✅ **编译器：共享栈上被覆盖的写被误删（已修，2026-09）**。`deadStores` 判「该槽之后没被读
> 过就直接删」，但**共享栈在每个 tick 边界都可能被别的芯片看到**——芯片跑满指令预算就被打断，
> 中间值照样会暴露。于是反编译产物里这种形状会丢掉一条可达的写：
>
> ```icg
> // icg: shared-stack
> var r0 = d0.Setting      // 编译期未知；运行时 r0 = 1
> var r1 = 1
> label L:
> if r0 < r1 { goto M }
> put(db, 0, 2)            // 运行时走这里
> goto L
> label M:
> put(db, 0, 3)
> ```
>
> **真机实测**（芯片 2，原件 299 条 `put` 效果）：修复前重编译版 **0 条**（整段程序等于空转），
> 修复后 299 条全部一致（0 差异）。`IC10C_NO_OPT=1` 时也有那条写。
>
> 修法：`deadStores` 只在 `fn.PrivateStack` 时才把 store 判死（私有栈确实没人能看见）。
> 语料行数**零影响**（这个优化本来也没在这些共享程序上触发）。反编译器现在给产物加
> `shared-stack`，所以这条约束默认生效。

> ✅ **间接寄存器组（已修，2026-09）**：反编译器现在分析运行期指针能命中的寄存器集合，
> 把这段范围保持为**物理寄存器**（发 `reserveRegs(lo, hi)`，访问写成 `ireg(N)` /
> `setIreg(N, v)`），与手写端口的做法一致。分析（`internal/decomp/bank.go`）是对解析后
> 指令表的小型抽象解释：寄存器取已知整数或未知，`sp` 当作 16 号寄存器跟踪（所以计数器
> 驱动的循环会终止），条件未知的分支两条边都走；**任何**动态间接访问的指针未知就放弃并
> 报一条 warning，而不是静默产出可能错的代码。起始状态按 IC10 语义取**全 0**
> （`r0..r15` 与 `sp` 都是 0），所以只靠初值 0 当索引的寄存器表也能定界——例如
> `indirectRegsHousing`（寄存器里放 4 个太阳能板 prefab hash，`list` 从不赋初值直接用）
> 现在会还原成 `reserveRegs(0, 3)` + `setIreg(0..3, hash(...))`。间接寄存器 / 寄存器组的
> 完整分析（编译期折叠、能否自动降级、端口测试盲区）见
> [`register-banks.md`](register-banks.md)。
>
> 配套的**代码生成折叠**（`internal/codegen`）把这种寄存器访问压回一行：
> `t = <op>; rrP = t` → `<op> rrP ...`（间接目标本来就是合法目标操作数），
> `u = ireg(rrP); d = u op b` → `d = rrP op b`（间接读是合法源操作数）。
>
> **结果**：`traderSolver{,RAW}` 126 行、`solverLarge{,RAW}` 106 行，
> **`knownUnsupported` 现在为空**——四个求解器都纳入写序列 + 用户栈往返测试，真机逐写
> A/B（`solverLarge` 去等待分支后 95 vs 88 效果）**0 差异**。
>
> `Barsiel_s Furnace` 的指针在某条路径上确实未知（`r5` 在到达 `trunc rr5 r0` 前可能来自
> 设备读），按设计报 warning。
>
> 已修（同轮）：`shared-stack` 静默删写、VM 的 `define` + `HASH` 解析、
> round-trip 测试盲区（见下）。
> - **round-trip 测试盲区**：以前只比较**设备写**，而这些程序的接口是**宿主栈**，
>   全 0 输入下输出退化、比较空过。新增 `TestIc10CodeRoundTripStack`：预填宿主栈、
>   跑到**稳态**、比较用户区 `[0,128)`（编译器的 data/spill 区在其上方，重编译可以合法
>   地不去动它）。
> - **`shared-stack` 静默错编译**：单芯片程序默认 `private-stack`，会把「自己从不读的
>   宿主栈写」当死存储删掉——而 `db` 栈是给别的芯片读的接口。`put db 0 42` 单独编译
>   得到**空程序**，`dirciStackloader.ic10` 被删到 **1 行**。反编译器现在总是给产物加
>   `// icg: shared-stack`（全语料代价仅 +23 行）。注意：**手写** `.icg` 若向宿主栈发布
>   数据，仍需自己写 `// icg: shared-stack`。
> - **VM 的 `define` + `HASH` 解析**：`define A HASH("x")` 后使用 `A` 会解析成字符串而
>   不是数字（`resolve` 只在初始符号上试 `HASH(...)`），于是原程序在 VM 里读到 0。
>   已修（每步符号展开都重试）。

---

## 优先级总表

| 优先级 | 项 | 主题 | 影响 | 工作量 | 状态 |
|---|---|---|---|---|---|
| **P1** | A1 | `const` / `data` 可调用**纯用户函数** | 省行 + 表达力 | 中 | ✅ |
| **P1** | A2 | 补齐可折叠的**纯内建** | 省行 | 小 | ✅ |
| **P2** | B1 | `import` / 多文件 / 本地库 | 库的载体 | 中 | ✅ |
| **P3** | C3 | 编译期**循环/生成器**（生成 `data`） | 去重 + 省行 | 中 | ✅ |
| **P3** | A4 | `data` 表可由编译期函数生成 | 省行 | 中 | ✅ |
| **P4** | D2 | 统一 **size model**，把 opt-in 优化变自动 | 省行 | 中 | 🚧 |
| **P4** | D3 | 放宽 **outline**（多层/非叶子） | 省行 | 中-大 | ✅ |
| **P4** | D4 | 特殊寄存器（`sp`/`ra`）算术**两地址折叠** | 省行 | 小 | ✅ |
| **P4** | D5 | **空分支清理**（目标即下一行） | 省行 | 小 | ✅ |
| **P4** | D6 | 间接寄存器操作数折叠（`rrN` 目标/源） | 省行 | 小 | ✅ |
| **P4** | D8 | 折叠扩展：分支/`Cmp` 操作数、`isSet`→`bdse`、`pop`→`rrN`、`constProp` 跨循环、`licm` 取更短 | 省行 | 小 | ✅ |
| **P5** | B3 | 小记录**多返回值**（非容器） | 表达力 | 中-大 | ✅ |
| **P6** | A3 | 编译期字符串（拼接 + hash） | 表达力 | 中 | ✅ |

---

## P1 — 编译期求值：从"常量折叠"升级为"编译期执行"

### A1 `const` / `data` 可调用纯用户函数 ✅

**问题**：`const X = helper(3)` 直接报 "not a compile-time expression"，即使 `helper` 是纯函数。求值器（`internal/sema/sema.go` 的 `Eval`/`evalCall`）只认内建。

**方案**：把 `const` / `data` 的求值**推迟到声明收集之后**（此时函数表已完整），用一个能解释纯用户函数的求值器：

- 纯函数判定（调用图闭包）：函数体不含设备访问、无副作用内建、无对非纯函数的调用、不读 `data` 表。
- 解释器支持：局部变量、`if/else`、`for`、`return`、赋值、算术/比较/三元、内建调用、用户函数调用。
- **步数预算**防止 `for {}` 不终止；超预算即判定"不是编译期表达式"并报错。
- 设备/data 参数、递归（语言本就不允许）一律不参与编译期求值。

**验收**：
- `const K = triple(3)` 折叠为 `9`，产物无运行期指令。
- `data T = [f(0), f(1), f(2)]` 可由纯函数生成。
- 非纯函数（含 `yield`/设备读/`push`）在 `const` 中仍报错。
- 不终止的 `const` 在步数预算内报错而非挂死。

### A2 补齐可折叠的纯内建 ✅

**问题**：`evalCall` 只折叠 `abs/sqrt/floor/ceil/round/trunc/pow/min/max/hash`；`const S = sin(0.5)`、`clamp(...)`、`sgn(...)` 失败。

**方案**：把以下纯内建加入编译期求值：
`sgn sin cos tan asin acos atan atan2 exp log clamp lerp isNaN`（`log` 为自然对数，与 IC10 的 `log` 一致）。

**验收**：上述函数在 `const` / `data` 中可用并折叠；`rand` 永不折叠。

---

## P2 — `import` / 多文件 / 本地库 ✅

**问题**：`.icg` 单文件编译，`const`/`func`/`data` 库只能整段复制粘贴，没有"标准库"。

**方案（已实现）**：顶层 `import "路径"`，相对导入者目录解析、省略扩展名补 `.icg`；
被导入文件只能声明 `const`/`data`/`func`（无 `chip`/`bus`/`use`/`main`）；菱形与循环
导入按文件去重；节点保留各自文件位置。**LSP/VSCode 也会跟随导入**（相对当前文件）。
库搜索目录由 API `Options.LibDirs` 提供，CLI 暴露为可重复的 `--lib DIR`，
VSCode 为设置 `icg.libDirs`（同时作用于语言服务器与编译/运行）。

**验收**：两个文件共享 `const`/`func` 并折叠进产物；缺失导入报错并指向正确行；
循环导入正常合并；被导入文件声明 `main` 报错。

---

## P3 — 编译期生成 ✅

### C3 编译期循环 / 生成器 ✅

**方案（已实现）**：`data` 支持**范围推导** `data T = [ expr for i in lo..hi ]`，在编译期
展开成整张表；元素表达式可调用纯函数，上下界必须是编译期常量整数，长度受 512 槽持久
栈限制。`for i in range` 形式之外（如展开重复语句块）暂未提供。

### A4 `data` 表由编译期函数生成 ✅

由 P1 的编译期执行直接得到：`data T = [f(0), f(1)]` 与推导形式都可用。

---

## P4 — 优化器默认强度 🚧

### D2 统一 size model 🚧
编译器本就会对 **inline/outline、data-read 折叠、mem2reg** 试多方案并取更短的 runtime；
本次又给 outline 增加了**单个函数**候选（整套外提不如只提一个时能选到）。另在**优化版
接近/超出 128 行或存在溢出**时，与**整体未优化**版比对取更短者（见
[`architecture.md`](architecture.md) §5）。其余 opt-in 开关**不适合默认开启**，原因如下，故保持手动：

- `--fast`：以运行速度为目标（多展开），不是省行。
- `--jump-table`：依赖 `jr` 的真机语义（与 `--rel-jump` 同类，需要真机验证）。
- `--auto-table`：会把 switch 放进数据段，**要求先跑 loader**，改变部署契约。
- `--redundant-device-writes`：改变可观测的设备写序列。
- `--merge-renamed-tails`：实验性，会改变寄存器使用。

### D3 放宽 outline：非叶子（D3a）✅

**已实现 D3a**（2026-09）。`PlanOutlines`/`outlinable` 现在允许**非叶子**函数：外提时其
函数体内所有调用都**内联**，因此外提体内不含 `jal`，IC10 唯一的 `ra` 依然安全。低层
`label`/`goto`/`call`/`ret` 改用**传递**的 `computeLabeledFuncs` 排除（只看函数自身会漏掉
“f 调用含 `call` 的 g”）。实现点：

- `internal/lower/outline.go`：`outlinable` 去掉 `leaf` 约束，改用传递 `labeled` 集合；非
  多返回值、语句数 ≥2 不变。
- `internal/lower/lower.go`：新增 `inOutlineBody`——`lowerOutlined` 期间置位，
  `lowerCallExpr` 不再走 `outlineCall`，于是体内全部内联。
- size model 不变（仍含“完全不外提”候选），**行数只减不增**；`IC10C_NO_OUTLINE` 可整体关闭。

**实测**：
- 现有语料仍为 **0** 收益（127 个 `.icg`，非叶子且被调 ≥2 的函数 = 0；叶子且被调 ≥2 = 16）。
- 合成场景 `experiments/outline-d3/`（`step` 非叶子、调 `clamp`、被调 3 次）：**53 → 28 行**，
  寄存器 **8 → 2**，VM 写序列一致。
- 真机场景 `testdata/bench/ingame/s50_nonleaf_outline.json`（A CHIP）：默认 **25 行 / 2 个
  `jal`** vs `IC10C_NO_OUTLINE=1` **39 行**；两种构建都通过 `testbench run --diff`（真机 +
  VM，3/3）。
- 测试：`internal/lower` `TestPlanOutlinesNonLeaf`（叶子候选 + 传递 labeled 排除）、
  `pkg/ic10` `TestOutlineNonLeaf`（行数下降 + 写序列一致）。

D3b（真正嵌套外提、保存 `ra`）未做，也不再需要：D3a 已覆盖“非叶子被多处复用”，且无
`ra` 风险。

**修复（2026-10-06）**：两处会让外提产物语义出错的问题（`testdata/cli` 的
`D3a: 与全内联语义一致` 一直失败）：

- **外提体没被发射**：驱动循环 `for _, name := range l.pending` 在进入时固定了长度，
  而发射一个外提体会遇到新的外提被调者（非叶子体里调用了另一个外提函数），这些名字被
  追加到 `l.pending` 却不在迭代范围内 → 函数体从未发射，`jal` 落到空块（`move r0 r0`）。
  改用 worklist（`for i := 0; i < len(l.pending); i++`）。
- **结果寄存器被下一次调用冲掉**：`outlineCall` 之后复制 `tmp = func$ret`，但 `ir.Call`
  没把 `func$ret` 记为「调用定义」，加上 regalloc 里一处特例**强行删除** `tmp` 与
  `func$ret` 的干涉边并合并 —— 于是 `f(x) + f(x+1)` 里第一次调用的结果在第二次调用时被
  覆盖（产物出现 `add r0 r2 r2`）。修法：`ir.Call`/`ir.BrCall` 新增 `Result *Reg`，
  `ir.TermDefs` 把它计入活跃性，删除那处不安全的合并特例。回归测试
  `TestOutlineResultAcrossCalls`（修复前失败）。
- 代价：语料仅 `testdata/bench/ingame/s50_nonleaf_outline.icg` 从 23→25 行（正确性优先）；
  examples 17 个文件行数不变。

**触发条件**：`import`（P2）催生的“分层库 + 非叶子 helper 多处复用”。两者都可作为候选
方案由 size model 取更短者，行数上不会变差。

### D4 特殊寄存器算术两地址折叠 ✅

**问题**：`sp`/`ra` 不参与通用分配，但可以像普通寄存器一样作为算术目标。编译器把
`sp = sp - 46` 降级成"临时寄存器 + 回写"：

```asm
move r0 sp
sub r0 r0 46
move sp r0
```

3 行，而 IC10 允许直接写 `sub sp sp 46`（`--spill stack` 的存取路径本就发
`add sp sp 1`）。

**方案**：`internal/codegen` 在**行号回填之前**做一次纯渲染折叠（不改 IR，因此对同一
`fn` 反复调用 `generate` 是安全的），识别两种形态：

- `t = a op b; sp = t` → `op sp a b`（2 行变 1 行）
- `t1 = sp; t2 = t1 op b; sp = t2` → `op sp sp b`（3 行变 1 行）

只在**临时寄存器/被读的特殊寄存器全局仅此一次使用**时折叠；`Bin`/`Un`/`Select` 都覆盖。
减行后跳转目标由现有布局重算。（`Select` 只在 `select` 与其 `sp` 存储在同一个基本块时触发，
见 [`special-reg-operands.md`](special-reg-operands.md)。）

**实测**（122 个语料脚本，反编译→重编译）：共省 **56 行**，8 个脚本变短
（`roboroutine` 114→101、`Vending_Machine_Controller` 112→100、`baseFriend` 82→71、
`dirciroutine` 108→102、`tugetest`/`printers` 123→118）。真机逐写 A/B（芯片
13/47/6/45）**0 差异**，其中芯片 13 的产物确实含 `sub sp sp 46` 等折叠行。

### D5 空分支清理 ✅

**问题**：产物里存在目标就是下一行的分支/跳转（`j 47`、`bne r3 2 61` 等）。两支落到
同一行，指令是空操作。

**方案**：`internal/codegen` 在行号回填前删除这类行，迭代到不动点（删行可能让别的分支
的目标变到相邻行）。**跳转表条目**（`j <case>`，按索引寻址，删了会错位）与**调用**
（`jal`、`b<cond>al`，必须真正执行）显式排除；`line` 结构新增 `table` 标记。

**实测**：语料里清掉 8 处，共省 8 行；真机逐写 A/B 同 D4 一样 **0 差异**。

### D6 间接寄存器操作数折叠 ✅

**问题**：IC10 的 `rrN` 既能作目标也能作源操作数，但 `.icg` 的 `setIreg(ptr, t)` / `t := ireg(ptr)`
先用临时寄存器再搬。反编译「间接寄存器组」程序时这类往返很多（`traderSolver` 里 13 处）。

**方案**：`internal/codegen` 在行号回填前折叠（都要求临时寄存器全局单次使用）：

- `t = <op>; rrP = t` → `<op> rrP ...`（跨过 lowerer 产生的一次拷贝也算）
- `u = ireg(rrP); d = u op b` → `d = rrP op b`（读必须紧邻消费者）
- `u = sp; d = u op b` → `d = sp op b`

消费者不限于算术：单次使用的读还可以直接喂给设备写（`s` / `ss` / `sd`）、内建
（`poke`/`put`/…）和 `select`——`u = sp; s db Setting u` → `s db Setting sp`、
`u = sp; poke u v` → `poke sp v`。另外 `select` 也支持写 `sp`/`ra`
（`t = select c a b; sp = t` → `select sp c a b`）。细节与 `reoreotest` 的 92→95 案例见
[`special-reg-operands.md`](special-reg-operands.md)。

**实测**：语料 5511 → **5490 行**（6 个脚本变短）。其中 `traderSolver` 139 → **126 行**，
`knownUnsupported` 因此清空；真机逐写 A/B（`solverLarge`，去掉等待分支以便确定）
**0 差异**。

### D7 设备参数外提（drN）🚧

**问题**：带设备实参的调用**强制内联**（`hasDeviceOrDataArg`），设备 helper 被调 K 次就
复制 K 遍。对照 [sicc](https://github.com/Alan-Chen99/sicc) 的温控示例：同样语义 17 行 vs
本项目 20 行，差距主要来自「设备端口走运行期寄存器 + 子程序只发一次」。
分析见 [`sicc-comparison.md`](sicc-comparison.md)。

**已实现（Round 1，2026-09）**：运行期设备端口。`dev.Prop` / `dev.slot[i].Prop` 在 `dev`
是运行期值（局部变量 / 参数 / 数值常量）时下沉为 IC10 的寄存器选择设备操作数 `drN`
（复用 `readDev`/`writeDev` 的 `LoadDyn`/`StoreDyn`、`readDevSlot`/`writeDevSlot` 的
`LoadSlot`/`StoreSlot`）。编译期端口仍走 `dN`，零开销。**顺带修复**：以前把运行期值传给
设备参数会静默输出非法的 `dev.Setting`（仅警告），现在输出合法 `drN`。

真机验证：`testdata/bench/ingame/s60_dyndev`（真机 + VM 差分 4/4）。

**已实现（Round 2，2026-09）**：设备 helper 外提 + 动态设备读去重。

- **外提决策**：去掉 `hasDeviceOrDataArg` 对设备实参的一票否决。外提时设备实参按端口号
  写进参数寄存器，函数体用 `drN`；`data` 表实参仍强制内联，`db` 实参也回退内联（无数
  值端口）。常量实参调用点有两种候选——**保持内联折叠**（旧行为）与**一起外提**（常量
  在共享体里经全局常量传播折叠）——由大小模型取更短者（`lower.Options.InlineConstArgs`
  + 编译器候选循环）。
- **`drN` 冗余读消除**：`loadKey` 覆盖 `LoadDyn`（属性读）与带 `DevPtr` 的 `LoadSlot`
  （运行期槽位读）。运行期端口注册到伪设备 `"*"`，任何设备写都使其失效（保守但正确）。
- **候选诊断隔离**：一个候选变体内部的 `ir.Verify` 失败不再污染整次编译；大小模型丢弃
  它并保留可用的候选（顺带让随机差分里此前被掩盖的 mem2reg + 外提组合不再让编译失败）。

**实测**：目标例（sicc 温控）**20 → 18 行 / 249 → 190 字节**（sicc 17 行 / 190 字节，
只差尾调用）；语料 `testdata/programs`+`ic10code`+`examples` 共 134 个 `.icg`，
**5320 → 5270 行（−50，8 个脚本变短，0 个变长）**。真机 + VM：新增
`testdata/bench/ingame/s61_dyndev_outline` 4/4，全部 ingame 场景 13/13。全量
`go test ./...`、`./build.sh assert`（58/58）绿；`TestFunctionSpecialization` 仍保留
「常量调用折叠 + 变量调用外提」的语义。

**已实现（Round 3，2026-09）**：尾调用 / `ra` 复用。返回块只做一次跳转的 `Call` 在
`internal/opt` 的 `tailCall` pass 里改写为「`ra = <目的地>` + `Jmp <被调体>」；被调体的
`j ra` 直接回到目的地。被调体紧邻布局时跳转被消掉，每处省 1 行。IR/CFG 早已支持
`ra = <label>; goto sub` 手写调用（`BuildCFG` 会把该 label 作为所有 `JmpRA` 的后继），
所以无需新增 IR 节点。安全性：外提体已内联其全部调用，体内无第二个 `jal`；且只有
返回块为空的 `Call` 才改写（有返回值的调用返回块含拷贝，天然排除）。

**实测**：目标例 **18 → 17 行 / 190 字节**，与 sicc **完全持平**。语料累计
`5320 → 5268 行`（−52；气闸控制 92→79、气闸双门 78→72）。真机 + VM：
`testdata/bench/ingame/s62_tailcall`（真机 + VM 差分 4/4），全部 ingame 场景 **14/14**。
测试 `TestTailCall`（断言 `move ra` 存在、`jal` 恰 1 个、写序列与 `IC10C_NO_OPT=1` 一致）。
全量 `go test ./...`、`./build.sh assert`（58/58）绿。

**现状**：D7（运行期设备端口、设备 helper 外提、`drN` 冗余读消除、尾调用）已全部实现。

---

### D8 折叠扩展：分支/`Cmp`、`isSet`、`pop`-bank、`constProp` 跨循环 ✅

**问题**：D4/D6 的操作数折叠只覆盖算术、设备写、内建、`select`；分支与比较的操作数、
`isSet` 条件、连续 `pop` 都还没折。

**方案与实测**（语料 `examples` + `testdata/programs` + `ic10code`，`v0.8.43 → v0.8.44`）：

- **分支 / `Cmp` 操作数折叠**（`codegen.foldLoadTerminator` / `foldLoadOperand`）：
  `u = sp; bgtz u L` → `bgtz sp L`，`u = ireg(rrP); beqz u L` → `beqz rrP L`，
  `u = sp; d = u > 5` → `sgt d sp 5`。**−16 行**。
- **跨过一次拷贝**：折叠跳过降低器为变量产生的一次拷贝（`v = sp; x = v; poke x 1` →
  `poke sp 1`；比较结果先拷贝再分支同理），兑现 D6 里 `poke`/`put`/`get` 的说法。
- **常量代入分支终止符**：`a := 1; if a < 5` → 无条件（原来常量到不了 `foldBranches`）。**−5 行**。
- **`constProp` 定点修复**：must 分析**从 ⊤ 起步 + RPO 顺序迭代**，事实才能跨回边（循环里的
  常量被折进循环体）；RPO 把迭代数压到 O(嵌套深度)，否则随机差分超时。**−25 行**。
- **`isSet`/`isUnset` → `bdse`/`bdns`**：`branchCond` 新增 `BrSet` 终止符，条件位置省掉 `sdse`
  （`!` 自动取反；作为值仍是 `sdse`/`sdns`）。**−52 行**。
- **`pop` 批量折叠（bank）**：连续 ≥5 个 `pop()` → `move rC base` / `pop rrC` / `add` / `ble`（4 行任意 N）。
- **`licm` 开/关取更短**：外提新增 preheader 可能变长，试两种取更短（有循环时才试）。**−6 行**。
- **私有栈 mem2reg 别名安全**：`pop`/`peek` 与绝对用户槽共用内存，修掉了「`poke` 被删、`pop` 读到 0」。

累计 **−106 行**（`v0.8.43` 之后 0 个文件变长）；随机差分（优化 vs `IC10C_NO_OPT`）通过。

---

## P5 — 小记录多返回值（非容器）✅

**方案（已实现）**：`func f() (num, num) { return a, b }` + `x, y := f()`（也支持
`x, y = f()`）。多返回值函数**始终内联**，结果映射到独立虚拟寄存器，不引入调用约定或
返回地址（`ra`）风险；也因此不能被外提、不能当普通表达式、不能出现在 `const`/`data`。
与"不做序列容器"的边界：固定小记录映射到寄存器，无界序列要占栈槽。

---

## P6 — 编译期字符串 ✅

**方案（已实现）**：`hash` / `str` / `raw` 的实参支持**编译期字符串拼接**
（`"A" + "B"`，可嵌套），在编译期折叠：`hash("Structure" + "GasSensor")` 得到与
`hash("StructureGasSensor")` 相同的 CRC-32，`str("Re" + "ady!")` 输出 `STR("Ready!")`。
更完整的文本库（查找/切片/parse）未做。

---

## 明确不做

- **新语法**：`.icg` 语法已冻结（见 [`spec.md`](spec.md)），不再新增关键字/语法形式，也不做破坏性改动；本清单只收实现层改进。
- **无界序列容器 / LINQ 查询 DSL**：在 128 行预算下是负资产（物化要占栈槽、Blocking 阶段代价高）。真正省行的"查询"形态（网络批量聚合、常量表折叠）已由 `batch.*` 与 `data` 段提供。

---

> 相关：语言规范 [`spec.md`](spec.md)、目标与内建表 [`target-ic10.md`](target-ic10.md)、优化通道 [`architecture.md`](architecture.md) §5。
