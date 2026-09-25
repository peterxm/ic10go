package lower

import (
	"strings"
	"testing"
)

// TestDataConstLineLimit verifies that the fold threshold follows the
// configured per-line character limit: a literal is folded only when it still
// fits an emitted line.
func TestDataConstLineLimit(t *testing.T) {
	long := strings.Repeat("7", 100) // not a float, so it stays a raw literal
	if _, ok := dataConst(long, 90); ok {
		t.Errorf("100-char literal folded at maxLineLen=90 (threshold 40)")
	}
	if _, ok := dataConst(long, 180); !ok {
		t.Errorf("100-char literal not folded at maxLineLen=180 (threshold 130)")
	}
	if _, ok := dataConst("12345", 90); !ok {
		t.Errorf("short literal should fold at maxLineLen=90")
	}
	// Zero keeps the historical conservative threshold of 40.
	if _, ok := dataConst(strings.Repeat("7", 41), 0); ok {
		t.Errorf("41-char literal should not fold with the default threshold")
	}
}
