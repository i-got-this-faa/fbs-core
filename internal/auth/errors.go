package auth

import (
	"errors"
	"fmt"
)

var (
	ErrNotApplicable      = errors.New("auth: not applicable")
	ErrMissingAuth        = errors.New("auth: missing authorization header")
	ErrUnsupportedScheme  = errors.New("auth: unsupported authorization scheme")
	ErrMalformedToken     = errors.New("auth: malformed bearer token")
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	ErrInactiveUser       = errors.New("auth: user is inactive")
	ErrInternal           = errors.New("auth: internal error")
	ErrUnauthorized       = errors.New("auth: unauthorized")
	ErrForbidden          = errors.New("auth: forbidden")
)

// internalError marks cause as ErrInternal while keeping it in the chain, so a
// responder can log why authentication failed without exposing it to clients.
func internalError(cause error) error {
	return fmt.Errorf("%w: %w", ErrInternal, cause)
}
