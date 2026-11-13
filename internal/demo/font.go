package demo

import (
	"image"
	"image/color"
	"image/draw"
	"strings"
	"unicode"
)

// A tiny 5x7 bitmap font (upper case only, accents drawn as an extra pixel
// row above the glyph). The Go standard library has no font rendering and
// golang.org/x/image is not a dependency, so the synthetic front pages use this.
var glyphs = map[rune][7]string{
	'A':  {"01110", "10001", "10001", "11111", "10001", "10001", "10001"},
	'B':  {"11110", "10001", "10001", "11110", "10001", "10001", "11110"},
	'C':  {"01110", "10001", "10000", "10000", "10000", "10001", "01110"},
	'D':  {"11110", "10001", "10001", "10001", "10001", "10001", "11110"},
	'E':  {"11111", "10000", "10000", "11110", "10000", "10000", "11111"},
	'F':  {"11111", "10000", "10000", "11110", "10000", "10000", "10000"},
	'G':  {"01110", "10001", "10000", "10111", "10001", "10001", "01111"},
	'H':  {"10001", "10001", "10001", "11111", "10001", "10001", "10001"},
	'I':  {"01110", "00100", "00100", "00100", "00100", "00100", "01110"},
	'J':  {"00111", "00010", "00010", "00010", "00010", "10010", "01100"},
	'K':  {"10001", "10010", "10100", "11000", "10100", "10010", "10001"},
	'L':  {"10000", "10000", "10000", "10000", "10000", "10000", "11111"},
	'M':  {"10001", "11011", "10101", "10101", "10001", "10001", "10001"},
	'N':  {"10001", "11001", "10101", "10011", "10001", "10001", "10001"},
	'O':  {"01110", "10001", "10001", "10001", "10001", "10001", "01110"},
	'P':  {"11110", "10001", "10001", "11110", "10000", "10000", "10000"},
	'Q':  {"01110", "10001", "10001", "10001", "10101", "10010", "01101"},
	'R':  {"11110", "10001", "10001", "11110", "10100", "10010", "10001"},
	'S':  {"01111", "10000", "10000", "01110", "00001", "00001", "11110"},
	'T':  {"11111", "00100", "00100", "00100", "00100", "00100", "00100"},
	'U':  {"10001", "10001", "10001", "10001", "10001", "10001", "01110"},
	'V':  {"10001", "10001", "10001", "10001", "10001", "01010", "00100"},
	'W':  {"10001", "10001", "10001", "10101", "10101", "11011", "10001"},
	'X':  {"10001", "10001", "01010", "00100", "01010", "10001", "10001"},
	'Y':  {"10001", "10001", "01010", "00100", "00100", "00100", "00100"},
	'Z':  {"11111", "00001", "00010", "00100", "01000", "10000", "11111"},
	'0':  {"01110", "10001", "10011", "10101", "11001", "10001", "01110"},
	'1':  {"00100", "01100", "00100", "00100", "00100", "00100", "01110"},
	'2':  {"01110", "10001", "00001", "00010", "00100", "01000", "11111"},
	'3':  {"11110", "00001", "00001", "01110", "00001", "00001", "11110"},
	'4':  {"00010", "00110", "01010", "10010", "11111", "00010", "00010"},
	'5':  {"11111", "10000", "11110", "00001", "00001", "10001", "01110"},
	'6':  {"00110", "01000", "10000", "11110", "10001", "10001", "01110"},
	'7':  {"11111", "00001", "00010", "00100", "01000", "01000", "01000"},
	'8':  {"01110", "10001", "10001", "01110", "10001", "10001", "01110"},
	'9':  {"01110", "10001", "10001", "01111", "00001", "00010", "01100"},
	'.':  {"00000", "00000", "00000", "00000", "00000", "01100", "01100"},
	',':  {"00000", "00000", "00000", "00000", "01100", "00100", "01000"},
	':':  {"00000", "01100", "01100", "00000", "01100", "01100", "00000"},
	';':  {"00000", "01100", "01100", "00000", "01100", "00100", "01000"},
	'-':  {"00000", "00000", "00000", "11111", "00000", "00000", "00000"},
	'\'': {"00100", "00100", "01000", "00000", "00000", "00000", "00000"},
	'"':  {"01010", "01010", "00000", "00000", "00000", "00000", "00000"},
	'(':  {"00010", "00100", "01000", "01000", "01000", "00100", "00010"},
	')':  {"01000", "00100", "00010", "00010", "00010", "00100", "01000"},
	'%':  {"11001", "11010", "00010", "00100", "01000", "01011", "10011"},
	'?':  {"01110", "10001", "00001", "00010", "00100", "00000", "00100"},
	'!':  {"00100", "00100", "00100", "00100", "00100", "00000", "00100"},
	'/':  {"00001", "00001", "00010", "00100", "01000", "10000", "10000"},
	'+':  {"00000", "00100", "00100", "11111", "00100", "00100", "00000"},
	'&':  {"01100", "10010", "10100", "01000", "10101", "10010", "01101"},
	'€':  {"00110", "01001", "11100", "01000", "11100", "01001", "00110"},
	'|':  {"00100", "00100", "00100", "00100", "00100", "00100", "00100"},
	'·':  {"00000", "00000", "00100", "00000", "00000", "00000", "00000"},
}

// fold maps a rune to its base glyph and the accent drawn above it
// (0 none, 1 acute, 2 tilde, 3 diaeresis).
func fold(r rune) (rune, int) {
	r = unicode.ToUpper(r)
	switch r {
	case 'Á':
		return 'A', 1
	case 'É':
		return 'E', 1
	case 'Í':
		return 'I', 1
	case 'Ó':
		return 'O', 1
	case 'Ú':
		return 'U', 1
	case 'Ü':
		return 'U', 3
	case 'Ñ':
		return 'N', 2
	case '¿':
		return '?', 0
	case '¡':
		return '!', 0
	case '«', '»', '“', '”':
		return '"', 0
	case '’', '‘':
		return '\'', 0
	case '–', '—':
		return '-', 0
	}
	return r, 0
}

// TextWidth is the pixel width of s at the given integer scale.
func TextWidth(s string, scale int) int { return len([]rune(s)) * 6 * scale }

// DrawText draws s (upper-cased) with its top-left corner at (x,y) and
// returns the x just after the last glyph.
func DrawText(img *image.RGBA, x, y, scale int, s string, c color.Color) int {
	src := image.NewUniform(c)
	for _, r := range s {
		g, acc := fold(r)
		if gl, ok := glyphs[g]; ok {
			for row, line := range gl {
				for col, ch := range line {
					if ch == '1' {
						draw.Draw(img, image.Rect(x+col*scale, y+row*scale, x+(col+1)*scale, y+(row+1)*scale), src, image.Point{}, draw.Src)
					}
				}
			}
			switch acc {
			case 1:
				draw.Draw(img, image.Rect(x+3*scale, y-2*scale, x+4*scale, y-scale), src, image.Point{}, draw.Src)
			case 2:
				draw.Draw(img, image.Rect(x+1*scale, y-2*scale, x+4*scale, y-scale), src, image.Point{}, draw.Src)
			case 3:
				draw.Draw(img, image.Rect(x+1*scale, y-2*scale, x+2*scale, y-scale), src, image.Point{}, draw.Src)
				draw.Draw(img, image.Rect(x+3*scale, y-2*scale, x+4*scale, y-scale), src, image.Point{}, draw.Src)
			}
		}
		x += 6 * scale
	}
	return x
}

// WrapText splits s into lines of at most width pixels at the given scale,
// returning at most maxLines (the last one ends with "..." when truncated).
func WrapText(s string, scale, width, maxLines int) []string {
	maxChars := width / (6 * scale)
	if maxChars < 6 {
		maxChars = 6
	}
	var lines []string
	cur := ""
	for _, w := range strings.Fields(s) {
		cand := w
		if cur != "" {
			cand = cur + " " + w
		}
		if len([]rune(cand)) <= maxChars || cur == "" {
			cur = cand
			continue
		}
		lines = append(lines, cur)
		cur = w
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		l := []rune(lines[maxLines-1])
		if len(l) > maxChars-3 {
			l = l[:maxChars-3]
		}
		lines[maxLines-1] = strings.TrimSpace(string(l)) + "..."
	}
	return lines
}
