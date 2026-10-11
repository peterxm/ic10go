package ic10

import (
	"fmt"
	"strconv"
	"strings"

	"ic10go/internal/minify"
)

// StackFold rewrites the "device ReferenceId kept in the host stack" idiom into
// literal ids, so a hand-written program that loads its devices with
//
//	move sp 6            ; the id of device N sits at stack[N-1] (pop reads sp-1)
//	pop  r9              ; r9 = stack[5]; sp = 5
//	l    r0 r9 TotalMoles
//	brgt sp 4 -3         ; repeat while sp > 4 (br* targets are relative)
//
// becomes straight-line code with r9 replaced by the id stored in each slot.
// `ids` maps a 0-based stack slot to the ReferenceId a one-time loader wrote
// there (the ids are not in the program, so a human has to supply them).
//
// It is conservative: when the shape is not exactly as expected it returns the
// input unchanged rather than risk changing behaviour, so it is safe to run
// before Minify or Decompile.
func StackFold(src string, ids map[int]int) (string, error) {
	return minify.StackFold(src, ids)
}

// ParseStackIDs parses `SLOT=REFID` arguments (as accepted by
// `ic10c minify/decompile --stack-id`) into the map StackFold takes.
func ParseStackIDs(args []string) (map[int]int, error) {
	if len(args) == 0 {
		return nil, nil
	}
	ids := make(map[int]int, len(args))
	for _, a := range args {
		slot, id, err := splitStackID(a)
		if err != nil {
			return nil, err
		}
		ids[slot] = id
	}
	return ids, nil
}

// splitStackID parses one `SLOT=REFID` (slot is the 0-based stack address).
func splitStackID(s string) (int, int, error) {
	eq := strings.IndexByte(s, '=')
	if eq <= 0 || eq == len(s)-1 {
		return 0, 0, fmt.Errorf("bad --stack-id %q (want SLOT=REFID)", s)
	}
	slot, err := strconv.Atoi(s[:eq])
	if err != nil || slot < 0 {
		return 0, 0, fmt.Errorf("bad --stack-id slot in %q", s)
	}
	id, err := strconv.Atoi(s[eq+1:])
	if err != nil {
		return 0, 0, fmt.Errorf("bad --stack-id ReferenceId in %q", s)
	}
	return slot, id, nil
}
