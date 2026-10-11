package mentalmodel

import (
	"memd/internal/domain"
	"reflect"
	"strings"
	"testing"
)

const sampleDoc = `Intro paragraph.

# Title

First para
continues here.

## Steps

- one
- two

` + "```go\n# not a heading\n\nfunc main() {}\n```" + `

| a | b |
| --- | --- |
| 1 | 2 |

## Steps

Duplicate heading body.
`

// sectionIDs lists a document's section ids in order.
func sectionIDs(d document) []string {
	var ids []string
	for _, s := range d.Sections {
		ids = append(ids, s.ID)
	}
	return ids
}

// blockTexts lists a section's block texts in order.
func blockTexts(s docSection) []string {
	var out []string
	for _, b := range s.Blocks {
		out = append(out, b.Text)
	}
	return out
}

// blockID returns the id of the block of section sec whose text is text.
func blockID(t *testing.T, d document, sec, text string) string {
	t.Helper()
	for _, s := range d.Sections {
		if s.ID != sec {
			continue
		}
		for _, b := range s.Blocks {
			if b.Text == text {
				return b.ID
			}
		}
	}
	t.Fatalf("no block %q in section %s", text, sec)
	return ""
}

func TestSplitMarkdownSections(t *testing.T) {
	d := splitMarkdown(sampleDoc)
	if got, want := sectionIDs(d), []string{"preamble", "title", "steps", "steps-2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("section ids = %v, want %v", got, want)
	}
	if d.Sections[0].Heading != "" || d.Sections[1].Level != 1 || d.Sections[2].Level != 2 {
		t.Fatalf("unexpected headings/levels: %+v", d.Sections)
	}
	wantSteps := []string{
		"- one\n- two",
		"```go\n# not a heading\n\nfunc main() {}\n```",
		"| a | b |\n| --- | --- |\n| 1 | 2 |",
	}
	if got := blockTexts(d.Sections[2]); !reflect.DeepEqual(got, wantSteps) {
		t.Fatalf("steps blocks = %q, want %q", got, wantSteps)
	}
	if got := blockTexts(d.Sections[1]); !reflect.DeepEqual(got, []string{"First para\ncontinues here."}) {
		t.Fatalf("title blocks = %q", got)
	}
	if d.blockCount() != 6 {
		t.Fatalf("blockCount = %d, want 6", d.blockCount())
	}
}

func TestSplitMarkdownBlockIDs(t *testing.T) {
	d1, d2 := splitMarkdown(sampleDoc), splitMarkdown(sampleDoc)
	if !reflect.DeepEqual(d1, d2) {
		t.Fatal("split is not deterministic")
	}
	seen := map[string]bool{}
	for _, s := range d1.Sections {
		for _, b := range s.Blocks {
			if len(b.ID) < 9 || b.ID[0] != 'b' || seen[b.ID] {
				t.Fatalf("bad or duplicate block id %q", b.ID)
			}
			seen[b.ID] = true
		}
	}
	dup := splitMarkdown("## A\n\nsame\n\n## B\n\nsame\n")
	a, b := dup.Sections[0].Blocks[0].ID, dup.Sections[1].Blocks[0].ID
	if b != a+"-2" {
		t.Fatalf("duplicate text ids = %q, %q; want second to be first + -2", a, b)
	}
}

func TestSplitRenderRoundTrip(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"empty":         {"", ""},
		"blank":         {"\n\n  \n", ""},
		"preamble only": {"hello\nworld", "hello\nworld\n"},
		"collapse blanks": {
			"# T\n\n\n\npara one\n\n\n\npara two\n\n",
			"# T\n\npara one\n\npara two\n",
		},
		"heading padding":    {"##   Spaced   \nbody", "## Spaced\n\nbody\n"},
		"crlf":               {"# T\r\n\r\nbody\r\n", "# T\n\nbody\n"},
		"hard break kept":    {"line  \nnext", "line  \nnext\n"},
		"tilde fence":        {"~~~\n# x\n\n```\n~~~\n", "~~~\n# x\n\n```\n~~~\n"},
		"not a heading":      {"#nospace\n\n####### seven", "#nospace\n\n####### seven\n"},
		"empty heading only": {"# A\n## B\n", "# A\n\n## B\n"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := renderDocument(splitMarkdown(c.in))
			if got != c.want {
				t.Fatalf("render(split(%q)) = %q, want %q", c.in, got, c.want)
			}
			if again := renderDocument(splitMarkdown(got)); again != got {
				t.Fatalf("render is not a fixed point: %q -> %q", got, again)
			}
		})
	}
	full := renderDocument(splitMarkdown(sampleDoc))
	if !strings.Contains(full, "```go\n# not a heading\n\nfunc main() {}\n```") {
		t.Fatalf("fence content altered:\n%s", full)
	}
	if renderDocument(splitMarkdown(full)) != full {
		t.Fatal("sample render is not a fixed point")
	}
}

func TestDocumentPrompt(t *testing.T) {
	d := splitMarkdown("## A\n\nx\n\n## B\n")
	p := d.prompt()
	if len(p.Sections) != 2 || p.Sections[0].ID != "a" || p.Sections[0].Level != 2 {
		t.Fatalf("prompt = %+v", p)
	}
	if p.Sections[0].Blocks[0].Text != "x" || p.Sections[0].Blocks[0].ID != d.Sections[0].Blocks[0].ID {
		t.Fatalf("prompt block = %+v", p.Sections[0].Blocks[0])
	}
	if p.Sections[1].Blocks == nil {
		t.Fatal("empty section must have a non-nil block list")
	}
}

func TestSlugifyAndCleanHeading(t *testing.T) {
	slugs := map[string]string{
		"Stop Conditions": "stop-conditions", "  Inputs & Context! ": "inputs-context", "!!!": "section",
	}
	for in, want := range slugs {
		if got := slugifyHeading(in); got != want {
			t.Errorf("slugifyHeading(%q) = %q, want %q", in, got, want)
		}
	}
	headings := map[string]string{"## Foo ": "Foo", "  #  Bar": "Bar", "a\nb": "a b", "###": ""}
	for in, want := range headings {
		if got := cleanHeading(in); got != want {
			t.Errorf("cleanHeading(%q) = %q, want %q", in, got, want)
		}
	}
}

const editDoc = "## Alpha\n\nfirst\n\nsecond\n\n## Beta\n\nthird\n"

func TestApplyEditsOpKinds(t *testing.T) {
	d := splitMarkdown(editDoc)
	first, second := blockID(t, d, "alpha", "first"), blockID(t, d, "alpha", "second")
	cases := []struct {
		name string
		op   domain.DeltaOp
		want string
	}{
		{
			"add_section",
			domain.DeltaOp{Kind: domain.DeltaOpAddSection, Heading: "## Gamma", BlocksMD: []string{"g1", "  ", "\ng2\n"}},
			editDoc + "\n## Gamma\n\ng1\n\ng2\n",
		},
		{
			"add_section duplicate heading",
			domain.DeltaOp{Kind: domain.DeltaOpAddSection, Heading: "Alpha"},
			editDoc + "\n## Alpha\n",
		},
		{"remove_section", domain.DeltaOp{Kind: domain.DeltaOpRemoveSection, SectionID: "alpha"}, "## Beta\n\nthird\n"},
		{
			"rename_section",
			domain.DeltaOp{Kind: domain.DeltaOpRenameSection, SectionID: "beta", Heading: "# Delta"},
			"## Alpha\n\nfirst\n\nsecond\n\n## Delta\n\nthird\n",
		},
		{"replace_section_blocks", domain.DeltaOp{
			Kind: domain.DeltaOpReplaceSectionBlocks, SectionID: "alpha",
			BlocksMD: []string{"x", "", "y"},
		}, "## Alpha\n\nx\n\ny\n\n## Beta\n\nthird\n"},
		{
			"append_block",
			domain.DeltaOp{Kind: domain.DeltaOpAppendBlock, SectionID: "alpha", BlocksMD: []string{"n1", "n2"}},
			"## Alpha\n\nfirst\n\nsecond\n\nn1\n\nn2\n\n## Beta\n\nthird\n",
		},
		{"insert_block after", domain.DeltaOp{
			Kind: domain.DeltaOpInsertBlock, SectionID: "alpha", AfterBlockID: first,
			BlocksMD: []string{"n1", "n2"},
		}, "## Alpha\n\nfirst\n\nn1\n\nn2\n\nsecond\n\n## Beta\n\nthird\n"},
		{
			"insert_block first",
			domain.DeltaOp{Kind: domain.DeltaOpInsertBlock, SectionID: "alpha", BlocksMD: []string{"n0"}},
			"## Alpha\n\nn0\n\nfirst\n\nsecond\n\n## Beta\n\nthird\n",
		},
		{"replace_block", domain.DeltaOp{
			Kind: domain.DeltaOpReplaceBlock, SectionID: "alpha", BlockID: second,
			BlocksMD: []string{"\nchanged\n"},
		}, "## Alpha\n\nfirst\n\nchanged\n\n## Beta\n\nthird\n"},
		{
			"remove_block",
			domain.DeltaOp{Kind: domain.DeltaOpRemoveBlock, SectionID: "alpha", BlockID: first},
			"## Alpha\n\nsecond\n\n## Beta\n\nthird\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, outcome := applyEdits(editDoc, []domain.DeltaOp{c.op})
			if !outcome[0].Applied || outcome[0].SkipReason != "" {
				t.Fatalf("op not applied: %+v", outcome[0])
			}
			if got != c.want {
				t.Fatalf("got\n%q\nwant\n%q", got, c.want)
			}
		})
	}
}

func TestApplyEditsSkipReasons(t *testing.T) {
	d := splitMarkdown(editDoc)
	first, third := blockID(t, d, "alpha", "first"), blockID(t, d, "beta", "third")
	cases := []struct {
		name string
		op   domain.DeltaOp
		want string
	}{
		{"unknown kind", domain.DeltaOp{Kind: "explode"}, "unknown op kind: explode"},
		{
			"unknown section",
			domain.DeltaOp{Kind: domain.DeltaOpAppendBlock, SectionID: "nope", BlocksMD: []string{"x"}},
			"unknown section_id: nope",
		},
		{
			"remove unknown section",
			domain.DeltaOp{Kind: domain.DeltaOpRemoveSection, SectionID: "nope"},
			"unknown section_id: nope",
		},
		{
			"unknown block",
			domain.DeltaOp{Kind: domain.DeltaOpRemoveBlock, SectionID: "alpha", BlockID: "bdeadbeef"},
			"unknown block_id: bdeadbeef",
		},
		{"insert after unknown block", domain.DeltaOp{
			Kind: domain.DeltaOpInsertBlock, SectionID: "alpha",
			AfterBlockID: "bdeadbeef", BlocksMD: []string{"x"},
		}, "unknown block_id: bdeadbeef"},
		{"wrong section", domain.DeltaOp{
			Kind: domain.DeltaOpReplaceBlock, SectionID: "alpha", BlockID: third,
			BlocksMD: []string{"x"},
		}, "block_id " + third + " belongs to section beta, not alpha"},
		{
			"empty append",
			domain.DeltaOp{Kind: domain.DeltaOpAppendBlock, SectionID: "alpha", BlocksMD: []string{" ", ""}},
			"block text is empty",
		},
		{"no blocks insert", domain.DeltaOp{Kind: domain.DeltaOpInsertBlock, SectionID: "alpha"}, "block text is empty"},
		{"empty replace", domain.DeltaOp{
			Kind: domain.DeltaOpReplaceBlock, SectionID: "alpha", BlockID: first,
			BlocksMD: []string{"\n"},
		}, "block text is empty"},
		{"replace two", domain.DeltaOp{
			Kind: domain.DeltaOpReplaceBlock, SectionID: "alpha", BlockID: first,
			BlocksMD: []string{"a", "b"},
		}, "replace_block needs exactly one block"},
		{
			"replace none",
			domain.DeltaOp{Kind: domain.DeltaOpReplaceBlock, SectionID: "alpha", BlockID: first},
			"replace_block needs exactly one block",
		},
		{"add empty heading", domain.DeltaOp{Kind: domain.DeltaOpAddSection, Heading: " ## "}, "heading is empty"},
		{"rename empty heading", domain.DeltaOp{Kind: domain.DeltaOpRenameSection, SectionID: "alpha"}, "heading is empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, outcome := applyEdits(editDoc, []domain.DeltaOp{c.op})
			if outcome[0].Applied || outcome[0].SkipReason != c.want {
				t.Fatalf("outcome = %+v, want skip %q", outcome[0], c.want)
			}
			if got != editDoc {
				t.Fatalf("doc changed on skip: %q", got)
			}
		})
	}
}

func TestApplyEditsPartialAndOrdered(t *testing.T) {
	ops := []domain.DeltaOp{
		{Kind: domain.DeltaOpRemoveSection, SectionID: "missing"},
		{Kind: domain.DeltaOpAddSection, Heading: "New Part"},
		{Kind: domain.DeltaOpAppendBlock, SectionID: "new-part", BlocksMD: []string{"added later"}},
		{Kind: domain.DeltaOpRemoveSection, SectionID: "beta"},
	}
	orig := make([]domain.DeltaOp, len(ops))
	copy(orig, ops)
	got, outcome := applyEdits(editDoc, ops)
	want := "## Alpha\n\nfirst\n\nsecond\n\n## New Part\n\nadded later\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	applied := []bool{false, true, true, true}
	for i, op := range outcome {
		if op.Applied != applied[i] {
			t.Fatalf("op %d applied = %v, want %v (%+v)", i, op.Applied, applied[i], op)
		}
	}
	if !reflect.DeepEqual(ops, orig) {
		t.Fatal("applyEdits mutated its input ops")
	}
}

// TestApplyEditsAllRejectedAndEmpty checks that a document is returned byte for byte (not re-rendered) when
// no op applies or there are no ops.
func TestApplyEditsAllRejectedAndEmpty(t *testing.T) {
	doc := "# Not   canonical\n\n\n\nbody" // a canonical render would differ
	got, outcome := applyEdits(doc, []domain.DeltaOp{{Kind: domain.DeltaOpRemoveSection, SectionID: "nope"}})
	if got != doc || len(outcome) != 1 || outcome[0].Applied || outcome[0].SkipReason == "" {
		t.Fatalf("all rejected: doc %q, outcome %+v", got, outcome)
	}
	for _, ops := range [][]domain.DeltaOp{nil, {}} {
		if got, _ = applyEdits(doc, ops); got != doc {
			t.Fatalf("empty ops: %q", got)
		}
	}
}

func TestApplyEditsOrphanTableRows(t *testing.T) {
	doc := "## T\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n\n## U\n\npara\n"
	d := splitMarkdown(doc)
	table := d.Sections[0].Blocks[0].ID
	want := "## T\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n| 3 | 4 |\n\n## U\n\npara\n"
	for _, op := range []domain.DeltaOp{
		{Kind: domain.DeltaOpAppendBlock, SectionID: "t", BlocksMD: []string{"| 3 | 4 |"}},
		{Kind: domain.DeltaOpInsertBlock, SectionID: "t", AfterBlockID: table, BlocksMD: []string{"\n| 3 | 4 |\n"}},
	} {
		got, outcome := applyEdits(doc, []domain.DeltaOp{op})
		if got != want || !outcome[0].Applied {
			t.Fatalf("%s: got %q (%+v), want %q", op.Kind, got, outcome[0], want)
		}
	}
	// Rows after a non-table block, or at the start of a section, stay a block of their own.
	row := []string{"| 9 |"}
	got, _ := applyEdits(doc, []domain.DeltaOp{{Kind: domain.DeltaOpAppendBlock, SectionID: "u", BlocksMD: row}})
	if !strings.HasSuffix(got, "para\n\n| 9 |\n") {
		t.Fatalf("orphan rows merged into a non-table: %q", got)
	}
	got, _ = applyEdits(doc, []domain.DeltaOp{{Kind: domain.DeltaOpInsertBlock, SectionID: "t", BlocksMD: row}})
	if !strings.HasPrefix(got, "## T\n\n| 9 |\n\n| a | b |") {
		t.Fatalf("orphan rows at position 0 merged: %q", got)
	}
}

func TestChangedFraction(t *testing.T) {
	cases := []struct {
		name          string
		before, after string
		want          float64
	}{
		{"no blocks", "", "anything", 0},
		{"identical", editDoc, editDoc, 0},
		{"one of three", editDoc, "## Alpha\n\nfirst\n\nNEW\n\n## Beta\n\nthird\n", 1.0 / 3},
		{"all gone", editDoc, "", 1},
		{"moved is not changed", editDoc, "## Beta\n\nthird\n\nsecond\n\nfirst\n", 0},
		{"duplicates counted", "a\n\na\n", "a\n", 0.5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := changedFraction(c.before, c.after); got != c.want {
				t.Fatalf("changedFraction = %v, want %v", got, c.want)
			}
		})
	}
}

// TestSplitMarkdownFences checks CommonMark fence rules: a fence closes only on the opener's character with
// a run at least as long and nothing but whitespace after it, and a backtick info string has no backtick.
func TestSplitMarkdownFences(t *testing.T) {
	cases := map[string]struct {
		in   string
		want []string
	}{
		"longer opener":       {"````\n```\n\n## x\n````", []string{"````\n```\n\n## x\n````"}},
		"other character":     {"~~~ \n```\n\n# x\n~~~", []string{"~~~ \n```\n\n# x\n~~~"}},
		"closer with text":    {"```\n``` not a closer\n\n# x\n```", []string{"```\n``` not a closer\n\n# x\n```"}},
		"longer closer":       {"```\na\n\nb\n`````\n\nafter", []string{"```\na\n\nb\n`````", "after"}},
		"backtick in info":    {"```cmd``` runs it\n\nnext", []string{"```cmd``` runs it", "next"}},
		"tilde info backtick": {"~~~ a`b\n\n~~~", []string{"~~~ a`b\n\n~~~"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			d := splitMarkdown(c.in)
			if len(d.Sections) != 1 || d.Sections[0].ID != preambleSectionID {
				t.Fatalf("sections = %v", sectionIDs(d))
			}
			if got := blockTexts(d.Sections[0]); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("blocks = %q, want %q", got, c.want)
			}
		})
	}
}

// TestApplyEditsRejectsMultiFragmentBlocks checks that an op whose BlocksMD entry is not one markdown
// fragment (heading, blank line or unclosed fence outside a fence) is skipped whole.
func TestApplyEditsRejectsMultiFragmentBlocks(t *testing.T) {
	first := blockID(t, splitMarkdown(editDoc), "alpha", "first")
	bad := map[string]string{
		"unclosed fence": "```python\nprint(1)",
		"heading":        "para\n## Injected",
		"blank line":     "p1\n\np2",
		"short closer":   "````\ncode\n```",
	}
	for name, text := range bad {
		for _, op := range []domain.DeltaOp{
			{Kind: domain.DeltaOpAppendBlock, SectionID: "alpha", BlocksMD: []string{"ok", text}},
			{Kind: domain.DeltaOpInsertBlock, SectionID: "alpha", BlocksMD: []string{text}},
			{Kind: domain.DeltaOpReplaceBlock, SectionID: "alpha", BlockID: first, BlocksMD: []string{text}},
			{Kind: domain.DeltaOpReplaceSectionBlocks, SectionID: "alpha", BlocksMD: []string{text}},
			{Kind: domain.DeltaOpAddSection, Heading: "New", BlocksMD: []string{text}},
		} {
			got, outcome := applyEdits(editDoc, []domain.DeltaOp{op})
			if outcome[0].Applied || outcome[0].SkipReason != reasonFragment || got != editDoc {
				t.Fatalf("%s %s: outcome %+v doc %q", name, op.Kind, outcome[0], got)
			}
		}
	}
	good := "```\ncode\n\n# comment\n```"
	op := domain.DeltaOp{Kind: domain.DeltaOpAppendBlock, SectionID: "beta", BlocksMD: []string{good}}
	got, outcome := applyEdits(editDoc, []domain.DeltaOp{op})
	if !outcome[0].Applied || got != editDoc+"\n"+good+"\n" {
		t.Fatalf("fenced fragment rejected: %+v %q", outcome[0], got)
	}
}
