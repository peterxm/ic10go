package builtin

// Sem describes a builtin's observable semantics. It is the single source of
// truth for the optimizer and the lowerer: adding a builtin must classify it
// here (see TestBuiltinSemantics), so a side effect cannot be forgotten.
type Sem struct {
	SideEffect bool // changes observable state
	ReadsDev   bool // reads a device or the stack
	WritesDev  bool // writes a device or the stack
	Barrier    bool // blocks moving other device/stack accesses across it
	DeviceArg  int  // index of the device argument, or -1 for none
}

// noDevice is the zero value used for builtins without a device argument.
var noDevice = Sem{DeviceArg: -1}

// semantics classifies every builtin, including the few lowered specially
// (write, readDev, setIreg, ...) that are not in Funcs.
var semantics = map[string]Sem{
	// Control.
	"yield": {SideEffect: true, Barrier: true, DeviceArg: -1},
	"sleep": {SideEffect: true, Barrier: true, DeviceArg: -1},
	"hcf":   {SideEffect: true, Barrier: true, DeviceArg: -1},

	// Stack.
	"push": {SideEffect: true, WritesDev: true, Barrier: true, DeviceArg: -1},
	"pop":  {SideEffect: true, WritesDev: true, Barrier: true, DeviceArg: -1},
	"peek": {ReadsDev: true, DeviceArg: -1},
	"poke": {SideEffect: true, WritesDev: true, Barrier: true, DeviceArg: -1},

	// Device stack / pin.
	"isSet":           {ReadsDev: true, DeviceArg: 0},
	"isUnset":         {ReadsDev: true, DeviceArg: 0},
	"rmap":            {ReadsDev: true, DeviceArg: 0},
	"get":             {ReadsDev: true, DeviceArg: 0},
	"put":             {SideEffect: true, WritesDev: true, Barrier: true, DeviceArg: 0},
	"clr":             {SideEffect: true, WritesDev: true, Barrier: true, DeviceArg: 0},
	"clrById":         {SideEffect: true, WritesDev: true, Barrier: true, DeviceArg: -1},
	"getd":            {ReadsDev: true, DeviceArg: -1},
	"putd":            {SideEffect: true, WritesDev: true, Barrier: true, DeviceArg: -1},
	"readReagent":     {ReadsDev: true, DeviceArg: 0},
	"readReagentById": {ReadsDev: true, DeviceArg: -1},

	// Dynamic logic type / device register (lowered specially).
	"read":         {ReadsDev: true, DeviceArg: 0},
	"write":        {SideEffect: true, WritesDev: true, Barrier: true, DeviceArg: 0},
	"readDev":      {ReadsDev: true, DeviceArg: -1},
	"writeDev":     {SideEffect: true, WritesDev: true, Barrier: true, DeviceArg: -1},
	"readById":     {ReadsDev: true, DeviceArg: -1},
	"writeById":    {SideEffect: true, WritesDev: true, Barrier: true, DeviceArg: -1},
	"readDevSlot":  {ReadsDev: true, DeviceArg: -1},
	"writeDevSlot": {SideEffect: true, WritesDev: true, Barrier: true, DeviceArg: -1},
	"setIreg":      {SideEffect: true, WritesDev: true, Barrier: true, DeviceArg: -1},
	"ireg":         {ReadsDev: true, DeviceArg: -1},
	"jump":         {SideEffect: true, Barrier: true, DeviceArg: -1},
}

// pureBuiltins are the builtins with no observable side effect and no device
// access. They are listed explicitly so that adding a builtin forces a
// classification decision (TestBuiltinSemantics).
var pureBuiltins = []string{
	"abs", "sgn", "sqrt", "exp", "log", "floor", "ceil", "round", "trunc", "rand",
	"sin", "cos", "tan", "asin", "acos", "atan", "isNaN",
	"pow", "atan2", "min", "max", "sla", "srl", "rol", "ror",
	"ext", "ins", "clamp", "lerp",
	"approx", "approxZero", "notApprox", "notApproxZero", "isNotNaN", "logicalNor",
}

func init() {
	for _, n := range pureBuiltins {
		semantics[n] = noDevice
	}
}

// SemOf returns the semantics of a builtin. Unknown names are treated as pure
// with no device argument.
func SemOf(name string) Sem {
	if s, ok := semantics[name]; ok {
		return s
	}
	return noDevice
}
