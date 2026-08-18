// Package yjsadapter implements Beresta's Yjs-compatible document boundary.
package yjsadapter

import (
	"errors"
	"fmt"
	"sync"
	"unicode/utf8"

	"github.com/reearth/ygo/crdt"
)

// MaxUpdateBytes bounds a single decoded CRDT update before allocation-heavy parsing.
const MaxUpdateBytes = 16 << 20

// Format identifies a Yjs binary update format.
type Format uint8

const (
	// FormatV1 is the original Yjs update encoding.
	FormatV1 Format = 1
	// FormatV2 is the column-oriented Yjs update encoding.
	FormatV2 Format = 2
)

var (
	ErrClosed             = errors.New("yjs adapter: document is closed")
	ErrInvalidRootName    = errors.New("yjs adapter: invalid root name")
	ErrInvalidUpdate      = errors.New("yjs adapter: invalid update")
	ErrUnsupportedFormat  = errors.New("yjs adapter: unsupported update format")
	ErrUpdateTooLarge     = errors.New("yjs adapter: update exceeds size limit")
	ErrInvalidText        = errors.New("yjs adapter: invalid text")
	ErrInvalidRange       = errors.New("yjs adapter: invalid index or length")
	ErrInvalidAttributes  = errors.New("yjs adapter: invalid formatting attributes")
	ErrUnsupportedContent = errors.New("yjs adapter: unsupported delta content")
)

// Attributes carries Quill-compatible rich-text formatting markers understood
// by ygo's ContentFormat. Inline keys (Attr*) apply to the text run they
// precede; block keys apply to the newline character that ends a line, per
// Quill/Yjs convention. Values are restricted to the lib0 "any" scalar
// domain: nil, bool, string, or a numeric type.
type Attributes map[string]any

// Well-known formatting attribute keys understood by the canonical Markdown
// projection. Unrecognized keys round-trip through the CRDT but are ignored
// by Markdown.
const (
	AttrBold       = "bold"
	AttrItalic     = "italic"
	AttrStrike     = "strike"
	AttrCode       = "code"
	AttrLink       = "link"       // string: URL
	AttrHeader     = "header"     // integer 1-6
	AttrBlockquote = "blockquote" // bool
	AttrList       = "list"       // string: ListBullet or ListOrdered
	AttrCodeBlock  = "code-block" // bool
)

// Values for the AttrList attribute.
const (
	ListBullet  = "bullet"
	ListOrdered = "ordered"
)

// DocumentCRDT is the dependency boundary owned by Beresta. No ygo type crosses it.
type DocumentCRDT interface {
	ApplyUpdate(Format, []byte) error
	EncodeStateAsUpdate(Format) ([]byte, error)
	EncodeStateVector() ([]byte, error)
	Text(string) (string, error)
	Markdown(string) (string, error)
	Insert(root string, index int, text string, attrs Attributes) error
	Delete(root string, index, length int) error
	Format(root string, index, length int, attrs Attributes) error
	Close()
}

// Document wraps one ygo document with a linearizable lifecycle.
type Document struct {
	mu  sync.RWMutex
	doc *crdt.Doc
}

// New creates an empty document with a cryptographically random ygo client ID.
func New() *Document {
	return &Document{doc: crdt.New()}
}

// ApplyUpdate validates and merges a Yjs V1 or V2 update.
func (d *Document) ApplyUpdate(format Format, update []byte) (err error) {
	if len(update) > MaxUpdateBytes {
		return ErrUpdateTooLarge
	}
	if len(update) == 0 {
		return ErrInvalidUpdate
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.doc == nil {
		return ErrClosed
	}

	// A malformed peer update is untrusted input. Keep an upstream decoder panic
	// inside the adapter even if a future dependency regression reintroduces one.
	defer func() {
		if recover() != nil {
			err = ErrInvalidUpdate
		}
	}()

	switch format {
	case FormatV1:
		err = crdt.ApplyUpdateV1(d.doc, update, nil)
	case FormatV2:
		err = crdt.ApplyUpdateV2(d.doc, update, nil)
	default:
		return ErrUnsupportedFormat
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidUpdate, err)
	}
	return nil
}

// EncodeStateAsUpdate returns the complete document state in the selected format.
func (d *Document) EncodeStateAsUpdate(format Format) ([]byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.doc == nil {
		return nil, ErrClosed
	}

	switch format {
	case FormatV1:
		return crdt.EncodeStateAsUpdateV1(d.doc, nil), nil
	case FormatV2:
		return crdt.EncodeStateAsUpdateV2(d.doc, nil), nil
	default:
		return nil, ErrUnsupportedFormat
	}
}

// Text returns the plain string projection of a named Y.Text root.
func (d *Document) Text(name string) (string, error) {
	if err := validateRootName(name); err != nil {
		return "", err
	}

	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.doc == nil {
		return "", ErrClosed
	}
	return d.doc.GetText(name).ToString(), nil
}

// Markdown returns the canonical Markdown projection of a named Y.Text root.
// It is a derived view used for search indexing, export, and diff
// presentation; it is never a merge input.
func (d *Document) Markdown(name string) (string, error) {
	if err := validateRootName(name); err != nil {
		return "", err
	}

	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.doc == nil {
		return "", ErrClosed
	}
	return renderMarkdown(d.doc.GetText(name).ToDelta())
}

// Insert inserts text with optional Quill-compatible formatting attributes at
// a logical character position within the named Y.Text root, creating the
// root if it does not exist. A nil or empty attrs inherits the formatting in
// effect at index. Block-level attributes (AttrHeader, AttrList, ...) belong
// on the newline character that ends a line.
func (d *Document) Insert(root string, index int, text string, attrs Attributes) (err error) {
	if err := validateRootName(root); err != nil {
		return err
	}
	if !utf8.ValidString(text) {
		return ErrInvalidText
	}
	if err := validateAttributes(attrs); err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.doc == nil {
		return ErrClosed
	}
	defer func() {
		if recover() != nil {
			err = ErrInvalidRange
		}
	}()

	d.doc.Transact(func(txn *crdt.Transaction) {
		t := txn.GetText(root)
		if index < 0 || index > t.Len() {
			panic(ErrInvalidRange)
		}
		t.Insert(txn, index, text, crdt.Attributes(attrs))
	})
	return nil
}

// Delete removes length characters starting at index within the named Y.Text
// root.
func (d *Document) Delete(root string, index, length int) (err error) {
	if err := validateRootName(root); err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.doc == nil {
		return ErrClosed
	}
	defer func() {
		if recover() != nil {
			err = ErrInvalidRange
		}
	}()

	d.doc.Transact(func(txn *crdt.Transaction) {
		t := txn.GetText(root)
		if index < 0 || length < 0 || index+length > t.Len() {
			panic(ErrInvalidRange)
		}
		if length > 0 {
			t.Delete(txn, index, length)
		}
	})
	return nil
}

// Format applies formatting attributes to the character range
// [index, index+length) within the named Y.Text root. A nil attribute value
// removes that attribute from the range.
func (d *Document) Format(root string, index, length int, attrs Attributes) (err error) {
	if err := validateRootName(root); err != nil {
		return err
	}
	if err := validateAttributes(attrs); err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.doc == nil {
		return ErrClosed
	}
	defer func() {
		if recover() != nil {
			err = ErrInvalidRange
		}
	}()

	d.doc.Transact(func(txn *crdt.Transaction) {
		t := txn.GetText(root)
		if index < 0 || length <= 0 || index+length > t.Len() {
			panic(ErrInvalidRange)
		}
		t.Format(txn, index, length, crdt.Attributes(attrs))
	})
	return nil
}

// EncodeStateVector returns the document's current state vector, used to
// detect whether an incoming update is already fully known.
func (d *Document) EncodeStateVector() ([]byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.doc == nil {
		return nil, ErrClosed
	}
	return crdt.EncodeStateVectorV1(d.doc), nil
}

// Restore creates a document from a snapshot previously captured by
// EncodeStateAsUpdate. A malformed or truncated snapshot is rejected the same
// way ApplyUpdate rejects a malformed update.
func Restore(format Format, snapshot []byte) (*Document, error) {
	doc := New()
	if err := doc.ApplyUpdate(format, snapshot); err != nil {
		doc.Close()
		return nil, err
	}
	return doc, nil
}

func validateRootName(name string) error {
	if name == "" || len(name) > 256 || !utf8.ValidString(name) {
		return ErrInvalidRootName
	}
	return nil
}

// validateAttributes rejects attribute values outside the closed scalar
// domain Beresta's Markdown projection understands, ahead of ygo's own lib0
// "any" domain check, so callers get a typed adapter error instead of a
// dependency panic. Well-known keys (see Attr* constants) are additionally
// checked against their documented value domain so a mistake such as
// {header: 99} is rejected here instead of being silently dropped by
// Markdown's rendering, which treats an out-of-range value as absent.
func validateAttributes(attrs Attributes) error {
	for key, value := range attrs {
		if key == "" || !utf8.ValidString(key) {
			return ErrInvalidAttributes
		}
		if value == nil {
			continue // nil always means "remove this attribute"
		}
		switch key {
		case AttrBold, AttrItalic, AttrStrike, AttrCode, AttrBlockquote, AttrCodeBlock:
			if _, ok := value.(bool); !ok {
				return ErrInvalidAttributes
			}
		case AttrHeader:
			level, ok := attrInt(value)
			if !ok || level < 1 || level > 6 {
				return ErrInvalidAttributes
			}
		case AttrList:
			s, ok := value.(string)
			if !ok || (s != ListBullet && s != ListOrdered) {
				return ErrInvalidAttributes
			}
		case AttrLink:
			s, ok := value.(string)
			if !ok || s == "" || !utf8.ValidString(s) {
				return ErrInvalidAttributes
			}
		default:
			if err := validateScalarAttributeValue(value); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateScalarAttributeValue accepts the lib0 "any" scalar domain for
// attribute keys Beresta does not itself interpret, so forward-compatible
// keys still round-trip through the CRDT without the adapter rejecting them.
func validateScalarAttributeValue(value any) error {
	switch v := value.(type) {
	case bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return nil
	case string:
		if !utf8.ValidString(v) {
			return ErrInvalidAttributes
		}
		return nil
	default:
		return ErrInvalidAttributes
	}
}

// Close releases document state. It is idempotent and excludes in-flight calls.
func (d *Document) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.doc == nil {
		return
	}
	d.doc.Destroy()
	d.doc = nil
}

var _ DocumentCRDT = (*Document)(nil)
