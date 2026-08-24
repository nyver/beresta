// Package sharecode encodes and decodes the two opaque, pasteable strings
// exchanged out-of-band when one account shares a workspace with another:
// an identity code (recipient -> sharer, "who I am") and a grant code
// (sharer -> recipient, "here is the sealed workspace key"). Both follow the
// same beresta:// URI idiom already used by the desktop server-connect QR
// field; encoding/decoding is pure and makes no network or account calls.
package sharecode

import (
	"encoding/hex"
	"fmt"
	"net/url"

	"github.com/beresta-app/beresta/core/model"
)

// EncodeIdentity renders userID and identityPublicKey as a beresta://identity
// code for the recipient to hand to whoever owns the workspace they want to
// join.
func EncodeIdentity(userID model.ID, identityPublicKey []byte) (string, error) {
	if err := userID.Validate(); err != nil {
		return "", fmt.Errorf("sharecode: invalid user id: %w", err)
	}
	if len(identityPublicKey) == 0 {
		return "", fmt.Errorf("sharecode: identity public key must not be empty")
	}
	values := url.Values{}
	values.Set("user", userID.String())
	values.Set("key", hex.EncodeToString(identityPublicKey))
	encoded := url.URL{Scheme: "beresta", Host: "identity", RawQuery: values.Encode()}
	return encoded.String(), nil
}

// DecodeIdentity parses a code produced by EncodeIdentity.
func DecodeIdentity(code string) (userID model.ID, identityPublicKey []byte, err error) {
	parsed, err := url.Parse(code)
	if err != nil || parsed.Scheme != "beresta" || parsed.Host != "identity" {
		return model.Nil, nil, fmt.Errorf("sharecode: not a valid identity code")
	}
	query := parsed.Query()
	userID, err = model.ParseIDString(query.Get("user"))
	if err != nil {
		return model.Nil, nil, fmt.Errorf("sharecode: invalid user id in identity code")
	}
	identityPublicKey, err = hex.DecodeString(query.Get("key"))
	if err != nil || len(identityPublicKey) == 0 {
		return model.Nil, nil, fmt.Errorf("sharecode: invalid identity key in identity code")
	}
	return userID, identityPublicKey, nil
}

// EncodeGrant renders a sealed workspace-membership grant as a
// beresta://grant code for the recipient to redeem with AcceptWorkspaceGrant.
// keyID, authorityPublicKey, and signature must all be non-empty.
func EncodeGrant(workspaceID model.ID, keyID, authorityPublicKey, signature []byte) (string, error) {
	if err := workspaceID.Validate(); err != nil {
		return "", fmt.Errorf("sharecode: invalid workspace id: %w", err)
	}
	if len(keyID) == 0 || len(authorityPublicKey) == 0 || len(signature) == 0 {
		return "", fmt.Errorf("sharecode: grant fields must not be empty")
	}
	values := url.Values{}
	values.Set("workspace", workspaceID.String())
	values.Set("key", hex.EncodeToString(keyID))
	values.Set("authority", hex.EncodeToString(authorityPublicKey))
	values.Set("sig", hex.EncodeToString(signature))
	encoded := url.URL{Scheme: "beresta", Host: "grant", RawQuery: values.Encode()}
	return encoded.String(), nil
}

// DecodeGrant parses a code produced by EncodeGrant.
func DecodeGrant(code string) (workspaceID model.ID, keyID, authorityPublicKey, signature []byte, err error) {
	parsed, err := url.Parse(code)
	if err != nil || parsed.Scheme != "beresta" || parsed.Host != "grant" {
		return model.Nil, nil, nil, nil, fmt.Errorf("sharecode: not a valid grant code")
	}
	query := parsed.Query()
	workspaceID, err = model.ParseIDString(query.Get("workspace"))
	if err != nil {
		return model.Nil, nil, nil, nil, fmt.Errorf("sharecode: invalid workspace id in grant code")
	}
	keyID, err = hex.DecodeString(query.Get("key"))
	if err != nil || len(keyID) == 0 {
		return model.Nil, nil, nil, nil, fmt.Errorf("sharecode: invalid key id in grant code")
	}
	authorityPublicKey, err = hex.DecodeString(query.Get("authority"))
	if err != nil || len(authorityPublicKey) == 0 {
		return model.Nil, nil, nil, nil, fmt.Errorf("sharecode: invalid authority key in grant code")
	}
	signature, err = hex.DecodeString(query.Get("sig"))
	if err != nil || len(signature) == 0 {
		return model.Nil, nil, nil, nil, fmt.Errorf("sharecode: invalid signature in grant code")
	}
	return workspaceID, keyID, authorityPublicKey, signature, nil
}
