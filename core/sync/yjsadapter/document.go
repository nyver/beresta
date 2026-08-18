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
	ErrClosed            = errors.New("yjs adapter: document is closed")
	ErrInvalidRootName   = errors.New("yjs adapter: invalid root name")
	ErrInvalidUpdate     = errors.New("yjs adapter: invalid update")
	ErrUnsupportedFormat = errors.New("yjs adapter: unsupported update format")
	ErrUpdateTooLarge    = errors.New("yjs adapter: update exceeds size limit")
)

// DocumentCRDT is the dependency boundary owned by Beresta. No ygo type crosses it.
type DocumentCRDT interface {
	ApplyUpdate(Format, []byte) error
	EncodeStateAsUpdate(Format) ([]byte, error)
	Text(string) (string, error)
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
	if name == "" || len(name) > 256 || !utf8.ValidString(name) {
		return "", ErrInvalidRootName
	}

	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.doc == nil {
		return "", ErrClosed
	}
	return d.doc.GetText(name).ToString(), nil
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
