# Stationeers 交易（Trading）玩法总结

> 来源：本地游戏本体 `Assembly-CSharp.dll`（游戏版本 `0.2.6428.27798`）反查的类型/字段。
> 整理时间：2026-10-03。

## 0. 一句话

> 商人不是随时有，而是**深空天线扫出来的、带寿命的「联系人」**：先**通电 + 对准 + 审问**联系上，再**呼叫**让商人穿梭机落到你的 **Landing Pad**，然后打开界面用 **Credits（信用点）**买他有的、卖他想要的；商人分五类，报价由 seed 随机；完事 `Depart` 送走。

流程：

```
卫星天线搜索/审问 ──> TraderContact(联系人) ──呼叫(CallTrader)──> TraderShuttle 飞抵
        ▲                     (有寿命/角度/功率要求)                     │
        │                                                               ▼
   StrongestContact*                                         LandingPadCenter（_isTraderReady）
                                                                        │
                                                              交互打开 TraderCanvas
                                                              Buy / Sell（Credits 结算）
                                                                        │
                                                                 Depart（送走）
```

---

## 1. 找商人 = 卫星天线搜索「联系人」

交易对象是 **`Assets.Scripts.TraderContact`**（联系人）：

| 字段 | 含义 |
|---|---|
| `ContactName` | 联系人名字 |
| `Angle` | 方位（要用天线对准） |
| `ShuttleType` | 来的是什么级别的穿梭机 |
| `Lifetime` / `InitialLifeTime` | **存活时间**（会过期） |
| `RequiredPadEnvironment` | 对着陆台环境的要求 |
| `WattsToResolve` / `MinimumWattsToContact` / `MinimumWattsToResolve` | 需要多少**功率**才能解析/联系 |
| `SecondsRequiredToContact` | 联系所需**时间** |
| `_interrogatingDish` | 正在审问它的**天线**（挂在卫星天线上） |
| `_contacted` | 是否已联系上 |
| `_currentlyTrading` / `_connectedPad` | 是否正在交易 / 关联的着陆台 |
| `BulkMultiplier` | 批量系数 |
| `HumanTradingSteamID` | 关联玩家 SteamID |

网络消息：`InterrogateTrader`（审问/联系）→ `CallTrader`（呼叫）→ `RequestTrade`（交易请求）。

> **与「chip 1/2」的关系**：那两块 128/127 行的芯片就是**卫星天线控制器**（扫天 + 精调锁定）。
> 交易系统里读「最强联系人」的三个逻辑就是给它用的：
> `TraderInstruction.StrongestContactIdHash` / `StrongestContactMetaData` / `StrongestContactSignalData`。

设备：`StructureSatelliteDish`（中）/ `StructureLargeSatelliteDish`（大）/ `StructureSmallSatelliteDish`（小）；
另有 `StructureGroundBasedTelescope`（望远镜）。

---

## 2. 呼叫 → 穿梭机降落到 Landing Pad

设备（prefab）：

| prefab | 显示名 |
|---|---|
| `StructureShuttleLandingPad` | Landing Pad（着陆台） |
| `ItemKitLandingPad` / `LandingPadBasic` / `LandingPadAtmos` / `LandingPadWaypoint` | 各种着陆台套件 |
| `StructureTraderWaypoint` | Trader Waypoint（商人航点） |

关键类型：

- **`Objects.Electrical.LandingPadCenter`**（着陆台中心）：
  `_trader`、`_shuttle`、`_isTraderReady`、`_currentTradingContact`、`_parentMotherboard`、
  `LandingPadModeStrings`、`_phase`（状态机）、`_virtualWaypointHeight`、`_locked`、`_nextWaypointReferenceId`。
- **`TraderShuttle`**（商人穿梭机，MonoBehaviour）：
  `_shuttleType`、`LandingPadCenter`、`TargetPosition` / `TargetRotation`、`pilotPosition`、爆炸/碰撞参数。
- **`Assets.Scripts.ShuttleType`（枚举）**：`None / Small / SmallGas / Medium / MediumGas / Large / LargeGas / MediumPlane / LargePlane`（0–8）。
  > 更正：`TraderIncoming / TraderLanded`（37/38）**不在** `ShuttleType` 里，它们属于 **`Sound` 事件音效枚举**；`CreditCard`(28) 属于 `SlotClass`。**没有任何设备把 `ShuttleType` 暴露成 IC10 逻辑字段**（见 §7）。

控制台命令 **`Util.Commands.TraderCommand`**：`trader call` / `land` / `depart` / `regenerate`
（手动叫来 / 降落 / 送走 / 重新生成商人）。

---

## 3. 交易界面与 Credits

- **`TraderUI.TraderCanvas`**：交易窗口（`_buyPanel`、`_sellPanel`、`_playerCreditsTextMesh`、`_departButton`、`_debugReadOnly`…）。
- **`TraderUI.TradeData`**：

  | 字段 | 含义 |
  |---|---|
  | `PlayerName` / `TraderName` | 玩家 / 商人名 |
  | `PlayerCredits` | 玩家信用点 |
  | `Buying` / `Selling` | 买入/卖出列表（`TradeItemData[]`） |

- **`TraderUI.TradeItemData`**（每一项货）：

  | 字段 | 含义 |
  |---|---|
  | `ItemName` | 物品名 |
  | `Cost` | 单价 |
  | `NumberAvailable` | 商人**有多少**（你能买多少） |
  | `NumberWanted` | 商人**想要多少**（你能卖多少） |
  | `TooltipText` / `ItemImage` | 说明 / 图标 |

- 结算货币：**Credits**；LED 可用 `DisplayMode.Credits`（值 `6`）显示点数。

---

## 4. 商人数据 / 类型

- **`Trading.TraderData`（模板定义）**：
  `Id`、`ShuttleVariant`、`IdHash`、`SlotTypes`、`Names`、`BuyData`、`SellData`、`_itemPools`；
  方法：`Initialize`、`Instantiate(seed, contactParent, referenceId)`、`Find(idHash)`、`TraderChecksum`。
- **`Trading.TraderDataInstance`（实例）**：
  `TraderData`、`BuyDataInstances`、`SellDataInstances`、`ContactParent`、`Seed`、`DisplayName`；
  → 用 **seed 随机**出这家商人卖什么、买什么、什么价。
- **`Trading.TraderItemPool`**：`Index`、`Transactions`、`Pick(random)`（按池随机抽货）。
- **`Assets.Scripts.TraderType`（枚举）**：`None / Ore / Alloy / Construction / Gas / Hydroponics`
  → 商人分 **矿石 / 合金 / 建材 / 气体 / 水培** 五类。
- **`Assets.Scripts.Objects.Electrical.TraderPilot`**：降落后的商人 NPC（`PilotType`）。
- 事件：`WorldLogSystem.TraderEnteredRangeEvent` / `TraderLeftRangeEvent` / `TraderCrashEvent`（进/出范围、坠毁）。

---

## 5. 自动化接口（IC10）

**`Assets.Scripts.Objects.Electrical.TraderInstruction`（逻辑枚举）**：

| 分组 | 指令 |
|---|---|
| 写数据 | `WriteTraderData`、`WriteTraderBuyData`、`WriteTraderSellData` |
| 读「买」报价 | `TraderBuyThingData`、`TraderBuyThingChildData`、`TraderBuyGasData` |
| 读「卖」报价 | `TraderSellThingData`、`TraderSellGasData`、`TraderSellThingChildData` |
| 联系人（天线） | `StrongestContactIdHash`、`StrongestContactMetaData`、`StrongestContactSignalData` |
| 过滤器 | `FilterPrefabHashEquals`、`FilterPrefabHashNotEquals`、`FilterSortingClassCompare`、`FilterQuantityCompare`、`FilterGasContains`、`FilterGasNotContains` |

配合 `Credits` 显示等，**理论上**可做自动收购/补货。但深挖后发现一个重要限制（见 §7）：

> **这些 `Trader*` 枚举不是 IC10 的 LogicType，也没有任何设备把它们暴露成逻辑字段** → 原版 IC10 **不能直接下单买卖**。能自动化的只有**卫星天线**那一侧（搜/锁/审问联系人）。

---

## 6. 玩法要点速查

- 商人 = **天线扫出的联系人**，有**寿命**，会过期。
- 联系需要：**给天线通电 + 对准 + 审问足够时间**（`WattsToResolve` / `SecondsRequiredToContact`）。
- 呼叫后穿梭机经历 `TraderIncoming(37)` → `TraderLanded(38)`，落好才能交易。
- 报价随机（seed）、商人分五类；你只能**买他有的、卖他想要的**。
- 货币是 **Credits**；交易完 `Depart` 送走。
- 管理员可用 `trader call/land/depart/regenerate` 直接控制。

---

## 7. 深挖结果

### 7.1 `TraderInstruction` 枚举（精确值）

| code | 指令 | code | 指令 |
|---|---|---|---|
| 0 | `None` | 10 | `TraderSellThingData` |
| 1 | `WriteTraderData` | 11 | `TraderSellGasData` |
| 2 | `StrongestContactIdHash` | 12 | `TraderSellThingChildData` |
| 3 | `StrongestContactMetaData` | 13 | `FilterPrefabHashEquals` |
| 4 | `StrongestContactSignalData` | 14 | `FilterPrefabHashNotEquals` |
| 5 | `WriteTraderBuyData` | 15 | `FilterSortingClassCompare` |
| 6 | `WriteTraderSellData` | 16 | `FilterQuantityCompare` |
| 7 | `TraderBuyThingData` | 17 | `FilterGasContains` |
| 8 | `TraderBuyThingChildData` | 18 | `FilterGasNotContains` |
| 9 | `TraderBuyGasData` | | |

**操作数/可用性（结论）**：
- `TraderInstruction` **不是 IC10 的 LogicType**；扫描全部 566 台设备的逻辑表，**没有一台**把 `TraderInstruction`（或 `ShuttleType`）暴露成逻辑字段。
- 它配套 **`CommsMotherboard.MotherboardCommand(command, reference, referenceInt, text)`** 使用（`command`=上表；`reference`=相关 Thing；`referenceInt`=索引/数量；`text`=字符串/源码），即**界面/内部命令通道**。
- **`CommsMotherboard`（通讯主板）不是 `ILogicable`**（无 `CanLogicRead/GetLogicValue`），`Motherboard` 基类也不是。
  → **原版 IC10 做不了"自动下单买卖"**；买卖只能在 **Comms Terminal 界面**里手动操作。
  → IC10 能自动化的是**天线**（找/对准/审问联系人）与读着陆台的 `ContactTypeId`。

### 7.2 逻辑字段表（来自游戏设备数据，含 read/write）

**卫星天线**（`Small/Medium/Large SatelliteDish`，23–24 项逻辑，**7 个可写**）：

| 可写 | `On`、`Activate`、`Setting`、`Horizontal`、`Vertical`、`BestContactFilter` |
|---|---|
| 只读·联系人 | `SignalStrength`、`SignalID`、`ContactTypeId`、`ContactSlotIndex`、`InterrogationProgress`、`MinimumWattsToContact`、`WattsReachingContact`、`Idle` |
| 只读·通用 | `Power`、`RequiredPower`、`Error`、`PrefabHash`、`NameHash`、`ReferenceId` |

**着陆台**：

| 设备 | 可写 | 只读（相关） |
|---|---|---|
| `StructureShuttleLandingPad`（着陆场） | `On`、`Activate` | `Power`、`Error`、`RequiredPower` |
| `Landingpad_DataConnectionPiece`（供电和网络块） | `On`、`Activate`、`Vertical` | **`ContactTypeId`**、`Mode` |
| `Landingpad_*`（气/液/储罐连接件） | `Setting` / `Mode` / `On` | — |
| `StructureVendingMachine`（智能货柜） | `Lock`、`Activate`、`On` | `TargetPrefabHash` |
| `StructureComputer`（电脑） | `Open`、`Lock`、`On` | — |

→ **结论：着陆台不能用 `set/get` 直接下买卖**；它只有电源/开关/`ContactTypeId`。买卖靠终端界面。

### 7.3 审问 / 呼叫的触发条件

**审问联系人（Interrogate）**——功率 + 时长 + 角度：
- `TraderContact`：`MinimumWattsToContact`（最低瓦数）、`SecondsRequiredToContact`（所需秒数）、`WattsToResolve` / `MinimumWattsToResolve`、`Angle`、`RequiredPadEnvironment`、`Lifetime`。
- `SatelliteDish`：`minWattage` / `maxWattage`、`dishFov`（视场角）、`GetWattageOnContact(contact)`、`DegreesToWattageMultiplierPlaceholder(degrees)`（**瓦数随对准偏差衰减**）、`DishContactAngleCheck(contact)`、`ProcessInterrogatingContact`。
- 游戏文案：**“Contact requires {Value}W for {Time}s”**；`InterrogationProgress` 从 0 涨到满；`WattsReachingContact ≥ MinimumWattsToContact` 才有进度。
- 一根天线同时只审问一个联系人：**“Highest wattage dish busy. Please wait, or use a separate Comms Terminal”**。

**呼叫降落（Call / Land）**：
- 前置：**“Build and power a 3x3 trader landing pad”**、**“Connect a Computer and Vending Machine to the Landing Pad”**、**“Connect a Dish and Resolve a trading contact”**。
- 失败原因（字符串）：`ContactTradeNoLandingPad`、`ContactTradePadOff`、`ContactTradePadUnpowered`、`ContactTradeLandingPadToSmall`（需 **3×3**）、`ContactTradeLandingObstructed` / `DepartingObstructed`、`ContactTradeLandingNoThreshold`、`ContactLandingRequestFailStorm`（风暴）、`...FailSatellite`、`...FailVendingMachine`、`ContactTradeRequestFailInUse`、`NotEnoughEnergy`。
- 气体交易还看**着陆台气体储罐容量**（“This volume is used for buying and selling gas to traders.”）。

**来源**：游戏 `Assembly-CSharp.dll`（类型/字段）+ `StreamingAssets/Data` 派生的设备目录（逻辑 r/w）+ `Language/english.xml`（文案）。

---

## 8. 社区实现：`autoTrader`（按类别自动找 / 接触 / 降落商人）

> 位于仓库 `ic10code/stationeers-workspace/src/Trading/`（第三方）。
> **来源**：`.ic10` 是社区原脚本（按 `.gitignore` 只在本地留存、不入库）；`.icg` 是**反编译派生**（`ic10c decompile` 由 `.ic10` 生成）后再**人工整理**的端口——见提交 `4a858a6`（vendor + 反编译 67 个脚本）与 `28f521c`（"hand-rewrite the 13 decompiler-derived ports"，含本目录全部 8 个）。因此代码仍带反编译器特征（`label/goto`、`reserveRegs`/`setIreg`/`ireg`、寄存器式变量名），只是重命名并加了中文注释。
> **校验**：`pkg/ic10/ports_test.go` 会把每个 `.icg` 与其 `.ic10` 原脚本在 VM 里对比「可达设备状态集合」，确保行为等价。
> 本目录两个 `*RAW.icg` 与对应非 RAW 版**正文完全相同**（只有头注释不同）。

### 8.1 文件与角色

| 文件 | 角色 |
|---|---|
| `common/autoTraderLoader` | 把 13 个**交易类别 hash** 压入宿主栈（跨芯片共享内存） |
| `common/autoTraderDisplay` | 选择旋钮 → 类别文字 + “找到商人” LED |
| `common/simpleDish` | 两个旋钮手动控制天线 H/V |
| `common/traderRelease` | 按钮脉冲各**着陆台 `Activate`**（叫商人降落） |
| `autoTrader/autoTrader` | 中天线扫描 + 类别过滤 + 三方定位 + 接触 |
| `autoTrader/traderSolver` | 由 3 组 (H,V,SignalStrength) 解出精确指向 |
| `autoTraderLarge/autoTraderLarge` | 中天线扫描 → 大天线对准并接触（功率 50000） |
| `autoTraderLarge/solverLarge` | 大天线版求解器（寄存器压力大，超 128 行，仅参考） |
| `hangarPressurizer/hangarPressurizer` | 机库循环：待机→（开关）抽真空→充气(≥40kPa)→保压→泄压→待机；控制内/外通风口、防爆门、小中大机库门；按 hash 批量读写气体传感器，压力 kPa→Pa(×1000) 上屏；交易屏读降落台 `Mode==4`(Ready) |
| `hangarPressurizer/hangarPressurizer2` | v2：带联锁——仅当降落台已占用(`Mode==4`)才增压、检测到占用传感器有人时禁止泄压；目标 150kPa |

### 8.2 13 个交易类别 hash（`BestContactFilter` 用；来自 `autoTraderLoader`）

| 类别 | hash | 类别 | hash |
|---|---|---|---|
| 矿石 ore | `-1374574351` | 工具 tools | `1325142661` |
| 合金 alloy | `54412100` | 消耗品 consumables | `-1650376125` |
| 食物 food | `-82964957` | 家电 appliances | `-1590718013` |
| 种子 seed | `-1077922067` | 基因 genetics | `-188927486` |
| 气体 gas | `-470575659` | 稀有 rare | `649254485` |
| 建材 construction | `175935584` | （无过滤） | `BestContactFilter = -1` = 全部 |
| 液体 liquid | `135244511` | | |

### 8.3 社区代码里的关键用法（等于实测）

- `BestContactFilter`：`-1` 不过滤；`<类别 hash>` 只找该类；`SignalID` 锁定当前联系人。
- `Setting`：**扫描用低功率**（1900/2000/3000），**接触用高功率**（中天线 7500；大天线 49999→50000）。
- `Activate` **脉冲 0→1** 才开始**审问/接触**；`InterrogationProgress` 到 `1` 完成。
- `MinimumWattsToContact` 未对准时为 `-1`；对准后才有效，且要求 **≥ ~200**；必须 **`WattsReachingContact ≥ MinimumWattsToContact`**，否则回去重扫。
- `ContactTypeId` 对比所选类别；`SignalID` / `SignalStrength` / `Horizontal` / `Vertical` / `Idle` 照读。
- **叫降落**：脉冲**着陆台的 `Activate`**（`traderRelease`）。

### 8.4 三方定位求解器（`traderSolver` / `solverLarge`）

1. 天线在附近取 **3 组 (H, V, SignalStrength)**；
2. 换算成单位向量 `(-sin v·cos h, cos v, sin v·sin h)`，强度作权 `cos(e·deg)`；
3. 用**克拉默法则**解出指向向量 `(tx,ty,tz)` → 转回 H/V；
4. 中天线版再对垂直角做校正 `V = V − 6.88·sin(V·0.0694)` 并用**牛顿迭代反解**；
5. 算完 `db[0]=H、db[1]=V` 写回，主程序据此把天线精确对准后再 `Activate`。

> 效果：比 “进入/离开取中点”（chip 1/2 的 `avg`）**精确得多**——这才是把商人**稳稳锁住**的做法。

### 8.5 结论（与 §7 一致）

- **IC10 可以**：自动扫描 / 按类别过滤 / 三方定位 / 精确对准 / **审问接触**（天线 `Activate`）/ **叫商人降落**（着陆台 `Activate`）。
- **IC10 不能**：自动**买卖**（`TraderInstruction` 非逻辑字段、`CommsMotherboard` 非 `ILogicable`）——买卖仍须在 **Comms Terminal / 交易界面**手动完成。

> 备注：枚举值与设备逻辑字段表为**静态精确**；§7.3 的瓦数/角度公式（`DegreesToWattageMultiplierPlaceholder`）为**命名推断**，精确曲线需真机实测。§8 的用法来自社区成品脚本，可信度高。
