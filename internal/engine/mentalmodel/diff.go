package mentalmodel

import (
	"fmt"
	"strings"
)

// diffContext is the number of unchanged lines shown around each change.
const diffContext = 3

// maxDiffLines caps each side of the LCS table; larger inputs fall back to a whole-range replace.
const maxDiffLines = 4000

// diffLine is one line of an edit script: ' ' kept, '-' removed from before, '+' added in after.
type diffLine struct {
	op   byte
	text string
	aPos int // lines of before consumed before this line
	bPos int // lines of after consumed before this line
}

// diffMarkdown returns a unified diff from before ("--- current") to after ("+++ proposed") with
// three lines of context, or "" when the two have the same lines.
func diffMarkdown(before, after string) string {
	script := editScript(diffLines(before), diffLines(after))
	ranges := hunkRanges(script)
	if len(ranges) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("--- current\n+++ proposed\n")
	for _, r := range ranges {
		writeHunk(&sb, script[r[0]:r[1]])
	}
	return sb.String()
}

// diffLines splits text into lines; a final newline does not start an extra empty line.
func diffLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// editScript computes a line edit script from a to b: common prefix and suffix are kept and the middle
// is diffed by LCS (or replaced whole when it is too large), then line positions are stamped.
func editScript(a, b []string) []diffLine {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	script := make([]diffLine, 0, len(a)+len(b))
	for _, l := range a[:pre] {
		script = append(script, diffLine{op: ' ', text: l})
	}
	script = append(script, middleScript(a[pre:len(a)-suf], b[pre:len(b)-suf])...)
	for _, l := range a[len(a)-suf:] {
		script = append(script, diffLine{op: ' ', text: l})
	}
	stampPositions(script)
	return script
}

// middleScript diffs the differing middle of two inputs.
func middleScript(a, b []string) []diffLine {
	if len(a) > maxDiffLines || len(b) > maxDiffLines {
		return replaceScript(a, b)
	}
	return lcsScript(a, b)
}

// replaceScript removes every line of a then adds every line of b.
func replaceScript(a, b []string) []diffLine {
	out := make([]diffLine, 0, len(a)+len(b))
	for _, l := range a {
		out = append(out, diffLine{op: '-', text: l})
	}
	for _, l := range b {
		out = append(out, diffLine{op: '+', text: l})
	}
	return out
}

// lcsScript builds a minimal edit script from a longest-common-subsequence table over lines.
// Removals come before additions within a changed run.
func lcsScript(a, b []string) []diffLine {
	n, m := len(a), len(b)
	w := m + 1
	// lcs[i*w+j] is the LCS length of a[i:] and b[j:]; values fit in uint16 because n, m <= maxDiffLines.
	lcs := make([]uint16, (n+1)*w)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				lcs[i*w+j] = lcs[(i+1)*w+j+1] + 1
			case lcs[(i+1)*w+j] >= lcs[i*w+j+1]:
				lcs[i*w+j] = lcs[(i+1)*w+j]
			default:
				lcs[i*w+j] = lcs[i*w+j+1]
			}
		}
	}
	out := make([]diffLine, 0, n+m)
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			out = append(out, diffLine{op: ' ', text: a[i]})
			i, j = i+1, j+1
		case j == m || (i < n && lcs[(i+1)*w+j] >= lcs[i*w+j+1]):
			out = append(out, diffLine{op: '-', text: a[i]})
			i++
		default:
			out = append(out, diffLine{op: '+', text: b[j]})
			j++
		}
	}
	return out
}

// stampPositions records, for every line, how many before and after lines precede it.
func stampPositions(script []diffLine) {
	ap, bp := 0, 0
	for k := range script {
		script[k].aPos, script[k].bPos = ap, bp
		if script[k].op != '+' {
			ap++
		}
		if script[k].op != '-' {
			bp++
		}
	}
}

// hunkRanges groups changed lines into [start, end) ranges of the script with diffContext lines of
// context, merging changes whose context would touch or overlap.
func hunkRanges(script []diffLine) [][2]int {
	var ranges [][2]int
	for k, l := range script {
		if l.op == ' ' {
			continue
		}
		start := max(0, k-diffContext)
		end := min(len(script), k+diffContext+1)
		if n := len(ranges); n > 0 && start <= ranges[n-1][1] {
			ranges[n-1][1] = end
			continue
		}
		ranges = append(ranges, [2]int{start, end})
	}
	return ranges
}

// writeHunk writes one "@@ -a,b +c,d @@" hunk and its lines.
func writeHunk(sb *strings.Builder, lines []diffLine) {
	aLen, bLen := 0, 0
	for _, l := range lines {
		if l.op != '+' {
			aLen++
		}
		if l.op != '-' {
			bLen++
		}
	}
	fmt.Fprintf(sb, "@@ -%d,%d +%d,%d @@\n", hunkStart(lines[0].aPos, aLen), aLen, hunkStart(lines[0].bPos, bLen), bLen)
	for _, l := range lines {
		sb.WriteByte(l.op)
		sb.WriteString(l.text)
		sb.WriteByte('\n')
	}
}

// hunkStart is the 1-based first line of a range, or the line before it when the range is empty.
func hunkStart(pos, length int) int {
	if length == 0 {
		return pos
	}
	return pos + 1
}
