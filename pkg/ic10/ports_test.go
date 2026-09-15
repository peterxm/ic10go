package ic10_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"ic10go/internal/vm"
	"ic10go/pkg/ic10"
)

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

// TestIc10CodePorts runs every hand-written port next to its original .ic
// script and checks that they leave the devices in the same state.
func TestIc10CodePorts(t *testing.T) {
	requireIc10Code(t)
	var ports []string
	err := filepath.Walk("../../ic10code", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".icg") {
			ports = append(ports, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) == 0 {
		t.Skip("no .icg ports found")
	}
	for _, port := range ports {
		t.Run(filepath.Base(port), func(t *testing.T) {
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
			compiled, diags, err := ic10.Compile(port, newSrc)
			if diags.HasErrors() || err != nil {
				t.Fatalf("port failed to compile: diags=%v err=%v", diags.Diags, err)
			}

			a := vm.New()
			portSetup(a)
			if err := a.Load(string(origSrc)); err != nil {
				t.Fatalf("original load: %v", err)
			}

			b := vm.New()
			portSetup(b)
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

			// Compare the set of device states reached rather than a
			// fixed-step snapshot: the port and the original may take a
			// different number of instructions per iteration, and may even
			// converge or cycle differently, but they must visit the same
			// device states.
			sa := stateSet(a, 8000)
			sb := stateSet(b, 8000)
			if !sameStateSet(sa, sb) {
				t.Errorf("reachable device states differ\n only original: %s\n only port: %s",
					diffStates(sa, sb), diffStates(sb, sa))
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

func stateSet(m *vm.Machine, maxSteps int) map[string]bool {
	seen := map[string]bool{deviceState(m): true}
	wrote := false
	m.OnWrite = func(dev, logic string, v float64) { wrote = true }
	for i := 0; i < maxSteps; i++ {
		wrote = false
		if err := m.Run(1); err != nil && err != vm.ErrStepLimit {
			break
		}
		if wrote {
			seen[deviceState(m)] = true
		}
	}
	return seen
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
