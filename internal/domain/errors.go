package domain

import "errors"

var (
	// ErrDuplicateProcessing is returned when a key is already being processed.
	ErrDuplicateProcessing = errors.New("payment is already being processed")

	// ErrParamsMismatch is returned when a duplicate key has different parameters.
	ErrParamsMismatch = errors.New("request parameters do not match original payment")

	// ErrAlreadyCompleted is returned when trying to complete an already-completed payment.
	ErrAlreadyCompleted = errors.New("payment already completed")

	// ErrKeyNotFound is returned when an idempotency key does not exist.
	ErrKeyNotFound = errors.New("idempotency key not found")

	// ErrKeyExpired is returned when a key is past its expiration window.
	ErrKeyExpired = errors.New("idempotency key has expired")

	// ErrInvalidStatus is returned when an invalid status is provided.
	ErrInvalidStatus = errors.New("invalid status: must be 'succeeded' or 'failed'")

	// ErrMerchantNotFound is returned when a merchant policy is not found.
	ErrMerchantNotFound = errors.New("merchant not found")
)
