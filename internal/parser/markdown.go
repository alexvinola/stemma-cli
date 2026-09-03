package parser

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/provenance"
)

// MaxDocumentBytes bounds a single Markdown document.
const MaxDocumentBytes = 2 << 20

// LineEnding describes the dominant line terminator of a document.
type LineEnding string

const (
	LineEndingLF    LineEnding = "lf"
	LineEndingCRLF  LineEnding = "crlf"
	LineEndingMixed LineEnding = "mixed"
	LineEndingNone  LineEnding = "none"
)

// Section is a Markdown region introduced by an ATX heading. The first section
// of a document may have Level 0 and an empty Heading: it is the preamble.
type Section struct {
	// Level is the heading level (1-6), or 0 for the preamble.
	Level int
	// Heading is the heading text without the leading '#' characters.
	Heading string
	// Content is the section body, excluding the heading line, with trailing
	// blank lines trimmed.
	Content string
	// HeadingLine is the 1-based line of the heading (0 for the preamble).
	HeadingLine int
	// Span locates the whole section, including its heading line.
	Span provenance.Span
}

// Document is a parsed Markdown file.
type Document struct {
	// Path is the repository-relative source path.
	Path string
	// Raw is the exact file content as read from disk.
	Raw []byte
	// Hash is the digest of Raw.
	Hash string
	// HasBOM reports whether the file started with a UTF-8 byte order mark.
	HasBOM bool
	// LineEnding is the dominant terminator.
	LineEnding LineEnding
	// Text is the content after the BOM with line endings normalised to "\n".
	Text string
	// FrontMatter is nil when the document has none.
	FrontMatter *FrontMatter
	// Body is Text after the front matter block.
	Body string
	// BodyLine is the 1-based line where Body starts.
	BodyLine int
	// BodyByte is the byte offset of Body inside Text.
	BodyByte int
	// Sections splits Body on ATX headings, ignoring headings inside fences.
	Sections []Section
	// Title is the text of the first level-1 heading, when present.
	Title string
	// Diagnostics collected while parsing. Parsing never fails outright.
	Diagnostics []diagnostics.Diagnostic
}

// Parse decodes a Markdown document. It never panics and never returns an
// error: every problem is reported as a diagnostic so that callers can decide
// whether to preserve the content opaquely.
func Parse(path string, raw []byte) Document {
	doc := Document{Path: path, Raw: raw, Hash: provenance.HashBytes(raw), BodyLine: 1}

	if len(raw) > MaxDocumentBytes {
		doc.Diagnostics = append(doc.Diagnostics, diagnostics.New(
			diagnostics.FileLimitReached, diagnostics.SeverityError,
			fmt.Sprintf("document is %d bytes (limit %d)", len(raw), MaxDocumentBytes)).WithPath(path))
		return doc
	}

	content := raw
	if len(content) >= 3 && content[0] == 0xEF && content[1] == 0xBB && content[2] == 0xBF {
		doc.HasBOM = true
		content = content[3:]
	}
	if !utf8.Valid(content) {
		doc.Diagnostics = append(doc.Diagnostics, diagnostics.New(
			diagnostics.InvalidEncoding, diagnostics.SeverityError,
			"file is not valid UTF-8").
			WithPath(path).
			WithDetail("Stemma refuses to interpret non-UTF-8 configuration because byte offsets and "+
				"rendering would be unreliable.").
			WithSuggestion("Re-encode the file as UTF-8, or exclude it from the import."))
		return doc
	}

	text := string(content)
	crlf := strings.Count(text, "\r\n")
	lf := strings.Count(text, "\n") - crlf
	switch {
	case crlf > 0 && lf > 0:
		doc.LineEnding = LineEndingMixed
		doc.Diagnostics = append(doc.Diagnostics, diagnostics.New(
			diagnostics.MixedLineEndings, diagnostics.SeverityInfo,
			"file mixes LF and CRLF line endings").
			WithPath(path).
			WithDetail("Generated output for this file will use LF."))
	case crlf > 0:
		doc.LineEnding = LineEndingCRLF
	case lf > 0:
		doc.LineEnding = LineEndingLF
	default:
		doc.LineEnding = LineEndingNone
	}
	// Work on LF-normalised text so offsets are consistent; the original bytes
	// remain available in Raw for byte-identical round trips.
	doc.Text = strings.ReplaceAll(text, "\r\n", "\n")

	body, bodyLine, bodyByte, fm, fmDiags := extractFrontMatter(path, doc.Text)
	doc.Diagnostics = append(doc.Diagnostics, fmDiags...)
	doc.FrontMatter = fm
	doc.Body = body
	doc.BodyLine = bodyLine
	doc.BodyByte = bodyByte
	doc.Sections = splitSections(body, bodyLine, bodyByte)
	for _, s := range doc.Sections {
		if s.Level == 1 {
			doc.Title = s.Heading
			break
		}
	}
	return doc
}

// extractFrontMatter finds a front matter block delimited by "---" lines at the
// very start of the document.
func extractFrontMatter(path, text string) (body string, bodyLine, bodyByte int, fm *FrontMatter, diags []diagnostics.Diagnostic) {
	if !strings.HasPrefix(text, "---\n") && text != "---" && !strings.HasPrefix(text, "---\r\n") {
		return text, 1, 0, nil, nil
	}
	lines := splitLines(text)
	closing := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t\r") == "---" {
			closing = i
			break
		}
	}
	if closing < 0 {
		return text, 1, 0, nil, []diagnostics.Diagnostic{
			diagnostics.New(diagnostics.InvalidFrontMatter, diagnostics.SeverityWarning,
				"document starts with '---' but no closing delimiter was found").
				WithPath(path).WithPosition(1, 1).
				WithDetail("The whole file is treated as Markdown body and preserved unchanged.").
				WithSuggestion("Close the front matter block with a '---' line, or remove the opening one."),
		}
	}
	rawFM := strings.Join(lines[1:closing], "\n")
	if len(rawFM) > MaxFrontMatterBytes {
		return text, 1, 0, nil, []diagnostics.Diagnostic{
			diagnostics.New(diagnostics.FrontMatterTooLarge, diagnostics.SeverityError,
				fmt.Sprintf("front matter is %d bytes (limit %d)", len(rawFM), MaxFrontMatterBytes)).
				WithPath(path).WithPosition(1, 1),
		}
	}
	fields, keys, fmDiags := parseFrontMatter(path, rawFM, 1)

	byteEnd := 0
	for i := 0; i <= closing; i++ {
		byteEnd += len(lines[i]) + 1
	}
	if byteEnd > len(text) {
		byteEnd = len(text)
	}
	fm = &FrontMatter{
		Raw:       rawFM,
		Fields:    fields,
		Keys:      keys,
		StartLine: 1,
		EndLine:   closing + 1,
		ByteStart: 0,
		ByteEnd:   byteEnd,
	}
	return text[byteEnd:], closing + 2, byteEnd, fm, fmDiags
}

// splitLines splits on "\n" without keeping a trailing empty element for text
// that ends with a newline.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	out := strings.Split(s, "\n")
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// fenceState tracks fenced code blocks so that '#', '---' and bullets inside
// them are never interpreted as structure.
type fenceState struct {
	open   bool
	marker byte
	length int
}

// step updates the fence state for a line and reports whether the line is
// inside a fenced block (including the fence markers themselves).
func (f *fenceState) step(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	indent := len(line) - len(trimmed)
	if indent >= 4 && !f.open {
		return true // indented code block
	}
	marker, length, info := fenceInfo(trimmed)
	if length == 0 {
		return f.open
	}
	if !f.open {
		f.open = true
		f.marker = marker
		f.length = length
		return true
	}
	// A closing fence must use the same marker, be at least as long, and carry
	// no info string.
	if marker == f.marker && length >= f.length && info == "" {
		f.open = false
		f.marker = 0
		f.length = 0
	}
	return true
}

func fenceInfo(trimmed string) (marker byte, length int, info string) {
	if len(trimmed) < 3 {
		return 0, 0, ""
	}
	c := trimmed[0]
	if c != '`' && c != '~' {
		return 0, 0, ""
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == c {
		n++
	}
	if n < 3 {
		return 0, 0, ""
	}
	rest := strings.TrimSpace(trimmed[n:])
	if c == '`' && strings.ContainsRune(rest, '`') {
		return 0, 0, "" // inline code, not a fence
	}
	return c, n, rest
}

// splitSections splits Markdown into heading-delimited sections.
func splitSections(body string, startLine, startByte int) []Section {
	lines := splitLines(body)
	if len(lines) == 0 {
		return nil
	}
	var sections []Section
	var fence fenceState

	type marker struct {
		index   int
		level   int
		heading string
	}
	var markers []marker
	for i, line := range lines {
		if fence.step(line) {
			continue
		}
		level, heading, ok := atxHeading(line)
		if !ok {
			continue
		}
		markers = append(markers, marker{index: i, level: level, heading: heading})
	}

	offsets := make([]int, len(lines)+1)
	for i, l := range lines {
		offsets[i+1] = offsets[i] + len(l) + 1
	}
	mk := func(level int, heading string, from, to, headingLine int) Section {
		contentFrom := from
		if headingLine > 0 {
			contentFrom = from + 1
		}
		content := ""
		if contentFrom < to {
			content = strings.Join(lines[contentFrom:to], "\n")
		}
		return Section{
			Level:       level,
			Heading:     heading,
			Content:     strings.Trim(content, "\n"),
			HeadingLine: headingLine,
			Span: provenance.Span{
				ByteStart: startByte + offsets[from],
				ByteEnd:   startByte + offsets[to],
				LineStart: startLine + from,
				LineEnd:   startLine + to - 1,
			},
		}
	}

	if len(markers) == 0 {
		if strings.TrimSpace(body) == "" {
			return nil
		}
		return []Section{mk(0, "", 0, len(lines), 0)}
	}
	if markers[0].index > 0 {
		preamble := mk(0, "", 0, markers[0].index, 0)
		if strings.TrimSpace(preamble.Content) != "" {
			sections = append(sections, preamble)
		}
	}
	for i, m := range markers {
		end := len(lines)
		if i+1 < len(markers) {
			end = markers[i+1].index
		}
		sections = append(sections, mk(m.level, m.heading, m.index, end, startLine+m.index))
	}
	return sections
}

// atxHeading recognises an ATX heading outside a code fence.
func atxHeading(line string) (level int, text string, ok bool) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) >= 4 {
		return 0, "", false // indented code
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == '#' {
		n++
	}
	if n == 0 || n > 6 {
		return 0, "", false
	}
	rest := trimmed[n:]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return 0, "", false
	}
	text = strings.TrimSpace(rest)
	text = strings.TrimRight(text, "#")
	return n, strings.TrimSpace(text), true
}

// Bullet is a top-level list item.
type Bullet struct {
	// Text is the item text, with continuation lines joined by newlines.
	Text string
	// Line is the 1-based line of the item marker.
	Line int
}

// Bullets extracts top-level list items from Markdown text, skipping fenced
// code blocks. Nested items are folded into their parent item's text.
func Bullets(text string, startLine int) []Bullet {
	lines := splitLines(text)
	var fence fenceState
	var out []Bullet
	current := -1
	for i, line := range lines {
		if fence.step(line) {
			if current >= 0 {
				out[current].Text += "\n" + line
			}
			continue
		}
		trimmed := strings.TrimLeft(line, " ")
		indent := len(line) - len(trimmed)
		isBullet := indent < 2 && len(trimmed) > 1 &&
			(trimmed[0] == '-' || trimmed[0] == '*' || trimmed[0] == '+') &&
			(trimmed[1] == ' ' || trimmed[1] == '\t')
		switch {
		case isBullet:
			out = append(out, Bullet{Text: strings.TrimSpace(trimmed[2:]), Line: startLine + i})
			current = len(out) - 1
		case strings.TrimSpace(line) == "":
			current = -1
		case current >= 0 && indent >= 2:
			out[current].Text += "\n" + strings.TrimRight(line, " ")
		default:
			current = -1
		}
	}
	return out
}

// HasFencedContent reports whether text contains a fenced code block.
func HasFencedContent(text string) bool {
	var fence fenceState
	for _, line := range splitLines(text) {
		fence.step(line)
		if fence.open {
			return true
		}
	}
	return false
}
