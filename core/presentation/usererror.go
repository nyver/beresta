package presentation

// UserErrorCategory is the closed set of actionable error categories a UI
// may present, per the "Actionable and safe error presentation"
// requirement in specs/product-experience. Stable backend codes map into
// exactly one of these; raw stack traces, database errors, cryptographic
// internals, and response bodies never appear as primary UI text.
type UserErrorCategory string

const (
	// ErrorCategoryNoNetwork means the device has no usable network path
	// to a configured transport.
	ErrorCategoryNoNetwork UserErrorCategory = "no_network"
	// ErrorCategoryServerUnavailable means a configured transport is
	// reachable at the network layer but is not currently serving
	// requests.
	ErrorCategoryServerUnavailable UserErrorCategory = "server_unavailable"
	// ErrorCategoryTrustOrConfiguration means the transport requires
	// trust or configuration action, such as accepting a changed
	// certificate fingerprint or correcting a server address.
	ErrorCategoryTrustOrConfiguration UserErrorCategory = "trust_or_configuration"
	// ErrorCategoryDeviceRevoked means this device's or user's access was
	// revoked and re-pairing is required.
	ErrorCategoryDeviceRevoked UserErrorCategory = "device_revoked"
	// ErrorCategoryUnsafeIncomingChange means an incoming operation
	// failed verification and was quarantined rather than applied.
	ErrorCategoryUnsafeIncomingChange UserErrorCategory = "unsafe_incoming_change"
)

// Valid reports whether c is one of the closed UserErrorCategory values.
func (c UserErrorCategory) Valid() bool {
	switch c {
	case ErrorCategoryNoNetwork, ErrorCategoryServerUnavailable,
		ErrorCategoryTrustOrConfiguration, ErrorCategoryDeviceRevoked,
		ErrorCategoryUnsafeIncomingChange:
		return true
	default:
		return false
	}
}

// RecoveryAction is the closed set of primary recovery actions a UI may
// offer for an actionable error. Each UserErrorCategory maps to exactly
// one primary RecoveryAction.
type RecoveryAction string

const (
	// RecoveryActionNone means no user action is offered; Beresta
	// recovers automatically.
	RecoveryActionNone RecoveryAction = "none"
	// RecoveryActionRetryNow means the UI offers an immediate retry of
	// the failed connection attempt.
	RecoveryActionRetryNow RecoveryAction = "retry_now"
	// RecoveryActionReviewConnection means the UI directs the user to
	// review server address, TLS policy, or certificate trust.
	RecoveryActionReviewConnection RecoveryAction = "review_connection"
	// RecoveryActionReconnectDevice means the UI directs the user to
	// re-pair this device.
	RecoveryActionReconnectDevice RecoveryAction = "reconnect_device"
	// RecoveryActionReviewUnsafeChange means the UI directs the user to
	// open unsafe-change details and choose retry or continued review.
	RecoveryActionReviewUnsafeChange RecoveryAction = "review_unsafe_change"
)

// Valid reports whether a is one of the closed RecoveryAction values.
func (a RecoveryAction) Valid() bool {
	switch a {
	case RecoveryActionNone, RecoveryActionRetryNow, RecoveryActionReviewConnection,
		RecoveryActionReconnectDevice, RecoveryActionReviewUnsafeChange:
		return true
	default:
		return false
	}
}

// PrimaryRecoveryAction returns the single primary RecoveryAction for an
// actionable UserErrorCategory. It returns RecoveryActionNone for an
// invalid category.
func PrimaryRecoveryAction(category UserErrorCategory) RecoveryAction {
	switch category {
	case ErrorCategoryNoNetwork, ErrorCategoryServerUnavailable:
		return RecoveryActionRetryNow
	case ErrorCategoryTrustOrConfiguration:
		return RecoveryActionReviewConnection
	case ErrorCategoryDeviceRevoked:
		return RecoveryActionReconnectDevice
	case ErrorCategoryUnsafeIncomingChange:
		return RecoveryActionReviewUnsafeChange
	default:
		return RecoveryActionNone
	}
}

// UserError is the bounded, locale-free description of an actionable
// error, carrying exactly the facts specs/product-experience requires a
// UI to state: what happened (Category), whether local data remains
// saved, what Beresta will do automatically, whether the user must act,
// and one primary recovery action. It never carries raw backend error
// text; that stays in sanitized technical diagnostics.
type UserError struct {
	// Category is the actionable error category.
	Category UserErrorCategory
	// LocalDataSaved is true when local work remains durably saved
	// despite this error.
	LocalDataSaved bool
	// AutomaticRetry is true when Beresta will retry this failure on its
	// own without user action.
	AutomaticRetry bool
	// RequiresUserAction is true when the failure cannot resolve without
	// the user taking PrimaryAction.
	RequiresUserAction bool
	// PrimaryAction is the single recovery action the UI should offer.
	PrimaryAction RecoveryAction
}

// NewUserError builds a UserError for category with its primary recovery
// action filled in automatically.
func NewUserError(category UserErrorCategory, localDataSaved, automaticRetry, requiresUserAction bool) UserError {
	return UserError{
		Category:           category,
		LocalDataSaved:     localDataSaved,
		AutomaticRetry:     automaticRetry,
		RequiresUserAction: requiresUserAction,
		PrimaryAction:      PrimaryRecoveryAction(category),
	}
}
