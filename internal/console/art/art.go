// Package art holds the pixel grids that make up the Abraham mascot and
// wordmark.
//
// Each string is one row of pixels. Two pixel rows become one terminal line,
// drawn with the upper-half block (U+2580) — that doubles the vertical
// resolution and makes the pixels square, since a terminal cell is about twice
// as tall as it is wide.
//
// Generated from the design sources. Editing by hand is fine, but check the
// result against docs/images before committing: a grid that stops being
// rectangular shifts the whole drawing left with no error at all.
package art

// Palette maps a grid character to its RGB colour.
var Palette = map[byte]string{
	'K': "#12120E",
	'D': "#A87E00",
	'S': "#E7AD00",
	'L': "#FFD34D",
	'W': "#FAF9F4",
	'G': "#247A47",
	'g': "#17552F",
	'h': "#2F9459",
}

// Mascot is the full detective meerkat — 32 columns by 14 terminal lines.
var Mascot = []string{
	"..........GGGGGGGG..............",
	".........GhhhhGGGGG.............",
	"........GhhhhGGGGGGG............",
	"........GhhhGGGGGGGG............",
	"........gggggggggggg............",
	"....GhhhhhhGGGGGGGGGgggg........",
	".....gggggggggggggggggg.........",
	"........DDDDDDDDDDDD............",
	".......SSSSSSSSSSSDDD....WWWW...",
	"......SSSSSSSSSSSSSSDD..WW..WW..",
	"......SKKKKKKKKKKKKKKD.WW....WW.",
	"......SKKWWKKKKKKWWKKDWW......WW",
	"......SKWKKWKKKKWKKWKDWW......WW",
	"......SKWWKWKKKKWWKWKDWW......WW",
	"......SKKWWKKKKKKWWKKDWW......WW",
	"......SSKKKKLLLLKKKKDD.WW....WW.",
	".......SSSSLLKKLLSSDD...WW..WW..",
	"........SSSSLLLLSSSD.....WWWW...",
	".........SSSSSSSSSD....WW.......",
	".........SSSSSSSSSSSD.WW........",
	"........SSSLLLLLLLLSSDS.........",
	"......SSSSSLLLLLLLLSSD..........",
	".....SS.SSLLLLLLLLLLSD..........",
	"....SS..SSLLLLLLLLLLSD..........",
	"....SS..SSLLLLLLLLLLSD..........",
	"....DD..SSLLLLLLLLLLSD..........",
	"....DD...SSLLLLLLLLSD...........",
	"....DDD.SSSSS....SSSSD..........",
}

// Mini is the small mascot for headers and the status bar — 14 by 5.
var Mini = []string{
	".....GGGG.....",
	"....hhhGGG....",
	"...hhhGGGGG...",
	".gggggggggggg.",
	"....SSSSSS....",
	"...SKKKKKKS...",
	"...SKWKKWKS...",
	"...SKKKKKKS...",
	"....SSLLSS....",
	".....SKKS.....",
}

// Wordmark spells ABRAHAM — 57 columns by 5 lines. '#' takes the gradient.
var Wordmark = []string{
	"..###...######..######....###...##...##...###...##.....##",
	".#####..#######.#######..#####..##...##..#####..###...###",
	"##...##.##...##.##...##.##...##.##...##.##...##.####.####",
	"##...##.##..##..##...##.##...##.##...##.##...##.##.###.##",
	"#######.######..#######.#######.#######.#######.##..#..##",
	"#######.######..######..#######.#######.#######.##.....##",
	"##...##.##..##..##.###..##...##.##...##.##...##.##.....##",
	"##...##.##...##.##..##..##...##.##...##.##...##.##.....##",
	"##...##.#######.##...##.##...##.##...##.##...##.##.....##",
	"##...##.######..##...##.##...##.##...##.##...##.##.....##",
}
