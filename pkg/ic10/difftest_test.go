package ic10_test

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"ic10go/internal/vm"
	"ic10go/pkg/ic10"
)

// TestDifferentialRandom generates random .icg programs, compiles each with and
// without optimisation, runs both in the VM with the same device state and
// checks that they perform exactly the same sequence of device writes. This
// catches optimisation bugs (e.g. a wrong hoist or a bad common-subexpression
// merge) that unit tests miss.
func TestDifferentialRandom(t *testing.T) {
	for _, opts := range []ic10.Options{{}, {StableInsOrder: true}, {JumpTable: true}, {Fast: true}, {RelJump: true}} {
		name := "default"
		if opts.StableInsOrder {
			name = "stable-ins"
		}
		if opts.JumpTable {
			name = "jump-table"
		}
		if opts.Fast {
			name = "fast"
		}
		if opts.RelJump {
			name = "rel-jump"
		}
		t.Run(name, func(t *testing.T) {
			runDifferential(t, opts, 1500, genProgram, false)
		})
	}
}

// TestDifferentialData runs the same comparison on programs that use a `data`
// table: the loader is installed once, then each runtime reads the table.
func TestDifferentialData(t *testing.T) {
	runDifferential(t, ic10.Options{}, 300, genDataProgram, true)
}

func runDifferential(t *testing.T, opts ic10.Options, seeds int, genFunc func(int64) string, withData bool) {
	t.Helper()
	for seed := int64(0); seed < int64(seeds); seed++ {
		src := genFunc(seed)

		t.Setenv("IC10C_NO_OPT", "")
		t.Setenv("IC10C_NO_OUTLINE", "")
		optRes, diags, err := ic10.CompileResult("t.icg", []byte(src), opts)
		optCode := optRes.Code
		if err != nil && (strings.Contains(err.Error(), "exceeding") ||
			strings.Contains(err.Error(), "did not converge")) {
			continue // program too big/spilly; not a correctness issue
		}
		if diags.HasErrors() || err != nil {
			t.Fatalf("seed %d: optimized compile failed: %v %v\n%s", seed, diags.Diags, err, src)
		}

		t.Setenv("IC10C_NO_OPT", "1")
		t.Setenv("IC10C_NO_OUTLINE", "1")
		rawRes, diags, err := ic10.CompileResult("t.icg", []byte(src), opts)
		rawCode := rawRes.Code
		if err != nil && (strings.Contains(err.Error(), "exceeding") ||
			strings.Contains(err.Error(), "did not converge")) {
			continue
		}
		if diags.HasErrors() || err != nil {
			t.Fatalf("seed %d: unoptimized compile failed: %v %v\n%s", seed, diags.Diags, err, src)
		}

		init := deviceInit(seed)
		var want, got []string
		var wantErr, gotErr bool
		if withData {
			base, lerr := ic10.DataLoaderWithOptions("t.icg", []byte(src), opts)
			if lerr != nil {
				t.Fatalf("seed %d: loader failed: %v\n%s", seed, lerr, src)
			}
			want, wantErr = runWithLoader(rawCode, base+rawRes.Loader, init)
			got, gotErr = runWithLoader(optCode, base+optRes.Loader, init)
		} else {
			want, wantErr = runWrites(rawCode, init)
			got, gotErr = runWrites(optCode, init)
		}
		if strings.Join(got, "|") != strings.Join(want, "|") || gotErr != wantErr {
			t.Fatalf("seed %d: optimized and unoptimized differ (err %v vs %v)\n%s\n--- optimized ---\n%v\n--- unoptimized ---\n%v\n--- optimized code ---\n%s",
				seed, gotErr, wantErr, src, got, want, optCode)
		}
	}
}

// deviceInit returns a deterministic initial device state for a seed.
func deviceInit(seed int64) map[[2]string]float64 {
	rng := rand.New(rand.NewSource(seed ^ 0x5eed))
	init := map[[2]string]float64{}
	devs := []string{"d0", "d1", "d2", "d3", "d4", "d5", "db"}
	logics := []string{"Setting", "On", "Mode", "Temperature", "Pressure"}
	for _, d := range devs {
		for _, l := range logics {
			init[[2]string{d, l}] = float64(rng.Intn(21) - 10)
		}
	}
	return init
}

func runWrites(code string, init map[[2]string]float64) ([]string, bool) {
	m := vm.New()
	setup(m, init)
	var writes []string
	m.OnWrite = func(dev, logic string, v float64) {
		writes = append(writes, fmt.Sprintf("%s.%s=%v", dev, logic, v))
	}
	if err := m.Load(code); err != nil {
		return writes, true
	}
	err := m.Run(200000)
	writes = append(writes, deviceStacks(m)...)
	return writes, err != nil && err != vm.ErrStepLimit
}

// deviceStacks snapshots the d0..d2 memory stacks so a build that leaves
// different stack contents is caught by the differential comparison. `db` is
// excluded (its stack is the chip's own, used by spilling and the data
// segment).
func deviceStacks(m *vm.Machine) []string {
	var out []string
	for _, name := range []string{"d0", "d1", "d2"} {
		d := m.Device(name)
		for i, v := range d.Stack {
			if v != 0 {
				out = append(out, fmt.Sprintf("%s.stack[%d]=%v", name, i, v))
			}
		}
	}
	return out
}

// runWithLoader installs the data segment, then runs the runtime on top. Writes
// from both the loader and the runtime are recorded, so a setup write hoisted
// into the loader is compared like one left in the runtime.
func runWithLoader(runtime, loader string, init map[[2]string]float64) ([]string, bool) {
	m := vm.New()
	setup(m, init)
	var writes []string
	m.OnWrite = func(dev, logic string, v float64) {
		writes = append(writes, fmt.Sprintf("%s.%s=%v", dev, logic, v))
	}
	if err := m.Load(loader); err != nil {
		return nil, true
	}
	if err := m.Run(10000); err != nil {
		return nil, true
	}
	if err := m.Load(runtime); err != nil {
		return writes, true
	}
	err := m.Run(200000)
	writes = append(writes, deviceStacks(m)...)
	return writes, err != nil && err != vm.ErrStepLimit
}

func setup(m *vm.Machine, init map[[2]string]float64) {
	for k, v := range init {
		m.Set(k[0], k[1], v)
	}
}

// --- random .icg generator ---

type gen struct {
	rng         *rand.Rand
	vars        []string
	funcs       []string
	consts      []string
	tables      []string
	tableLen    int
	loops       int
	labels      int
	callees     []string
	loopDepth   int
	switchDepth int
	inFunc      bool
	stack       int // conservative lower bound on stack depth
}

func genProgram(seed int64) string {
	g := &gen{rng: rand.New(rand.NewSource(seed))}
	for i := 0; i < g.rng.Intn(3); i++ {
		g.consts = append(g.consts, fmt.Sprintf("C%d", i))
	}
	var sb strings.Builder
	for i, c := range g.consts {
		fmt.Fprintf(&sb, "const %s = %d\n", c, g.rng.Intn(10)+i)
	}

	for i := 0; i < g.rng.Intn(2); i++ {
		name := fmt.Sprintf("f%d", i)
		fmt.Fprintf(&sb, "\nfunc %s(a num, b num) num {\n", name)
		fmt.Fprintf(&sb, "    var x = 0\n    var y = 0\n")
		saved := g.vars
		g.vars = []string{"a", "b", "x", "y"}
		g.inFunc = true
		g.block(&sb, 1, 1+g.rng.Intn(2))
		g.inFunc = false
		g.vars = saved
		fmt.Fprintf(&sb, "    return (x + y + a + b)\n}\n")
		g.funcs = append(g.funcs, name)
	}

	sb.WriteString("\nfunc main() {\n")
	for i := 0; i < 2+g.rng.Intn(2); i++ {
		fmt.Fprintf(&sb, "    var v%d = %d\n", i, g.rng.Intn(4))
		g.vars = append(g.vars, fmt.Sprintf("v%d", i))
	}
	for i := 0; i < 3; i++ {
		fmt.Fprintf(&sb, "    var g%d = 0\n", i)
	}
	for i := 0; i < 3; i++ {
		sb.WriteString("    push(0)\n")
		g.stack++
	}
	g.block(&sb, 1, 2+g.rng.Intn(2))
	g.flushCallees(&sb)
	sb.WriteString("}\n")
	return sb.String()
}

// genDataProgram builds a program with a `data` table read at runtime.
func genDataProgram(seed int64) string {
	g := &gen{rng: rand.New(rand.NewSource(seed))}
	g.tables = []string{"T"}
	n := 2 + g.rng.Intn(4)
	g.tableLen = n
	var sb strings.Builder
	sb.WriteString("data T = [")
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "%d", g.rng.Intn(20))
	}
	sb.WriteString("]\n\nfunc main() {\n")
	for i := 0; i < 2+g.rng.Intn(2); i++ {
		fmt.Fprintf(&sb, "    var v%d = %d\n", i, g.rng.Intn(4))
		g.vars = append(g.vars, fmt.Sprintf("v%d", i))
	}
	for i := 0; i < 3; i++ {
		fmt.Fprintf(&sb, "    var g%d = 0\n", i)
	}
	for i := 0; i < 3; i++ {
		sb.WriteString("    push(0)\n")
		g.stack++
	}
	g.block(&sb, 1, 2+g.rng.Intn(2))
	g.flushCallees(&sb)
	sb.WriteString("}\n")
	return sb.String()
}

func (g *gen) block(sb *strings.Builder, depth, n int) {
	for i := 0; i < n; i++ {
		g.stmt(sb, depth)
	}
}

// flushCallees emits a halt followed by the low-level callee routines collected
// while generating main, so main's fall-through does not enter them.
func (g *gen) flushCallees(sb *strings.Builder) {
	if len(g.callees) == 0 {
		return
	}
	sb.WriteString("    jump(9999)\n")
	for _, c := range g.callees {
		sb.WriteString(c)
	}
}

func (g *gen) stmt(sb *strings.Builder, depth int) {
	ind := strings.Repeat("    ", depth)
	if depth >= 4 { // cap nesting so the unoptimized form still fits 128 lines
		g.simple(sb, ind)
		return
	}
	switch g.rng.Intn(26) {
	case 0, 1:
		g.simple(sb, ind)
	case 2, 3:
		fmt.Fprintf(sb, "%s%s += %s\n", ind, g.varName(), g.expr(2))
	case 4:
		fmt.Fprintf(sb, "%s%s -= %s\n", ind, g.varName(), g.expr(2))
	case 5, 6:
		fmt.Fprintf(sb, "%s%s = %s\n", ind, g.devTarget(), g.expr(2))
	case 7:
		fmt.Fprintf(sb, "%s%s.slot[0].Mature = %s\n", ind, g.devPort(), g.expr(2))
	case 8:
		fmt.Fprintf(sb, "%spush(%s)\n", ind, g.expr(2))
		g.stack++
	case 9:
		if g.stack > 0 && g.loopDepth == 0 {
			fmt.Fprintf(sb, "%s%s = pop()\n", ind, g.varName())
			g.stack--
		} else {
			g.simple(sb, ind)
		}
	case 10:
		fmt.Fprintf(sb, "%spoke(%d, %s)\n", ind, g.rng.Intn(30), g.expr(2))
	case 11:
		if g.stack > 0 {
			fmt.Fprintf(sb, "%s%s = peek()\n", ind, g.varName())
		} else {
			g.simple(sb, ind)
		}
	case 12:
		fmt.Fprintf(sb, "%sbatch.write(hash(\"StructureBattery\"), \"Setting\", %s)\n", ind, g.expr(2))
	case 13:
		fmt.Fprintf(sb, "%s%s = batch.read(hash(\"StructureBattery\"), \"Setting\", \"Sum\")\n", ind, g.varName())
	case 14:
		fmt.Fprintf(sb, "%swrite(%s, %s, %s)\n", ind, g.devPort(), g.expr(2), g.expr(2))
	case 15:
		fmt.Fprintf(sb, "%s%s = read(%s, %s)\n", ind, g.varName(), g.devPort(), g.expr(2))
	case 16, 17:
		if g.rng.Intn(2) == 0 {
			iv := fmt.Sprintf("i%d", g.loops)
			g.loops++
			fmt.Fprintf(sb, "%sif %s := %s; %s {\n", ind, iv, g.expr(2), g.expr(2))
		} else {
			fmt.Fprintf(sb, "%sif %s {\n", ind, g.expr(2))
		}
		savedStack := g.stack
		g.block(sb, depth+1, 1+g.rng.Intn(2))
		g.stack = savedStack
		if g.rng.Intn(2) == 0 {
			fmt.Fprintf(sb, "%s} else {\n", ind)
			g.block(sb, depth+1, 1+g.rng.Intn(2))
			g.stack = savedStack
		}
		fmt.Fprintf(sb, "%s}\n", ind)
	case 18:
		iv := fmt.Sprintf("i%d", g.loops)
		g.loops++
		g.loopDepth++
		if g.rng.Intn(2) == 0 {
			fmt.Fprintf(sb, "%sfor %s := range %d {\n", ind, iv, 1+g.rng.Intn(4))
		} else {
			fmt.Fprintf(sb, "%sfor %s := 0; %s < %d; %s++ {\n", ind, iv, iv, 1+g.rng.Intn(4), iv)
		}
		savedStack := g.stack
		g.block(sb, depth+1, 1+g.rng.Intn(2))
		g.stack = savedStack
		g.loopDepth--
		fmt.Fprintf(sb, "%s}\n", ind)
	case 19:
		if g.inFunc {
			g.simple(sb, ind)
			return
		}
		// A bounded low-level label/goto loop.
		gv := fmt.Sprintf("g%d", g.labels%3)
		lbl := fmt.Sprintf("L%d", g.labels)
		g.labels++
		fmt.Fprintf(sb, "%s%s = 0\n", ind, gv)
		fmt.Fprintf(sb, "%slabel %s:\n", ind, lbl)
		fmt.Fprintf(sb, "%s%s += 1\n", ind, gv)
		fmt.Fprintf(sb, "%sif %s < %d {\n", ind, gv, 1+g.rng.Intn(3))
		fmt.Fprintf(sb, "%s    goto %s\n", ind, lbl)
		fmt.Fprintf(sb, "%s}\n", ind)
	case 20:
		if g.inFunc {
			g.simple(sb, ind)
			return
		}
		// A labeled loop with a labeled break/continue.
		lbl := fmt.Sprintf("L%d", g.labels)
		g.labels++
		iv := fmt.Sprintf("i%d", g.loops)
		g.loops++
		g.loopDepth++
		fmt.Fprintf(sb, "%slabel %s:\n", ind, lbl)
		fmt.Fprintf(sb, "%sfor %s := 0; %s < %d; %s++ {\n", ind, iv, iv, 2+g.rng.Intn(3), iv)
		if g.rng.Intn(2) == 0 {
			fmt.Fprintf(sb, "%s    break %s\n", ind, lbl)
		} else {
			fmt.Fprintf(sb, "%s    continue %s\n", ind, lbl)
		}
		g.loopDepth--
		fmt.Fprintf(sb, "%s}\n", ind)
	case 21:
		if g.inFunc {
			g.simple(sb, ind)
			return
		}
		// A conditional low-level call: `if cond { call C }`.
		name := fmt.Sprintf("C%d", g.labels)
		g.labels++
		fmt.Fprintf(sb, "%sif %s { call %s }\n", ind, g.expr(2), name)
		var body strings.Builder
		fmt.Fprintf(&body, "    label %s:\n", name)
		fmt.Fprintf(&body, "    %s += 1\n", g.varName())
		body.WriteString("    ret\n")
		g.callees = append(g.callees, body.String())
	case 23:
		fmt.Fprintf(sb, "%sput(%s, %d, %s)\n", ind, g.stackDev(), g.rng.Intn(8), g.expr(2))
	case 24:
		fmt.Fprintf(sb, "%s%s = get(%s, %d)\n", ind, g.varName(), g.stackDev(), g.rng.Intn(8))
	case 25:
		fmt.Fprintf(sb, "%sclr(%s)\n", ind, g.stackDev())
	default:
		ncase := 2 + g.rng.Intn(9) // 2..10 dense cases
		if g.rng.Intn(2) == 0 {
			sv := fmt.Sprintf("s%d", g.loops)
			g.loops++
			fmt.Fprintf(sb, "%sswitch %s := %s; %s {\n", ind, sv, g.expr(2), g.expr(2))
		} else {
			fmt.Fprintf(sb, "%sswitch %s {\n", ind, g.expr(2))
		}
		g.switchDepth++
		savedStack := g.stack
		for ci := 0; ci < ncase; ci++ {
			if ci == 0 && g.rng.Intn(2) == 0 {
				fmt.Fprintf(sb, "%scase 0..1:\n", ind)
			} else {
				fmt.Fprintf(sb, "%scase %d:\n", ind, ci)
			}
			g.block(sb, depth+1, 1)
			g.stack = savedStack
		}
		fmt.Fprintf(sb, "%sdefault:\n", ind)
		g.block(sb, depth+1, 1)
		g.stack = savedStack
		g.switchDepth--
		fmt.Fprintf(sb, "%s}\n", ind)
	}
}

// simple emits an assignment, optionally a loop control statement.
func (g *gen) simple(sb *strings.Builder, ind string) {
	if g.loopDepth > 0 && g.switchDepth == 0 && g.rng.Intn(6) == 0 {
		if g.rng.Intn(2) == 0 {
			fmt.Fprintf(sb, "%sbreak\n", ind)
		} else {
			fmt.Fprintf(sb, "%scontinue\n", ind)
		}
		return
	}
	fmt.Fprintf(sb, "%s%s = %s\n", ind, g.varName(), g.expr(2))
}

func (g *gen) varName() string { return g.vars[g.rng.Intn(len(g.vars))] }

func (g *gen) devPort() string {
	devs := []string{"d0", "d1", "d2", "db"}
	return devs[g.rng.Intn(len(devs))]
}

// stackDev returns a device port whose memory stack is safe to use. `db` is
// excluded because its stack is the chip's own persistent stack (register
// spilling and the data segment lay it out differently between builds).
func (g *gen) stackDev() string {
	devs := []string{"d0", "d1", "d2"}
	return devs[g.rng.Intn(len(devs))]
}

func (g *gen) devTarget() string {
	logics := []string{"On", "Setting", "Mode"}
	return g.devPort() + "." + logics[g.rng.Intn(len(logics))]
}

func (g *gen) expr(depth int) string {
	if depth <= 0 {
		return g.atom()
	}
	switch g.rng.Intn(17) {
	case 0:
		return g.atom()
	case 1:
		return fmt.Sprintf("(%s + %s)", g.expr(depth-1), g.expr(depth-1))
	case 2:
		return fmt.Sprintf("(%s - %s)", g.expr(depth-1), g.expr(depth-1))
	case 3:
		return fmt.Sprintf("(%s * %s)", g.expr(depth-1), g.expr(depth-1))
	case 4:
		return fmt.Sprintf("(%s && %s)", g.expr(depth-1), g.expr(depth-1))
	case 5:
		return fmt.Sprintf("(%s || %s)", g.expr(depth-1), g.expr(depth-1))
	case 6:
		return fmt.Sprintf("(%s ? %s : %s)", g.expr(depth-1), g.expr(depth-1), g.expr(depth-1))
	case 7:
		return fmt.Sprintf("max(%s, %s)", g.expr(depth-1), g.expr(depth-1))
	case 8:
		return fmt.Sprintf("min(%s, %s)", g.expr(depth-1), g.expr(depth-1))
	case 9:
		return fmt.Sprintf("(%s < %s)", g.expr(depth-1), g.expr(depth-1))
	case 10:
		return fmt.Sprintf("(%s == %s)", g.expr(depth-1), g.expr(depth-1))
	case 11:
		return fmt.Sprintf("!%s", g.atom())
	case 12:
		return fmt.Sprintf("abs(%s)", g.expr(depth-1))
	case 13:
		return fmt.Sprintf("ins(%s, 0, 8)", g.expr(depth-1))
	case 14:
		return fmt.Sprintf("ext(%s, 0, 8)", g.expr(depth-1))
	case 15:
		return fmt.Sprintf("approx(%s, %s, %s)", g.expr(depth-1), g.expr(depth-1), g.expr(depth-1))
	case 16:
		return fmt.Sprintf("notApprox(%s, %s, %s)", g.expr(depth-1), g.expr(depth-1), g.expr(depth-1))
	default:
		return g.atom()
	}
}

func (g *gen) atom() string {
	switch g.rng.Intn(10) {
	case 0:
		return strconv.Itoa(g.rng.Intn(20))
	case 1, 2:
		return g.varName()
	case 3:
		return g.devRead()
	case 4:
		if len(g.consts) > 0 {
			return g.consts[g.rng.Intn(len(g.consts))]
		}
		return "1"
	case 5:
		if len(g.funcs) > 0 {
			f := g.funcs[g.rng.Intn(len(g.funcs))]
			return fmt.Sprintf("%s(%s, %s)", f, g.expr(1), g.expr(1))
		}
		return "2"
	case 6:
		if len(g.tables) > 0 && g.tableLen > 0 {
			t := g.tables[g.rng.Intn(len(g.tables))]
			return fmt.Sprintf("%s[%d]", t, g.rng.Intn(g.tableLen))
		}
		return "3"
	default:
		return strconv.Itoa(g.rng.Intn(5))
	}
}

func (g *gen) devRead() string {
	switch g.rng.Intn(4) {
	case 3:
		return g.devPort() + ".slot[0].Occupied"
	default:
		logics := []string{"Setting", "On", "Temperature"}
		return g.devPort() + "." + logics[g.rng.Intn(len(logics))]
	}
}

// TestDifferentialIndirect runs the differential comparison on programs that
// use reserveRegs / setIreg / ireg (IC10 rrN). The optimiser must not reorder
// or common up indirect register accesses across an indirect write.
func TestDifferentialIndirect(t *testing.T) {
	runDifferential(t, ic10.Options{}, 800, genIndirectProgram, false)
}

func genIndirectProgram(seed int64) string {
	rng := rand.New(rand.NewSource(seed))
	var sb strings.Builder
	sb.WriteString("func main() {\n")
	sb.WriteString("    reserveRegs(2, 4)\n")
	for r := 2; r <= 4; r++ {
		fmt.Fprintf(&sb, "    setIreg(%d, %d)\n", r, rng.Intn(20))
	}
	fmt.Fprintf(&sb, "    for i := 0; i < %d; i++ {\n", 2+rng.Intn(3))
	for k := 0; k < 3+rng.Intn(4); k++ {
		switch rng.Intn(5) {
		case 0:
			r := 2 + rng.Intn(3)
			fmt.Fprintf(&sb, "        setIreg(%d, ireg(%d) + %d)\n", r, r, rng.Intn(5))
		case 1:
			r := 2 + rng.Intn(3)
			fmt.Fprintf(&sb, "        setIreg(%d, %d)\n", r, rng.Intn(20))
		case 2:
			fmt.Fprintf(&sb, "        d0.Setting = ireg(%d)*10 + ireg(%d)\n", 2+rng.Intn(3), 2+rng.Intn(3))
		case 3:
			r := 2 + rng.Intn(3)
			fmt.Fprintf(&sb, "        if ireg(%d) > %d { setIreg(%d, %d) }\n", r, rng.Intn(15), 2+rng.Intn(3), rng.Intn(20))
		case 4:
			fmt.Fprintf(&sb, "        d0.Setting = (ireg(%d) + ireg(%d)) %% 100\n", 2+rng.Intn(3), 2+rng.Intn(3))
		}
	}
	sb.WriteString("    }\n")
	sb.WriteString("    d0.Setting = ireg(2)*100 + ireg(3)*10 + ireg(4)\n")
	sb.WriteString("}\n")
	return sb.String()
}
