package codegen

import (
	"strings"
	"testing"
)

func TestValidateOK(t *testing.T) {
	if err := Validate("move r0 1\ns d0 On r0\n"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTooManyLines(t *testing.T) {
	code := strings.Repeat("move r0 1\n", MaxLines+1)
	if err := Validate(code); err == nil {
		t.Fatal("expected a line count error")
	}
}

func TestValidateLineTooLong(t *testing.T) {
	code := "s d0 Setting " + strings.Repeat("1", MaxLineLen) + "\n"
	if err := Validate(code); err == nil {
		t.Fatal("expected a line length error")
	}
}

func TestValidateTooManyBytes(t *testing.T) {
	// 90-character lines that fit the line count but exceed the byte budget.
	line := "s d0 Setting " + strings.Repeat("1", MaxLineLen-len("s d0 Setting ")-1)
	code := strings.Repeat(line+"\n", MaxBytes/len(line)+2)
	if err := Validate(code); err == nil {
		t.Fatal("expected a byte size error")
	}
}

func TestValidateLineLimitMentionsBudget(t *testing.T) {
	err := Validate(strings.Repeat("move r0 1\n", MaxLines+1))
	if err == nil || !strings.Contains(err.Error(), "stats") {
		t.Fatalf("line limit error should hint at `ic10c stats`, got: %v", err)
	}
}

func TestValidateWithCustomLimits(t *testing.T) {
	code := strings.Repeat("move r0 1\n", 5)
	if err := ValidateWith(code, Limits{Lines: 5}); err != nil {
		t.Fatalf("5 lines should fit Lines=5: %v", err)
	}
	if err := ValidateWith(code, Limits{Lines: 4}); err == nil {
		t.Fatal("expected a line count error with Lines=4")
	}

	long := "s d0 Setting " + strings.Repeat("1", 100) + "\n"
	if err := ValidateWith(long, Limits{LineLen: 200}); err != nil {
		t.Fatalf("long line should fit LineLen=200: %v", err)
	}
	if err := ValidateWith(long, Limits{LineLen: 50}); err == nil {
		t.Fatal("expected a line length error with LineLen=50")
	}

	big := strings.Repeat("move r0 1\n", 200)
	if err := ValidateWith(big, Limits{Lines: 500, Bytes: 1 << 20}); err != nil {
		t.Fatalf("large program should fit raised lines/bytes: %v", err)
	}
}

func TestLimitsResolveDefaults(t *testing.T) {
	got := Limits{Lines: 256}.Resolve()
	if got.Lines != 256 || got.Bytes != MaxBytes || got.LineLen != MaxLineLen {
		t.Fatalf("Resolve = %+v, want {256 %d %d}", got, MaxBytes, MaxLineLen)
	}
}
