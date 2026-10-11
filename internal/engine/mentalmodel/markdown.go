package mentalmodel

import (
	"crypto/sha1" //nolint:gosec // G505: sha1 only derives short content ids, not used for security
	"encoding/hex"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/prompts"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// ErrAllOpsRejected is returned (wrapped) when a delta's non-empty op list still had no op applied after
// the one retry.
var ErrAllOpsRejected = errors.New("mentalmodel: every delta op was rejected")

// preambleSectionID is the id of the untitled section holding content that precedes the first heading.
const preambleSectionID = "preamble"

// Skip reasons recorded on ops that were not applied.
const (
	reasonEmptyBlock   = "block text is empty"
	reasonEmptyHeading = "heading is empty"
	reasonReplaceCount = "replace_block needs exactly one block"
	reasonFragment     = "block must be one markdown fragment: no heading, blank line or unclosed code fence " +
		"outside a code fence"
)

var (
	headingRx        = regexp.MustCompile(`^(#{1,6})\s+(.*\S)\s*$`)
	fenceRx          = regexp.MustCompile("^\\s*(`{3,}|~{3,})(.*)$")
	slugRx           = regexp.MustCompile(`[^a-z0-9]+`)
	tableRowRx       = regexp.MustCompile(`^\s*\|`)
	tableSeparatorRx = regexp.MustCompile(`^\s*\|?[\s:]*-{2,}[\s:|-]*\|?\s*$`)
)

// docBlock is one markdown fragment (paragraph, list, table or code fence) with its content-derived id.
type docBlock struct {
	ID   string
	Text string
}

// docSection is a heading and the blocks under it; the preamble section has an empty heading.
type docSection struct {
	ID      string
	Heading string
	Level   int
	Blocks  []docBlock
}

// document is a markdown page as ordered sections. Ids are recomputed from content by splitMarkdown.
type document struct {
	Sections []docSection
}

// prompt converts the document to the shape the MENTAL_MODEL_DELTA prompt reads.
func (d document) prompt() prompts.DeltaDocument {
	out := prompts.DeltaDocument{Sections: make([]prompts.DeltaSection, 0, len(d.Sections))}
	for _, s := range d.Sections {
		blocks := make([]prompts.DeltaBlock, 0, len(s.Blocks))
		for _, b := range s.Blocks {
			blocks = append(blocks, prompts.DeltaBlock{ID: b.ID, Text: b.Text})
		}
		out.Sections = append(out.Sections, prompts.DeltaSection{
			ID: s.ID, Heading: s.Heading, Level: s.Level, Blocks: blocks,
		})
	}
	return out
}

// blockCount is the number of blocks across all sections.
func (d document) blockCount() int {
	n := 0
	for _, s := range d.Sections {
		n += len(s.Blocks)
	}
	return n
}

// slugifyHeading derives a stable slug from a heading ("Stop Conditions" -> "stop-conditions").
func slugifyHeading(heading string) string {
	slug := strings.Trim(slugRx.ReplaceAllString(strings.ToLower(strings.TrimSpace(heading)), "-"), "-")
	if slug == "" {
		return "section"
	}
	return slug
}

// makeUniqueID disambiguates base against existing by appending -2, -3, ...
func makeUniqueID(base string, existing map[string]bool) string {
	if !existing[base] {
		return base
	}
	for i := 2; ; i++ {
		id := base + "-" + strconv.Itoa(i)
		if !existing[id] {
			return id
		}
	}
}

// makeBlockID derives a short, deterministic, document-unique id: "b" plus 8 hex digits of sha1(text).
func makeBlockID(text string, existing map[string]bool) string {
	sum := sha1.Sum([]byte(text)) //nolint:gosec // G401: content id, not a security primitive
	return makeUniqueID("b"+hex.EncodeToString(sum[:])[:8], existing)
}

// normalizeBlockText trims blank lines around a fragment without touching its interior.
func normalizeBlockText(text string) string {
	lines := strings.Split(text, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// cleanHeading strips leading '#' markers and surrounding space and keeps the heading on one line.
func cleanHeading(heading string) string {
	h := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(heading), "#"))
	return strings.Join(strings.FieldsFunc(h, func(r rune) bool { return r == '\n' || r == '\r' }), " ")
}

// splitLines splits markdown into lines, accepting \n, \r\n and \r line endings.
func splitLines(md string) []string {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	md = strings.ReplaceAll(md, "\r", "\n")
	return strings.Split(md, "\n")
}

// trackFence updates the open fence (its opening run) for line and reports whether line opens or closes a
// fence. As in CommonMark, a backtick opener's info string has no backtick, and a closer is a run of the
// opener's character at least as long as the opener with only whitespace after it.
func trackFence(fence *string, line string) bool {
	m := fenceRx.FindStringSubmatch(line)
	if m == nil {
		return false
	}
	run, rest := m[1], m[2]
	if *fence == "" {
		if run[0] == '`' && strings.Contains(rest, "`") {
			return false
		}
		*fence = run
		return true
	}
	if run[0] == (*fence)[0] && len(run) >= len(*fence) && strings.TrimSpace(rest) == "" {
		*fence = ""
		return true
	}
	return false
}

// isFragment reports whether text splits into exactly one block: no heading or blank line outside a code
// fence, and every fence it opens is closed.
func isFragment(text string) bool {
	fence := ""
	for _, line := range splitLines(normalizeBlockText(text)) {
		if trackFence(&fence, line) || fence != "" {
			continue
		}
		if strings.TrimSpace(line) == "" || headingRx.MatchString(line) {
			return false
		}
	}
	return fence == ""
}

// badFragments reports whether any non-blank text is not a single markdown fragment.
func badFragments(texts []string) bool {
	for _, t := range texts {
		if strings.TrimSpace(t) != "" && !isFragment(t) {
			return true
		}
	}
	return false
}

// usesBlocks reports whether an op kind reads BlocksMD.
func usesBlocks(kind domain.DeltaOpKind) bool {
	switch kind {
	case domain.DeltaOpAddSection, domain.DeltaOpReplaceSectionBlocks, domain.DeltaOpAppendBlock,
		domain.DeltaOpInsertBlock, domain.DeltaOpReplaceBlock:
		return true
	default:
		return false
	}
}

// splitBlocks groups a section's lines into blocks on blank lines, ignoring blank lines inside fences.
func splitBlocks(lines []string, blockIDs map[string]bool) []docBlock {
	var blocks []docBlock
	var current []string
	flush := func() {
		text := normalizeBlockText(strings.Join(current, "\n"))
		current = current[:0]
		if strings.TrimSpace(text) == "" {
			return
		}
		id := makeBlockID(text, blockIDs)
		blockIDs[id] = true
		blocks = append(blocks, docBlock{ID: id, Text: text})
	}
	fence := ""
	for _, line := range lines {
		if !trackFence(&fence, line) && fence == "" && strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		current = append(current, line)
	}
	flush()
	return blocks
}

// splitter accumulates sections while splitMarkdown walks the lines.
type splitter struct {
	sections   []docSection
	sectionIDs map[string]bool
	blockIDs   map[string]bool
	pending    []string
	current    *docSection
}

// close attaches the pending lines to sec as blocks and appends sec to the document.
func (s *splitter) close(sec docSection) {
	sec.Blocks = splitBlocks(s.pending, s.blockIDs)
	s.pending = nil
	s.sections = append(s.sections, sec)
}

// flushOpen closes the open section, or the preamble when content precedes the first heading.
func (s *splitter) flushOpen() {
	switch {
	case s.current != nil:
		s.close(*s.current)
	case hasContent(s.pending):
		id := makeUniqueID(preambleSectionID, s.sectionIDs)
		s.sectionIDs[id] = true
		s.close(docSection{ID: id, Level: 2})
	default:
		s.pending = nil
	}
}

// hasContent reports whether any line is non-blank.
func hasContent(lines []string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			return true
		}
	}
	return false
}

// splitMarkdown splits markdown into sections (at ATX headings outside fences) and blocks (at blank lines
// outside fences), losslessly: every non-blank line comes back verbatim, in order. Section ids are unique
// heading slugs; block ids are content hashes. Splitting the same markdown always yields the same ids.
func splitMarkdown(md string) document {
	s := &splitter{sectionIDs: map[string]bool{}, blockIDs: map[string]bool{}}
	fence := ""
	for _, line := range splitLines(md) {
		if trackFence(&fence, line) || fence != "" {
			s.pending = append(s.pending, line)
			continue
		}
		m := headingRx.FindStringSubmatch(line)
		if m == nil {
			s.pending = append(s.pending, line)
			continue
		}
		s.flushOpen()
		heading := strings.TrimSpace(m[2])
		id := makeUniqueID(slugifyHeading(heading), s.sectionIDs)
		s.sectionIDs[id] = true
		s.current = &docSection{ID: id, Heading: heading, Level: len(m[1])}
	}
	s.flushOpen()
	return document{Sections: s.sections}
}

// renderSection renders the heading (when there is one) then the blocks, blank-line separated.
func renderSection(s docSection) string {
	var parts []string
	if h := strings.TrimSpace(s.Heading); h != "" {
		parts = append(parts, strings.Repeat("#", s.Level)+" "+h)
	}
	for _, b := range s.Blocks {
		if strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// renderDocument renders the document to canonical markdown, ending in "\n" when non-empty.
func renderDocument(d document) string {
	var parts []string
	for _, s := range d.Sections {
		if r := renderSection(s); r != "" {
			parts = append(parts, r)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n") + "\n"
}

// allTableRows reports whether text has lines and every line starts with a pipe.
func allTableRows(lines []string) bool {
	if len(lines) == 0 {
		return false
	}
	for _, l := range lines {
		if !tableRowRx.MatchString(l) {
			return false
		}
	}
	return true
}

// hasTableSeparator reports whether any line is a table separator row.
func hasTableSeparator(lines []string) bool {
	for _, l := range lines {
		if tableSeparatorRx.MatchString(l) {
			return true
		}
	}
	return false
}

// isTable reports whether text is a table: a header, a separator and pipes on every line.
func isTable(text string) bool {
	lines := strings.Split(text, "\n")
	return len(lines) >= 2 && allTableRows(lines) && hasTableSeparator(lines)
}

// isOrphanTableRows reports whether text is bare table rows with no separator of their own.
func isOrphanTableRows(text string) bool {
	lines := strings.Split(text, "\n")
	return allTableRows(lines) && !hasTableSeparator(lines)
}

// editor applies ops to a working document, handing out block ids that stay unique for the whole batch.
type editor struct {
	doc      document
	reserved map[string]bool
}

// newEditor builds an editor over d, reserving every existing block id.
func newEditor(d document) *editor {
	e := &editor{doc: d, reserved: map[string]bool{}}
	for _, s := range d.Sections {
		for _, b := range s.Blocks {
			e.reserved[b.ID] = true
		}
	}
	return e
}

// section finds a section by exact id.
func (e *editor) section(id string) *docSection {
	for i := range e.doc.Sections {
		if e.doc.Sections[i].ID == id {
			return &e.doc.Sections[i]
		}
	}
	return nil
}

// newBlock normalizes text and gives it a fresh batch-unique id.
func (e *editor) newBlock(text string) docBlock {
	text = normalizeBlockText(text)
	id := makeBlockID(text, e.reserved)
	e.reserved[id] = true
	return docBlock{ID: id, Text: text}
}

// newBlocks builds blocks from the non-blank texts, in order.
func (e *editor) newBlocks(texts []string) []docBlock {
	var out []docBlock
	for _, t := range texts {
		if strings.TrimSpace(t) != "" {
			out = append(out, e.newBlock(t))
		}
	}
	return out
}

// resolveBlock finds blockID in sec; when it is missing it returns the skip reason.
func (e *editor) resolveBlock(sec *docSection, blockID string) (int, string) {
	for i, b := range sec.Blocks {
		if b.ID == blockID {
			return i, ""
		}
	}
	for _, s := range e.doc.Sections {
		for _, b := range s.Blocks {
			if b.ID == blockID {
				return -1, fmt.Sprintf("block_id %s belongs to section %s, not %s", blockID, s.ID, sec.ID)
			}
		}
	}
	return -1, "unknown block_id: " + blockID
}

// insertBlocks inserts the non-blank texts at pos in order. Bare table rows that follow a table are
// merged into it instead of becoming a broken fragment of their own.
func (e *editor) insertBlocks(sec *docSection, pos int, texts []string) {
	for _, t := range texts {
		if strings.TrimSpace(t) == "" {
			continue
		}
		norm := normalizeBlockText(t)
		if pos > 0 && isOrphanTableRows(norm) && isTable(sec.Blocks[pos-1].Text) {
			sec.Blocks[pos-1].Text += "\n" + norm
			continue
		}
		b := e.newBlock(norm)
		sec.Blocks = slices.Insert(sec.Blocks, pos, b)
		pos++
	}
}

// allBlank reports whether every text is blank (or there are none).
func allBlank(texts []string) bool {
	for _, t := range texts {
		if strings.TrimSpace(t) != "" {
			return false
		}
	}
	return true
}

// unknownSection is the skip reason for a section id that does not resolve.
func unknownSection(id string) string {
	return "unknown section_id: " + id
}

// apply applies one op and returns "" when applied, else the skip reason. An op whose BlocksMD has an entry
// that is not one markdown fragment is skipped whole.
func (e *editor) apply(op domain.DeltaOp) string {
	if usesBlocks(op.Kind) && badFragments(op.BlocksMD) {
		return reasonFragment
	}
	switch op.Kind {
	case domain.DeltaOpAddSection:
		return e.addSection(op)
	case domain.DeltaOpRemoveSection:
		return e.removeSection(op)
	case domain.DeltaOpRenameSection:
		return e.renameSection(op)
	case domain.DeltaOpReplaceSectionBlocks:
		return e.replaceSectionBlocks(op)
	case domain.DeltaOpAppendBlock:
		return e.appendBlock(op)
	case domain.DeltaOpInsertBlock:
		return e.insertBlock(op)
	case domain.DeltaOpReplaceBlock:
		return e.replaceBlock(op)
	case domain.DeltaOpRemoveBlock:
		return e.removeBlock(op)
	default:
		return "unknown op kind: " + string(op.Kind)
	}
}

// addSection appends a level-2 section at the end of the document, id = unique slug of the heading.
func (e *editor) addSection(op domain.DeltaOp) string {
	heading := cleanHeading(op.Heading)
	if heading == "" {
		return reasonEmptyHeading
	}
	ids := map[string]bool{}
	for _, s := range e.doc.Sections {
		ids[s.ID] = true
	}
	e.doc.Sections = append(e.doc.Sections, docSection{
		ID:      makeUniqueID(slugifyHeading(heading), ids),
		Heading: heading,
		Level:   2,
		Blocks:  e.newBlocks(op.BlocksMD),
	})
	return ""
}

// removeSection drops a section and its blocks.
func (e *editor) removeSection(op domain.DeltaOp) string {
	for i, s := range e.doc.Sections {
		if s.ID == op.SectionID {
			e.doc.Sections = slices.Delete(e.doc.Sections, i, i+1)
			return ""
		}
	}
	return unknownSection(op.SectionID)
}

// renameSection replaces a section's heading; its id stays the same for the rest of the batch.
func (e *editor) renameSection(op domain.DeltaOp) string {
	sec := e.section(op.SectionID)
	if sec == nil {
		return unknownSection(op.SectionID)
	}
	heading := cleanHeading(op.Heading)
	if heading == "" {
		return reasonEmptyHeading
	}
	sec.Heading = heading
	return ""
}

// replaceSectionBlocks replaces every block of a section with the non-blank texts.
func (e *editor) replaceSectionBlocks(op domain.DeltaOp) string {
	sec := e.section(op.SectionID)
	if sec == nil {
		return unknownSection(op.SectionID)
	}
	sec.Blocks = e.newBlocks(op.BlocksMD)
	return ""
}

// appendBlock adds the blocks at the end of a section.
func (e *editor) appendBlock(op domain.DeltaOp) string {
	sec := e.section(op.SectionID)
	if sec == nil {
		return unknownSection(op.SectionID)
	}
	if allBlank(op.BlocksMD) {
		return reasonEmptyBlock
	}
	e.insertBlocks(sec, len(sec.Blocks), op.BlocksMD)
	return ""
}

// insertBlock adds the blocks after AfterBlockID, or first in the section when it is empty.
func (e *editor) insertBlock(op domain.DeltaOp) string {
	sec := e.section(op.SectionID)
	if sec == nil {
		return unknownSection(op.SectionID)
	}
	if allBlank(op.BlocksMD) {
		return reasonEmptyBlock
	}
	pos := 0
	if op.AfterBlockID != "" {
		i, reason := e.resolveBlock(sec, op.AfterBlockID)
		if reason != "" {
			return reason
		}
		pos = i + 1
	}
	e.insertBlocks(sec, pos, op.BlocksMD)
	return ""
}

// replaceBlock replaces one block's text, keeping its id for the rest of the batch.
func (e *editor) replaceBlock(op domain.DeltaOp) string {
	sec := e.section(op.SectionID)
	if sec == nil {
		return unknownSection(op.SectionID)
	}
	if len(op.BlocksMD) != 1 {
		return reasonReplaceCount
	}
	if strings.TrimSpace(op.BlocksMD[0]) == "" {
		return reasonEmptyBlock
	}
	i, reason := e.resolveBlock(sec, op.BlockID)
	if reason != "" {
		return reason
	}
	sec.Blocks[i].Text = normalizeBlockText(op.BlocksMD[0])
	return ""
}

// removeBlock drops one block.
func (e *editor) removeBlock(op domain.DeltaOp) string {
	sec := e.section(op.SectionID)
	if sec == nil {
		return unknownSection(op.SectionID)
	}
	i, reason := e.resolveBlock(sec, op.BlockID)
	if reason != "" {
		return reason
	}
	sec.Blocks = slices.Delete(sec.Blocks, i, i+1)
	return ""
}

// applyEdits splits doc, applies ops in order and returns the rendered result plus every op with Applied /
// SkipReason filled in (same order and length as ops). It never mutates ops. When no op applies, doc is
// returned unchanged; otherwise the result is the canonical render of the edited document.
func applyEdits(doc string, ops []domain.DeltaOp) (string, []domain.DeltaOp) {
	e := newEditor(splitMarkdown(doc))
	out := make([]domain.DeltaOp, len(ops))
	applied := false
	for i, op := range ops {
		reason := e.apply(op)
		op.Applied = reason == ""
		op.SkipReason = reason
		applied = applied || op.Applied
		out[i] = op
	}
	if !applied {
		return doc, out
	}
	return renderDocument(e.doc), out
}

// changedFraction is the share of before's blocks (by exact text) that are absent from after, in [0,1];
// 0 when before has no blocks.
func changedFraction(before, after string) float64 {
	remaining := map[string]int{}
	for _, s := range splitMarkdown(after).Sections {
		for _, b := range s.Blocks {
			remaining[b.Text]++
		}
	}
	total, missing := 0, 0
	for _, s := range splitMarkdown(before).Sections {
		for _, b := range s.Blocks {
			total++
			if remaining[b.Text] > 0 {
				remaining[b.Text]--
				continue
			}
			missing++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(missing) / float64(total)
}
