// Command dumpenums prints the compiler's compile-time enum table
// (internal/builtin.EnumConstants) as JSON. The testbench A/B harness
// (testdata/bench/ab) uses it to normalize enum literals such as
// `Color.Green` to the numeric value a recompiled program emits.
//
//	go run ./tools/dumpenums > testdata/bench/ab/enums.json
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"ic10go/internal/builtin"
)

func main() {
	b, err := json.MarshalIndent(builtin.EnumConstants, "", " ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "dumpenums:", err)
		os.Exit(1)
	}
	fmt.Println(string(b))
}
