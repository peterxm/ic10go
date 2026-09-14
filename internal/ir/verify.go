package ir

import "fmt"

// Verify checks the structural invariants of a function: every block has a
// terminator, every terminator only references blocks that are part of the
// function, the entry block is present, and blocks are unique.
//
// It is a safety net for the compiler pipeline: a violation means an earlier
// pass produced a malformed CFG (for example a removed block still referenced
// by a jump table), which used to fail silently as a miscompile.
func Verify(fn *Function) error {
	if fn == nil {
		return fmt.Errorf("ir: nil function")
	}
	if fn.Entry == nil {
		return fmt.Errorf("ir: function %q has no entry block", fn.Name)
	}
	inFunc := make(map[*Block]bool, len(fn.Blocks))
	for _, b := range fn.Blocks {
		if b == nil {
			return fmt.Errorf("ir: function %q has a nil block", fn.Name)
		}
		if inFunc[b] {
			return fmt.Errorf("ir: block %d appears more than once", b.ID)
		}
		inFunc[b] = true
	}
	if !inFunc[fn.Entry] {
		return fmt.Errorf("ir: entry block %d is not in the function", fn.Entry.ID)
	}
	for _, b := range fn.Blocks {
		if b.Term == nil {
			return fmt.Errorf("ir: block %d has no terminator", b.ID)
		}
		for _, s := range b.Term.Successors() {
			if s == nil {
				return fmt.Errorf("ir: block %d has a nil successor", b.ID)
			}
			if !inFunc[s] {
				return fmt.Errorf("ir: block %d references block %d, which is not in the function", b.ID, s.ID)
			}
		}
	}
	return nil
}

// VerifyReachable is Verify plus a check that every block is reachable from the
// entry. Lowering may leave unreachable blocks (code after return/goto or an
// infinite loop), so this is only meaningful after dead-block elimination.
func VerifyReachable(fn *Function) error {
	if err := Verify(fn); err != nil {
		return err
	}
	seen := make(map[*Block]bool, len(fn.Blocks))
	var walk func(b *Block)
	walk = func(b *Block) {
		if b == nil || seen[b] {
			return
		}
		seen[b] = true
		for _, s := range b.Term.Successors() {
			walk(s)
		}
	}
	walk(fn.Entry)
	for _, b := range fn.Blocks {
		if !seen[b] {
			return fmt.Errorf("ir: block %d is unreachable", b.ID)
		}
	}
	return nil
}
