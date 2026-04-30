package auth

import (
	"errors"
	"net/http"
)

type Authenticator interface {
	Authenticate(r *http.Request) (Principal, error)
}

type ChainAuthenticator struct {
	Authenticators []Authenticator
}

func (c *ChainAuthenticator) Authenticate(r *http.Request) (Principal, error) {
	var authErr error
	headerPresent := r.Header.Get("Authorization") != ""

	for _, a := range c.Authenticators {
		p, err := a.Authenticate(r)
		if err == nil {
			return p, nil
		}
		if errors.Is(err, ErrNotApplicable) {
			continue
		}
		if authErr == nil {
			authErr = err
		}
	}
	if authErr != nil {
		return Principal{}, authErr
	}
	if headerPresent {
		return Principal{}, ErrUnsupportedScheme
	}
	return Principal{}, ErrMissingAuth
}
