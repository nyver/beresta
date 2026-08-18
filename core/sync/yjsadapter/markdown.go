package yjsadapter

import (
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
