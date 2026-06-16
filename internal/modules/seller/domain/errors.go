package domain

import "errors"

// Sentinel errors mapped to the standard HTTP envelope by the handler.
var (
	ErrApplicationExists   = errors.New("an active seller application already exists")
	ErrAlreadySeller       = errors.New("user is already a seller")
	ErrApplicationNotFound = errors.New("seller application not found")
	ErrStoreNotFound       = errors.New("store not found")
	ErrNotApprovable       = errors.New("application is not in a state that can be approved")
	ErrNotRejectable       = errors.New("application is not in a state that can be rejected")
	ErrNotSuspendable      = errors.New("seller is not in a state that can be suspended")
	ErrNotApproved         = errors.New("seller is not approved") // store edits require an approved seller
)
