package reflect

import (
	"strings"
)

// doneDocument is the structured document the document-mode done tool takes.
type doneDocument struct {
	// Sections are the document's sections in order.
	Sections []docSection `json:"sections"`
}

// docSection is one section of a doneDocument.
type docSection struct {
	// Heading is the heading text without '#'; empty = no heading line.
	Heading string `json:"heading"`
	// Level is the heading level, 1-6 (clamped).
	Level int `json:"level"`
	// Blocks are the section's paragraphs, lists, tables or code fences.
	Blocks []string `json:"blocks"`
}

// markdown renders the document: each section's heading as an ATX heading at its level (clamped to 1-6),
// then its non-empty blocks, everything separated by blank lines.
func (d doneDocument) markdown() string {
	var parts []string
	for _, s := range d.Sections {
		if h := strings.TrimSpace(s.Heading); h != "" {
			level := min(max(s.Level, 1), 6)
			parts = append(parts, strings.Repeat("#", level)+" "+h)
		}
		for _, b := range s.Blocks {
			if b = strings.TrimSpace(b); b != "" {
				parts = append(parts, b)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}
