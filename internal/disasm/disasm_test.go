package disasm

import (
	"strings"
	"testing"
)

func TestDisassemble(t *testing.T) {
	src := "move r0 0\nbeqz r0 3\nmove r1 99\ns d0 Setting r1"
	out := Disassemble(src)
	if !strings.Contains(out, "L0=3") {
		t.Errorf("missing target annotation:\n%s", out)
	}
	if !strings.Contains(out, "3  s d0 Setting r1  ; L0:") {
		t.Errorf("missing label marker:\n%s", out)
	}
}

func TestDisassembleNamedLabel(t *testing.T) {
	src := "loop:\nyield\nj loop"
	out := Disassemble(src)
	if !strings.Contains(out, "L0=0") {
		t.Errorf("named label not resolved:\n%s", out)
	}
}
