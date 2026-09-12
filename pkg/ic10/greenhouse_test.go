package ic10_test

import (
	"os"
	"strings"
	"testing"

	"ic10go/internal/vm"
	"ic10go/pkg/ic10"
)

func greenhouseSetup(activate float64, pressure float64) func(*vm.Machine) {
	return func(m *vm.Machine) {
		m.Device("d0").Values["PressureOutput"] = pressure
		m.Device("db").Values["PressureOutput"] = pressure
		s := m.Device("sensor")
		s.Hash = vm.HashOf(1076425094)
		s.Values["Activate"] = activate
		m.Device("grow").Hash = vm.HashOf(-1758710260)
		m.Device("long").Hash = vm.HashOf(797794350)
	}
}

func TestGreenhousePort(t *testing.T) {
	orig, err := os.ReadFile("../../ic10code/温室照明与通风过滤.ic")
	if err != nil {
		t.Skip("ic10code/ corpus not present (third-party, not in this repo)")
	}
	port, err := os.ReadFile("../../ic10code/温室照明与通风过滤.icg")
	if err != nil {
		t.Skip("ic10code/ corpus not present (third-party, not in this repo)")
	}
	code, diags, err := ic10.Compile("port.icg", port)
	if diags.HasErrors() || err != nil {
		t.Fatalf("compile: diags=%v err=%v", diags.Diags, err)
	}
	for _, tc := range []struct {
		name     string
		activate float64
		pressure float64
	}{
		{"sunny-low", 1, 40000},
		{"dark-high", 0, 50000},
		{"dark-low", 0, 40000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := vm.New()
			greenhouseSetup(tc.activate, tc.pressure)(a)
			if err := a.Load(string(orig)); err != nil {
				t.Fatal(err)
			}
			b := vm.New()
			greenhouseSetup(tc.activate, tc.pressure)(b)
			if err := b.Load(code); err != nil {
				t.Fatal(err)
			}
			wa := writeSequence(a, 30, 20000)
			wb := writeSequence(b, 30, 20000)
			if strings.Join(wa, "|") != strings.Join(wb, "|") {
				t.Errorf("writes differ\n orig: %v\n port: %v", wa, wb)
			}
		})
	}
}
