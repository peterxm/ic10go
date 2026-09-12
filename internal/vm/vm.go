// Package vm is a small IC10 interpreter used only for testing the compiler.
// It models registers, the stack, device ports and batched device access well
// enough to run compiler output and assert on resulting device state.
package vm

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"ic10go/internal/builtin"
	"ic10go/internal/ic10asm"
)

const (
	regCount  = 16
	regRA     = 16
	regSP     = 17
	numRegs   = 18
	stackSize = 512
	eps       = 2.220446049250313e-16 // float64 machine epsilon
)

// Device is a mock device with logic values, slots, a stack and a pin state.
type Device struct {
	Name     string
	Hash     uint32
	NameHash uint32
	Set      bool
	Values   map[string]float64
	Slots    map[int]map[string]float64
	Stack    []float64
	// NoStore lists logic types the device refuses to store (bdnvs).
	NoStore map[string]bool
}

func newDevice(name string) *Device {
	return &Device{
		Name:    name,
		Values:  map[string]float64{},
		Slots:   map[int]map[string]float64{},
		Stack:   make([]float64, stackSize),
		NoStore: map[string]bool{},
	}
}

// Program is a parsed IC10 program. Instrs is indexed by line number.
type Program struct {
	Instrs  []*Instr
	Labels  map[string]int
	Symbols map[string]string
}

// Instr is a single parsed instruction.
type Instr struct {
	Op   string
	Args []string
	Line int
}

// Machine executes a program.
type Machine struct {
	Regs    [numRegs]float64
	Stack   []float64
	Program *Program
	PC      int
	Clock   float64
	Ticks   int
	Halted  bool
	Devices map[string]*Device
	order   []*Device
	// Trace, when non-nil, receives one line per executed instruction.
	Trace io.Writer
	// OnWrite, when non-nil, is called for every device logic write.
	OnWrite func(dev, logic string, v float64)
	// LogicByID maps IC10 logicType enum values to names, used to resolve
	// runtime (register) logic type operands.
	LogicByID map[int]string
}

// New returns an empty machine.
func New() *Machine {
	m := &Machine{Devices: map[string]*Device{}, Stack: make([]float64, stackSize), LogicByID: map[int]string{}}
	for id, name := range builtin.LogicTypeNames {
		m.LogicByID[id] = name
	}
	m.Regs[regSP] = 0
	return m
}

// logicName resolves a device logic type operand, which may be a name or a
// register holding a logicType enum value.
func (m *Machine) logicName(s string) string {
	if i, ok := regIndex(s); ok {
		return m.logicNameByID(int(m.Regs[i]))
	}
	if i, ok := m.indirectIndex(s); ok {
		return m.logicNameByID(int(m.Regs[i]))
	}
	return s
}

func (m *Machine) logicNameByID(id int) string {
	if name, ok := m.LogicByID[id]; ok {
		return name
	}
	return fmt.Sprintf("logic#%d", id)
}

// Device returns the device with the given port name, creating it if needed.
func (m *Machine) Device(name string) *Device {
	if d, ok := m.Devices[name]; ok {
		return d
	}
	d := newDevice(name)
	if name == "db" && m.Stack != nil {
		// On a standard IC host the housing's stack is the chip's own stack:
		// get/put db and push/pop/poke/peek address the same memory.
		d.Stack = m.Stack
	}
	m.Devices[name] = d
	m.order = append(m.order, d)
	return d
}

// dev resolves a device operand, which may be a plain port name or an IC10
// device register such as dr15 ("d" followed by a register holding the port
// index).
func (m *Machine) dev(s string) *Device { return m.Device(m.devName(s)) }

// devName resolves device-register operands (dr15, drr0) to a port name and
// returns other operands unchanged.
func (m *Machine) devName(s string) string {
	if len(s) < 3 || s[0] != 'd' || s[1] != 'r' {
		return s
	}
	base := s[1:]
	var idx int
	switch {
	case strings.HasPrefix(base, "rr"):
		i, ok := m.indirectIndex(base)
		if !ok {
			return s
		}
		idx = int(m.Regs[i])
	default:
		i, ok := regIndex(base)
		if !ok {
			return s
		}
		idx = int(m.Regs[i])
	}
	return "d" + strconv.Itoa(idx)
}

// Set sets a logic value on a device port.
func (m *Machine) Set(name, logic string, v float64) {
	m.Device(name).Values[logic] = v
}

// Get reads a logic value from a device port.
func (m *Machine) Get(name, logic string) float64 {
	return m.Device(name).Values[logic]
}

// SetSlot sets a slot logic value.
func (m *Machine) SetSlot(name string, slot int, logic string, v float64) {
	d := m.Device(name)
	if d.Slots[slot] == nil {
		d.Slots[slot] = map[string]float64{}
	}
	d.Slots[slot][logic] = v
}

// GetSlot reads a slot logic value.
func (m *Machine) GetSlot(name string, slot int, logic string) float64 {
	d := m.Device(name)
	if d.Slots[slot] == nil {
		return 0
	}
	return d.Slots[slot][logic]
}

// Load parses IC10 source into the machine.
func (m *Machine) Load(src string) error {
	prog, err := Parse(src)
	if err != nil {
		return err
	}
	m.Program = prog
	m.PC = 0
	m.Halted = false
	return nil
}

// Run executes at most maxSteps instructions. It returns ErrStepLimit if the
// limit is reached before the program ends.
func (m *Machine) Run(maxSteps int) error {
	if m.Program == nil {
		return fmt.Errorf("vm: no program loaded")
	}
	executed := 0
	for executed < maxSteps {
		if m.Halted || m.PC < 0 || m.PC >= len(m.Program.Instrs) {
			return nil
		}
		ins := m.Program.Instrs[m.PC]
		if ins == nil {
			// Labels, comments and alias/define lines are not instructions and
			// do not consume the step budget.
			m.PC++
			continue
		}
		next := m.PC + 1
		if m.Trace != nil {
			fmt.Fprintf(m.Trace, "%4d  %s %s\n", m.PC, ins.Op, strings.Join(ins.Args, " "))
		}
		if err := m.exec(ins, &next); err != nil {
			return fmt.Errorf("vm: line %d: %w", m.PC, err)
		}
		m.PC = next
		executed++
	}
	return ErrStepLimit
}

// ErrStepLimit indicates the execution budget was exhausted.
var ErrStepLimit = fmt.Errorf("vm: step limit reached")

// Parse parses IC10 source into a Program.
func Parse(src string) (*Program, error) {
	lines := strings.Split(src, "\n")
	prog := &Program{
		Instrs:  make([]*Instr, len(lines)),
		Labels:  map[string]int{},
		Symbols: map[string]string{},
	}
	for k, v := range builtin.EnumConstants {
		prog.Symbols[k] = strconv.FormatFloat(v, 'g', -1, 64)
	}
	for name, id := range builtin.LogicTypeIDs {
		prog.Symbols["LogicType."+name] = strconv.Itoa(id)
	}
	resolve := func(s string) string {
		for i := 0; i < 10; i++ {
			v, ok := prog.Symbols[s]
			if !ok {
				return s
			}
			s = v
		}
		return s
	}
	for i, raw := range lines {
		line := strings.TrimSpace(ic10asm.StripComment(raw))
		if line == "" {
			continue
		}
		if ic10asm.IsLabel(line) {
			prog.Labels[strings.TrimSuffix(line, ":")] = i
			continue
		}
		fields := ic10asm.Tokenize(line)
		op := strings.ToLower(fields[0])
		switch op {
		case "alias", "define":
			if len(fields) >= 3 {
				prog.Symbols[fields[1]] = strings.Join(fields[2:], " ")
			}
			continue
		}
		args := make([]string, 0, len(fields)-1)
		for _, f := range fields[1:] {
			args = append(args, resolve(f))
		}
		prog.Instrs[i] = &Instr{Op: op, Args: args, Line: i}
	}
	return prog, nil
}

// ---------------------------------------------------------------------------
// Operand resolution
// ---------------------------------------------------------------------------

func regIndex(s string) (int, bool) {
	switch s {
	case "ra":
		return regRA, true
	case "sp":
		return regSP, true
	}
	if len(s) >= 2 && s[0] == 'r' {
		if n, err := strconv.Atoi(s[1:]); err == nil && n >= 0 && n < regCount {
			return n, true
		}
	}
	return 0, false
}

func parseNum(s string) (float64, bool) {
	switch s {
	case "nan":
		return math.NaN(), true
	case "pinf":
		return math.Inf(1), true
	case "ninf":
		return math.Inf(-1), true
	}
	if strings.HasPrefix(s, "$") {
		if v, err := strconv.ParseUint(s[1:], 16, 64); err == nil {
			return float64(v), true
		}
	}
	if strings.HasPrefix(s, "%") {
		if v, err := strconv.ParseUint(strings.ReplaceAll(s[1:], "_", ""), 2, 64); err == nil {
			return float64(v), true
		}
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return v, true
	}
	return 0, false
}

func (m *Machine) num(s string) (float64, error) {
	if i, ok := regIndex(s); ok {
		return m.Regs[i], nil
	}
	if i, ok := m.indirectIndex(s); ok {
		return m.Regs[i], nil
	}
	if v, ok := parseNum(s); ok {
		return v, nil
	}
	return 0, fmt.Errorf("not a number or register: %q", s)
}

func (m *Machine) reg(s string) (int, error) {
	if i, ok := regIndex(s); ok {
		return i, nil
	}
	if i, ok := m.indirectIndex(s); ok {
		return i, nil
	}
	return 0, fmt.Errorf("not a register: %q", s)
}

// indirectIndex resolves an IC10 indirect register operand (rrN) to a register
// index. Multi-level indirection is not supported.
func (m *Machine) indirectIndex(s string) (int, bool) {
	if !strings.HasPrefix(s, "rr") {
		return 0, false
	}
	base, ok := regIndex(s[1:])
	if !ok {
		return 0, false
	}
	idx := int(m.Regs[base])
	if idx < 0 || idx >= regCount {
		return 0, false
	}
	return idx, true
}

func (m *Machine) target(s string) (int, error) {
	if line, ok := m.Program.Labels[s]; ok {
		return line, nil
	}
	if i, ok := regIndex(s); ok {
		return int(m.Regs[i]), nil
	}
	if v, ok := parseNum(s); ok {
		return int(v), nil
	}
	return 0, fmt.Errorf("not a branch target: %q", s)
}

// ---------------------------------------------------------------------------
// Execution
// ---------------------------------------------------------------------------

func (m *Machine) exec(ins *Instr, next *int) error {
	switch ins.Op {
	case "yield":
		m.Ticks++
		return nil
	case "sleep":
		v, err := m.num(ins.Args[0])
		if err != nil {
			return err
		}
		m.Clock += v
		m.Ticks++
		return nil
	case "hcf":
		m.Halted = true
		return nil
	case "j":
		t, err := m.target(ins.Args[0])
		if err != nil {
			return err
		}
		*next = t
		return nil
	case "jal":
		t, err := m.target(ins.Args[0])
		if err != nil {
			return err
		}
		m.Regs[regRA] = float64(ins.Line + 1)
		*next = t
		return nil
	case "jr":
		v, err := m.num(ins.Args[0])
		if err != nil {
			return err
		}
		*next = ins.Line + int(v)
		return nil
	}

	if isBranch(ins.Op) {
		return m.execBranch(ins, next)
	}
	return m.execOp(ins)
}

func (m *Machine) execOp(ins *Instr) error {
	a := ins.Args
	switch ins.Op {
	case "move":
		return m.setDst(a[0], mustNum(m, a[1]))
	case "select":
		dst, _ := m.reg(a[0])
		cond, _ := m.num(a[1])
		if cond != 0 {
			m.Regs[dst] = mustNum(m, a[2])
		} else {
			m.Regs[dst] = mustNum(m, a[3])
		}
		return nil
	case "add", "sub", "mul", "div", "mod", "pow", "atan2", "min", "max":
		return m.binOp(ins.Op, a[0], a[1], a[2])
	case "and", "or", "xor", "sll", "sra", "srl", "sla", "rol", "ror":
		return m.bitOp(ins.Op, a[0], a[1], a[2])
	case "not":
		return m.setDst(a[0], float64(^int64(mustNum(m, a[1]))))
	case "ext":
		dst, _ := m.reg(a[0])
		src := int64(mustNum(m, a[1]))
		off := uint(mustNum(m, a[2]))
		length := uint(mustNum(m, a[3]))
		m.Regs[dst] = float64((src >> off) & bitMask(length))
		return nil
	case "ins":
		dst, _ := m.reg(a[0])
		base := int64(m.Regs[dst])
		field := int64(mustNum(m, a[1]))
		off := uint(mustNum(m, a[2]))
		length := uint(mustNum(m, a[3]))
		mask := bitMask(length) << off
		m.Regs[dst] = float64((base & ^mask) | ((field << off) & mask))
		return nil
	case "neg":
		return m.setDst(a[0], -mustNum(m, a[1]))
	case "abs", "sgn", "sqrt", "exp", "log", "floor", "ceil", "round", "trunc",
		"sin", "cos", "tan", "asin", "acos", "atan":
		return m.unOp(ins.Op, a[0], a[1])
	case "rand":
		return m.setDst(a[0], 0)
	case "clamp":
		v := mustNum(m, a[1])
		lo := mustNum(m, a[2])
		hi := mustNum(m, a[3])
		return m.setDst(a[0], math.Min(math.Max(v, lo), hi))
	case "lerp":
		x := mustNum(m, a[1])
		y := mustNum(m, a[2])
		t := mustNum(m, a[3])
		t = math.Min(math.Max(t, 0), 1)
		return m.setDst(a[0], x+(y-x)*t)
	case "seq", "sne", "slt", "sle", "sgt", "sge", "sap", "sna", "sapz", "snaz":
		return m.cmpOp(ins.Op, a)
	case "seqz", "snez", "sltz", "slez", "sgtz", "sgez", "snan", "snanz":
		return m.cmpZeroOp(ins.Op, a[0], a[1])
	case "l":
		dst, _ := m.reg(a[0])
		m.Regs[dst] = m.dev(a[1]).Values[m.logicName(a[2])]
		return nil
	case "s":
		logic := m.logicName(a[1])
		v := mustNum(m, a[2])
		m.dev(a[0]).Values[logic] = v
		if m.OnWrite != nil {
			m.OnWrite(m.devName(a[0]), logic, v)
		}
		return nil
	case "ls":
		dst, _ := m.reg(a[0])
		slot, _ := m.num(a[2])
		m.Regs[dst] = m.GetSlot(m.devName(a[1]), int(slot), a[3])
		return nil
	case "ss":
		slot, _ := m.num(a[1])
		m.SetSlot(m.devName(a[0]), int(slot), a[2], mustNum(m, a[3]))
		return nil
	case "lb", "lbn", "lbs", "lbns":
		return m.batchLoad(ins.Op, a)
	case "sb", "sbn", "sbs":
		return m.batchStore(ins.Op, a)
	case "push":
		v := mustNum(m, a[0])
		sp := int(m.Regs[regSP])
		if sp < 0 || sp >= stackSize {
			return fmt.Errorf("stack overflow")
		}
		m.Stack[sp] = v
		m.Regs[regSP] = float64(sp + 1)
		return nil
	case "pop":
		sp := int(m.Regs[regSP]) - 1
		if sp < 0 {
			return fmt.Errorf("stack underflow")
		}
		m.Regs[regSP] = float64(sp)
		return m.setDst(a[0], m.Stack[sp])
	case "peek":
		sp := int(m.Regs[regSP]) - 1
		if sp < 0 {
			return fmt.Errorf("stack underflow")
		}
		return m.setDst(a[0], m.Stack[sp])
	case "poke":
		addr, _ := m.num(a[0])
		m.Stack[int(addr)] = mustNum(m, a[1])
		return nil
	case "sdse":
		dst, _ := m.reg(a[0])
		if m.dev(a[1]).Set {
			m.Regs[dst] = 1
		} else {
			m.Regs[dst] = 0
		}
		return nil
	case "sdns":
		dst, _ := m.reg(a[0])
		if !m.dev(a[1]).Set {
			m.Regs[dst] = 1
		} else {
			m.Regs[dst] = 0
		}
		return nil
	case "rmap":
		return m.setDst(a[0], 0)
	case "get":
		dst, _ := m.reg(a[0])
		addr, _ := m.num(a[2])
		m.Regs[dst] = m.dev(a[1]).Stack[int(addr)]
		return nil
	case "put":
		addr, _ := m.num(a[1])
		m.dev(a[0]).Stack[int(addr)] = mustNum(m, a[2])
		return nil
	case "getd":
		dst, _ := m.reg(a[0])
		addr, _ := m.num(a[2])
		m.Regs[dst] = m.deviceByID(mustNum(m, a[1])).Stack[int(addr)]
		return nil
	case "putd":
		addr, _ := m.num(a[1])
		m.deviceByID(mustNum(m, a[0])).Stack[int(addr)] = mustNum(m, a[2])
		return nil
	case "clr":
		d := m.dev(a[0])
		if d.Name == "db" {
			// Keep the shared backing array so push/pop see the clear too.
			for i := range d.Stack {
				d.Stack[i] = 0
			}
			return nil
		}
		d.Stack = make([]float64, stackSize)
		return nil
	}
	return fmt.Errorf("unsupported instruction %q", ins.Op)
}

func (m *Machine) deviceByID(id float64) *Device {
	for _, d := range m.order {
		if v, ok := d.Values["ReferenceId"]; ok && v == id {
			return d
		}
	}
	return m.Device("db")
}

func (m *Machine) setDst(reg string, v float64) error {
	i, err := m.reg(reg)
	if err != nil {
		return err
	}
	m.Regs[i] = v
	return nil
}

func mustNum(m *Machine, s string) float64 {
	v, err := m.num(s)
	if err != nil {
		return 0
	}
	return v
}

func (m *Machine) binOp(op, dst, x, y string) error {
	a := mustNum(m, x)
	b := mustNum(m, y)
	var r float64
	switch op {
	case "add":
		r = a + b
	case "sub":
		r = a - b
	case "mul":
		r = a * b
	case "div":
		r = a / b
	case "mod":
		r = ic10Mod(a, b)
	case "pow":
		r = math.Pow(a, b)
	case "atan2":
		r = math.Atan2(a, b)
	case "min":
		r = math.Min(a, b)
	case "max":
		r = math.Max(a, b)
	}
	return m.setDst(dst, r)
}

func (m *Machine) bitOp(op, dst, x, y string) error {
	a := int64(mustNum(m, x))
	b := uint(mustNum(m, y)) & 63
	var r int64
	switch op {
	case "and":
		r = a & int64(mustNum(m, y))
	case "or":
		r = a | int64(mustNum(m, y))
	case "xor":
		r = a ^ int64(mustNum(m, y))
	case "sll":
		r = a << b
	case "sra":
		r = a >> b
	case "srl":
		r = int64(uint64(a) >> b)
	case "sla":
		r = a << b
	case "rol":
		r = int64((uint64(a) << b) | (uint64(a) >> (64 - b)))
	case "ror":
		r = int64((uint64(a) >> b) | (uint64(a) << (64 - b)))
	}
	return m.setDst(dst, float64(r))
}

func (m *Machine) unOp(op, dst, x string) error {
	a := mustNum(m, x)
	var r float64
	switch op {
	case "abs":
		r = math.Abs(a)
	case "sgn":
		switch {
		case a < 0:
			r = -1
		case a > 0:
			r = 1
		default:
			r = 0
		}
	case "sqrt":
		r = math.Sqrt(a)
	case "exp":
		r = math.Exp(a)
	case "log":
		r = math.Log(a)
	case "floor":
		r = math.Floor(a)
	case "ceil":
		r = math.Ceil(a)
	case "round":
		r = math.Round(a)
	case "trunc":
		r = math.Trunc(a)
	case "sin":
		r = math.Sin(a)
	case "cos":
		r = math.Cos(a)
	case "tan":
		r = math.Tan(a)
	case "asin":
		r = math.Asin(a)
	case "acos":
		r = math.Acos(a)
	case "atan":
		r = math.Atan(a)
	}
	return m.setDst(dst, r)
}

func (m *Machine) cmpOp(op string, a []string) error {
	x := mustNum(m, a[1])
	y := mustNum(m, a[2])
	var r bool
	switch op {
	case "seq":
		r = x == y
	case "sne":
		r = x != y
	case "slt":
		r = x < y
	case "sle":
		r = x <= y
	case "sgt":
		r = x > y
	case "sge":
		r = x >= y
	case "sap":
		tol := mustNum(m, a[3])
		r = math.Abs(x-y) <= math.Max(tol*math.Max(math.Abs(x), math.Abs(y)), eps*8)
	case "sna":
		tol := mustNum(m, a[3])
		r = math.Abs(x-y) > math.Max(tol*math.Max(math.Abs(x), math.Abs(y)), eps*8)
	case "sapz":
		tol := mustNum(m, a[2])
		r = math.Abs(x) <= math.Max(tol*math.Abs(x), eps*8)
	case "snaz":
		tol := mustNum(m, a[2])
		r = math.Abs(x) > math.Max(tol*math.Abs(x), eps*8)
	}
	return m.setDst(a[0], boolNum(r))
}

func (m *Machine) cmpZeroOp(op, dst, x string) error {
	v := mustNum(m, x)
	var r bool
	switch op {
	case "seqz":
		r = v == 0
	case "snez":
		r = v != 0
	case "sltz":
		r = v < 0
	case "slez":
		r = v <= 0
	case "sgtz":
		r = v > 0
	case "sgez":
		r = v >= 0
	case "snan":
		r = math.IsNaN(v)
	case "snanz":
		r = !math.IsNaN(v)
	}
	return m.setDst(dst, boolNum(r))
}

func boolNum(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func ic10Mod(x, y float64) float64 {
	r := math.Mod(x, y)
	if r != 0 && (r < 0) != (y < 0) {
		r += y
	}
	return r
}

func bitMask(length uint) int64 {
	if length >= 64 {
		return -1
	}
	return (int64(1) << length) - 1
}

// ---------------------------------------------------------------------------
// Branching
// ---------------------------------------------------------------------------

func isBranch(op string) bool {
	_, _, _, ok := ic10asm.BranchInfo(op)
	return ok
}

func (m *Machine) execBranch(ins *Instr, next *int) error {
	cond, relative, withRA, ok := ic10asm.BranchInfo(ins.Op)
	if !ok {
		return fmt.Errorf("unsupported branch %q", ins.Op)
	}
	args := ins.Args
	var targetArg string
	var take bool
	switch {
	case ic10asm.IsBinaryCond(cond):
		take = branchTake(cond, mustNum(m, args[0]), mustNum(m, args[1]))
		targetArg = args[2]
	case cond == "ap" || cond == "na":
		x, y, tol := mustNum(m, args[0]), mustNum(m, args[1]), mustNum(m, args[2])
		take = math.Abs(x-y) <= math.Max(tol*math.Max(math.Abs(x), math.Abs(y)), eps*8)
		if cond == "na" {
			take = !take
		}
		targetArg = args[3]
	case cond == "apz" || cond == "naz":
		x, tol := mustNum(m, args[0]), mustNum(m, args[1])
		take = math.Abs(x) <= math.Max(tol*math.Abs(x), eps*8)
		if cond == "naz" {
			take = !take
		}
		targetArg = args[2]
	case cond == "dns" || cond == "dse":
		take = !m.dev(args[0]).Set
		if cond == "dse" {
			take = !take
		}
		targetArg = args[1]
	case cond == "dnvl" || cond == "dnvs":
		dev := m.dev(args[0])
		if cond == "dnvl" {
			_, ok := dev.Values[args[1]]
			take = !ok
		} else {
			take = dev.NoStore[args[1]]
		}
		targetArg = args[2]
	default: // unary comparison
		take = branchTake(cond, mustNum(m, args[0]), 0)
		targetArg = args[1]
	}
	if !take {
		return nil
	}
	if withRA {
		m.Regs[regRA] = float64(ins.Line + 1)
	}
	if relative {
		off, _ := m.num(targetArg)
		*next = ins.Line + int(off)
		return nil
	}
	t, err := m.target(targetArg)
	if err != nil {
		return err
	}
	*next = t
	return nil
}

func branchTake(cond string, x, y float64) bool {
	switch cond {
	case "eq":
		return x == y
	case "ne":
		return x != y
	case "lt":
		return x < y
	case "le":
		return x <= y
	case "gt":
		return x > y
	case "ge":
		return x >= y
	case "eqz":
		return x == 0
	case "nez":
		return x != 0
	case "ltz":
		return x < 0
	case "lez":
		return x <= 0
	case "gtz":
		return x > 0
	case "gez":
		return x >= 0
	case "nan":
		return math.IsNaN(x)
	}
	return false
}

// ---------------------------------------------------------------------------
// Batched device access
// ---------------------------------------------------------------------------

func (m *Machine) matching(hash, nameHash float64) []*Device {
	h := hashBits(hash)
	n := hashBits(nameHash)
	var out []*Device
	for _, d := range m.order {
		if d.Hash != 0 && h == d.Hash && (nameHash == 0 || n == d.NameHash) {
			out = append(out, d)
		}
	}
	return out
}

// HashOf normalises a signed or unsigned 32-bit hash value for device setup.
func HashOf(v int64) uint32 { return uint32(v) }

func hashBits(v float64) uint32 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return uint32(int64(v))
}

func (m *Machine) batchLoad(op string, a []string) error {
	dst, _ := m.reg(a[0])
	hash := mustNum(m, a[1])
	var nameHash float64
	var slot float64
	var logic string
	var mode float64
	switch op {
	case "lb":
		logic = a[2]
		mode = m.batchMode(a[3])
	case "lbn":
		nameHash = mustNum(m, a[2])
		logic = a[3]
		mode = m.batchMode(a[4])
	case "lbs":
		slot = mustNum(m, a[2])
		logic = a[3]
		mode = m.batchMode(a[4])
	case "lbns":
		nameHash = mustNum(m, a[2])
		slot = mustNum(m, a[3])
		logic = a[4]
		mode = m.batchMode(a[5])
	}
	devs := m.matching(hash, nameHash)
	var vals []float64
	for _, d := range devs {
		if op == "lbs" || op == "lbns" {
			if s, ok := d.Slots[int(slot)]; ok {
				vals = append(vals, s[logic])
			}
		} else {
			vals = append(vals, d.Values[logic])
		}
	}
	m.Regs[dst] = aggregate(int(mode), vals)
	return nil
}

// batchMode resolves a batch aggregation mode, which may be a name
// (Average/Sum/Minimum/Maximum) or a numeric expression.
func (m *Machine) batchMode(s string) float64 {
	switch s {
	case "Average":
		return 0
	case "Sum":
		return 1
	case "Minimum":
		return 2
	case "Maximum":
		return 3
	}
	return mustNum(m, s)
}

func aggregate(mode int, vals []float64) float64 {
	if len(vals) == 0 {
		switch mode {
		case 0:
			return math.NaN()
		case 3:
			return math.Inf(-1)
		default:
			return 0
		}
	}
	switch mode {
	case 0:
		sum := 0.0
		for _, v := range vals {
			sum += v
		}
		return sum / float64(len(vals))
	case 1:
		sum := 0.0
		for _, v := range vals {
			sum += v
		}
		return sum
	case 2:
		min := vals[0]
		for _, v := range vals[1:] {
			min = math.Min(min, v)
		}
		return min
	case 3:
		max := vals[0]
		for _, v := range vals[1:] {
			max = math.Max(max, v)
		}
		return max
	}
	return 0
}

func (m *Machine) batchStore(op string, a []string) error {
	hash := mustNum(m, a[0])
	var nameHash, slot float64
	var logic string
	var value float64
	switch op {
	case "sb":
		logic = a[1]
		value = mustNum(m, a[2])
	case "sbn":
		nameHash = mustNum(m, a[1])
		logic = a[2]
		value = mustNum(m, a[3])
	case "sbs":
		slot = mustNum(m, a[1])
		logic = a[2]
		value = mustNum(m, a[3])
	}
	for _, d := range m.matching(hash, nameHash) {
		if op == "sbs" {
			if d.Slots[int(slot)] == nil {
				d.Slots[int(slot)] = map[string]float64{}
			}
			d.Slots[int(slot)][logic] = value
		} else {
			d.Values[logic] = value
			if m.OnWrite != nil {
				m.OnWrite(d.Name, logic, value)
			}
		}
	}
	return nil
}
