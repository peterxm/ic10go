package tick_test

import (
	"os"
	"testing"

	"ic10go/internal/tick"
	"ic10go/pkg/ic10"
)

func BenchmarkAnalyze(b *testing.B) {
	src, err := os.ReadFile("../../examples/自动打印机产量控制-多机-排料保护-表驱动.icg")
	if err != nil {
		b.Skip(err)
	}
	code, diags, err := ic10.Compile("bench.icg", src)
	if err != nil || diags.HasErrors() {
		b.Fatalf("compile: %v %v", err, diags.Diags)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := tick.Analyze(code, tick.DefaultLimit); err != nil {
			b.Fatal(err)
		}
	}
}
