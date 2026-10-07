# 设备栈指令（Printer / Sorter）

> 有些机器内部有一段 **「栈程序」**：IC 用 `put(设备, 地址, 值)` 写入、`get(设备, 地址)` 读取，
> 机器自己解释执行这段栈。每一条槽是一个整数，**低 8 位是 OP 码**，其余位是参数字段。
> 本文覆盖两类：
>
> - **打印机栈（`PrinterInstruction`，§1–§5）**：Autolathe / Electronics Printer /
>   Hydraulic Pipe Bender / Tool Manufactory / Security Printer / Rocket Manufactory，
>   64 槽（`PrinterStack.Size`）。
> - **分拣器栈（`SorterInstruction`，§6）**：Logic Sorter，32 槽（`SorterStack.Size`）。
>
> （另外 Medium Satellite Dish 用 `TraderInstruction` 的另一套，不在本文。）
>
> 关联：[`spec.md` §8.8](spec.md)（`printer.*` / `sorter.*` 构建器）、[`target-ic10.md`](target-ic10.md)
> （枚举与常量）、`Stationeers_IC10_参考文档.md`（附：内部栈编程）。

---

## 1. 内存布局与约束

| 槽位 | 用途 |
|---|---|
| `0 .. 53` | 普通指令（`ExecuteRecipe`、`Jump*`、`Eject*`、`WaitUntilNextValid`、`DeviceSetLock`、`None`） |
| `54 .. 62` | `MissingRecipeReagent` —— **打印机回填**的「缺失反应物」条目 |
| `63` | `StackPointer`（唯一合法位置） |

- 栈大小 64（`PrinterStack.Size`）。
- 指令 = `低 8 位 OP` + 上方位段的参数。
- 打印机**空闲时循环遍历栈**：只要栈里还留着 `ExecuteRecipe`，它就会一轮一轮重复打印。
  **要停就必须把命令清掉**（写成 `None` 或改写 `StackPointer`）。

---

## 2. 指令一览

| OP | 指令 | 参数位段 | 作用 | 典型用法 |
|---|---|---|---|---|
| 0 | `None` | — | 空操作 | 清槽（停打印） |
| 1 | `StackPointer` | `INDEX` uint16 @8–23 | 把执行指针设到某槽 | **启动/重定位**程序（只写槽 63） |
| 2 | `ExecuteRecipe` | `QUANTITY` byte8 @8–15，`PREFAB_HASH` int32 @16–47 | 立即开始打印；quantity 每产出一件递减，**减到 0 不打印** | 生产 |
| 3 | `WaitUntilNextValid` | — | 等配方有效（反应物齐全）才继续 | 放在 `ExecuteRecipe` 前，**缺料就等**而不是跳过 |
| 4 | `JumpIfNextInvalid` | `STACK_ADDRESS` uint16 @8–23 | **下一条无效**时跳到该槽 | 条件分支（缺料/配方不可用时跳走） |
| 5 | `JumpToAddress` | `STACK_ADDRESS` uint16 @8–23 | 无条件跳到该槽 | 循环 / 跳到子程序 |
| 6 | `DeviceSetLock` | `LOCK_STATE` bool8 @8–15 | 锁 / 解锁打印机 | 打印中防拿取 |
| 7 | `EjectReagent` | `REAGENT_HASH` int32 @8–39 | 排出指定反应物 | 排料 / 清料 |
| 8 | `EjectAllReagents` | — | 排出全部反应物 | 清空 |
| 9 | `MissingRecipeReagent` | `QUANTITY_CEIL` byte8 @8–15，`REAGENT_HASH` **uint32** @16–47 | 打印机**回填**到 54–62，报告缺哪些反应物 | IC 侧 `get(printer, 54..62)` 读取 |

### 语义细节

- **`ExecuteRecipe`**：`QUANTITY=0` 不打印；`PREFAB_HASH` 决定产出的物品。打印机每产出一件把
  quantity 递减；一轮打完就停（除非栈里又被重新指向它）。
- **`WaitUntilNextValid`**：缺反应物时会等在那里；否则 `ExecuteRecipe` 无效只是**被跳过**，打印机
  继续执行下一条。想要「缺料等待」就把它放在 `ExecuteRecipe` 前面。
- **`MissingRecipeReagent`**：由打印机写入槽 **54–62**（当前配方缺的反应物及数量上限）。IC 读
  第一条：`miss := get(printer, PrinterStack.MissingRecipeReagent)`（=54）。字段布局是
  `OP | quantityCeil<<8 | reagentHash<<16`；**hash 以无符号存放**，取用时按需转符号
  （参考文档用 `sra` 去掉 OP/数量、保留符号位；`rmap` 可把反应物 hash 换成对应锭 hash）。
- **`StackPointer`**：写入后从指定槽开始执行，常用于「重装程序并从头跑」。

---

## 3. `.icg` 构建器

不需要手拼位段，用 `printer.*`（与 `sorter.*` 同族命名空间；设备仍用 `dN`/`db` 或别名）：

```go
put(p, 0, printer.none())                                    // 0
put(p, 63, printer.stackPointer(0))                          // 1        （仅槽 63）
put(p, 1, printer.executeRecipe(50, hash("ItemCableCoil")))  // qty<<8 | hash<<16 | 2
put(p, 2, printer.waitUntilNextValid())                      // 3
put(p, 3, printer.jumpIfNextInvalid(0))                      // addr<<8 | 4
put(p, 4, printer.jumpToAddress(10))                         // addr<<8 | 5
put(p, 5, printer.deviceSetLock(1))                          // lock<<8 | 6
put(p, 6, printer.ejectReagent(hash("Iron")))                // hash<<8 | 7
put(p, 7, printer.ejectAllReagents())                        // 8
put(p, 8, printer.missingRecipeReagent(2, hash("Iron")))     // ceil<<8 | hash<<16 | 9
```

- 参数可以是**运行期值**（生成 `sll`/`or`）；**常量**参数会做**位宽校验**（如
  `executeRecipe` 的 quantity 只有 8 位，超出会报错而不是串到 hash 字段）。
- 常量：`PrinterStack.Size`(64) / `PrinterStack.StackPointer`(63) / `PrinterStack.MissingRecipeReagent`(54)。

---

## 4. 三种常见用法

### 4.1 最小生产程序（缺料等待）
```go
put(p, 0, printer.waitUntilNextValid())            // 缺料就等
put(p, 1, printer.executeRecipe(50, hash("ItemCableCoil")))
put(p, PrinterStack.StackPointer, printer.stackPointer(0))  // 从槽 0 开始
```
放到打印机栈后，它自己循环执行：有料就打印 50 个，缺料就停在 `WaitUntilNextValid`。

### 4.2 缺料分支（JumpIfNextInvalid）
```go
put(p, 0, printer.jumpIfNextInvalid(5))    // 下一条 ExecuteRecipe 无效 → 跳到槽 5
put(p, 1, printer.executeRecipe(50, item))
put(p, 5, printer.ejectAllReagents())      // 缺料时排空，避免堵料
put(p, PrinterStack.StackPointer, printer.stackPointer(0))
```

### 4.3 由 IC 触发、可控停（推荐）
打印机空闲会循环遍历栈，所以「打印由用户触发、到量/松手停」要由 IC 管：
```go
// 用户把打印机的 Activate 拉起来才装程序
if readById(pa, LogicType.Activate) != 0 {
    if get(pa, 0) == 0 {                       // 栈槽 0 为 0 = 还没装
        put(pa, 0, printer.executeRecipe(QTY, readById(pa, LogicType.RecipeHash)))
        put(pa, PrinterStack.StackPointer, printer.stackPointer(0))
        db.stack[base] = readById(pa, LogicType.ExportCount)   // 记录本轮基线
    } else if readById(pa, LogicType.ExportCount) - db.stack[base] >= readById(st, LogicType.Setting) {
        writeById(pa, LogicType.Activate, 0)   // 到量：停机
    }
} else if get(pa, 0) != 0 {                     // 触发结束/到量后：清栈
    put(pa, 0, printer.none())
    put(pa, PrinterStack.StackPointer, printer.stackPointer(0))
}
```
要点：
- **清槽 0 为 `none()` + 重设 `StackPointer`** 就能停打印（不必清全部槽）。
- 用**栈槽 0 本身**（`get(pa,0) != 0`）表示「已装程序」，不用额外的状态数组。
- 缺料回读：`miss := get(pa, 54)`，非 0 时 `printer.ejectReagent(miss >> 16)`。

完整示例：`examples/` 里的打印机控制器（表驱动多机、按名字 A/B 区分）。

---

## 5. 坑与注意

| 现象 | 原因 | 处理 |
|---|---|---|
| 打印机停不下来 / 一直重打 | 空闲会**循环遍历栈**，`ExecuteRecipe` 还在 | 清槽（`none()` + 重设 `StackPointer`），或让 IC 在到量/松手时清 |
| 「按 `Activate` 就打印」但 IC 的逻辑没生效 | 打印机在 `Activate` 下会打**它自己的配方**；IC 只是读状态 | 确认 IC 真的解析到了设备（`get`/`readName` 的名字要对上） |
| 到量不停 / 手动停无效 | **堆垛机/打印机名字没匹配上**（多机必须 A/B 区分） | 打印机按 `prefab + 名字`、堆垛机按名字匹配；确认名字与厂里一致 |
| `MissingRecipeReagent` 的 hash 是负的/对不上 | 该字段按**无符号**存 | 用 `sra`/自行转符号后再比较/`rmap` |
| `ExecuteRecipe` 没反应 | 反应物不足 → 指令无效被跳过 | 前面加 `WaitUntilNextValid`，或读 54–62 处理缺料 |

> 真机上验证过：`executeRecipe` + `stackPointer` 写进栈后打印机会自己开印（`ExportCount` 涨、
> `Reagents` 降）；清栈后停止；`get(54)` 能读到缺失反应物。多机时**名字必须 A/B 区分**，
> 否则解析不到对应设备。

---

## 6. 分拣器栈（Logic Sorter）

Logic Sorter 内部有 **32 槽（`SorterStack.Size`）**，每槽一条**过滤条件**。物品经过时，
分拣器把它和栈里所有条件比对，再按 `Mode` 决定怎么分流。同样是「整数指令、低 8 位 OP」。

### 6.1 指令一览

| OP | 指令 | 参数位段 | 作用 |
|---|---|---|---|
| 0 | `None` | — | 空操作（清槽） |
| 1 | `FilterPrefabHashEquals` | `PREFAB_HASH` int32 @8–39 | 物品预制体 hash **相等**才匹配 |
| 2 | `FilterPrefabHashNotEquals` | `PREFAB_HASH` int32 @8–39 | 预制体 hash **不等**才匹配 |
| 3 | `FilterSortingClassCompare` | `OP`(条件) byte8 @8–15，`CLASS` uint16 @16–31 | 按**分类**（`SortingClass`）比较 |
| 4 | `FilterSlotTypeCompare` | `OP`(条件) byte8 @8–15，`CLASS` uint16 @16–31 | 按**槽类型**（`SlotClass`）比较 |
| 5 | `FilterQuantityCompare` | `OP`(条件) byte8 @8–15，`QUANTITY` uint16 @16–31 | 按**数量**比较 |
| 6 | `LimitNextExecutionByCount` | `COUNT` int32 @8–39 | 限制下一条执行**次数**（用一定次数后失效） |

**条件运算 `OP`**（`ConditionOperation`）：`Equals`(0) / `Greater`(1) / `Less`(2) / `NotEquals`(3)。

### 6.2 Mode（配合栈生效）

分拣器的 `Mode`（`LogicType.Mode`）决定栈里多个条件怎么组合：

| Mode | 含义 |
|---|---|
| `All`(0，默认) | **所有**条件都匹配才通过 |
| `Any`(1) | **至少一个**匹配就通过 |
| `None`(2) | 条件**全不匹配**才通过 |

> 分类多种物品时靠切换 `Mode` 来表达「这组条件」。例如 `FilterPrefabHashEquals` 想反转输出侧，
> 可在 `Any`(1) 与 `None`(2) 之间切换。

### 6.3 `.icg` 构建器

```go
put(s, 0, sorter.filterPrefabHash(hash("ItemIronOre")))             // hash<<8 | 1
put(s, 1, sorter.filterPrefabHashNotEquals(hash("ItemGold")))       // hash<<8 | 2
put(s, 2, sorter.filterSortingClass(Equals, SortingClass.Ores))     // class<<16 | op<<8 | 3
put(s, 3, sorter.filterSlotType(Greater, SlotClass.Battery))        // class<<16 | op<<8 | 4
put(s, 4, sorter.filterQuantity(Less, 10))                          // qty<<16   | op<<8 | 5
put(s, 5, sorter.limitNextExecutionByCount(5))                      // count<<8  | 6
```

- 条件常量取 `Equals`/`Greater`/`Less`/`NotEquals`（0/1/2/3）；分类/槽位取
  `SortingClass.*`（0–10）/ `SlotClass.*`（0–43）。
- 常量参数同样有**位宽校验**（`filterQuantity` 的数量 16 位等）。

### 6.4 例：只分拣铁矿石

```go
const S = d0  // Logic Sorter
func main() {
    for {
        yield()
        put(S, 0, sorter.filterPrefabHash(hash("ItemIronOre")))
        writeById(S, LogicType.Mode, 0)   // All：只有铁矿石通过
    }
}
```

### 6.5 注意

- 分拣器**没有 `StackPointer`/`ExecuteRecipe`** 这类“执行”指令，栈就是一组**过滤条件**，
  物品经过时逐条比对；要改分类规则就重写相应槽并设 `Mode`。
- `LimitNextExecutionByCount` 用来让某个条件**只生效若干次**（数量/批次限制）。
- 占满 32 槽前记得清没用的槽（写 `None`），否则旧条件继续参与比对。
