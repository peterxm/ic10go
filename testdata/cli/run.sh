#!/usr/bin/env bash
# ic10c 端到端断言测试（黑盒）。
#
# 编译/运行一段小程序并校验结果：退出码、诊断码、关键指令、行数、VM 设备值，
# 外加优化前后与 D3a 的语义差分。通过率与报告由断言结果自动生成。
#
#   sh testdata/cli/run.sh                        # 用仓库根的 ./ic10c
#   IC10C=/path/to/ic10c sh testdata/cli/run.sh   # 指定编译器
#   REPORT=/tmp/report.md sh testdata/cli/run.sh  # 额外写出 Markdown 报告
#
# 退出码：0 = 全部通过；1 = 有失败；2 = 找不到编译器。
# 脚本用 bash 数组，若被 `sh`（Ubuntu 的 dash）调用则先用 bash 重启自己。
if [ -z "${BASH_VERSION:-}" ]; then
    exec bash "$0" "$@"
fi
set -u

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
IC10C="${IC10C:-$REPO_ROOT/ic10c}"
REPORT="${REPORT:-}"
TEST_DIR="$(mktemp -d)"
trap 'rm -rf "$TEST_DIR"' EXIT

if [ ! -x "$IC10C" ]; then
    echo "错误：找不到可执行的 ic10c：$IC10C" >&2
    echo "先构建（./build.sh build）或设置 IC10C=/path/to/ic10c" >&2
    exit 2
fi

PASS=0
FAIL=0
RESULTS=()
VERSION="$("$IC10C" version 2>/dev/null | head -1)"

ok()  { PASS=$((PASS + 1)); RESULTS+=("PASS|$1|$2"); printf '  \033[32mPASS\033[0m %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); RESULTS+=("FAIL|$1|$2"); printf '  \033[31mFAIL\033[0m %s — %s\n' "$1" "$2"; }

# eq NAME ACTUAL EXPECTED [detail]
eq() { if [ "$2" = "$3" ]; then ok "$1" "${4:-= $3}"; else bad "$1" "期望 [$3]，实际 [$2]"; fi; }
# has NAME HAYSTACK NEEDLE [detail]
has() { case "$2" in *"$3"*) ok "$1" "${4:-包含 $3}";; *) bad "$1" "缺少 [$3]";; esac; }
# count NAME HAYSTACK NEEDLE EXPECTED
count() { local n; n=$(printf '%s\n' "$2" | grep -cF -- "$3"); eq "$1" "$n" "$4" "出现 $n 次"; }

json() { "$IC10C" build --json "$1" 2>/dev/null; }
jqr()  { printf '%s' "$1" | jq -r "$2" 2>/dev/null; }

# snap FILE STEPS --set ...  →  规范化设备快照（语义差分）
snap() {
    local file="$1" steps="$2"; shift 2
    "$IC10C" run --steps "$steps" --json "$@" "$file" 2>/dev/null | jq -Sc '.devices' 2>/dev/null
}
# snap_env "ENV=1 ..." FILE STEPS --set ...
snap_env() {
    local env="$1" file="$2" steps="$3"; shift 3
    env $env "$IC10C" run --steps "$steps" --json "$@" "$file" 2>/dev/null | jq -Sc '.devices' 2>/dev/null
}

echo "=========================================="
echo "ic10c 端到端断言测试"
echo "编译器: $IC10C"
echo "版本:   $VERSION"
echo "日期:   $(date '+%Y-%m-%d %H:%M:%S')"
echo "=========================================="

# ---------------------------------------------------------------- 1 基础算术
echo ">>> 1 基础算术"
cat > "$TEST_DIR/arithmetic.icg" <<'EOF'
func main() {
    for {
        yield()
        a := d1.Setting
        b := 10
        d0.Setting = a + b
        d0.Setting = a - b
        d0.Setting = a * b
        d0.Setting = a / b
    }
}
EOF
j=$(json "$TEST_DIR/arithmetic.icg"); code=$(jqr "$j" .code)
eq "arith: ok" "$(jqr "$j" .ok)" "true"
eq "arith: 11 行" "$(jqr "$j" .stats.lines)" "11"
has "arith: add" "$code" "add r0 r1 10"
has "arith: div" "$code" "div r0 r1 10"

# ---------------------------------------------------------------- 2 比较运算
echo ">>> 2 比较运算"
cat > "$TEST_DIR/compare.icg" <<'EOF'
func main() {
    for {
        yield()
        x := d1.Setting
        if x > 50 { d0.Setting = 1 }
        if x == 50 { d0.Setting = 2 }
        if x < 50 { d0.Setting = 3 }
        if x != 50 { d0.Setting = 4 }
    }
}
EOF
code=$(jqr "$(json "$TEST_DIR/compare.icg")" .code)
has "compare: ble" "$code" "ble r0 50"
has "compare: bne" "$code" "bne r0 50"
has "compare: bge" "$code" "bge r0 50"

# ---------------------------------------------------------------- 3 位运算 + 警告
echo ">>> 3 位运算（未知 logic 警告）"
cat > "$TEST_DIR/bitwise.icg" <<'EOF'
func main() {
    for {
        yield()
        a := d1.Setting
        b := 5
        d0.Setting = a & b
        d0.Setting = a | b
        d0.Setting = a ^ b
        d0.SetSetting = ~a
    }
}
EOF
j=$(json "$TEST_DIR/bitwise.icg"); code=$(jqr "$j" .code)
eq "bitwise: 警告不阻断编译" "$(jqr "$j" .ok)" "true"
eq "bitwise: 诊断码" "$(jqr "$j" '.diagnostics[0].code')" "unknown-logic-type"
has "bitwise: 拼写建议" "$(jqr "$j" '.diagnostics[0].message')" "did you mean"
has "bitwise: and" "$code" "and r0 r1 5"
has "bitwise: not" "$code" "not r0 r1"

# ---------------------------------------------------------------- 4 数学函数
echo ">>> 4 数学函数"
cat > "$TEST_DIR/math.icg" <<'EOF'
func main() {
    for {
        yield()
        x := d1.Setting
        d0.Setting = abs(x)
        d0.Setting = sqrt(x)
        d0.Setting = exp(x)
        d0.Setting = log(x)
        d0.Setting = floor(x)
        d0.Setting = ceil(x)
        d0.Setting = round(x)
    }
}
EOF
code=$(jqr "$(json "$TEST_DIR/math.icg")" .code)
for fn in abs sqrt exp log floor ceil round; do
    has "math: $fn" "$code" "$fn r0 r1"
done

# ---------------------------------------------------------------- 5 嵌套内联
echo ">>> 5 函数内联"
cat > "$TEST_DIR/function.icg" <<'EOF'
func double(x num) num { return x * 2 }
func add10(x num) num { return x + 10 }
func main() {
    for {
        yield()
        x := d1.Setting
        d0.Setting = double(add10(x))
    }
}
EOF
j=$(json "$TEST_DIR/function.icg"); code=$(jqr "$j" .code)
eq "inline: 折叠为 6 行" "$(jqr "$j" .stats.lines)" "6"
has "inline: add10" "$code" "add r0 r0 10"
has "inline: double" "$code" "mul r0 r0 2"

# ---------------------------------------------------------------- 6 常量折叠
echo ">>> 6 常量折叠"
cat > "$TEST_DIR/constant.icg" <<'EOF'
const Threshold = 290.0
func main() {
    for {
        yield()
        d0.Setting = Threshold * 2 + 10
    }
}
EOF
has "const: 290*2+10=590" "$(jqr "$(json "$TEST_DIR/constant.icg")" .code)" "s d0 Setting 590"

# ---------------------------------------------------------------- 7 循环
echo ">>> 7 循环"
cat > "$TEST_DIR/loop.icg" <<'EOF'
func main() {
    for {
        yield()
        sum := 0
        for i := range 10 { sum += i }
        d0.Setting = sum
        count := 0
        for d1.Setting > 0 {
            d1.Setting = d1.Setting - 1
            count += 1
        }
        d0.Setting = count
    }
}
EOF
code=$(jqr "$(json "$TEST_DIR/loop.icg")" .code)
has "loop: for range" "$code" "bge r0 10"
has "loop: 条件循环" "$code" "blez"

# ---------------------------------------------------------------- 7b 每 tick 预算
echo ">>> 7b 每 tick 指令预算"
cat > "$TEST_DIR/tick.icg" <<'EOF'
func main() {
    for {
        yield()
        for i := 0; i < 3; i++ {
            d0.Setting = i
        }
    }
}
EOF
j=$(json "$TEST_DIR/tick.icg")
eq "tick: limit" "$(jqr "$j" '.tick.limit')" "128"
eq "tick: 不超限" "$(jqr "$j" '.tick.exceeds')" "false"
has "tick: CLI 报告" "$("$IC10C" tick "$TEST_DIR/tick.icg" 2>/dev/null)" "per-tick budget"
"$IC10C" tick --strict --limit 4 "$TEST_DIR/tick.icg" >/dev/null 2>&1
eq "tick: --strict 超限退出码" "$?" "1"

# ---------------------------------------------------------------- 8 设备/槽位
echo ">>> 8 设备访问"
cat > "$TEST_DIR/device.icg" <<'EOF'
func main() {
    for {
        yield()
        t := d1.Temperature
        d0.Temperature = t
        slotVal := d2.slot[0].Occupied
        d2.slot[0].Quantity = 100
    }
}
EOF
code=$(jqr "$(json "$TEST_DIR/device.icg")" .code)
has "device: 读" "$code" "l r0 d1 Temperature"
has "device: 槽位写" "$code" "ss d2 0 Quantity 100"

# ---------------------------------------------------------------- 9 负例：栈参数错误
echo ">>> 9 负例：peek(10) 应报错"
cat > "$TEST_DIR/stack.icg" <<'EOF'
func main() {
    for {
        yield()
        push(1)
        w := peek(10)
    }
}
EOF
err=$("$IC10C" build "$TEST_DIR/stack.icg" 2>&1 >/dev/null); rc=$?
eq "stack: 退出码为 1" "$rc" "1"
has "stack: 错误信息" "$err" "peek expects 0 arguments"

# ---------------------------------------------------------------- 10 批量 IO
echo ">>> 10 批量 IO"
cat > "$TEST_DIR/batch.icg" <<'EOF'
func main() {
    for {
        yield()
        total := batch.read(hash("StructureBattery"), "Charge", "Sum")
        d0.Setting = total
    }
}
EOF
has "batch: lb + 编译期 hash" "$(jqr "$(json "$TEST_DIR/batch.icg")" .code)" "lb r0 -400115994 Charge 1"

# ---------------------------------------------------------------- 11 单位转换
echo ">>> 11 单位转换"
cat > "$TEST_DIR/units.icg" <<'EOF'
const MaxTempC = 23c
const MaxTempF = 73.4f
const MaxPreMPa = 0.101MPa
const MaxPreBar = 1.01bar
func main() {
    for {
        yield()
        d0.Setting = MaxTempC
        d0.Setting = MaxTempF
        d0.Setting = MaxPreMPa
        d0.Setting = MaxPreBar
    }
}
EOF
code=$(jqr "$(json "$TEST_DIR/units.icg")" .code)
count "units: 296.15 x2" "$code" "s d0 Setting 296.15" 2
count "units: 101 x2 (0.101MPa/1.01bar)" "$code" "s d0 Setting 101" 2

# ---------------------------------------------------------------- 12 多芯片
echo ">>> 12 多芯片"
cat > "$TEST_DIR/multichip.icg" <<'EOF'
chip ChipA { func main() { for { yield(); d0.Setting = d1.Setting } } }
chip ChipB { func main() { for { yield(); d2.Setting = d3.Setting } } }
EOF
j=$(json "$TEST_DIR/multichip.icg")
eq "multichip: 两块芯片" "$(jqr "$j" '.chips | length')" "2"
eq "multichip: 名称" "$(jqr "$j" '[.chips[].name] | join(",")')" "ChipA,ChipB"
has "multichip: ChipA 代码" "$(jqr "$j" '.chips[0].code')" "s d0 Setting r0"

# ---------------------------------------------------------------- 13 枚举/哈希
echo ">>> 13 枚举与哈希"
cat > "$TEST_DIR/enum.icg" <<'EOF'
func main() {
    for {
        yield()
        d0.Mode = AirCon.Cold
        d1.Setting = GasType.Oxygen
        d2.Setting = hash("Oxygen")
        d3.Setting = hash("AirCon")
    }
}
EOF
code=$(jqr "$(json "$TEST_DIR/enum.icg")" .code)
has "enum: AirCon.Cold=0" "$code" "s d0 Mode 0"
has "enum: GasType.Oxygen=1" "$code" "s d1 Setting 1"
has "enum: hash(Oxygen)" "$code" "s d2 Setting 1866552090"
has "enum: hash(AirCon)" "$code" "s d3 Setting 442568175"

# ---------------------------------------------------------------- 14 三元 → select
echo ">>> 14 三元（无分支）"
cat > "$TEST_DIR/ternary.icg" <<'EOF'
func main() {
    for {
        yield()
        x := d1.Setting
        y := x > 50 ? 100 : 200
        d0.Setting = y
    }
}
EOF
j=$(json "$TEST_DIR/ternary.icg"); code=$(jqr "$j" .code)
has "ternary: select" "$code" "select r0 r0 100 200"
eq "ternary: 无跳转" "$(printf '%s\n' "$code" | grep -c '^j ')" "1" "仅尾部 j 0"

# ---------------------------------------------------------------- 15 数据段
echo ">>> 15 数据段查表"
cat > "$TEST_DIR/lookup.icg" <<'EOF'
data TemperatureTable = [ 273.15, 293.15, 313.15, 333.15 ]
data PressureTable = [ 101.3, 202.6, 303.9, 405.2 ]
func main() {
    for {
        yield()
        idx := d1.Setting
        d0.Setting = TemperatureTable[idx]
        d0.Setting = PressureTable[idx]
    }
}
EOF
j=$(json "$TEST_DIR/lookup.icg")
eq "data: needed" "$(jqr "$j" .data.needed)" "true"
eq "data: sentinel == start" "$(jqr "$j" .data.sentinel)" "$(jqr "$j" .data.start)"
eq "data: loader 哨兵行" "$(jqr "$j" '.data.loader' | head -1)" "put db 503 -1863895356"
has "data: runtime 哨兵校验" "$(jqr "$j" .code)" "bne r0 -1863895356 129"

# ---------------------------------------------------------------- 16 复杂控制流
echo ">>> 16 复杂控制流"
cat > "$TEST_DIR/complex.icg" <<'EOF'
func main() {
    for {
        yield()
        x := d1.Setting
        result := 0
        switch x {
        case 0..9: result = 100
        case 10..19: result = 200
        case 20..29: result = 300
        default: result = 400
        }
        label Outer:
        for i := 0; i < 5; i++ {
            for j := 0; j < 5; j++ {
                if i == j { continue Outer }
                if i + j > 7 { break Outer }
                result += 1
            }
        }
        d0.Setting = result
    }
}
EOF
j=$(json "$TEST_DIR/complex.icg")
eq "complex: 29 行" "$(jqr "$j" .stats.lines)" "29"
has "complex: default" "$(jqr "$j" .code)" "move r3 400"

# ---------------------------------------------------------------- 17 VM 语义
echo ">>> 17 VM 语义"
cat > "$TEST_DIR/thermo.icg" <<'EOF'
const MinTemp = 283.15
const MaxTemp = 296.15
func main() {
    for {
        yield()
        var on = 0
        t := d1.Temperature
        if t < MinTemp { on = 1 }
        if t > MaxTemp { on = 0 }
        d0.On = on
        d0.Setting = d1.Setting + 1
    }
}
EOF
out=$(snap "$TEST_DIR/thermo.icg" 40 --set d1.Temperature=280 --set d1.Setting=41 --set d0.On=9 --set d0.Setting=9)
eq "vm: 低温开加热器" "$(printf '%s' "$out" | jq -r '.d0.logic.On')" "1"
eq "vm: Setting=d1+1" "$(printf '%s' "$out" | jq -r '.d0.logic.Setting')" "42"
out=$(snap "$TEST_DIR/thermo.icg" 40 --set d1.Temperature=300 --set d1.Setting=0 --set d0.On=9 --set d0.Setting=9)
eq "vm: 高温关加热器" "$(printf '%s' "$out" | jq -r '.d0.logic.On')" "0"

# ---------------------------------------------------------------- 18 优化差分
echo ">>> 18 优化前后语义差分（VM）"
a=$(snap "$TEST_DIR/thermo.icg" 60 --set d1.Temperature=290 --set d1.Setting=7 --set d0.On=0 --set d0.Setting=0)
b=$(snap_env "IC10C_NO_OPT=1" "$TEST_DIR/thermo.icg" 60 --set d1.Temperature=290 --set d1.Setting=7 --set d0.On=0 --set d0.Setting=0)
c=$(snap_env "IC10C_NO_OUTLINE=1" "$TEST_DIR/thermo.icg" 60 --set d1.Temperature=290 --set d1.Setting=7 --set d0.On=0 --set d0.Setting=0)
eq "diff: 优化 vs 未优化" "$b" "$a"
eq "diff: 优化 vs 不内联/外提" "$c" "$a"

# ---------------------------------------------------------------- 19 import + size
echo ">>> 19 import 与 size"
mkdir -p "$TEST_DIR/lib"
cat > "$TEST_DIR/lib/util.icg" <<'EOF'
func triple(x num) num { return x * 3 }
EOF
cat > "$TEST_DIR/importer.icg" <<'EOF'
import "lib/util.icg"
func main() {
    for {
        yield()
        d0.Setting = triple(d1.Setting)
    }
}
EOF
j=$(json "$TEST_DIR/importer.icg")
eq "import: 编译成功" "$(jqr "$j" .ok)" "true"
has "import: 折入产物" "$(jqr "$j" .code)" "mul r0 r0 3"
"$IC10C" size "$TEST_DIR/importer.icg" >/dev/null 2>&1
eq "import: size 不再失败" "$?" "0"

# ---------------------------------------------------------------- 20 非叶子外提 + 差分
echo ">>> 20 非叶子外提（D3a）与差分"
cat > "$TEST_DIR/nonleaf.icg" <<'EOF'
func clamp(x num, lo num, hi num) num {
    if x < lo { return lo }
    if x > hi { return hi }
    return x
}
func adjust(x num) num {
    a := clamp(x*2.0, -100.0, 100.0)
    return clamp(a+1.5, -100.0, 100.0)
}
func main() {
    for {
        yield()
        x := d4.Setting
        d0.Setting = adjust(x+1.0) + adjust(x-1.0)
    }
}
EOF
jl=$(json "$TEST_DIR/nonleaf.icg")
tl=$(jqr "$jl" .stats.lines)
has "D3a: 外提非叶子 adjust" "$(jqr "$jl" .code)" "jal "
ol=$(IC10C_NO_OUTLINE=1 "$IC10C" build --json "$TEST_DIR/nonleaf.icg" 2>/dev/null | jq -r .stats.lines)
eq "D3a: 比全内联更短" "$([ "$tl" -lt "$ol" ] && echo yes || echo no)" "yes" "$tl < $ol"
s1=$(snap "$TEST_DIR/nonleaf.icg" 60 --set d4.Setting=4 --set d0.Setting=0)
s2=$(snap_env "IC10C_NO_OUTLINE=1" "$TEST_DIR/nonleaf.icg" 60 --set d4.Setting=4 --set d0.Setting=0)
eq "D3a: 与全内联语义一致" "$s2" "$s1"

# ---------------------------------------------------------------- 汇总 + 报告
echo "=========================================="
echo "结果: $PASS 通过, $FAIL 失败"
echo "=========================================="

if [ -n "$REPORT" ]; then
    {
        echo "# ic10c 端到端断言报告"
        echo
        echo "- **编译器**: \`$VERSION\`"
        echo "- **二进制**: \`$IC10C\`"
        echo "- **日期**: $(date '+%Y-%m-%d %H:%M:%S')"
        echo "- **结果**: **$PASS 通过 / $FAIL 失败**"
        echo
        echo "| 状态 | 用例 | 详情 |"
        echo "|------|------|------|"
        for r in "${RESULTS[@]}"; do
            st="${r%%|*}"; rest="${r#*|}"; name="${rest%%|*}"; detail="${rest#*|}"
            [ "$st" = "PASS" ] && mark="✅" || mark="❌"
            echo "| $mark | $name | $detail |"
        done
    } > "$REPORT"
    echo "报告已写入: $REPORT"
fi

[ "$FAIL" -eq 0 ]
