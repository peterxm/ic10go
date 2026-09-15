package token

import (
	"strconv"
	"strings"
)

// Unit describes a numeric-literal suffix and its conversion into the game's
// base unit: Kelvin for temperature, kPa for pressure, W for power, seconds for
// time, a 0..1 ratio for percent.
type Unit struct {
	Name    string
	Convert func(float64) float64
}

// Units lists the recognized suffixes, longest first so that e.g. "kPa" is
// preferred over "k". The lexer also requires the byte after a suffix to be a
// non-identifier, so "20count" is not read as "20c".
var Units = []Unit{
	// pressure -> kPa
	{"MPa", func(v float64) float64 { return v * 1000 }},
	{"kPa", func(v float64) float64 { return v }},
	{"GPa", func(v float64) float64 { return v * 1e6 }},
	{"bar", func(v float64) float64 { return v * 100 }},
	{"psi", func(v float64) float64 { return v * 6.894757293168361 }},
	{"Pa", func(v float64) float64 { return v * 0.001 }},
	// power -> W
	{"kW", func(v float64) float64 { return v * 1000 }},
	{"MW", func(v float64) float64 { return v * 1e6 }},
	{"W", func(v float64) float64 { return v }},
	// time -> s
	{"ms", func(v float64) float64 { return v * 0.001 }},
	{"min", func(v float64) float64 { return v * 60 }},
	{"h", func(v float64) float64 { return v * 3600 }},
	{"s", func(v float64) float64 { return v }},
	// angle: readability only, no conversion (device angles are degrees; IC10
	// trig takes radians, so convert explicitly when needed).
	{"deg", func(v float64) float64 { return v }},
	{"rad", func(v float64) float64 { return v }},
	// ratio: 50% -> 0.5
	{"pct", func(v float64) float64 { return v * 0.01 }},
	{"%", func(v float64) float64 { return v * 0.01 }},
	// temperature -> K
	{"c", func(v float64) float64 { return v + 273.15 }},
	{"C", func(v float64) float64 { return v + 273.15 }},
	{"f", func(v float64) float64 { return (v-32)*5/9 + 273.15 }},
	{"F", func(v float64) float64 { return (v-32)*5/9 + 273.15 }},
	{"k", func(v float64) float64 { return v }},
	{"K", func(v float64) float64 { return v }},
}

// ConvertUnit parses a decimal literal with a unit suffix (e.g. "20.1MPa" or
// "20c") into the game's base unit. ok is false when no suffix matches.
func ConvertUnit(s string) (float64, bool) {
	for _, u := range Units {
		if len(s) <= len(u.Name) || !strings.HasSuffix(s, u.Name) {
			continue
		}
		num := strings.ReplaceAll(s[:len(s)-len(u.Name)], "_", "")
		if v, err := strconv.ParseFloat(num, 64); err == nil {
			return u.Convert(v), true
		}
	}
	return 0, false
}
