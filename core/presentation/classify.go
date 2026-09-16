package presentation

import "github.com/beresta-app/beresta/core/transport"

// ClassifyTransportError maps a stable transport.ErrorClass (see
// core/transport's Diagnose/classifyTransportError) to the actionable
// UserError a UI must present, per the "Actionable and safe error
// presentation" requirement in specs/product-experience. localDataSaved
// reflects whether local commits remain durable despite the transport
// failure; it is always true for a well-behaved offline-first client,
// since transport failures never block a local commit.
//
// Authentication failures currently classify as
// ErrorCategoryTrustOrConfiguration rather than ErrorCategoryDeviceRevoked:
// the transport layer alone cannot yet distinguish a revoked device from a
// misconfigured or expired credential (both surface as the same rejected
// challenge/session). A caller that has independent evidence this device
// was revoked (for example, a processed device-revocation record) should
// report ErrorCategoryDeviceRevoked directly via NewUserError instead of
// calling this function.
func ClassifyTransportError(class transport.ErrorClass, localDataSaved bool) UserError {
	switch class {
	case transport.ErrorClassNoNetwork:
		return NewUserError(ErrorCategoryNoNetwork, localDataSaved, true, false)
	case transport.ErrorClassServerUnavailable:
		return NewUserError(ErrorCategoryServerUnavailable, localDataSaved, true, false)
	case transport.ErrorClassCertificateMismatch, transport.ErrorClassAuthenticationFailed:
		return NewUserError(ErrorCategoryTrustOrConfiguration, localDataSaved, false, true)
	default:
		return NewUserError(ErrorCategoryServerUnavailable, localDataSaved, true, false)
	}
}

// ClassifyQuarantine returns the UserError for an incoming operation that
// failed verification and was quarantined rather than applied. Beresta
// never retries a quarantined operation automatically or offers a skip
// path; the user must review it (see specs/sync-engine).
func ClassifyQuarantine(localDataSaved bool) UserError {
	return NewUserError(ErrorCategoryUnsafeIncomingChange, localDataSaved, false, true)
}
