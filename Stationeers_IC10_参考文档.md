# Stationeers IC10 编程参考文档

> 本文档整合自 Stationeers Community Wiki 的 **IC10** 与 **IC10/instructions** 页面。
> IC10 是 Stationeers 中的内置脚本语言，其语法灵感来自 MIPS 架构。程序在 **IC10 芯片**上运行，芯片通过电子打印机（Electronics Printer）制作。

---

## 目录

1. [寄存器概述](#寄存器概述)
2. [设备 IO（读写设备寄存器）](#设备-io读写设备寄存器)
3. [批量 IO](#批量-io)
4. [特殊寄存器](#特殊寄存器ra与sp)
5. [栈内存](#栈内存)
6. [栈遍历](#栈遍历)
7. [注释](#注释)
8. [设备端口](#设备端口)
9. [标签](#标签)
10. [常量](#常量)
11. [数值](#数值)
12. [间接引用](#间接引用)
13. [动态切换 LogicType](#动态切换-logictype)
14. [网络引用 / 通道](#网络引用--通道)
15. [逻辑门 / 位运算 / 逻辑运算](#逻辑门--位运算--逻辑运算)
16. [调试技巧](#调试技巧)
17. [通过批量或 ReferenceId 访问设备](#通过批量或-referenceid-访问设备)
18. [指令大全](#指令大全)
19. [设备变量](#设备变量)
20. [槽位变量](#槽位变量)
21. [编写脚本的方法论](#编写脚本的方法论)
22. [示例脚本](#示例脚本)

---

## 寄存器概述

IC10 有两类寄存器：

### 内部寄存器 `r?`

- IC 内含 **16 个 CPU 寄存器**，编号 `r0` ~ `r15`，统称为 `r?`。
- 所有计算都在 `r?` 寄存器之间进行，可理解为编程中的"变量"。
- 可用 `alias` 给寄存器起一个方便的名字（见后文）。

### 设备寄存器 `d?`（logicType）

- 设备寄存器用于 IC 与外部设备之间的读写。
- 编号 `d0` ~ `d5`（通过螺丝刀切换），或 `db`（IC 所插入的设备）。统称为 `d?`。
- `d?` 的具体含义由 **logicType**（逻辑类型，即设备属性名，如 `Temperature`、`On`）决定。

> ⚠️ IC 本身并不关心 `d?` 到底接在了哪个设备上。若出现 `logicType` 错误，请检查 `d?` 编号，或确认螺丝已正确固定在插槽上。`alias` 只是方便阅读，并不会真正设置螺丝。

### 寄存器操作方式

**写常量值**
```
move r0 2      # 将 r0 设置为 2
```

**计算**（运算均在寄存器之间进行）
```
add r1 r0 3    # 将 r0 加 3，结果写入 r1
```

---

## 设备 IO（读写设备寄存器）

`d?` 表示设备，`?` 为插槽上对应的螺丝设备选择编号。也可读写 IC 所插入的设备 `db`。

### 从设备读取（load）`l`

```
l r0 d0 Temperature
```
将设备 `d0` 的 `Temperature`（温度）逻辑值读入寄存器 `r0`。可用别名：
```
alias Sensor d0
l r0 Sensor Temperature
```

### 写入设备（set）`s`
```
s d0 On r0
```
将寄存器 `r0` 的值写回设备 `d0` 的 `On` 属性。若 `r0 == 1` 则开启，`r0 == 0` 则关闭。

### 通过 `db` 读写 IC 自身插入的设备
```
s db Setting r0     # 把 r0 写入 IC 宿主（Housing）的 Setting 参数
```

---

## 批量 IO

批量指令针对某一类**所有**设备（通过 `deviceHash` 而非 `d?` 寻址）。`deviceHash` 与设备类型一一对应，可在 Stationpedia 中查得。

### 基础批量指令
```
lb r? deviceHash logicType batchMode      # 批量读取
sb deviceHash logicType r?                # 批量写入
```

### 按名称过滤的批量指令
```
lbn r? deviceHash nameHash logicType batchMode   # 批量读取（按名称）
sbn deviceHash nameHash logicType r?             # 批量写入（按名称）
```

> 所有 hash 均为字符串的 **CRC-32 校验和**。配合 `HASH()` 函数使用更佳：
> ```
> lbn r0 HASH("StructureGasSensor") HASH("Sensor 1") Temperature Average
> # 读取网络上名为 "Sensor 1" 的所有气体传感器的平均温度到 r0
> ```

### batchMode（批量模式）

| 值 | 名称 | 说明 |
|----|------|------|
| `0` | Average | 平均值 |
| `1` | Sum | 求和 |
| `2` | Minimum | 最小值 |
| `3` | Maximum | 最大值 |

> 可用名称或数字。

### 空网络的批量读取结果

| 批量模式 | 结果 |
|----------|------|
| Average (0) | `nan` |
| Sum (1) | `0` |
| Minimum (2) | `0`（旧版本为 `inf`） |
| Maximum (3) | `ninf` |

> `pinf` / `ninf` 为 IC10 的"正/负无穷"常量。

---

## 特殊寄存器（ra 与 sp）

| 寄存器 | 含义 |
|--------|------|
| `ra` | 返回地址寄存器，由带 `-al` 后缀的跳转/分支指令用来记录应返回的代码行号 |
| `sp` | 栈指针，跟踪栈（最多 512 个值）中下一个待压入/弹出的索引 |

两者均**可被任意指令修改**，并非受保护寄存器。

---

## 栈内存

栈最多可容纳 **512** 个值。每个 IC10 拥有自己的栈，部分设备（如逻辑分类器 Logic Sorter）也有栈。

```
push r?          # 将 r? 的值压入栈，sp +1
pop r?           # 取出栈顶（sp-1）写入 r?，sp -1
peek r?          # 读取栈顶（sp-1）但不移动 sp
poke address(r?|num) value(r?|num)   # 在指定地址写入值
get r? d? address(r?|num)            # 从指定设备的指定地址读取
getd r? id(r?|num) address(r?|num)   # 按设备 id 读取
put d? address(r?|num) value(r?|num) # 向指定设备指定地址写入
putd id(r?|num) address(r?|num) value(r?|num)  # 按设备 id 写入
```

> - 读取（`peek`/`pop`）时 `sp` 须在 **1 ~ 512**；写入（`push`）时 `sp` 须在 **0 ~ 511**。
> - 栈内存在逻辑芯片上是**持久**的：即使推送值的代码被移除，栈仍保留那些值。但值不会随代码复制到其它芯片，需要各自单独编程。

### 栈遍历

**方式一：用 `peek` 递增遍历**
```
move sp {min}
loop:
add sp sp 1
peek r0
# 对 r0 中的栈值进行操作
blt sp {max} loop
```

**方式二：用 `pop` 递减遍历（更高效）**
```
move sp {max}
add sp sp 1
loop:
pop r0
# 对 r0 中的栈值进行操作
bgt sp {min} loop
```

---

## 注释

使用 `#` 符号。`#` 之后的整行内容都会被游戏忽略。
```
alias MyAlias r0          # 行尾注释
# 独立成行的注释
```

---

## 设备端口

IC 可通过 `d0` ~ `d5` 与最多 6 个设备交互，以及通过 `db` 与所插入的设备交互。用螺丝刀在 IC 插槽中设置设备。

- 可读取或设置设备的任意属性（如读取房间压力、氧含量）。
- 也可将**其它 IC 插槽**设为设备，实现跨 IC 联动。例如气体混合 IC 可读取"大气传感器 IC"的 `Setting` 字段并据此行动。

```
# 从 d0 读取大气传感器的 Temperature 到 r0
l r0 d0 Temperature
# 将 r0 写入 d1 设备的 Setting
s d1 Setting r0
```

---

## 标签

标签用于在代码行间跳转，其数值等于所在行号。虽然可用于计算，但不建议这么做——因为改动代码会改变行号。
```
main:           # 定义标签 'main'
j main          # 跳回 main
```

> ⚠️ 标签命名要唯一。若标签名与 IC10 关键字（如 `Temperature:`、`Setting:`）相同，会覆盖该关键字原意，导致指令报错。

---

## 常量

用 `define` 定义常量，可节省寄存器空间。常量在整个程序中会被替换为对应值。
```
define pi 3.14159
move r0 pi      # 将常量 pi 的值写入 r0
```

---

## 数值

- 寄存器与常量通常为**双精度浮点数（double）**。
- 整数不是独立类型，double 可精确表示约 54 位以内的整数。
- 十六进制：数字前加 `$`。超过 54 位可能损坏。常用于 ReferenceId。
- 二进制：数字前加 `%`，`_` 仅用于可读性会被忽略。

```
move r0 12345
move r1 123.456
move r2 $E1B2
```

---

## 间接引用

用另一个寄存器作为指针来访问寄存器。在寄存器名前加一个 `r` 即启用间接引用。指针值须在 **0 ~ 15**（对应 r0 ~ r15），否则报错。
```
move r0 5        # r0 = 5
move rr0 10      # 等价于 move r5 10（r0=5 指向 r5）
```

可多次叠加间接引用：
```
move r1 2        # r1 = 2
move r2 3        # r2 = 3
move rrr1 4      # 等价于 move r3 4（r1→r2→r3）
```

也可用于设备：
```
move r0 2        # r0 = 2
s dr0 On 1       # 等价于 s d2 On 1
```

---

## 动态切换 LogicType

`l` / `s` 读写设备寄存器时，logicType 是要交互的属性（如 `l r0 myDevice Temperature` 中的 `Temperature`）。通常硬编码，但也可动态切换。logicType 是枚举，每个类型有唯一整数值，因此可循环切换：
```
# 循环遍历一组 LogicType
push LogicType.RatioOxygen
push LogicType.RatioVolatiles
push LogicType.Temperature
loop:
pop r1
l r0 myDevice r1
bgtz sp loop     # 栈空则结束循环
```

---

## 网络引用 / 通道

所有电缆网络都有 **8 个通道（Channel0 ~ Channel7）**，可通过设备与连接引用读写。

> 通道是**易失**的：电缆网络结构改变、移除、新增，或退出世界时数据都会被清除。默认值为 `NaN`（"安静 NaN"，非错误，仅表示"尚未有数值"）。建议用于网络间的数据交换，而非长期存储。

> 一个 IC 可读取其能引用的所有网络——不仅限于自身本地网络，还包括任何被引用设备所连的网络。

```
# d0 是设备 0，:0 表示该设备第 0 个连接
l r0 d0:0 Channel0

# 在 IC 插槽上，0 连接是数据口、1 是电源口
# 可将 r0 写入该 Housing 电源网络的 Channel0
s db:1 Channel0 r0
```

循环读取全部 8 个通道并写入 r0 ~ r7：
```
move r15 LogicType.Channel0   # logicType 整数值
move r14 0                    # 间接引用指针
loop:
l rr14 db:0 r15
add r15 r15 1
add r14 r14 1
ble r15 LogicType.Channel7 loop
```

---

## 逻辑门 / 位运算 / 逻辑运算

MIPS 中的所有逻辑门都是**位运算（bitwise）**。可用：`NOT`、`AND`、`OR`、`XOR`、`NOR`（缺少 XNOR 和 NAND）。

位运算中每一位单独匹配，包括符号位。

### 位运算示例
```
not r0 0
# 0 = %00000000_..._0000
# 翻转所有位
# 全 1 = -1
# r0 = -1

and r0 3 6
# 3 = %011, 6 = %110
# 只有 "2" 位匹配
# r0 = %010 = 2
```

### 逻辑运算（非完美替代）
> 逻辑运算对负数处理不同，且可能产生非二元输出。设备需要二元值时：`>= 1` 视为 `1`，`< 1` 视为 `0`。
```
# r0 = 结果, r1 = 输入A, r2 = 输入B
Logical NOT = seqz r0 r1
Logical AND = min r0 r1 r2
Logical OR  = max r0 r1 r2
Logical XOR = sne r0 r1 r2      # 仅适用于二元输入
Logical NAND = not and
Logical NOR = not or
Logical XNOR = not xor
```

---

## 调试技巧

### 显示寄存器值
写入 IC 宿主的 `Setting` 参数即可显示（除非宿主是空调）。靠近并直视宿主即可查看。
```
s db Setting r0      # 将 r0 写入 IC 宿主的 Setting
```

### 用数字标记代码块是否执行
```
s db Setting 97      # 在宿主上显示数字 97
```

### 模拟断点（breakpoint）
```
... some code...
jal debug
... some more code...
jal debug
... rest of the code....
debug:
  s db Setting ra     # 打印存储的行号
  s db On 0           # 停止执行，手动开启宿主以继续
  j ra                # 恢复正常执行
```

### 配置卡
插入平板的配置卡可查看所注视设备的所有可用值与配置参数。

---

## 通过批量或 ReferenceId 访问设备

6 个配置引脚可灵活选择由 IC 控制的设备。

### 批量指令 `lb` / `sb`
```
# 获取所有电池的平均电荷比
lb r0 HASH("StructureBattery") Ratio Average

# 获取名为 "Sorter Corn" 的分类器的 ReferenceId
lbn r1 HASH("StructureLogicSorter") HASH("Sorter Corn") ReferenceId Maximum
ble r1 ninf ra
# 用 ReferenceId 设置该分类器模式
sd r1 Mode 1
```

> - 配置引脚：便于编写可复用脚本，由安装者用引脚选择设备。
> - 批量按名称：免去调整引脚的麻烦，且可控制超过 6 个设备（需通过贴标机命名）。

---

## 指令大全

> 以下按类别整理自 IC10/instructions 页面。`r?` 表示寄存器，`num` 表示数值，`d?` 表示设备，`id` 表示设备 id。

### 工具（Utility）

| 指令 | 说明 |
|------|------|
| `alias str r?\|d?` | 给寄存器/设备起别名；设备别名还会影响 IC 底座螺丝显示 |
| `define str num` | 定义常量，全文替换为对应值 |
| `move r? a(r?\|num)` | 将数值或寄存器值写入寄存器 |
| `yield` | 暂停 1 个 tick |
| `sleep a(r?\|num)` | 在 IC 上暂停 `a` 秒 |
| `hcf` | 暂停并起火（Halt and catch fire），炸毁芯片，在可燃气体中引发火灾 |

### 数学（Mathematical）

| 指令 | 说明 |
|------|------|
| `abs r? a` | 取 `a` 的绝对值 |
| `sgn r a` | 存 `a` 的符号：负为 -1，正为 1，0 或 NaN 为 0 |
| `add r? a b` | `a + b` |
| `ceil r? a` | 大于 `a` 的最小整数 |
| `div r? a b` | `a / b` |
| `pow r? a b` | `a` 的 `b` 次幂（遵循 IEEE-754） |
| `exp r? a` | `e^a` |
| `floor r? a` | 小于 `a` 的最大整数 |
| `log r? a` | 自然对数 `ln(a)` |
| `max r? a b` | `a`、`b` 较大值（任一为 NaN 则返回 NaN） |
| `min r? a b` | `a`、`b` 较小值（任一为 NaN 则返回 NaN） |
| `clamp r? a min max` | 将 `a` 限制在 `[min, max]`（任一为 NaN 返回 NaN） |
| `mod r? a b` | `a mod b`（注意不是 `a % b`，结果符号跟随 `b`） |
| `mul r? a b` | `a * b` |
| `rand r?` | 返回 `[0, 1)` 的随机值 |
| `round r? a` | 四舍五入到最近整数 |
| `sqrt r? a` | `a` 的平方根 |
| `sub r? a b` | `a - b` |
| `trunc r? a` | 去掉小数部分 |
| `lerp r? a b c` | 在 `a`、`b` 间按比例 `c` 线性插值（`c` 钳制到 0~1） |

### 数学 / 三角（Trigonometric）

| 指令 | 说明 |
|------|------|
| `acos r? a` | 反余弦（弧度） |
| `asin r? a` | 反正弦（弧度） |
| `atan r? a` | 反正切（弧度） |
| `atan2 r? a b` | 由 `a/b` 求角（弧度） |
| `cos r? a` | 余弦 |
| `sin r? a` | 正弦 |
| `tan r? a` | 正切 |

### 栈（Stack）

| 指令 | 说明 |
|------|------|
| `clr d?` | 清空指定设备的栈 |
| `clrd id(r?\|num)` | 按设备 id 清空栈 |
| `get r? device(r?\|id) address` | 从指定设备指定地址读取 |
| `getd r? id address` | 按设备 id 读取栈 |
| `peek r?` | 取栈顶但不移动 sp |
| `poke address value` | 在指定地址写入值 |
| `pop r?` | 取栈顶并 sp-1 |
| `push a(r?\|num)` | 压入值并 sp+1 |
| `put device address value` | 向指定设备指定地址写入 |
| `putd id address value` | 按设备 id 写入栈 |

### 槽位 / 逻辑（Slot / Logic）

| 指令 | 说明 |
|------|------|
| `l r? device logicType` | 从设备读取逻辑值到寄存器 |
| `lr r? device reagentMode int` | 读取设备 reagent（Contents/Required/Recipe = 0/1/2） |
| `ls r? device slotIndex logicSlotType` | 从设备槽位读取逻辑值 |
| `s device logicType r?` | 将寄存器值写入设备逻辑属性 |
| `ss device slotIndex logicSlotType r?` | 将寄存器值写入设备槽位逻辑属性 |
| `rmap r? d? reagentHash` | 将 reagent hash 映射为设备所需的 prefab hash |

### 槽位 / 逻辑 / 批量（Slot / Logic / Batched）

| 指令 | 说明 |
|------|------|
| `lb r? deviceHash logicType batchMode` | 批量读取（Average/Sum/Minimum/Maximum） |
| `lbn r? deviceHash nameHash logicType batchMode` | 按名称批量读取 |
| `lbns r? deviceHash nameHash slotIndex logicSlotType batchMode` | 按名称批量读取槽位 |
| `lbs r? deviceHash slotIndex logicSlotType batchMode` | 批量读取槽位 |
| `sb deviceHash logicType r?` | 批量写入 |
| `sbn deviceHash nameHash logicType r?` | 按名称批量写入 |
| `sbs deviceHash slotIndex logicSlotType r?` | 批量写入槽位 |

### 位运算（Bitwise）

| 指令 | 说明 |
|------|------|
| `and r? a b` | 位与 |
| `nor r? a b` | 位或非（NOR） |
| `not r? a` | 位非（1→0，0→-2 等，注意是位运算） |
| `or r? a b` | 位或 |
| `sla r? a b` | 算术左移 |
| `sll r? a b` | 逻辑左移 |
| `sra r? a b` | 算术右移（保留符号位） |
| `srl r? a b` | 逻辑右移 |
| `rol r? a b` | 左旋 |
| `ror r? a b` | 右旋 |
| `xor r? a b` | 位异或 |
| `ext r? source offset length` | 从 source 提取位域（最终长度 ≤ 53 位） |
| `ins r? field offset length` | 将位域插入寄存器（最终长度 ≤ 53 位） |

> `ins` 顺序为 `field offset length`；注意稳定版（截至 2026-01-12）存在参数顺序 bug（实际为 offset-length-field），beta 版正确。

### 比较（Comparison）

| 指令 | 说明 |
|------|------|
| `select r? a b c` | `a` 非零取 `b`，否则取 `c`（可作三元运算符） |

### 比较 / 设备引脚（Comparison / Device Pin）

| 指令 | 说明 |
|------|------|
| `sdns r? device` | 设备未设置返回 1，否则 0 |
| `sdse r? device` | 设备已设置返回 1，否则 0 |

### 比较 / 数值（Comparison / Value）

| 指令 | 说明 |
|------|------|
| `sap r? a b c` | `abs(a-b) <= max(c*max(\|a\|,\|b\|), ε*8)` 为 1（近似相等，等价 Python `math.isclose`） |
| `sapz r? a b` | `abs(a) <= max(b*abs(a), ε*8)` 为 1（近似为 0） |
| `seq r? a b` | `a == b` 为 1 |
| `seqz r? a` | `a == 0` 为 1 |
| `sge r? a b` | `a >= b` 为 1 |
| `sgez r? a` | `a >= 0` 为 1 |
| `sgt r? a b` | `a > b` 为 1 |
| `sgtz r? a` | `a > 0` 为 1 |
| `sle r? a b` | `a <= b` 为 1 |
| `slez r? a` | `a <= 0` 为 1 |
| `slt r? a b` | `a < b` 为 1 |
| `sltz r? a` | `a < 0` 为 1 |
| `sna r? a b c` | `abs(a-b) > max(c*max(\|a\|,\|b\|), ε*8)` 为 1（近似不等） |
| `snan r? a` | `a` 为 NaN 为 1 |
| `snanz r? a` | `a` 非 NaN 为 1 |
| `snaz r? a b` | `abs(a) > max(b*abs(a), ε)` 为 1（近似不为 0） |
| `sne r? a b` | `a != b` 为 1 |
| `snez r? a` | `a != 0` 为 1 |

### 跳转（Branching）

| 指令 | 说明 |
|------|------|
| `j int` | 无条件跳转到行 `a`（或标签） |
| `jal int` | 跳转到行 `a` 并把下一行号存入 `ra`（用于函数调用） |
| `jr int` | 相对跳转 |

### 跳转 / 设备引脚（Branching / Device Pin）

| 指令 | 说明 |
|------|------|
| `bdnvl device logicType a` | 设备对该 logicType 的 load 无效时跳到 `a` |
| `bdnvs device logicType a` | 设备对该 logicType 的 store 无效时跳到 `a` |
| `bdns d? a` | 设备 `d` 未设置时跳到 `a` |
| `bdnsal d? a` | 同上并存 ra |
| `bdse d? a` | 设备 `d` 已设置时跳到 `a` |
| `bdseal d? a` | 同上并存 ra |
| `brdns d? a` | 相对：设备未设置跳到 `a` |
| `brdse d? a` | 相对：设备已设置跳到 `a` |

### 跳转 / 比较（Branching / Comparison）

> `-al` 后缀表示跳转时把下一行号存入 `ra`；`br-` 表示相对跳转（目标为最后一个参数）。

| 指令 | 条件 |
|------|------|
| `bap a b c d` | `a ≈ b`（近似）跳到 `d` |
| `brap a b c d` | 相对：`a ≈ b` 跳到 `d` |
| `bapal a b c d` | `a ≈ b` 跳到 `d` 并存 ra |
| `bapz a b c` | `a ≈ 0` 跳到 `c` |
| `brapz a b c` | 相对：`a ≈ 0` 跳到 `c` |
| `bapzal a b c` | `a ≈ 0` 跳到 `c` 并存 ra |
| `beq a b c` | `a == b` 跳到 `c` |
| `breq a b c` | 相对：`a == b` 跳到 `c` |
| `beqal a b c` | `a == b` 跳到 `c` 并存 ra |
| `beqz a b` | `a == 0` 跳到 `b` |
| `breqz a b` | 相对：`a == 0` 跳到 `b` |
| `beqzal a b` | `a == 0` 跳到 `b` 并存 ra |
| `bge a b c` | `a >= b` 跳到 `c` |
| `brge a b c` | 相对：`a >= b` 跳到 `c` |
| `bgeal a b c` | `a >= b` 跳到 `c` 并存 ra |
| `bgez a b` | `a >= 0` 跳到 `b` |
| `brgez a b` | 相对：`a >= 0` 跳到 `b` |
| `bgezal a b` | `a >= 0` 跳到 `b` 并存 ra |
| `bgt a b c` | `a > b` 跳到 `c` |
| `brgt a b c` | 相对：`a > b` 跳到 `c` |
| `bgtal a b c` | `a > b` 跳到 `c` 并存 ra |
| `bgtz a b` | `a > 0` 跳到 `b` |
| `brgtz a b` | 相对：`a > 0` 跳到 `b` |
| `bgtzal a b` | `a > 0` 跳到 `b` 并存 ra |
| `ble a b c` | `a <= b` 跳到 `c` |
| `brle a b c` | 相对：`a <= b` 跳到 `c` |
| `bleal a b c` | `a <= b` 跳到 `c` 并存 ra |
| `blez a b` | `a <= 0` 跳到 `b` |
| `brlez a b` | 相对：`a <= 0` 跳到 `b` |
| `blezal a b` | `a <= 0` 跳到 `b` 并存 ra |
| `blt a b c` | `a < b` 跳到 `c` |
| `brlt a b c` | 相对：`a < b` 跳到 `c` |
| `bltal a b c` | `a < b` 跳到 `c` 并存 ra |
| `bltz a b` | `a < 0` 跳到 `b` |
| `brltz a b` | 相对：`a < 0` 跳到 `b` |
| `bltzal a b` | `a < 0` 跳到 `b` 并存 ra |
| `bna a b c d` | `a ≈ b` 不成立跳到 `d` |
| `brna a b c d` | 相对：`a ≈ b` 不成立跳到 `d` |
| `bnaal a b c d` | `a ≈ b` 不成立跳到 `d` 并存 ra |
| `bnan a b` | `a` 为 NaN 跳到 `b` |
| `brnan a b` | 相对：`a` 为 NaN 跳到 `b` |
| `bnaz a b c` | `a ≈ 0` 不成立跳到 `c` |
| `brnaz a b c` | 相对：`a ≈ 0` 不成立跳到 `c` |
| `bnazal a b c` | `a ≈ 0` 不成立跳到 `c` 并存 ra |
| `bne a b c` | `a != b` 跳到 `c` |
| `brne a b c` | 相对：`a != b` 跳到 `c` |
| `bneal a b c` | `a != b` 跳到 `c` 并存 ra |
| `bnez a b` | `a != 0` 跳到 `b` |
| `brnez a b` | 相对：`a != 0` 跳到 `b` |
| `bnezal a b` | `a != 0` 跳到 `b` 并存 ra |

### 分支指令速查表

| 后缀 | 含义 | 无条件 | 分支 | 分支存 ra | 相对分支 | 设置寄存器 |
|------|------|--------|------|-----------|----------|-----------|
| — | 无条件 | `j` | `jal` | `jr` | — | — |
| `-eq` | `a == b` | `beq` | `beqal` | `breq` | `seq` |
| `-eqz` | `a == 0` | `beqz` | `beqzal` | `breqz` | `seqz` |
| `-ge` | `a >= b` | `bge` | `bgeal` | `brge` | `sge` |
| `-gez` | `a >= 0` | `bgez` | `bgezal` | `brgez` | `sgez` |
| `-gt` | `a > b` | `bgt` | `bgtal` | `brgt` | `sgt` |
| `-gtz` | `a > 0` | `bgtz` | `bgtzal` | `brgtz` | `sgtz` |
| `-le` | `a <= b` | `ble` | `bleal` | `brle` | `sle` |
| `-lez` | `a <= 0` | `blez` | `blezal` | `brlez` | `slez` |
| `-lt` | `a < b` | `blt` | `bltal` | `brlt` | `slt` |
| `-ltz` | `a < 0` | `bltz` | `bltzal` | `brltz` | `sltz` |
| `-ne` | `a != b` | `bne` | `bneal` | `brne` | `sne` |
| `-nez` | `a != 0` | `bnez` | `bnezal` | `brnez` | `snez` |
| `-nan` | `a == NaN` | `bnan` | — | `brnan` | `snan` |
| `-nanz` | `a != NaN` | — | — | — | `snanz` |
| `-dns` | 设备未设置 | `bdns` | `bdnsal` | `brdns` | `sdns` |
| `-dse` | 设备已设置 | `bdse` | `bdseal` | `brdse` | `sdse` |
| `-ap` | `a ≈ b` | `bap` | `bapal` | `brap` | `sap` |
| `-apz` | `a ≈ 0` | `bapz` | `bapzal` | `brapz` | `sapz` |
| `-na` | `a 不≈ b` | `bna` | `bnaal` | `brna` | `sna` |
| `-naz` | `a 不≈ 0` | `bnaz` | `bnazal` | `brnaz` | `snaz` |

> - 所有 `b-` 指令以**目标行号**为最后参数。
> - 所有 `s-` 指令以**目标寄存器**为第一个参数。
> - 所有 `br-` 指令以**相对跳转目标**为最后参数（如 `breq a b 3` 表示若 `a==b` 则跳到其后 3 行）。
> - 所有近似函数需要额外参数表示"多接近才算相等"。公式：`abs(a-b) <= max(c * max(|a|,|b|), float.epsilon * 8)`（`-ap`）；其余类似。

---

## 设备变量

| 变量 | 说明 |
|------|------|
| `Activate` | 设备激活（通常=运行中）：1=激活，0=未激活 |
| `AirRelease` | 空气释放 |
| `Charge` | 设备当前电量 |
| `ClearMemory` | 置 1 清除计数器记忆（如 ExportCount），触发后自动归 0 |
| `Data Network` | 逻辑网络颜色（见下方颜色表） |
| `CompletionRatio` | 完成度比例 |
| `ElevatorLevel` | 电梯层级 |
| `ElevatorSpeed` | 电梯速度 |
| `Error` | 设备错误状态：1=错误，0=正常 |
| `ExportCount` | 自上次 ClearMemory 以来的导出数量 |
| `Filtration` | 过滤系统状态（如 Hardsuit 过滤开启=1） |
| `Harvest` | 执行收获动作：`s d0 Harvest 1` |
| `Horizontal` | 水平角度 |
| `HorizontalRatio` | 水平比例 |
| `Idle` | 空闲 |
| `ImportCount` | 导入数量 |
| `Lock` | 锁定 |
| `Maximum` | 最大值 |
| `Mode` | 模式 |
| `On` | 开关 |
| `Open` | 开启 |
| `Output` | 输出 |
| `Plant` | 执行播种动作：`s d0 Plant 1` |
| `PositionX/Y/Z` | 坐标 |
| `Power` | 功率 |
| `PowerActual` | 实际功率 |
| `PowerPotential` | 潜在功率 |
| `PowerRequired` | 所需功率 |
| `Pressure` | 压力 |
| `PressureExternal` | 外部压力 |
| `PressureInteral` | 内部压力 |
| `PressureSetting` | 压力设置 |
| `Quantity` | 设备内总数量 |
| `Ratio` | 上下文相关的 0~1 比例值 |
| `RatioCarbonDioxide` | CO₂ 比例 |
| `RatioNitrogen` | 氮气比例 |
| `RatioOxygen` | 氧气比例 |
| `RatioPollutant` | 污染物比例 |
| `RatioVolatiles` | 挥发物比例 |
| `RatioWater` | 水比例 |
| `Reagents` | 反应物 |
| `RecipeHash` | 配方 hash |
| `ReferenceId` | 设备唯一标识（每个存档不同） |
| `RequestHash` | 请求 hash |
| `RequiredPower` | 所需功率 |
| `Setting` | 设置值 |
| `SolarAngle` | 太阳能角度：`l r0 d0 SolarAngle` |
| `Temperature` | 温度 |
| `TemperatureSettings` | 温度设置 |
| `TotalMoles` | 总摩尔数 |
| `VelocityMagnitude` | 速度大小 |
| `VelocityRelativeX/Y/Z` | 相对速度分量 |
| `Vertical` | 垂直设置 |
| `VerticalRatio` | 垂直比例 |
| `Volume` | 设备气体体积 |

### Data Network 颜色表

| 值 | 颜色 | Hex |
|----|------|-----|
| 0 | 蓝 | `#212AA5` |
| 1 | 灰 | `#7B7B7B` |
| 2 | 绿 | `#3F9B39` |
| 3 | 橙 | `#FF662B` |
| 4 | 红 | `#E70200` |
| 5 | 黄 | `#FFBC1B` |
| 6 | 白 | `#E7E7E7` |
| 7 | 黑 | `#080908` |
| 8 | 棕 | `#633C2B` |
| 9 | 卡其 | `#63633F` |
| 10 | 粉 | `#E41C99` |
| 11 | 紫 | `#732CA7` |

---

## 槽位变量

一般（过滤器等例外）槽位分配如下：

| 槽位 | 用途 |
|------|------|
| 0 | 进口（Import） |
| 1 | 导出（Export） |
| 2 | 机器内部（Inside Machine） |

| 变量 | 说明 |
|------|------|
| `Occupied` | 槽位是否被占用：`ls r0 d0 2 Occupied` |
| `OccupantHash` | 占据物 hash |
| `Quantity` | 数量 |
| `Damage` | 损坏 |
| `Efficiency` | 效率 |
| `FilterType` | 过滤器类型（见下表） |
| `Health` | 健康度 |
| `Growth` | 生长阶段：`ls r0 d0 0 Growth` |
| `Pressure` | 压力 |
| `Temperature` | 温度 |
| `Charge` | 电量 |
| `ChargeRatio` | 电量比例 |
| `Class` | 类别 |
| `PressureWaste` | 废弃压力 |
| `PressureAir` | 气体压力 |
| `MaxQuantity` | 最大数量 |
| `Mature` | 成熟度：`ls r0 d0 0 Mature` |
| `ReferenceId` | 设备唯一标识 |

### FilterType 返回值

| 值 | 过滤器类型 |
|----|-----------|
| 1 | 氧气 Oxygen |
| 2 | 氮气 Nitrogen |
| 4 | 二氧化碳 Carbon Dioxide |
| 8 | 挥发物 Volatiles |
| 16 | 污染物 Pollutants |
| 32 | 水 Water |
| 64 | 一氧化二氮 Nitrous Oxide |
| 16384 | 氢气 Hydrogen |
| 65536 | 污染水 Polluted Water |
| 131072 | 联氨 Hydrazine |
| 524288 | 酒精 Alcohol |
| 1048576 | 氦 Helium |
| 2097152 | 液态氯化钠 Liquid Sodium Chloride |
| 4194304 | 硅醇 Silanol |
| 16777216 | 盐酸 Hydrochloric Acid |
| 67108864 | 臭氧 Ozone |

---

## 编写脚本的方法论

### 两种主要结构

1. **主循环（Main Loop）**：每个 tick 访问所有任务。最常见，适合同时处理两个以上无关任务。
2. **状态机（State Machine）**：每个 tick 只访问一个任务。每个状态有自己的循环与任务，直到任务完成才进入下一状态。适合有固定顺序的多步流程（如气闸循环）。

> IC10 **没有事件驱动指令**——脚本无法等待设备"通知"，必须反复读取逻辑值并决定如何动作。

### 编写步骤

1. 定义目标。
2. 确定需要交互的设备。
3. 在 Stationpedia 查找设备的可用逻辑变量。
4. 实验观察，测试变量与行为。
5. 将操作分解为必要步骤或任务。
6. 编写脚本：使用分支、循环与逻辑控制流程。
7. 测试与调试。

### 执行机制

- 新脚本从第 0 行开始执行。一次性操作放在顶部。
- 绝大多数脚本使用循环以永久运行。
- 每执行 **128 行**（空行也算）后自动暂停 1 个 tick（0.5 秒），或遇到 `yield`/`sleep`。

### 脚本约束（编辑器硬限制）

游戏内 IC 编辑器对脚本有三个明确的上限，编写时要留意，否则超出的部分会被截断或报错：

| 约束 | 限制值 | 说明 |
|------|--------|------|
| 脚本总大小 | **4 KiB** | 整个脚本（含代码、注释、空行）不能超过 4 KiB |
| 最大行数 | **128 行** | 脚本最多 128 行（空行也算行数） |
| 每行长度 | **90 字符** | 单行不超过 90 字符 |

> ⚠️ 注意区分两个「128」：
> - **执行机制里的 128 行**：跑满 128 行就**自动暂停 1 tick**（节奏控制，不是上限）。
> - **编辑器限制里的 128 行**：脚本**最多**只能有 128 行（硬上限，超出截断）。
>
> 两者数值相同但含义不同，容易混。


### 入门必学的 20 条指令

| 指令 | 说明 |
|------|------|
| `alias` | 给寄存器/设备起名字（可选，但极大提升可读性） |
| `label:` | 跳转标签 |
| `yield` | 暂停 1 个 tick |
| `j` | 跳转到标签或 `ra` |
| `jal` | 跳转并存返回地址到 `ra`（函数调用） |
| `beq` | 相等时跳转 |
| `bne` | 不等时跳转 |
| `bgt` | 大于时跳转 |
| `blt` | 小于时跳转 |
| `l` | 从设备加载逻辑值 |
| `lb` | 批量加载 |
| `ls` | 加载槽位逻辑值 |
| `s` | 写入设备 |
| `sb` | 批量写入 |
| `move` | 拷贝值到寄存器 |
| `add` | 加法 |
| `sub` | 减法 |
| `mul` | 乘法 |
| `div` | 除法 |
| `seqz` | 等于零时为 1（可作非门，0→1，1→0） |

> 每个 `b-` 指令加 `-al`（如 `beqal`）可在跳转时把下一行号存入 `ra`。

---

## 示例脚本

### 示例 1：Klaxon 播放器（状态机）

在火星上，大气中含有有价值的气体，但也含有少量需要清除的污染物。下面展示用按钮控制 Klaxon 扬声器播放/切换歌曲的状态机。

```
alias Klaxon d0
alias Button d1
alias SoundToPlay r15
move SoundToPlay 7      # 三首歌曲编号为 7、8、9
s Klaxon On 1           # 先开启扬声器
StateSilent:
yield
l r0 Button Activate    # 按钮是否按下？
beq r0 0 StateSilent    # Activate=0 则保持静音
s Klaxon SoundAlert SoundToPlay   # 播放歌曲
yield                   # bug 修复：按钮需要更多时间复位
StatePlaying:
yield
l r0 Button Activate    # 按钮是否按下？
beq r0 0 StatePlaying   # Activate=0 则保持播放
s Klaxon SoundAlert 0   # 关闭音乐
yield                   # bug 修复
# 选择下一首歌曲
add SoundToPlay SoundToPlay 1   # 下一首
blt SoundToPlay 10 StateSilent  # <10 则回到静音
move SoundToPlay 7            # >=10 则回到第一首
j StateSilent
```

### 示例 2：Schmitt 触发器（传感器范围控制）

用 `select` 的三元条件，根据传感器读数范围切换设备开关状态（以温度控制为例）。

**标准（"冷却"）——温度高于上限开启，低于下限关闭：**
```
define TempMax 296.15   # 23°C
define TempMin 283.15   # 10°C
alias Valve d0
alias Sensor d1

example1:
 yield
 l r0 Valve On
 select r0 r0 TempMin TempMax
 l r1 Sensor Temperature
 sgt r0 r1 r0
 s Valve On r0
j example1
```

**反转（"加热"）——温度低于下限开启，高于上限关闭：**
```
define TempMax 296.15
define TempMin 283.15
alias Valve d0
alias Sensor d1

example2:
 yield
 l r0 Valve On
 select r0 r0 TempMax TempMin
 l r1 Sensor Temperature
 slt r0 r1 r0
 s Valve On r0
j example2
```

### 示例 3：执行行数实验

无 `yield` 的脚本，用 IC 宿主显示执行行数。每 0.5 秒出现一次数字，可推断出每个 tick 约执行 **128 行**（+129/+129/+126 的周期说明真实值为 128）。

```
move r0 1        # 第 0 行
add r0 r0 3
s db Setting r0
j 1
```

### 示例 4：多级函数调用中的 ra 压栈/弹栈

```
orientPanelsToStar:
push ra           # 保存由 jal 设置的返回地址
# ... 计算面板朝向，结果存入 r0、r1 ...
jal orientPanelsTo
# ... 如需可调用更多函数 ...
pop ra            # 恢复 orientPanelsToStar 自身的返回地址
j ra              # 返回调用者

orientPanelsTo:
# ... 实际设置面板朝向 ...
j ra              # 返回调用者
```

> 仅靠 `move` 把 `ra` 存到别的寄存器（如 r15）只能支持两层函数调用；`push/pop ra` 可解决任意深度（最大 512 层）的问题。
> 若脚本会 push/pop 值，建议在主循环前 `clr db` 清空栈（除非 IC 插在非 IC 宿主的插槽中，如空调）。

### 示例 5：Harvie 自动化（批量指令）

用 `sb` 控制网络上所有 Harvie 设备，但只有一台 Harvie 和一台 Tray 作为主控读取其余值，其余 Harvie 重复主单元的动作。

```
alias dHarvie d0
alias dTray d1
alias rHarvieHash r8
alias rTrayHash r9
l rHarvieHash dHarvie PrefabHash
l rTrayHash dTray PrefabHash

main:
yield
ls r0 dTray 0 Mature       # 成熟植物返回 1，幼芽返回 0，无返回 -1
beq r0 -1 plantCrop
beq r0 1 harvestCrop
ls r0 dTray 0 Seeding      # 有种子返回 1，否则返回 -1
beq r0 1 harvestCrop
j main

plantCrop:
ls r0 dHarvie 0 Occupied   # 无种子则停止播种
beq r0 0 main
sb rHarvieHash Plant 1
j main

harvestCrop:
sb rHarvieHash Harvest 1
j main
```

### 示例 6：太阳能板双轴追踪

```
# 将一块设为 Vertical=15（最小值），Horizontal=0
# 记录面板朝向，将光照传感器平放指向该方向
alias sensor d0

define Heavy -934345724
define HeavyDual -1545574413
define Solar -2045627372
define SolarDual -539224550

start:
yield
l r0 sensor Activate
beqz r0 reset
l r0 sensor Horizontal
sb Heavy Horizontal r0
sb HeavyDual Horizontal r0
sb Solar Horizontal r0
sb SolarDual Horizontal r0
l r0 sensor Vertical
sub r0 90 r0
sb Heavy Vertical r0
sb HeavyDual Vertical r0
sb Solar Vertical r0
sb SolarDual Vertical r0
j start

reset:
yield
sb Heavy Horizontal 270
sb HeavyDual Horizontal 270
sb Solar Horizontal 270
sb SolarDual Horizontal 270
sb Heavy Vertical 0
sb HeavyDual Vertical 0
sb Solar Vertical 0
sb SolarDual Vertical 0
sleep 10
j start
```

---

## 主循环 vs 状态机：对比示例

下面用**同一个常见需求**分别用两种写法演示，帮助你体会两者的差异。

---

### 写法一：主循环（Main Loop）

**特点**：每个 tick 都遍历所有任务，适合**互相独立、需要同时处理**的多件事。

**示例：温度控制 + 状态显示（两个无关任务同时处理）**

```bash
# ============================
# 主循环 (Main Loop) 示例
# 每个 tick 同时处理两件无关的事：
#   1) 读取温度，用施密特触发器控制冷却阀开关
#   2) 把当前温度写入 IC 宿主用于显示
# ============================

alias Cooler d0      # 冷却设备
alias Sensor d1      # 温度传感器

define TempMax 296.15   # 上限 23°C
define TempMin 283.15   # 下限 10°C

start:
yield
# ---- 任务 A：温度控制（施密特触发器，防止在阈值附近抖动）----
l r0 Sensor Temperature
blt r0 TempMin turnOn     # 低于下限 -> 开冷却
bgt r0 TempMax turnOff    # 高于上限 -> 关冷却
# ---- 任务 B：显示当前温度（与任务 A 完全无关）----
s db Setting r0
j start

turnOn:
s Cooler On 1
j start
turnOff:
s Cooler On 0
j start
```

**说明**：每个 tick 都先读一次温度、判断是否开关冷却阀，同时把温度写到宿主显示。两个任务彼此不阻塞——这正是主循环的优势：适合「温度控制 + 压力监控 + 日志上报」这类彼此独立、需要每 tick 都检查的任务。

---

### 写法二：状态机（State Machine）

**特点**：每个 tick 只执行**一个状态**，该状态完成后才进入下一状态。适合**有固定顺序的多步流程**（如气闸、填充-处理-排出）。

**示例：气闸循环（关门 → 加压 → 开门）**

```bash
# ============================
# 状态机 (State Machine) 示例
# 气闸循环：关门 -> 加压到目标压力 -> 开门 -> 回到开始
# 每个 tick 只执行一个状态，状态未完成就不往下走
# ============================

alias Door d0                 # 气闸门
alias PressureSensor d1       # 压力传感器

define TargetPressure 100     # 目标压力

start:
yield
# 先确认门已关闭，没关好就进入关门状态
l r0 Door Open
bnez r0 StateClose
j StatePressurize

# ---- 状态 1：关门 ----
StateClose:
yield
s Door Open 0          # 关闭闸门
j StatePressurize      # 关好后进入加压

# ---- 状态 2：加压，直到达到目标压力 ----
StatePressurize:
yield
l r0 PressureSensor Pressure
blt r0 TargetPressure StatePressurize   # 未到目标 -> 继续加压
j StateOpen            # 达到目标 -> 开门

# ---- 状态 3：开门，让人员/车辆通过 ----
StateOpen:
yield
s Door Open 1          # 打开闸门
yield                  # 留出通过时间
j start                # 回到循环起点
```

**说明**：脚本在同一时刻只「活」在一个状态里——关门没关好就一直待在 `StateClose`，压力没到目标就一直待在 `StatePressurize`，只有当前步骤**完成**才会 `j` 到下一状态。这就是状态机：适合「必须按顺序、且每步需要等待完成信号」的流程。

---

### 两者怎么选？

| 维度 | 主循环 (Main Loop) | 状态机 (State Machine) |
|------|--------------------|------------------------|
| 每个 tick 做什么 | 访问**所有**任务 | 只访问**一个**状态 |
| 适用场景 | 多个**相互独立**的任务同时运行 | 有**固定顺序**的逐步流程 |
| 优点 | 各任务互不阻塞、实时性好 | 流程清晰、易调试、天然带「等待」 |
| 典型例子 | 多传感器监控 + 多设备控制 | 气闸循环、填充→处理→排出、收割周期 |

> 💡 同一个需求两种写法通常都能实现。选择依据是**任务的性质**：彼此独立 → 主循环；按顺序执行、需要等待 → 状态机。

---

## 附：内部栈编程（Stack Programming）

以下设备支持栈指令：Logic Sorter、Autolathe、Electronics Printer、Hydraulic Pipe Bender、Tool Manufactory、Security Printer、Rocket Manufactory。Medium Satellite Dish 也支持（用于查看交易数据）。

栈指令是设备可解释的**整数编码**。每个指令是一个数字，其位被划分为多个字段：低 8 位为 **OP 码（操作码）**，其余位为参数。

### 示例：用 Logic Sorter 过滤 Iron Ore

Logic Sorter 有两个过滤 OP 码：`FilterPrefabHashEquals`（=1）与 `FilterPrefabHashNotEquals`（=2）。格式：低 8 位为 OP 码，接下来 32 位为 Prefab Hash。

```
# FilterPrefabHash 指令格式 = %[32-bit prefab hash][8-bit OP code]
# ItemIronOre 的 Prefab Hash = 1758427767

alias LogicSorter d0
sll r0 HASH("ItemIronOre") 8      # hash 左移 8 位
or r0 r0 SorterInstruction.FilterPrefabHashEquals   # 合并 OP 码
put LogicSorter 0 r0              # 写入分类器栈地址 0
```

也可用 `mul`（×256 等价左移 8 位）+ `add`，或用 `ins` 直接操作位域实现。

### 模式（Mode）

Logic Sorter 的 `Mode`：`All`(0) / `Any`(1) / `None`(2)，默认为 `All`。
- `All`：所有栈指令都必须匹配
- `Any`：至少一个匹配
- `None`：全部不匹配

> 分类多个物品时需切换 Mode。若要用 FilterPrefabHashEquals 反转输出侧，可在 `Any`(1) 与 `None`(2) 间切换。

### 打印机指令

- `PrinterInstruction.ExecuteRecipe`：立即开始打印，指令的 quantity 每生产一件递减；quantity=0 不打印。
- 若因缺反应物中断，会继续执行下一条指令（除非前面有 `WaitUntilNextValid`）。
- 打印机空闲时会循环遍历栈，从而在恢复反应物后继续未完成的打印任务。
- 注意：`StackPointer` 存于栈地址 63；`MissingRecipeReagent` 条目位于地址 54~62。

```
alias printer d0
move r0 PrinterInstruction.ExecuteRecipe
ins r0 50 8 8          # 在 8 位起插入数量 50
ins r0 HASH("ItemCableCoil") 16 32   # 在 16 位起插入物品 hash
put printer 0 PrinterInstruction.WaitUntilNextValid   # 用于填充缺失反应物
put printer 1 r0       # ExecuteRecipe 放在 WaitUntilNextValid 之后
get r0 printer 54      # 读取第一个缺失反应物的栈地址
sra r1 r0 16           # 去除 OP 码与数量，保留符号位
rmap r2 printer r1     # 将反应物 hash 转换为对应的 ingot hash
```

---

## 附：IC10 模拟器

可在不反复"死亡"的情况下开发代码的在线模拟器：

- Stationeers Code Simulator
- Stationeers IC10 Editor & Emulator（带调试器与模拟器的功能完整的编辑器）
- Stationeering（游戏内 IC10 芯片的模拟，带错误检查、栈与寄存器可视化）
- EASy68K（68000 结构化汇编语言 IDE）
- 多个 VS Code / kate / Notepad++ 的 IC10 语法高亮插件
- 大量代码示例仓库

---

> **参考来源**：Stationeers Community Wiki —— `IC10`（最后编辑 2026-06-13）与 `IC10/instructions`（最后编辑 2026-06-09）。
> 整理：本地参考文档。使用时请以游戏内 IC 编辑器（按 F / X / S 调出）与 Stationpedia 为准。
