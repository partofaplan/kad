package cli

import "runtime"

// Glyphs used in output. Windows consoles still commonly run a legacy code
// page where UTF-8 box and arrow characters render as mojibake, so the same
// output is expressed in ASCII there rather than gambling on the terminal.
var (
	markOK   = "✓"
	markWarn = "!"
	markFail = "✗"
	arrow    = "→"
	fixArrow = "↳"
)

func init() {
	if runtime.GOOS == "windows" {
		markOK, markWarn, markFail = "[ok]", "[!!]", "[XX]"
		arrow, fixArrow = "->", "fix:"
	}
}
