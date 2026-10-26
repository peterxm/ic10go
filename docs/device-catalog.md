# 设备目录（device catalog）

编译器/编辑器里有三类游戏数据，来源不同：

| 数据 | 来源 | 是否需要游戏本体 |
|------|------|------------------|
| 枚举表（`LogicType`、`GasType`…） | `tools/genenums` 读 `Assembly-CSharp.dll` | 否（只要 DLL） |
| 预制体名/标题、IC10 指令说明 | `tools/gengamedata` 读 `StreamingAssets/Language/english.xml` | 否（只要游戏文件） |
| **设备 → 可读写 logic type / 槽位属性** | `tools/ingame-exporter`（游戏内扫描）→ `tools/import-devices` | **是**（要运行游戏） |

本文只讲第三类。前两类见 [`target-ic10.md` §5.6](target-ic10.md#56-预制体--指令表) 与 §5.8。

---

## 为什么必须在游戏里取

`CanLogicRead` / `CanLogicWrite` 是设备类上的方法，但它们读的状态
（`HasOnOffState`、`HasPowerState`、`HasColorState`…）序列化在 Unity 的资产包
里，不在 `Assembly-CSharp.dll`。所以「GasSensor 能不能读 `On`」这个问题，离线
读 DLL 回答不了，只能在游戏里对每个预制体逐个探测。

## 导出器（`tools/ingame-exporter`）

一个极小的 Stationeers 模组：

- **无 Harmony patch、无运行期行为**——加载时扫一遍 `Prefab.AllPrefabs`，写文件，
  然后什么都不做。
- 产出两个文件到 `Documents/My Games/Stationeers/ic10go/`：
  - `prefabs.json`：每个预制体的 `name` / `hash` / `displayName`。
  - `devices.json`（format 2）：每个可逻辑设备的槽位数、可读写 `LogicType`，**逐槽位**的
    `class` / `key` / `specificPrefabHashes` / 可读 `ls` / 可写 `ss` 的 `LogicSlotType`，
    以及逻辑内存（`get`/`put`）能力：`readable` / `writeable` / `stackSize`，
    有指令协议的设备还带 `instructionType` 和 `instructions`（opcode + 位域说明）。
- `devices.json` 与 IZCode 的目录同格式，两个模组任一产出都能被消费。

构建与安装见 [`tools/ingame-exporter/README.md`](../tools/ingame-exporter/README.md)。
要点：需要 .NET SDK + 游戏；Mod 加载需要 BepInEx + StationeersLaunchPad。

## 导入（`tools/import-devices`）

```bash
go run ./tools/import-devices                 # 自动在常见用户目录找两个 JSON
go run ./tools/import-devices prefabs.json devices.json outdir
```

- `prefabs.json` **合并**进 `internal/builtin/prefabs.go`：只补本地化没有的名字
  （程序化残骸、套件），**不覆盖**既有英文标题（运行时 `displayName` 跟随玩家语言）。
- `devices.json` 写进 `internal/builtin/devicecatalog.json`（`go:embed` 加载），
  并生成 22 行的 loader `internal/builtin/devicecatalog_gen.go`——数据放在 JSON 里，
  生成源码保持很小。

## 编辑器用它做什么

`DeviceCatalog` 非空时，`all(Prefab).` 的补全只列**该预制体自己的属性**，并标注
`r` / `rw` / `w`；目录为空时回退到全部 logic type（默认仓库里是空目录，所以离线
克隆不会退化）。见 `internal/lsp/lsp.go` 的 `devicePropertyItems`。

## 现状

- 已随仓库提交：**452 个设备、9073 条属性**（游戏 `0.2.6428.27798`），其中 197 个有
  槽位、30 个有逻辑内存（18 个带指令协议）；以及 161 个本地化缺失的预制体名。
- 游戏更新后重跑导出器并 `go run ./tools/import-devices` 即可。

## 逻辑内存 / 指令栈

`memory` 字段对应游戏的 `IMemory`（`GetStackSize`）、`IMemoryReadable`
（`ReadMemory` → IC10 `get`）、`IMemoryWritable`（`WriteMemory` → IC10 `put`）和
`IInstructable`（`GetInstructions` / `GetInstructionDescription`）。典型：

| 设备 | 指令枚举 | 可读 | 可写 | 地址数 |
|---|---|---|---|---|
| 太空服收纳柜 `StructureSuitStorage` | `SuitStorageInstruction` | 是 | 否 | 24 |
| 打印机 / 制造台 | `PrinterInstruction` | 是 | 是 | 见导出 |
| 逻辑分拣机 | `SorterInstruction` | 是 | 是 | 见导出 |
| 筒仓 `Silo` | `SiloInstruction` | 是 | 否 | 见导出 |
| 占用传感器 | `OccupancyInstruction` | 是 | 否 | 见导出 |
| 中型卫星天线 | `TraderInstruction` | 是 | 是 | 见导出 |
| 火箭航电 | `RocketAvionicsInstruction` | 是 | 是 | 见导出 |

`instructions[].format` 就是游戏内 Stationpedia「内存」页的位域表（已去掉颜色标签）。

## 未做

- 按 **具体接线** 校验（`d0` 接的是哪个设备）——离线编译器看不到接线，只有游戏内
  编辑器能。ic10go 目前用目录做「预制体级」的收窄，不做「端口级」校验。
- 按槽位索引的补全过滤（`all(Prefab).slot[i].` 仍列该预制体的槽位属性并集）。
- 槽位写能力（`ss`）在预制体上无法直接探测：`Device.CanLogicWrite` 要求
  `IsStructureCompleted`，预制体模板永远不满足。导出器按游戏同样的槽位 `class`
  规则回退（`On`：Helmet/Tool/Appliance；`Lock`：Helmet；`Open`：
  Helmet/GasCanister/LiquidCanister/LiquidBottle；`Console` 额外 `Mode`）。
