package ic10_test

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"ic10go/internal/vm"
	"ic10go/pkg/ic10"
)

// housingStackWrite matches a write to the housing/user stack: put(db, N, ...),
// db.stack[N] = ..., or poke(N, ...). The single-chip default is private-stack,
// which deletes such writes when the program never reads them back, so a port
// that publishes to db without `// icg: shared-stack` can silently drop its
// outputs.
var housingStackWrite = regexp.MustCompile(`put\(\s*db\s*,|db\.stack\s*\[[0-9]+\]\s*=[^=]|poke\s*\(`)

// housingStackHint logs a port that writes the housing stack but does not carry
// the shared-stack pragma, since its db writes may be removed.
func housingStackHint(t *testing.T, port, src string) {
	t.Helper()
	if strings.Contains(src, "shared-stack") || !housingStackWrite.MatchString(src) {
		return
	}
	t.Logf("%s writes the housing stack (db) but has no `// icg: shared-stack`; the single-chip private-stack default may delete those writes. Add the pragma if they are outputs (see solverLarge).",
		filepath.Base(port))
}

// portSetup gives both the original and the ported script the same devices and
// values so their observable behaviour can be compared.
func portSetup(m *vm.Machine) {
	for i := 0; i < 6; i++ {
		d := m.Device(fmt.Sprintf("d%d", i))
		d.Set = true
		d.Values["Temperature"] = float64(20 + i*10)
		d.Values["Pressure"] = float64(1000 * (i + 1))
		d.Values["PressureOutput"] = float64(5000 * (i + 1))
		d.Values["PressureInput"] = float64(5000 * (i + 1))
		d.Values["Ratio"] = float64(i) / 10
		d.Values["On"] = float64(i % 2)
		d.Values["Activate"] = 1
		d.Values["Setting"] = float64(i)
		d.Values["Open"] = 0
		d.Values["Rpm"] = float64(50 * i)
		d.Values["Stress"] = float64(5 * i)
		d.Values["Throttle"] = float64(10 * i)
		d.Values["Reagents"] = float64(100 * i)
		d.Values["SignalID"] = float64(i)
		d.Values["SignalStrength"] = float64(i) / 10
	}
	m.Device("db").Values["Setting"] = 0.00300006003000050000
	m.Device("db").Set = true
}

// portSetupSeed is portSetup with deterministic pseudo-random logic values, so
// that value-dependent branches the fixed setup never reaches are exercised
// too. Both the original and the port are given the same values.
func portSetupSeed(m *vm.Machine, seed int64) {
	portSetup(m)
	rng := rand.New(rand.NewSource(seed))
	devs := []string{"d0", "d1", "d2", "d3", "d4", "d5", "db"}
	logics := []string{
		"Setting", "On", "Mode", "Activate", "Open", "Lock",
		"Temperature", "Pressure", "Stress", "Rpm", "Ratio",
		"Throttle", "CombustionLimiter", "Reagents", "RecipeHash",
		"CompletionRatio", "RatioMethane", "Color", "SignalID", "SignalStrength",
	}
	for _, name := range devs {
		d := m.Device(name)
		d.Set = true
		for _, k := range logics {
			d.Values[k] = float64(rng.Intn(31) - 15)
		}
	}
}

// TestIc10CodePorts runs every hand-written port next to its original .ic
// script and checks that they leave the devices in the same state.
func TestIc10CodePorts(t *testing.T) {
	requireIc10Code(t)
	ports := corpusFiles(t, ".icg")
	if len(ports) == 0 {
		t.Skip("no .icg ports found")
	}
	for _, port := range ports {
		t.Run(filepath.Base(port), func(t *testing.T) {
			skipKnownUnsupportedPort(t, port)
			orig := strings.TrimSuffix(port, ".icg")
			switch {
			case fileExists(orig + ".ic"):
				orig += ".ic"
			case fileExists(orig + ".ic10"):
				orig += ".ic10"
			default:
				t.Skip("no original script")
			}
			origSrc, err := os.ReadFile(orig)
			if err != nil {
				t.Fatal(err)
			}
			newSrc, err := os.ReadFile(port)
			if err != nil {
				t.Fatal(err)
			}
			housingStackHint(t, port, string(newSrc))
			// The ported scripts are real-world IC10 that predate the user
			// stack partition, so compile them with the dynamic boundary.
			compiled, diags, err := ic10.CompileWithOptions(port, newSrc, ic10.Options{DynamicStack: true})
			if diags.HasErrors() || err != nil {
				t.Fatalf("port failed to compile: diags=%v err=%v", diags.Diags, err)
			}

			// Compare the set of device states reached rather than a
			// fixed-step snapshot: the port and the original may take a
			// different number of instructions per iteration, and may even
			// converge or cycle differently, but they must visit the same
			// device states. Run the fixed setup plus several random
			// device-value seeds so value-dependent branches are covered.
			for _, seed := range []int64{0, 1, 2, 3, 4, 5} {
				a := vm.New()
				b := vm.New()
				if seed == 0 {
					portSetup(a)
					portSetup(b)
				} else {
					portSetupSeed(a, seed)
					portSetupSeed(b, seed)
				}
				if err := a.Load(string(origSrc)); err != nil {
					t.Fatalf("original load: %v", err)
				}
				// If the port uses a data segment, install it with the loader.
				if loader, lerr := ic10.DataLoader(port, newSrc); lerr == nil && loader != "" {
					lm := vm.New()
					if err := lm.Load(loader); err != nil {
						t.Fatalf("loader load: %v", err)
					}
					if err := lm.Run(200); err != nil && err != vm.ErrStepLimit {
						t.Fatalf("loader run: %v", err)
					}
					copy(b.Stack, lm.Stack)
				}
				if err := b.Load(compiled); err != nil {
					t.Fatalf("port load: %v", err)
				}

				// Compare only the writes both scripts got through within the
				// step budget. A port that executes fewer instructions per
				// iteration reaches more writes in the same number of steps;
				// charging it for those extra writes would flag a port that is
				// behaviourally identical, just faster. Truncating to the
				// common prefix keeps the check about the device writes, not
				// the instruction count.
				ta := stateTrace(a, 8000)
				tb := stateTrace(b, 8000)
				n := len(ta)
				if len(tb) < n {
					n = len(tb)
				}
				sa := prefixStateSet(ta, n)
				sb := prefixStateSet(tb, n)
				if !sameStateSet(sa, sb) {
					t.Errorf("seed %d: reachable device states differ\n only original: %s\n only port: %s",
						seed, diffStates(sa, sb), diffStates(sb, sa))
				}
			}
		})
	}
}

func deviceState(m *vm.Machine) string {
	var names []string
	for n := range m.Devices {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		d := m.Devices[n]
		var keys []string
		for k := range d.Values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s.%s=%v;", n, k, d.Values[k])
		}
		// Include a device's memory stack (get/put target) so a port that
		// writes the wrong stack instruction is caught, not just logic writes.
		// Skip the IC host (db): its stack is the chip's own persistent stack,
		// which the data segment and register spilling legitimately lay out
		// differently from the original script.
		if n == "db" {
			continue
		}
		for i, v := range d.Stack {
			if v != 0 {
				fmt.Fprintf(&b, "%s.stack[%d]=%v;", n, i, v)
			}
		}
	}
	return b.String()
}

// stateTrace runs the machine for up to maxSteps single steps and returns the
// device state after the initial load plus after every logic write. Indexing by
// write rather than by step keeps the trace independent of how many
// instructions a script spends per iteration.
func stateTrace(m *vm.Machine, maxSteps int) []string {
	trace := []string{deviceState(m)}
	m.OnWrite = func(dev, logic string, v float64) {
		trace = append(trace, deviceState(m))
	}
	for i := 0; i < maxSteps; i++ {
		if err := m.Run(1); err != nil && err != vm.ErrStepLimit {
			break
		}
	}
	return trace
}

// prefixStateSet is the set of distinct states among the first n entries of a
// trace.
func prefixStateSet(trace []string, n int) map[string]bool {
	if n > len(trace) {
		n = len(trace)
	}
	set := make(map[string]bool, n)
	for _, st := range trace[:n] {
		set[st] = true
	}
	return set
}

func sameStateSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for s := range a {
		if !b[s] {
			return false
		}
	}
	return true
}

func diffStates(a, b map[string]bool) string {
	var only []string
	for s := range a {
		if !b[s] {
			only = append(only, s)
		}
	}
	sort.Strings(only)
	if len(only) > 2 {
		only = only[:2]
	}
	return strings.Join(only, " || ")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
