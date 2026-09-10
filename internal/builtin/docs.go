package builtin

// Doc is a short usage/documentation entry shown by the language server and
// the CLI. Signature is language-neutral; EN/ZH are the descriptions.
type Doc struct {
	Signature string
	EN        string
	ZH        string
}

// Docs documents the built-in functions.
var Docs = map[string]Doc{
	"yield": {"yield()", "Pause until the next game tick.", "暂停到下一个游戏 tick。"},
	"sleep": {"sleep(sec)", "Pause for sec seconds.", "暂停 sec 秒。"},
	"hcf":   {"hcf()", "Halt and catch fire (destroys the chip).", "停机并起火（会炸毁芯片）。"},

	"abs":   {"abs(x)", "Absolute value.", "绝对值。"},
	"sgn":   {"sgn(x)", "Sign of x: -1, 0 or 1.", "x 的符号：-1、0 或 1。"},
	"sqrt":  {"sqrt(x)", "Square root.", "平方根。"},
	"exp":   {"exp(x)", "e raised to x.", "e 的 x 次幂。"},
	"log":   {"log(x)", "Natural logarithm.", "自然对数。"},
	"floor": {"floor(x)", "Round down.", "向下取整。"},
	"ceil":  {"ceil(x)", "Round up.", "向上取整。"},
	"round": {"round(x)", "Round to nearest.", "四舍五入。"},
	"trunc": {"trunc(x)", "Drop the fractional part.", "去掉小数部分。"},
	"rand":  {"rand()", "Random value in [0, 1).", "[0, 1) 的随机值。"},
	"sin":   {"sin(x)", "Sine (radians).", "正弦（弧度）。"},
	"cos":   {"cos(x)", "Cosine (radians).", "余弦（弧度）。"},
	"tan":   {"tan(x)", "Tangent (radians).", "正切（弧度）。"},
	"asin":  {"asin(x)", "Arc sine.", "反正弦。"},
	"acos":  {"acos(x)", "Arc cosine.", "反余弦。"},
	"atan":  {"atan(x)", "Arc tangent.", "反正切。"},
	"atan2": {"atan2(y, x)", "Arc tangent of y/x using signs.", "按 y/x 的符号求反正切。"},
	"isNaN": {"isNaN(x)", "1 if x is NaN else 0.", "x 是 NaN 时为 1，否则 0。"},

	"pow": {"pow(a, b)", "a to the power b.", "a 的 b 次幂。"},
	"min": {"min(a, b)", "Smaller of a and b.", "a、b 中较小者。"},
	"max": {"max(a, b)", "Larger of a and b.", "a、b 中较大者。"},
	"sla": {"sla(a, b)", "Arithmetic left shift.", "算术左移。"},
	"srl": {"srl(a, b)", "Logical right shift.", "逻辑右移。"},
	"rol": {"rol(a, b)", "Rotate left.", "循环左移。"},
	"ror": {"ror(a, b)", "Rotate right.", "循环右移。"},

	"ext": {"ext(source, offset, length)", "Extract a bit field.", "提取位域。"},
	"ins": {"ins(field, offset, length)", "Insert a bit field.", "插入位域。"},

	"clamp": {"clamp(x, lo, hi)", "Clamp x to [lo, hi].", "把 x 限制在 [lo, hi]。"},
	"lerp":  {"lerp(a, b, t)", "Linear interpolation from a to b by t.", "按 t 在 a、b 间线性插值。"},

	"push": {"push(x)", "Push x onto the stack.", "把 x 压入栈。"},
	"pop":  {"pop()", "Pop the top of the stack.", "弹出栈顶。"},
	"peek": {"peek()", "Read the top of the stack.", "读取栈顶（不弹出）。"},
	"poke": {"poke(addr, v)", "Write v to stack address addr.", "把 v 写到栈地址 addr。"},

	"approx":     {"approx(a, b, tol)", "1 if a ≈ b within tol.", "在容差 tol 内 a ≈ b 时为 1。"},
	"approxZero": {"approxZero(a, tol)", "1 if a ≈ 0 within tol.", "在容差 tol 内 a ≈ 0 时为 1。"},

	"isSet":   {"isSet(dev)", "1 if the device is connected.", "设备已连接时为 1。"},
	"isUnset": {"isUnset(dev)", "1 if the device is not connected.", "设备未连接时为 1。"},
	"rmap":    {"rmap(dev, hash)", "Map a reagent hash to the prefab the device needs.", "把反应物 hash 映射为设备所需的 prefab。"},
	"get":     {"get(dev, addr)", "Read a device memory address.", "读取设备内存地址。"},
	"put":     {"put(dev, addr, v)", "Write a device memory address.", "写入设备内存地址。"},
	"clr":     {"clr(dev)", "Clear a device.", "清空设备。"},
	"getd":    {"getd(id, addr)", "Read a device address by id.", "按设备 id 读取地址。"},
	"putd":    {"putd(id, addr, v)", "Write a device address by id.", "按设备 id 写入地址。"},

	"read":         {"read(dev, lt)", "Read a device logic type chosen at runtime.", "读取运行期决定的 logic type。"},
	"write":        {"write(dev, lt, v)", "Write a device logic type chosen at runtime.", "写入运行期决定的 logic type。"},
	"isLoadValid":  {"isLoadValid(dev, \"lt\")", "Condition: device supports reading lt.", "条件：设备支持读取该 logic type。"},
	"isStoreValid": {"isStoreValid(dev, \"lt\")", "Condition: device supports writing lt.", "条件：设备支持写入该 logic type。"},
}

// BatchDocs documents the batch.* methods (keyed by method name).
var BatchDocs = map[string]Doc{
	"read":     {"batch.read(typeHash, logic, mode)", "Aggregate a logic type over devices of one type.", "对同类型设备聚合读取一个 logic type。"},
	"readName": {"batch.readName(typeHash, nameHash, logic, mode)", "Aggregate over devices matching a name hash.", "按名字 hash 筛选后聚合读取。"},
	"readSlot": {"batch.readSlot(typeHash, slot, logic, mode)", "Aggregate a slot property.", "聚合读取槽位属性。"},
	"readNameSlot": {"batch.readNameSlot(typeHash, nameHash, slot, logic, mode)",
		"Aggregate a slot property by name hash.", "按名字 hash + 槽位聚合读取。"},
	"write":     {"batch.write(typeHash, logic, value)", "Write a logic type to all devices of one type.", "对所有同类型设备写入一个 logic type。"},
	"writeName": {"batch.writeName(typeHash, nameHash, logic, value)", "Write to devices matching a name hash.", "按名字 hash 筛选后写入。"},
	"writeSlot": {"batch.writeSlot(typeHash, slot, logic, value)", "Write a slot property.", "写入槽位属性。"},
}

// LogicTypeDocs documents the most common device logic types.
var LogicTypeDocs = map[string]Doc{
	"On":                {"On", "Device on/off (1/0).", "设备开关（1/0）。"},
	"Open":              {"Open", "Open/closed (1/0).", "开/关（1/0）。"},
	"Mode":              {"Mode", "Operating mode.", "运行模式。"},
	"Setting":           {"Setting", "Generic set point.", "通用设定值。"},
	"Activate":          {"Activate", "Device active (1/0).", "设备激活（1/0）。"},
	"Lock":              {"Lock", "Locked (1/0).", "锁定（1/0）。"},
	"Temperature":       {"Temperature", "Temperature in Kelvin.", "温度（开尔文）。"},
	"Pressure":          {"Pressure", "Pressure in kPa.", "压力（kPa）。"},
	"PressureExternal":  {"PressureExternal", "External pressure.", "外部压力。"},
	"PressureInternal":  {"PressureInternal", "Internal pressure.", "内部压力。"},
	"PressureOutput":    {"PressureOutput", "Output-side pressure.", "出口压力。"},
	"PressureInput":     {"PressureInput", "Input-side pressure.", "入口压力。"},
	"Ratio":             {"Ratio", "A 0..1 ratio.", "0..1 的比例。"},
	"Charge":            {"Charge", "Stored energy.", "存储的电量。"},
	"Error":             {"Error", "Error state (1/0).", "错误状态（1/0）。"},
	"Filtration":        {"Filtration", "Filtration on/off.", "过滤开关。"},
	"AirRelease":        {"AirRelease", "Release air.", "释放空气。"},
	"Rpm":               {"Rpm", "Rotational speed.", "转速。"},
	"Stress":            {"Stress", "Mechanical stress.", "机械应力。"},
	"Throttle":          {"Throttle", "Throttle 0..100.", "节流 0..100。"},
	"CombustionLimiter": {"CombustionLimiter", "Combustion limiter.", "燃烧限制。"},
	"ExportCount":       {"ExportCount", "Items exported since last clear.", "自上次清零以来的导出数量。"},
	"Occupied":          {"Occupied", "Slot occupied (1/0).", "槽位是否占用（1/0）。"},
	"Quantity":          {"Quantity", "Stack quantity.", "堆叠数量。"},
	"Mature":            {"Mature", "Plant matured (1/0).", "植物成熟（1/0）。"},
	"Harvest":           {"Harvest", "Harvest a plant (write 1).", "收获植物（写 1）。"},
	"Damage":            {"Damage", "Damage level.", "损坏程度。"},
	"PositionX":         {"PositionX", "World X coordinate.", "世界坐标 X。"},
	"PositionY":         {"PositionY", "World Y coordinate.", "世界坐标 Y。"},
	"PositionZ":         {"PositionZ", "World Z coordinate.", "世界坐标 Z。"},
	"Horizontal":        {"Horizontal", "Horizontal angle.", "水平角度。"},
	"Vertical":          {"Vertical", "Vertical angle.", "垂直角度。"},
}

// KeywordDocs documents the language keywords and low-level control flow.
var KeywordDocs = map[string]Doc{
	"func":     {"func name(params) type { ... }", "Function; fully inlined, no recursion.", "函数；全内联，不支持递归。"},
	"const":    {"const Name = value", "Compile-time constant.", "编译期常量。"},
	"var":      {"var x = value", "Local variable.", "局部变量。"},
	"if":       {"if cond { ... } else { ... }", "Conditional branch.", "条件分支。"},
	"for":      {"for cond { ... }", "Loop.", "循环。"},
	"switch":   {"switch x { case 1: ... }", "Multi-way branch.", "多分支。"},
	"return":   {"return expr", "Return from a function.", "从函数返回。"},
	"break":    {"break", "Leave the loop.", "跳出循环。"},
	"continue": {"continue", "Next loop iteration.", "进入下一次循环。"},
	"label":    {"label name:", "Jump target (low-level).", "跳转目标（底层）。"},
	"goto":     {"goto name", "Unconditional jump (low-level).", "无条件跳转（底层）。"},
	"call":     {"call name", "Call a label, saving the return address.", "调用标签并保存返回地址。"},
	"ret":      {"ret", "Return to the saved address.", "返回保存的地址。"},
}
