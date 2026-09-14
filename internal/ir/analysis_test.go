package ir

import "testing"

func TestDefUse(t *testing.T) {
	a := &Reg{ID: 0}
	b := &Reg{ID: 1}
	dst := &Reg{ID: 2}

	// ins reads its destination (read-modify-write).
	u, def := DefUse(&Builtin{Name: "ins", Dst: dst, Args: []Value{a, b}})
	if len(def) != 1 || def[0] != dst {
		t.Fatalf("ins def = %v, want %v", def, dst)
	}
	want := map[*Reg]bool{a: true, b: true, dst: true}
	if len(u) != len(want) {
		t.Fatalf("ins uses = %v, want %v", u, want)
	}
	for _, r := range u {
		if !want[r] {
			t.Errorf("unexpected ins use %v", r)
		}
	}

	// A store has a use but no def; a load has a def but no use.
	if u, def := DefUse(&Store{Src: a}); len(u) != 1 || len(def) != 0 {
		t.Errorf("store use=%v def=%v", u, def)
	}
	if u, def := DefUse(&Load{Dst: dst}); len(u) != 0 || len(def) != 1 {
		t.Errorf("load use=%v def=%v", u, def)
	}
}

func TestLiveness(t *testing.T) {
	fn := &Function{Name: "f"}
	entry := fn.NewBlock()
	end := fn.NewBlock()
	a := &Reg{ID: 0}
	b := &Reg{ID: 1}
	// entry: b = a; jump end
	entry.Instrs = append(entry.Instrs, &Assign{Dst: b, Src: a})
	entry.Term = &Jmp{Target: end}
	end.Term = &Ret{}
	fn.Entry = entry
	fn.BuildCFG()

	in, _ := Liveness(fn)
	if len(in[end]) != 0 {
		t.Errorf("live-in[end] = %v, want empty", in[end])
	}
	if !in[entry][a] {
		t.Errorf("a is used at entry, live-in = %v", in[entry])
	}
	if in[entry][b] {
		t.Errorf("b is defined before use, live-in = %v", in[entry])
	}
}
