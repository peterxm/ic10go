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
	for seed := int64(0); seed < 500; seed++ {
		src := genProgram(seed)

		t.Setenv("IC10C_NO_OPT", "")
		t.Setenv("IC10C_NO_OUTLINE", "")
		optCode, diags, err := ic10.Compile("t.icg", []byte(src))
		if diags.HasErrors() || err != nil {
			t.Fatalf("seed %d: optimized compile failed: %v %v\n%s", seed, diags.Diags, err, src)
		}

		t.Setenv("IC10C_NO_OPT", "1")
		t.Setenv("IC10C_NO_OUTLINE", "1")
		rawCode, diags, err := ic10.Compile("t.icg", []byte(src))
		if err != nil && strings.Contains(err.Error(), "exceeding") {
			continue // unoptimized form is too big; not a correctness issue
		}
		if diags.HasErrors() || err != nil {
			t.Fatalf("seed %d: unoptimized compile failed: %v %v\n%s", seed, diags.Diags, err, src)
		}

		init := deviceInit(seed)
		want := runWrites(rawCode, init)
		got := runWrites(optCode, init)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("seed %d: optimized and unoptimized differ\n%s\n--- optimized ---\n%v\n--- unoptimized ---\n%v\n--- optimized code ---\n%s",
				seed, src, got, want, optCode)
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

func runWrites(code string, init map[[2]string]float64) []string {
	m := vm.New()
	for k, v := range init {
		m.Set(k[0], k[1], v)
	}
	var writes []string
	m.OnWrite = func(dev, logic string, v float64) {
		writes = append(writes, fmt.Sprintf("%s.%s=%v", dev, logic, v))
	}
	if err := m.Load(code); err != nil {
		writes = append(writes, "LOADERR:"+err.Error())
		return writes
	}
	if err := m.Run(200000); err != nil && err != vm.ErrStepLimit {
		writes = append(writes, "RUNERR:"+err.Error())
	}
	return writes
}

// --- random .icg generator ---

type gen struct {
	rng   *rand.Rand
	vars  []string
	loops int
}

func genProgram(seed int64) string {
	g := &gen{rng: rand.New(rand.NewSource(seed))}
	nv := 2 + g.rng.Intn(2)
	for i := 0; i < nv; i++ {
		g.vars = append(g.vars, fmt.Sprintf("v%d", i))
	}
	var sb strings.Builder
	sb.WriteString("func main() {\n")
	for _, v := range g.vars {
		fmt.Fprintf(&sb, "    var %s = %d\n", v, g.rng.Intn(4))
	}
	g.block(&sb, 1, 3+g.rng.Intn(2))
	sb.WriteString("}\n")
	return sb.String()
}

func (g *gen) block(sb *strings.Builder, depth, n int) {
	for i := 0; i < n; i++ {
		g.stmt(sb, depth)
	}
}

func (g *gen) stmt(sb *strings.Builder, depth int) {
	ind := strings.Repeat("    ", depth)
	if depth >= 4 { // cap nesting so the unoptimized form still fits 128 lines
		fmt.Fprintf(sb, "%s%s = %s\n", ind, g.varName(), g.expr(2))
		return
	}
	switch g.rng.Intn(12) {
	case 0, 1, 2:
		fmt.Fprintf(sb, "%s%s = %s\n", ind, g.varName(), g.expr(2))
	case 3, 4:
		fmt.Fprintf(sb, "%s%s = %s\n", ind, g.devTarget(), g.expr(2))
	case 5, 6:
		fmt.Fprintf(sb, "%sif %s {\n", ind, g.expr(2))
		g.block(sb, depth+1, 1+g.rng.Intn(2))
		if g.rng.Intn(2) == 0 {
			fmt.Fprintf(sb, "%s} else {\n", ind)
			g.block(sb, depth+1, 1+g.rng.Intn(2))
		}
		fmt.Fprintf(sb, "%s}\n", ind)
	case 7, 8:
		iv := fmt.Sprintf("i%d", g.loops)
		g.loops++
		fmt.Fprintf(sb, "%sfor %s := 0; %s < %d; %s++ {\n", ind, iv, iv, 1+g.rng.Intn(4), iv)
		g.block(sb, depth+1, 1+g.rng.Intn(2))
		fmt.Fprintf(sb, "%s}\n", ind)
	default:
		fmt.Fprintf(sb, "%s%s = %s\n", ind, g.varName(), g.expr(3))
	}
}

func (g *gen) varName() string { return g.vars[g.rng.Intn(len(g.vars))] }

func (g *gen) devTarget() string {
	devs := []string{"d0", "d1", "d2", "db"}
	logics := []string{"On", "Setting", "Mode"}
	return devs[g.rng.Intn(len(devs))] + "." + logics[g.rng.Intn(len(logics))]
}

func (g *gen) expr(depth int) string {
	if depth <= 0 {
		return g.atom()
	}
	switch g.rng.Intn(10) {
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
		return fmt.Sprintf("(%s < %s)", g.expr(depth-1), g.expr(depth-1))
	default:
		return g.atom()
	}
}

func (g *gen) atom() string {
	switch g.rng.Intn(5) {
	case 0:
		return strconv.Itoa(g.rng.Intn(20))
	case 1, 2:
		return g.varName()
	case 3:
		return g.devRead()
	default:
		return strconv.Itoa(g.rng.Intn(5))
	}
}

func (g *gen) devRead() string {
	devs := []string{"d0", "d1", "d2", "db"}
	logics := []string{"Setting", "On", "Temperature"}
	return devs[g.rng.Intn(len(devs))] + "." + logics[g.rng.Intn(len(logics))]
}
