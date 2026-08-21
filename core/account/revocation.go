package account

import (
	"errors"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/model"
)

// RevocationKind distinguishes what a signed revocation record targets.
type RevocationKind uint8

const (
	// RevocationKindDevice targets one of this account's own devices. It is
	// account-wide: devices are not scoped to a single workspace.
	RevocationKindDevice RevocationKind = iota + 1
	// RevocationKindMember targets a workspace membership.
	RevocationKindMember
)

// RevocationRecord is a signed, self-contained statement that an authorized
// account revoked a device or workspace member as of a given local HLC
// event. It exists independently of the server's own authorization check
// (see server.Storage.RevokeDevice / RevokeMember, which enforce the
// boundary immediately): the signature lets any later reader - including a
// device that was offline during the revocation - verify the boundary was
// authorized by this account's authority key, not merely asserted by the
// server.
type RevocationRecord struct {
	Kind        RevocationKind
	TargetID    model.ID
	WorkspaceID model.ID // zero for RevocationKindDevice
	Clock       model.HLC
	Signature   []byte
}

// SignDeviceRevocation signs a record revoking targetDeviceID. It performs
// no network access and does not itself reject future authentication from
// the target; the caller still submits the revocation through a transport
// (see core/transport.HTTP.RevokeDevice), which is what the server actually
// enforces.
func (a *Account) SignDeviceRevocation(targetDeviceID model.ID) (RevocationRecord, error) {
	if err := targetDeviceID.Validate(); err != nil {
		return RevocationRecord{}, errors.New("account: invalid target device")
	}
	a.mu.Lock()
	if a.locked {
		a.mu.Unlock()
		return RevocationRecord{}, ErrAccountLocked
	}
	authorityPrivate := a.authorityPrivate
	a.mu.Unlock()

	clock, err := a.tick()
	if err != nil {
		return RevocationRecord{}, err
	}
	signature, err := corecrypto.SignCanonical(corecrypto.CryptoProfileV1, authorityPrivate, corecrypto.SignatureDomainRevocation,
		revocationSignatureInput(RevocationKindDevice, targetDeviceID, model.Nil, clock))
	if err != nil {
		return RevocationRecord{}, err
	}
	return RevocationRecord{Kind: RevocationKindDevice, TargetID: targetDeviceID, Clock: clock, Signature: signature}, nil
}

// SignMemberRevocation signs a record revoking targetUserID's membership in
// workspaceID. As with SignDeviceRevocation, the caller still submits the
// revocation through a transport and must follow it with a workspace key
// rotation (see BeginWorkspaceKeyRotation) so the removed member cannot
// decrypt content written after this point.
func (a *Account) SignMemberRevocation(workspaceID, targetUserID model.ID) (RevocationRecord, error) {
	if err := workspaceID.Validate(); err != nil || targetUserID.Validate() != nil {
		return RevocationRecord{}, errors.New("account: invalid member revocation")
	}
	a.mu.Lock()
	if a.locked {
		a.mu.Unlock()
		return RevocationRecord{}, ErrAccountLocked
	}
	authorityPrivate := a.authorityPrivate
	a.mu.Unlock()

	clock, err := a.tick()
	if err != nil {
		return RevocationRecord{}, err
	}
	signature, err := corecrypto.SignCanonical(corecrypto.CryptoProfileV1, authorityPrivate, corecrypto.SignatureDomainRevocation,
		revocationSignatureInput(RevocationKindMember, targetUserID, workspaceID, clock))
	if err != nil {
		return RevocationRecord{}, err
	}
	return RevocationRecord{Kind: RevocationKindMember, TargetID: targetUserID, WorkspaceID: workspaceID, Clock: clock, Signature: signature}, nil
}

// VerifyRevocation checks a RevocationRecord's signature against the
// authorizing account's authority public key.
func VerifyRevocation(authorityPublicKey []byte, record RevocationRecord) error {
	return corecrypto.VerifyCanonical(corecrypto.CryptoProfileV1, authorityPublicKey, corecrypto.SignatureDomainRevocation,
		revocationSignatureInput(record.Kind, record.TargetID, record.WorkspaceID, record.Clock), record.Signature)
}

func revocationSignatureInput(kind RevocationKind, targetID, workspaceID model.ID, clock model.HLC) []byte {
	result := append([]byte(nil), byte(kind))
	result = appendLP(result, targetID.Bytes())
	result = appendLP(result, workspaceID.Bytes())
	result = appendU32(result, uint32(clock.PhysicalMS>>32))
	result = appendU32(result, uint32(clock.PhysicalMS))
	result = appendU32(result, clock.Logical)
	result = appendLP(result, clock.DeviceID.Bytes())
	return result
}
