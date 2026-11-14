package layout

import (
	"strings"
	"unicode/utf8"
)

// Measure returns the cell width of a string.
type Measure func(string) int

func runeCount(s string) int { return utf8.RuneCountInString(s) }

// truncate cuts s to at most w cells, ending in "..." when cut.
func truncate(s string, w int, m Measure) string {
	if w <= 0 {
		return ""
	}
	if m(s) <= w {
		return s
	}
	if w <= 3 {
		return strings.Repeat(".", w)
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := m(string(r))
		if used+rw > w-3 {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + "..."
}

// pad fills s with spaces on the right to exactly w cells.
func pad(s string, w int, m Measure) string {
	n := m(s)
	if n >= w {
		return s
	}
	return s + strings.Repeat(" ", w-n)
}

// wrap breaks text into lines of at most w cells, on spaces where it can.
// Paragraph breaks in the source are kept.
func wrap(text string, w int, m Measure) []string {
	var out []string
	for _, para := range strings.Split(strings.TrimSpace(text), "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			continue
		}
		line := ""
		for _, word := range words {
			for m(word) > w {
				// a word longer than the line: split it hard
				head := truncateHard(word, w, m)
				if line != "" {
					out = append(out, line)
					line = ""
				}
				out = append(out, head)
				word = word[len(head):]
			}
			switch {
			case line == "":
				line = word
			case m(line)+1+m(word) <= w:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func truncateHard(s string, w int, m Measure) string {
	used := 0
	for i, r := range s {
		rw := m(string(r))
		if used+rw > w {
			return s[:i]
		}
		used += rw
	}
	return s
}
