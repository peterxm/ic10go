package cli

import (
	"strings"
	"testing"
)

func TestParseLang(t *testing.T) {
	cases := map[string]Lang{
		"en": EN, "en-US": EN, "english": EN,
		"zh": ZH, "zh-CN": ZH, "zh_Hans": ZH, "中文": ZH,
	}
	for in, want := range cases {
		got, ok := ParseLang(in)
		if !ok || got != want {
			t.Errorf("ParseLang(%q) = %v, %v; want %v, true", in, got, ok, want)
		}
	}
	if _, ok := ParseLang("fr"); ok {
		t.Error("ParseLang(fr) should not be recognised")
	}
}

func TestDetectEnv(t *testing.T) {
	t.Setenv("IC10C_LANG", "en")
	if got := Detect(); got != EN {
		t.Errorf("Detect() = %v, want en", got)
	}
	t.Setenv("IC10C_LANG", "zh-CN")
	if got := Detect(); got != ZH {
		t.Errorf("Detect() = %v, want zh", got)
	}
}

func TestUsageLanguages(t *testing.T) {
	en := Usage(EN)
	if !strings.Contains(en, "Usage:") || !strings.Contains(en, "Commands:") {
		t.Errorf("English usage missing headers:\n%s", en)
	}
	zh := Usage(ZH)
	if !strings.Contains(zh, "用法:") || !strings.Contains(zh, "命令:") {
		t.Errorf("Chinese usage missing headers:\n%s", zh)
	}
}

func TestCommandHelpAll(t *testing.T) {
	for _, c := range Commands {
		for _, l := range []Lang{EN, ZH} {
			h, ok := CommandHelp(l, c.Name)
			if !ok {
				t.Fatalf("CommandHelp(%v, %q) not found", l, c.Name)
			}
			if h == "" {
				t.Errorf("CommandHelp(%v, %q) empty", l, c.Name)
			}
		}
		if c.Summary.get(EN) == "" || c.Summary.get(ZH) == "" {
			t.Errorf("command %q is missing a summary", c.Name)
		}
		if c.Long.get(EN) == "" || c.Long.get(ZH) == "" {
			t.Errorf("command %q is missing long help", c.Name)
		}
	}
}

func TestUsageLine(t *testing.T) {
	if got := UsageLine(EN, "build"); got != "usage: ic10c build <file.icg>" {
		t.Errorf("EN usage line = %q", got)
	}
	if got := UsageLine(ZH, "build"); got != "用法: ic10c build <file.icg>" {
		t.Errorf("ZH usage line = %q", got)
	}
}

func TestUnknownOptionHint(t *testing.T) {
	if got := UnknownOption(EN, "-s"); !strings.Contains(got, "unknown option") || !strings.Contains(got, "decompile -s") {
		t.Errorf("EN unknown option = %q", got)
	}
	if got := UnknownOption(ZH, "-s"); !strings.Contains(got, "未知选项") || !strings.Contains(got, "decompile -s") {
		t.Errorf("ZH unknown option = %q", got)
	}
}

func TestIC10Hint(t *testing.T) {
	if got := IC10Hint(EN, "a.ic"); !strings.Contains(got, "decompile") {
		t.Errorf("EN IC10 hint = %q", got)
	}
	if got := IC10Hint(ZH, "a.ic"); !strings.Contains(got, "反编译") {
		t.Errorf("ZH IC10 hint = %q", got)
	}
}
