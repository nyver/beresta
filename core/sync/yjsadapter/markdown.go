package yjsadapter

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/reearth/ygo/crdt"
)

// run is one inline text span with its own formatting attributes.
type run struct {
	text  string
	attrs Attributes
}

// line is one paragraph: its inline runs plus the block-level attributes
// carried by the newline that ended it (per Quill/Yjs convention). A
// document's trailing, not-yet-newline-terminated content has a nil block.
type line struct {
	runs  []run
	block Attributes
}

// renderMarkdown projects a Y.Text delta (as returned by ToDelta) into
// canonical Markdown. It is a derived, best-effort view for search indexing,
// export, and diff presentation; it never feeds back into CRDT merges, so
// unrecognized attributes are silently ignored rather than rejected.
func renderMarkdown(deltas []crdt.Delta) (string, error) {
	lines, err := splitLines(deltas)
	if err != nil {
		return "", err
	}

	var out strings.Builder
	orderedIndex := 0
	inFence := false
	for i, ln := range lines {
		if i > 0 {
			out.WriteByte('\n')
		}
		fenced := truthyBool(ln.block[AttrCodeBlock])
		if fenced != inFence {
			out.WriteString("```\n")
			inFence = fenced
		}

		if fenced {
			orderedIndex = 0
			out.WriteString(plainText(ln.runs))
			continue
		}
		orderedIndex = renderLine(&out, ln, orderedIndex)
	}
	if inFence {
		out.WriteString("\n```")
	}
	return out.String(), nil
}

// splitLines groups a flat delta sequence into per-line runs, splitting on
// literal newlines. The attrs attached to the delta segment containing a
// newline describe that line's block formatting, matching Quill semantics.
func splitLines(deltas []crdt.Delta) ([]line, error) {
	var lines []line
	var current []run
	for _, delta := range deltas {
		if delta.Op != crdt.DeltaOpInsert {
			continue
		}
		text, ok := delta.Insert.(string)
		if !ok {
			return nil, ErrUnsupportedContent
		}
		attrs := Attributes(delta.Attributes)

		for {
			i := strings.IndexByte(text, '\n')
			if i < 0 {
				if text != "" {
					current = append(current, run{text: text, attrs: attrs})
				}
				break
			}
			if i > 0 {
				current = append(current, run{text: text[:i], attrs: attrs})
			}
			lines = append(lines, line{runs: current, block: attrs})
			current = nil
			text = text[i+1:]
		}
	}
	if len(current) > 0 {
		lines = append(lines, line{runs: current})
	}
	return lines, nil
}

// renderLine writes one non-fenced line's Markdown form and returns the
// ordered-list index to use for the next line.
func renderLine(out *strings.Builder, ln line, orderedIndex int) int {
	content := renderRuns(mergeRuns(ln.runs))

	switch {
	case ln.block[AttrHeader] != nil:
		if level, ok := attrInt(ln.block[AttrHeader]); ok && level >= 1 && level <= 6 {
			out.WriteString(strings.Repeat("#", int(level)))
			out.WriteByte(' ')
		}
		orderedIndex = 0
	case truthyBool(ln.block[AttrBlockquote]):
		out.WriteString("> ")
		orderedIndex = 0
	case ln.block[AttrList] == ListBullet:
		out.WriteString("- ")
		orderedIndex = 0
	case ln.block[AttrList] == ListOrdered:
		orderedIndex++
		out.WriteString(strconv.Itoa(orderedIndex))
		out.WriteString(". ")
	default:
		orderedIndex = 0
	}
	out.WriteString(content)
	return orderedIndex
}

// mergeRuns coalesces adjacent runs sharing identical attributes so the
// projection is stable regardless of how the CRDT internally fragmented
// equally-formatted text across edits.
func mergeRuns(runs []run) []run {
	merged := make([]run, 0, len(runs))
	for _, r := range runs {
		if n := len(merged); n > 0 && attrsEqual(merged[n-1].attrs, r.attrs) {
			merged[n-1].text += r.text
			continue
		}
		merged = append(merged, r)
	}
	return merged
}

func attrsEqual(a, b Attributes) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func plainText(runs []run) string {
	var sb strings.Builder
	for _, r := range runs {
		sb.WriteString(r.text)
	}
	return sb.String()
}

// renderInlineOrder fixes a deterministic nesting order for inline markers so
// the same formatting always produces the same Markdown text.
func renderRuns(runs []run) string {
	var sb strings.Builder
	for _, r := range runs {
		sb.WriteString(renderInline(r.text, r.attrs))
	}
	return sb.String()
}

func renderInline(text string, attrs Attributes) string {
	if text == "" {
		return ""
	}
	if truthyBool(attrs[AttrCode]) {
		return "`" + text + "`"
	}
	if truthyBool(attrs[AttrStrike]) {
		text = "~~" + text + "~~"
	}
	if truthyBool(attrs[AttrItalic]) {
		text = "*" + text + "*"
	}
	if truthyBool(attrs[AttrBold]) {
		text = "**" + text + "**"
	}
	if href, ok := attrs[AttrLink].(string); ok && href != "" {
		text = "[" + text + "](" + href + ")"
	}
	return text
}

func truthyBool(v any) bool {
	b, _ := v.(bool)
	return b
}

// attrInt widens any lib0 "any"-domain numeric value to int64. Numeric
// attributes round-trip as different concrete Go types depending on whether
// they came from an in-memory Insert/Format call or were decoded from wire
// bytes (see encoding.Decoder.ReadAny), so every integer and float kind is
// accepted here.
func attrInt(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint:
		return int64(n), true
	case uint8:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint64:
		return int64(n), true
	case float32:
		return int64(n), true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}

// parseMarkdownLines converts Markdown text into the same line/run structure
// renderMarkdown consumes, in reverse. It is the write-side counterpart to
// renderMarkdown, for a caller (such as the mobile editor, which only ever
// sees the Markdown projection) that must turn a Markdown-edited string back
// into structured runs without flattening it to literal, unformatted text.
// Like renderMarkdown, it is best-effort: it recognizes exactly the syntax
// renderMarkdown itself emits and falls back to literal text for anything it
// does not recognize.
func parseMarkdownLines(markdown string) []line {
	rawLines := strings.Split(markdown, "\n")
	lines := make([]line, 0, len(rawLines))
	inFence := false
	for _, raw := range rawLines {
		if raw == "```" {
			inFence = !inFence
			continue
		}
		if inFence {
			lines = append(lines, line{runs: []run{{text: raw}}, block: Attributes{AttrCodeBlock: true}})
			continue
		}
		lines = append(lines, parseBlockLine(raw))
	}
	return lines
}

var orderedListMarker = regexp.MustCompile(`^\d+\. `)

// parseBlockLine recognizes raw's leading block-level marker (header,
// blockquote, bullet list, or ordered list), if any, and parses the
// remainder as inline runs.
func parseBlockLine(raw string) line {
	block := Attributes{}
	content := raw
	switch {
	case headerLevel(raw) > 0:
		level := headerLevel(raw)
		block[AttrHeader] = level
		content = raw[level+1:]
	case strings.HasPrefix(raw, "> "):
		block[AttrBlockquote] = true
		content = raw[2:]
	case strings.HasPrefix(raw, "- "):
		block[AttrList] = ListBullet
		content = raw[2:]
	case orderedListMarker.MatchString(raw):
		content = raw[len(orderedListMarker.FindString(raw)):]
		block[AttrList] = ListOrdered
	}
	return line{runs: parseInline(content), block: block}
}

// headerLevel returns the ATX header level (1-6) raw starts with, or 0 if
// raw is not a header line (a "#" run immediately followed by a space).
func headerLevel(raw string) int {
	level := 0
	for level < len(raw) && level < 6 && raw[level] == '#' {
		level++
	}
	if level == 0 || level >= len(raw) || raw[level] != ' ' {
		return 0
	}
	return level
}

// inlineMarker identifies which inline delimiter parseInline matched next.
type inlineMarker int

const (
	markerNone inlineMarker = iota
	markerCode
	markerLink
	markerBoldItalic
	markerBold
	markerItalic
	markerStrike
)

// nextInlineMarker returns the byte offset and kind of the earliest inline
// delimiter in s, or (-1, markerNone) if s has none. A lone "~" or a "*" that
// is not part of a longer run still reports its position so parseInline can
// fall back to treating it as literal text once no closing delimiter is
// found.
func nextInlineMarker(s string) (int, inlineMarker) {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '`':
			return i, markerCode
		case '[':
			return i, markerLink
		case '*':
			switch {
			case strings.HasPrefix(s[i:], "***"):
				return i, markerBoldItalic
			case strings.HasPrefix(s[i:], "**"):
				return i, markerBold
			default:
				return i, markerItalic
			}
		case '~':
			if strings.HasPrefix(s[i:], "~~") {
				return i, markerStrike
			}
		}
	}
	return -1, markerNone
}

// extractDelims reports the content between a leading open delimiter and the
// next occurrence of close, plus the text remaining after it. ok is false if
// s does not start with open, or close never appears.
func extractDelims(s, open, close string) (content, remainder string, ok bool) {
	if !strings.HasPrefix(s, open) {
		return "", "", false
	}
	rest := s[len(open):]
	idx := strings.Index(rest, close)
	if idx < 0 {
		return "", "", false
	}
	return rest[:idx], rest[idx+len(close):], true
}

// parseLink parses a "[text](url)" span starting at s[0], recursively
// parsing the link text for further inline formatting and attaching the
// link's URL to every run it produces (matching renderInline's outermost-link
// nesting). ok is false when s does not start a well-formed link, in which
// case the caller falls back to treating "[" as literal text.
func parseLink(s string) (out []run, remainder string, ok bool) {
	closeBracket := strings.Index(s, "](")
	if closeBracket < 0 {
		return nil, s, false
	}
	rest := s[closeBracket+2:]
	endParen := strings.IndexByte(rest, ')')
	if endParen < 0 {
		return nil, s, false
	}
	linkText := s[1:closeBracket]
	url := rest[:endParen]
	inner := parseInline(linkText)
	if url != "" {
		inner = attachAttr(inner, AttrLink, url)
	}
	return inner, rest[endParen+1:], true
}

// attachAttr adds one attribute to every run, merging it into that run's
// existing attributes rather than replacing them, so nested markers (for
// example, a link wrapped around bold text) compose instead of clobbering
// each other. Runs with no text (an empty formatted span, e.g. "****") are
// left alone, matching renderInline's own empty-text no-op.
func attachAttr(runs []run, key string, value any) []run {
	for i := range runs {
		if runs[i].text == "" {
			continue
		}
		merged := make(Attributes, len(runs[i].attrs)+1)
		for k, v := range runs[i].attrs {
			merged[k] = v
		}
		merged[key] = value
		runs[i].attrs = merged
	}
	return runs
}

// parseInline tokenizes one line's content into runs, recognizing the same
// markers renderInline emits: code spans (highest precedence, non-nesting),
// links, and bold/italic/strike (applied inside-out, matching renderInline's
// fixed strike/italic/bold/link nesting order). An unmatched opening
// delimiter (no closing counterpart) is emitted as literal text.
func parseInline(s string) []run {
	var out []run
	for len(s) > 0 {
		idx, kind := nextInlineMarker(s)
		if idx < 0 {
			out = append(out, run{text: s})
			break
		}
		if idx > 0 {
			out = append(out, run{text: s[:idx]})
			s = s[idx:]
		}

		consumed := false
		switch kind {
		case markerCode:
			if content, remainder, ok := extractDelims(s, "`", "`"); ok {
				if content != "" {
					out = append(out, run{text: content, attrs: Attributes{AttrCode: true}})
				}
				s = remainder
				consumed = true
			}
		case markerLink:
			var linkRuns []run
			linkRuns, s, consumed = parseLink(s)
			out = append(out, linkRuns...)
		case markerBoldItalic:
			if content, remainder, ok := extractDelims(s, "***", "***"); ok {
				inner := attachAttr(attachAttr(parseInline(content), AttrBold, true), AttrItalic, true)
				out = append(out, inner...)
				s = remainder
				consumed = true
			}
		case markerBold:
			if content, remainder, ok := extractDelims(s, "**", "**"); ok {
				out = append(out, attachAttr(parseInline(content), AttrBold, true)...)
				s = remainder
				consumed = true
			}
		case markerItalic:
			if content, remainder, ok := extractDelims(s, "*", "*"); ok {
				out = append(out, attachAttr(parseInline(content), AttrItalic, true)...)
				s = remainder
				consumed = true
			}
		case markerStrike:
			if content, remainder, ok := extractDelims(s, "~~", "~~"); ok {
				out = append(out, attachAttr(parseInline(content), AttrStrike, true)...)
				s = remainder
				consumed = true
			}
		}
		if !consumed {
			out = append(out, run{text: s[:1]})
			s = s[1:]
		}
	}
	return out
}
