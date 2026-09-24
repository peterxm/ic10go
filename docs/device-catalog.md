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
  - `devices.json`：每个可逻辑设备的槽位数、可读写 `LogicType`、槽位属性。
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

- 已随仓库提交：566 个设备、10495 条属性（游戏 `0.2.6428.27798`），以及 161 个
  本地化缺失的预制体名。
- 游戏更新后重跑导出器并 `go run ./tools/import-devices` 即可。

## 未做

- 按 **具体接线** 校验（`d0` 接的是哪个设备）——离线编译器看不到接线，只有游戏内
  编辑器能。ic10go 目前用目录做「预制体级」的收窄，不做「端口级」校验。
- 槽位属性的按预制体过滤（`all(Prefab).slot[0].` 仍列全部槽位属性）。
