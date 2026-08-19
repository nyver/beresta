package main

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
	"github.com/beresta-app/beresta/core/sync/yjsadapter"
)

// parseID decodes a canonical dashed-hex UUID string (as produced by
// idString) back into a model.ID. An empty string decodes to model.Nil,
// the sentinel every core service treats as "workspace root" / "no
// value" for optional ID fields. Every failure is an *AppError with
// ErrCodeInvalidInput, since a malformed identifier is always caller
// (frontend) input error, not an internal one.
func parseID(s string) (model.ID, error) {
	if s == "" {
		return model.Nil, nil
	}
	compact := strings.ReplaceAll(s, "-", "")
	raw, err := hex.DecodeString(compact)
	if err != nil {
		return model.Nil, &AppError{Code: ErrCodeInvalidInput, Message: fmt.Sprintf("invalid identifier %q", s)}
	}
	id, err := model.ParseID(raw)
	if err != nil {
		return model.Nil, &AppError{Code: ErrCodeInvalidInput, Message: fmt.Sprintf("invalid identifier %q", s)}
	}
	return id, nil
}

// idString renders a model.ID for the JS bridge. model.Nil renders as "",
// the counterpart parseID expects back for an optional/root reference.
func idString(id model.ID) string {
	if id.IsZero() {
		return ""
	}
	return id.String()
}

// idStrings renders a slice of IDs for the JS bridge.
func idStrings(ids []model.ID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = idString(id)
	}
	return out
}

// parseIDs decodes a slice of dashed-hex UUID strings.
func parseIDs(values []string) ([]model.ID, error) {
	ids := make([]model.ID, len(values))
	for i, v := range values {
		id, err := parseID(v)
		if err != nil {
			return nil, err
		}
		ids[i] = id
	}
	return ids, nil
}

// blobIDString hex-encodes a store.BlobID for the JS bridge. store.BlobID
// has no String method of its own (it is a raw content-address, not a
// display identifier), so the desktop binding layer owns this encoding.
func blobIDString(id store.BlobID) string {
	return hex.EncodeToString(id.Bytes())
}

// parseBlobID decodes a hex-encoded blob ID from the JS bridge.
func parseBlobID(s string) (store.BlobID, error) {
	raw, err := hex.DecodeString(s)
	if err != nil {
		return store.BlobID{}, &AppError{Code: ErrCodeInvalidInput, Message: fmt.Sprintf("invalid attachment identifier %q", s)}
	}
	blobID, err := store.ParseBlobID(raw)
	if err != nil {
		return store.BlobID{}, &AppError{Code: ErrCodeInvalidInput, Message: fmt.Sprintf("invalid attachment identifier %q", s)}
	}
	return blobID, nil
}

// decodeBase64 decodes a standard-encoding base64 payload sent across the
// JS bridge (Wails marshals []byte parameters as base64 JSON strings, so
// binding methods that need raw bytes, like a Yjs update, accept a string
// and decode it themselves for an explicit, testable contract).
func decodeBase64(s string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, &AppError{Code: ErrCodeInvalidInput, Message: "invalid base64 payload"}
	}
	return data, nil
}

// parseYjsFormat maps the JS-facing "v1"/"v2" format identifier to
// yjsadapter.Format.
func parseYjsFormat(s string) (yjsadapter.Format, error) {
	switch s {
	case "v1":
		return yjsadapter.FormatV1, nil
	case "v2":
		return yjsadapter.FormatV2, nil
	default:
		return 0, &AppError{Code: ErrCodeInvalidInput, Message: fmt.Sprintf("invalid Yjs update format %q", s)}
	}
}

// yjsFormatString is parseYjsFormat's inverse, for responses that hand the
// client a Format alongside encoded update bytes.
func yjsFormatString(format yjsadapter.Format) string {
	switch format {
	case yjsadapter.FormatV1:
		return "v1"
	case yjsadapter.FormatV2:
		return "v2"
	default:
		return ""
	}
}
