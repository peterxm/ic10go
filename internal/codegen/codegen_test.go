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
