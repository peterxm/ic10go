package ic10

import "ic10go/internal/minify"

// MinifyOptions controls Minify.
type MinifyOptions = minify.Options

// Minify rewrites IC10 assembly with fewer lines while preserving its
// observable behaviour: it drops comments and blank lines, inlines
// alias/define symbols, converts labels to absolute line numbers, renumbers
// jump targets and removes unreachable instructions.
func Minify(src string, opt MinifyOptions) (string, error) {
	return minify.Minify(src, opt)
}
