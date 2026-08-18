// Package mobileapi exposes value-oriented bindings suitable for gomobile.
package mobileapi

import (
	"github.com/beresta-app/beresta/core/store/sqlcipherdb"
	"github.com/beresta-app/beresta/core/sync/yjsadapter"
)

// Document is a gomobile-safe façade for the shared Yjs document adapter.
type Document struct {
	doc *yjsadapter.Document
}

// NewDocument creates an empty Yjs-compatible document.
func NewDocument() *Document {
	return &Document{doc: yjsadapter.New()}
}

// ApplyUpdateV1 merges a Yjs V1 update.
func (d *Document) ApplyUpdateV1(update []byte) error {
	return d.doc.ApplyUpdate(yjsadapter.FormatV1, update)
}

// ApplyUpdateV2 merges a Yjs V2 update.
func (d *Document) ApplyUpdateV2(update []byte) error {
	return d.doc.ApplyUpdate(yjsadapter.FormatV2, update)
}

// EncodeStateAsUpdateV1 returns the complete document state in Yjs V1 format.
func (d *Document) EncodeStateAsUpdateV1() ([]byte, error) {
	return d.doc.EncodeStateAsUpdate(yjsadapter.FormatV1)
}

// EncodeStateAsUpdateV2 returns the complete document state in Yjs V2 format.
func (d *Document) EncodeStateAsUpdateV2() ([]byte, error) {
	return d.doc.EncodeStateAsUpdate(yjsadapter.FormatV2)
}

// GetText returns the plain string projection of a named Y.Text root.
func (d *Document) GetText(name string) (string, error) {
	return d.doc.Text(name)
}

// Close releases the document. It is safe to call more than once.
func (d *Document) Close() {
	d.doc.Close()
}

// RunSQLCipherProbe verifies an encrypted database round trip through the
// Android binding. It returns the SQLCipher version used by the native core.
func RunSQLCipherProbe(path string, key []byte, value string) (string, error) {
	result, err := sqlcipherdb.Probe(path, key, value)
	if err != nil {
		return "", err
	}
	return result.CipherVersion, nil
}
