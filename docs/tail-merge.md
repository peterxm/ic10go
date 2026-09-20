# 尾块合并与寄存器颜色：气闸控制案例（待决）

> 状态：**暂不改编译器**，记录案例与取舍，待以后决定。
> 相关代码：`internal/opt/opt.go` 的 `MergeTailsColored` / `mergeOneTail`。

## 1. 现象

`examples/气闸控制-双门.icg` 的**同一份逻辑**，写法不同，产物行数不同：

| 写法 | 直接编译 | 反编译→重编译 |
|------|----------|----------------|
| 函数式（`initAirlock` / `cycle` / `switchDoor`，各自末尾调 `idleAll`+`syncButtons`） | 88 行 | 82 行 |
| 单函数（两个换门方向写在 `main` 的 `if/else` 里，共用末尾 `idleAll()`/`syncButtons()`） | **78 行** | 78 行 |

也就是说：函数式写法**编译器没能合并公共尾块**，而反编译产物（goto 结构）反而更小。

## 2. 复现

```bash
ic10c build "examples/气闸控制-双门.icg" > d.ic
wc -l d.ic                                  # 当前单函数写法：78

ic10c decompile -s d.ic > d.dec.icg
ic10c build d.dec.icg > d.re.ic
wc -l d.re.ic                               # 78（已持平）

ic10c graph --level ir "examples/气闸控制-双门.icg"   # 看 IR 基本块
```

## 3. 根因：尾块合并按「物理寄存器号」判等

Pipeline 里唯一的尾块合并是 `MergeTailsColored`（`internal/opt/opt.go:2479`），它在**寄存器分配之后**运行，并把寄存器操作数的身份定义为**物理寄存器号**：

```go
// MergeTailsColored 传给 mergeTailsWith 的 reg 回调
if c, ok := colors[r]; ok { return strconv.Itoa(c) }   // 物理寄存器号
```

`instrKey`（`:2587`）对 `Load`/`Store` 等用的正是它：

- `Load`  → `"load|" + Dev + "|" + Logic + "|" + reg(Dst)`
- `Store` → `"store|" + Dev + "|" + Logic + "|" + vk(Src)`

因此 `l r0 d1 Open` 与 `l r1 d1 Open` 被视为**不同指令**，不会合并。

### IR 证据

函数式写法（`ic10c graph --level ir`）：

```
b17 · finish   l r0 d1 Open; s d1 Setting r0; l r0 d0 Open; s d0 Setting r0; j   ← r0
b23 · finish   l r1 d1 Open; s d1 Setting r1; l r1 d0 Open; s d0 Setting r1; j   ← r1
```

三条路径的 `syncButtons` 是三次内联、虚拟寄存器各不相同；分配器按各自活跃区间/干涉图把它们分到了**不同物理寄存器**（init 路径 r0、cycle 路径 r1）。于是：

1. 三个 `finish` 块里，用 r1 的两个合并成 `b23`，用 r0 的 `b17` 落单；
2. init 尾块（→`b17`）与 switchDoor2 尾块（→`b56`）虽然指令完全相同，但**终止符目标不同**（`termKey` 不同），连前面那段相同的 `idleAll` 也没合并。

单函数写法里分配器对这些 load 统一用了 **r0**，颜色一致 → 尾块能合并，78 行：

```
b16 · finish   l r0 d1 Open; ...
b51 · finish   l r0 d1 Open; ...   ← 同色
```

> 补充：结构化的 `mergeTails`（用虚拟寄存器 ID，`:2471`）只在 `opt_test.go` 里用，**没进 pipeline**；它也不能合并上面两块（虚拟寄存器 ID 本就不同），其作用只是**忽略物理颜色差异**。真正需要的是「颜色不同但结构相同、且活跃性安全」时也能合并。

## 4. 拟改进方案（未实施）

在 `mergeOneTail` 里，除「颜色完全相同」外，再做一次「**结构相同、忽略寄存器身份**」的匹配；仅当满足安全条件时才合并：

> 该后缀的 **live-in / live-out 物理寄存器集合**在所有被合并块上必须一致，且共享块对这些寄存器的读/写对得上；**只有“后缀内定义、后缀后死亡”的寄存器才允许不同**。

安全判据必须比较**完整的 live-in/live-out 集合**，而不是只看差异寄存器对。反例：

```
b1: r0 = load d0 Setting; store d3 Setting r0; jmp end     // r0 死后缀
b2: r1 = load d0 Setting; store d3 Setting r1; jmp end     // r1 死后缀，但 r0 在 end 还要用（live-through）
```

结构相同、差异寄存器是 r0/r1，看似都「死后缀」；但共享块（取自 b1）会写 **r0**，在 b2 路径上 r0 是 live-through，被冲掉 → `end` 读到错值。只检查「差异寄存器死后缀」会漏掉这种情况。

## 5. 风险

- **正确性（最高）**：合并错了**不报错**，只是设备行为偶发不对；差分测试能抓「影响写序列」的错误，但**抓不全**。IC10 只有 16 个寄存器、分配器复用激进，live-through 很常见 → 危险场景不罕见。
- **活跃性计算**：后缀在块尾，live-out = 块 live-out；live-in 要由「块 live-in − 前缀定义 + 后缀内先读后写」推出，算错即回到正确性风险。`mergeTailsWith` 是定点迭代，合并会改变 CFG，需每轮重算或增量维护。
- **收益/布局**：合并引入跳转；`mergeOneTail` 在「不省行」的组里也会合并“最不坏”的（省字节），新候选可能改变“最优组”的选择，个别程序行数/字节可能变差。
- **与 regalloc 强耦合**：安全判据依赖物理颜色，regalloc 策略一变就要回归；共享块采用 `first` 的寄存器，结果对 `fn.Blocks` 遍历顺序敏感。

## 6. 维护成本

- **代码复杂度**：`mergeOneTail` 从 ~50 行涨到 ~120–150 行（第二套 key + 活跃性判据 + 组选择约束）；`suffixKey`/`instrKey` 要支持忽略寄存器身份的渲染。
- **跨包不变量**：`MergeTailsColored` 的「颜色有效」注释要扩展；判据同时依赖 `internal/ir` 的 `DefUse`/`Liveness`（`internal/ir/analysis.go:112`），两边改动必须对齐。
- **测试矩阵**：差异寄存器死后缀→合并；live-in 不同→不合并；live-out 不同→不合并；live-through→不合并；每新增指令类型（`Builtin`/`Batch`/`StoreDyn` …）都要同步 `instrKey` 与判据。
- **调试可解释性**：现在「为什么合并」= 颜色一致；改后需要 dump IR + 活跃性 + regalloc 才能解释。
- **长期守护**：属于「一次性实现 + 长期随指令类型/活跃性/regalloc 同步维护」，漏判即静默错编译。

## 7. 决策（暂定）

- **不改编译器**。把「让尾段可合并」交给**源码结构**（公共尾段写在一起，如单函数写法），编译器零新增复杂度。
- 单函数写法已 78 行（≤ 反编译产物 82），可读性不受影响。
- 若以后确有必要做成通用能力，按 §4 的安全判据实施，并补齐 §6 的测试矩阵与文档，可先做成默认关闭的开关验证。

## 8. 代码索引

| 位置 | 作用 |
|------|------|
| `internal/opt/opt.go:2471` | `mergeTails`（虚拟寄存器 key，仅测试用） |
| `internal/opt/opt.go:2479` | `MergeTailsColored`（物理颜色 key，pipeline 用） |
| `internal/opt/opt.go:2492` | `mergeTailsWith`（定点迭代） |
| `internal/opt/opt.go:2515` | `mergeOneTail`（候选分组与选择） |
| `internal/opt/opt.go:2570` | `trimBlock`（裁剪 + 跳转共享块） |
| `internal/opt/opt.go:2576` | `suffixKey` / `instrKey`（`:2587`）/ `termKey`（`:2643`） |
| `pkg/ic10/compiler.go:628`、`pkg/ic10/size.go:85` | `MergeTailsColored` 调用点 |
| `internal/ir/analysis.go:112` | `Liveness` |

---

## 附：两份代码

### A. 函数式写法（直接编译 88 行；反编译→重编译 82 行）

`initAirlock` / `cycle` / `switchDoor` 各自末尾都重复「关通风口、开门、`idleAll`、`syncButtons`」这段公共尾段；编译器因三条路径的物理寄存器颜色不同而未能合并。

```go
const (
    dExitDoor     = d0
    dEntranceDoor = d1
    dExitVent     = d2
    dEntranceVent = d3
    dAirSensor    = d4
    STATE         = 0
    VACUUM        = 0.1Pa
)

func main() {
    dExitDoor.Mode = 1
    dEntranceDoor.Mode = 1
    if db.stack[STATE] == 0 {
        syncButtons()
        initAirlock()
        db.stack[STATE] = 1
    }
    for {
        yield()
        if buttonPressed() {
            cycle()
        }
    }
}

func initAirlock() {
    lockAll()
    dExitDoor.Open = 0
    dEntranceDoor.Open = 0
    for {
        yield()
        if dExitDoor.Idle && dEntranceDoor.Idle {
            dExitVent.Mode = 1
            dExitVent.On = 1
            if dAirSensor.Pressure < VACUUM {
                dExitVent.On = 0
                dEntranceDoor.Open = 1
                idleAll()
                syncButtons()
                return
            }
        }
    }
}

func cycle() {
    if dEntranceDoor.Open != 0 {
        switchDoor(dEntranceDoor, dEntranceVent, dExitDoor)
        return
    }
    switchDoor(dExitDoor, dExitVent, dEntranceDoor)
}

func switchDoor(from, fromVent, to) {
    lockAll()
    from.Open = 0
    for {
        yield()
        if from.Idle {
            fromVent.Mode = 1
            fromVent.On = 1
            if dAirSensor.Pressure < VACUUM {
                fromVent.On = 0
                to.Open = 1
                idleAll()
                syncButtons()
                return
            }
        }
    }
}

func buttonPressed() bool {
    return dEntranceDoor.Open != dEntranceDoor.Setting ||
        dExitDoor.Open != dExitDoor.Setting
}

func syncButtons() {
    dEntranceDoor.Setting = dEntranceDoor.Open
    dExitDoor.Setting = dExitDoor.Open
}

func lockAll() {
    dExitDoor.Lock = 1
    dEntranceDoor.Lock = 1
    dExitVent.Lock = 1
    dEntranceVent.Lock = 1
}

func idleAll() {
    dExitVent.On = 0
    dEntranceVent.On = 0
    dExitDoor.Lock = 0
    dEntranceDoor.Lock = 0
    dExitVent.Lock = 0
    dEntranceVent.Lock = 0
}
```

### B. 单函数写法（直接编译 78 行；反编译→重编译 78 行）

两个换门方向直接写在 `main` 的 `if/else`，共用末尾的 `idleAll()`/`syncButtons()`；这样三条路径的公共尾段颜色一致，能被 `MergeTailsColored` 合并。

```go
const (
    dExitDoor     = d0
    dEntranceDoor = d1
    dExitVent     = d2
    dEntranceVent = d3
    dAirSensor    = d4

    STATE = 0 // 0 = 未初始化；非 0 = 已初始化

    VACUUM = 0.1Pa // 抽真空目标（Pa）
)

func main() {
    dExitDoor.Mode = 1
    dEntranceDoor.Mode = 1

    // 首次运行：关两门、用出口通风口抽真空，再开入口门。
    if db.stack[STATE] == 0 {
        lockAll()
        dExitDoor.Open = 0
        dEntranceDoor.Open = 0
        for {
            yield()
            if !(dExitDoor.Idle && dEntranceDoor.Idle) {
                continue
            }
            dExitVent.Mode = 1
            dExitVent.On = 1
            if dAirSensor.Pressure >= VACUUM {
                continue
            }
            break
        }
        dExitVent.On = 0
        dEntranceDoor.Open = 1
        idleAll()
        syncButtons()
        db.stack[STATE] = 1
    }

    for {
        yield()
        if !buttonPressed() {
            continue
        }
        if dEntranceDoor.Open != 0 {
            // 入口开着 → 关入口、抽真空、开出口
            lockAll()
            dEntranceDoor.Open = 0
            for {
                yield()
                if !dEntranceDoor.Idle {
                    continue
                }
                dEntranceVent.Mode = 1
                dEntranceVent.On = 1
                if dAirSensor.Pressure >= VACUUM {
                    continue
                }
                break
            }
            dEntranceVent.On = 0
            dExitDoor.Open = 1
        } else {
            // 出口开着 → 关出口、抽真空、开入口
            lockAll()
            dExitDoor.Open = 0
            for {
                yield()
                if !dExitDoor.Idle {
                    continue
                }
                dExitVent.Mode = 1
                dExitVent.On = 1
                if dAirSensor.Pressure >= VACUUM {
                    continue
                }
                break
            }
            dExitVent.On = 0
            dEntranceDoor.Open = 1
        }
        idleAll()
        syncButtons()
    }
}

// buttonPressed 任一门的开关被按下：实际 Open 与 Setting 不一致。
func buttonPressed() bool {
    return dEntranceDoor.Open != dEntranceDoor.Setting ||
        dExitDoor.Open != dExitDoor.Setting
}

// syncButtons 把 Setting 同步为 Open，表示按钮请求已处理。
func syncButtons() {
    dEntranceDoor.Setting = dEntranceDoor.Open
    dExitDoor.Setting = dExitDoor.Open
}

func lockAll() {
    dExitDoor.Lock = 1
    dEntranceDoor.Lock = 1
    dExitVent.Lock = 1
    dEntranceVent.Lock = 1
}

func idleAll() {
    dExitVent.On = 0
    dEntranceVent.On = 0
    dExitDoor.Lock = 0
    dEntranceDoor.Lock = 0
    dExitVent.Lock = 0
    dEntranceVent.Lock = 0
}
```

> 两份逻辑完全等价（同一状态守卫、同一换门流程）；区别只在公共尾段是否写在同一个函数里。实际采用的 B 见 `examples/气闸控制-双门.icg`。
