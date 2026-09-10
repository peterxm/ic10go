package ic10_test

import (
	"fmt"
	"os"
	"path/filepath"
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
}

// TestIc10CodePorts runs every hand-written port next to its original .ic
// script and checks that they leave the devices in the same state.
func TestIc10CodePorts(t *testing.T) {
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
		t.Fatal("no .icg ports found")
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
			if err := a.Run(4000); err != nil && err != vm.ErrStepLimit {
				t.Fatalf("original run: %v", err)
			}

			b := vm.New()
			portSetup(b)
			if err := b.Load(compiled); err != nil {
				t.Fatalf("port load: %v", err)
			}
			if err := b.Run(4000); err != nil && err != vm.ErrStepLimit {
				t.Fatalf("port run: %v", err)
			}

			compareDevices(t, a, b)
		})
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
