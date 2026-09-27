# 真机逐写 A/B（测试台）

把「反编译 → 重编译」后的程序推到**真实存档的芯片**上，逐条执行并比较它发出的
**设备写效果序列**，验证重编译不乱改行为。

```bash
python3 testdata/bench/ab/ab_trace.py 13 ic10code/tugetest.ic10
# 输出（示例）:
#   store effects: original=659 recompiled=570
#   O1~O2 (control): 659 vs 659 effects, compared 659, 0 mismatches
#   O1~R  (A/B):     659 vs 570 effects, compared 570, 0 mismatches
#   restored the original on the chip
```

## 原理

- IC10 解释器把每一行**编译成 `ProgrammableChip+_Operation` 的子类**，
  写在各自 `Execute(index)` 里（**不经过** `ILogicable.SetLogicValue`，
  所以普通 Harmony 钩子抓不到芯片的写）。
- 测试台的 `trace {n}` 命令用 `Execute(1)` 一次一条推进，读 PC 与 `_Registers`，
  返回每条**存储指令**（`s/sd/ss/put/putd/clr/clrd/sb…`）及其寄存器快照。
- 本脚本按源码的 `alias`/`define`/寄存器/枚举把每条 store 还原成
  **效果** `(助记符, 设备 id, logic, 值)`。效果与**寄存器分配无关**，
  所以「原版 ×2」应当完全一致（确定性对照），「原版 vs 重编译」也必须一致。
- `s <reg> …` 与 `sd <reg> …` 是同一「按 ReferenceId 写」的两种写法，
  脚本归一化；枚举 `Color.Green` 与数值 `2` 通过 `enums.json` 归一化。
- **每轮前显式 `reset`**：`push` 会忽略它的 `reset` 参数，所以脚本另发一次
  `reset`，让两次运行都从程序第一行、同一个 sp 开始（芯片 12 之前 154 处差异就是
  相位/入口不同造成的，加上它后 0 差异）。
- ⚠️ **`reset` 不清零寄存器**：实测把 `move r7 5` 推给芯片后，再推一个只读 r7 的
  程序，r7 仍是 5。寄存器跨 `push` 保留，所以「读自己从不写的寄存器」的程序**不是**
  从受控状态开跑。要控制状态就先推一个只设置这些寄存器的小程序再推被测程序
  （或直接改被测源码、保持行数不变）。
- `putd` 与 `put` 归一化为同一助记符：编译器对（游戏里已废弃的）按 id 形式
  统一发 `put`，两者的设备/地址/值相同。

## 前置

1. 游戏运行中，装有 `tools/ingame-testbench` mod（含 `device` / `trace` / `writes`
   命令），`testbench.json` 里 `autoload` 指向要测的存档。
2. `ic10c` 在 `PATH`（`./build.sh install`）。

### 编译并安装 mod

BepInEx/LaunchPad **不能可靠热加载**改动过的方法体（会报 `Method has zero rva`），
所以每次改 mod 都要重编 + 拷 DLL + **重启游戏**：

```bash
cd tools/ingame-testbench
DOTNET_ROOT=/path/to/dotnet PATH=/path/to/dotnet:$PATH \
  dotnet build -c Release -p:StationeersDir="$HOME/.local/share/Steam/steamapps/common/Stationeers"
cp bin/Release/ic10go-testbench.dll \
  "$HOME/.local/share/Steam/steamapps/compatdata/544550/pfx/drive_c/users/steamuser/Documents/My Games/Stationeers/mods/ic10go-testbench/"
# 重启游戏（会按 autoload 载入存档）
```

## 注意

- **不要在游戏内用 `world.load` 载档**：它只能在主菜单用，游戏内载档会让世界崩溃。
- 比较是**固定指令预算内的前缀**（`--n`，默认 4000 条指令）。原版与重编译
  每轮指令数不同，所以总效果条数会不同；只要前缀逐条一致即可。
- **反馈型程序**（写自己又读的设备）每轮会改变下一轮的输入；两种形式速度不同，
  相位可能错开。此时先看**对照组**（原版 ×2）是否 0 差异——若对照就挂（如芯片
  43/46），该窗口不可用（原件本身不确定）；若对照通过而 A/B 挂，再查是否驱动
  状态差异（一般仍需同轮对齐才能定论）。
- **`reset` 不清宿主栈**：它清零寄存器/sp/PC，但 512 槽栈在多轮之间保留。读栈
  的程序（`pop`/`get db`）在两轮之间状态不同，**对照组就会挂**。测这类程序时，
  把源码里某个等长分支（如等待循环的 `beqz`）换成 `clr db`，让每轮从干净栈开始
  （源码行数不变，绝对跳转目标仍然正确）。
- 若芯片是**事件驱动**（等待占用传感器/按钮/拉杆），窗口内可能没有写——先触发它
  再跑，或换持续输出的芯片。
- `enums.json` 由 `go run ./tools/dumpenums > testdata/bench/ab/enums.json` 生成，
  游戏枚举更新后重新生成。

## 相关命令（测试台协议）

| 命令 | 用途 |
|---|---|
| `device {ids:[…]}` | 按 ReferenceId 读设备的 logic + 槽位（可读的才读得到） |
| `trace {n}` | 逐条 `Execute(1)`，返回执行到的存储指令 + 寄存器 |
| `program` | 芯片当前 IC10 源码（配合 `--from-chip`，无需从存档猜源码） |
| `writes {clear?,from?}` | Harmony 钩子记录的 logic 写（含**游戏自身**的写；对芯片的写无效） |
| `get`/`set` | `port` 或 `id` |
