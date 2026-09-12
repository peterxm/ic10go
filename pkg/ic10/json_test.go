package ic10_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ic10go/pkg/ic10"
)

const jsonDataSrc = `
const N = 2
data T = [10, 20]
func main() {
    for {
        yield()
        d0.On = T[0]
    }
}
`

const jsonPlainSrc = `
func main() {
    for {
        yield()
        d0.On = 1
    }
}
`

func TestBuildJSONSuccessWithData(t *testing.T) {
	res, err := ic10.BuildJSON("t.icg", []byte(jsonDataSrc), ic10.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.APIVersion != ic10.APIVersion {
		t.Errorf("apiVersion = %d, want %d", res.APIVersion, ic10.APIVersion)
	}
	if !res.OK {
		t.Fatalf("ok = false, diagnostics: %+v", res.Diagnostics)
	}
	if res.Code == "" {
		t.Fatal("code is empty")
	}
	if len(res.Lines) != res.Stats.Lines {
		t.Errorf("lines=%d, stats.lines=%d", len(res.Lines), res.Stats.Lines)
	}
	if !res.Data.Needed {
		t.Fatal("data.needed = false, want true")
	}
	if res.Data.Loader == "" {
		t.Error("data.loader is empty")
	}
	if res.Data.Start <= 0 || res.Data.End < res.Data.Start {
		t.Errorf("bad data range [%d..%d]", res.Data.Start, res.Data.End)
	}
	if res.Data.Sentinel != res.Data.Start {
		t.Errorf("sentinel=%d, start=%d", res.Data.Sentinel, res.Data.Start)
	}
	if res.Data.Access != "get" || res.Data.Layout != "top" {
		t.Errorf("access=%q layout=%q", res.Data.Access, res.Data.Layout)
	}
	if res.Limits != ic10.LimitsOf() {
		t.Errorf("limits = %+v", res.Limits)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("unexpected diagnostics: %+v", res.Diagnostics)
	}
	assertJSONFields(t, res, "apiVersion", "ok", "code", "lines", "data", "stats", "limits", "diagnostics")
}

func TestBuildJSONNoData(t *testing.T) {
	res, err := ic10.BuildJSON("t.icg", []byte(jsonPlainSrc), ic10.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("ok = false, diagnostics: %+v", res.Diagnostics)
	}
	if res.Data.Needed {
		t.Errorf("data.needed = true, want false")
	}
	if res.Data.Loader != "" {
		t.Errorf("data.loader = %q, want empty", res.Data.Loader)
	}
	if res.Lines == nil {
		t.Error("lines is nil, want empty array")
	}
}

func TestBuildJSONSyntaxError(t *testing.T) {
	res, err := ic10.BuildJSON("bad.icg", []byte("func main() {\n    x :=\n}\n"), ic10.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("ok = true, want false")
	}
	if res.Code != "" {
		t.Errorf("code = %q, want empty", res.Code)
	}
	if len(res.Diagnostics) == 0 {
		t.Fatal("no diagnostics")
	}
	d := res.Diagnostics[0]
	if d.Severity != "error" {
		t.Errorf("severity = %q", d.Severity)
	}
	if d.Range.Start.Line == 0 {
		t.Error("diagnostic has no position")
	}
}

func TestBuildJSONWarningCode(t *testing.T) {
	src := "func main() {\n    for {\n        yield()\n        d0.Bogus = 1\n    }\n}\n"
	res, err := ic10.BuildJSON("warn.icg", []byte(src), ic10.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("ok = false, diagnostics: %+v", res.Diagnostics)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(res.Diagnostics), res.Diagnostics)
	}
	d := res.Diagnostics[0]
	if d.Severity != "warning" || d.Code != "unknown-logic-type" {
		t.Errorf("got severity=%q code=%q", d.Severity, d.Code)
	}
	if !strings.Contains(d.Message, "Bogus") {
		t.Errorf("message = %q", d.Message)
	}
}

func TestBuildJSONNoMain(t *testing.T) {
	res, err := ic10.BuildJSON("nomain.icg", []byte("func foo() {}\n"), ic10.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("ok = true, want false")
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != "no-main" {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
}

func TestBuildJSONDataOptions(t *testing.T) {
	res, err := ic10.BuildJSON("t.icg", []byte(jsonDataSrc), ic10.Options{
		DataAccessStack: true,
		DataLayout:      "middle",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Data.Access != "stack" || res.Data.Layout != "middle" {
		t.Errorf("access=%q layout=%q", res.Data.Access, res.Data.Layout)
	}
	if !strings.HasPrefix(res.Data.Loader, "poke ") {
		t.Errorf("loader does not use poke: %q", res.Data.Loader)
	}
}

// assertJSONFields marshals v and checks the top-level object has exactly the
// given keys, so accidental schema changes are caught.
func assertJSONFields(t *testing.T, v any, keys ...string) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			t.Errorf("missing JSON field %q", k)
		}
	}
	if len(m) != len(keys) {
		t.Errorf("JSON has %d fields, want %d: %v", len(m), len(keys), m)
	}
}
