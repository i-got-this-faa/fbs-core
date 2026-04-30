package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/i-got-this-faa/fbs/internal/metadata"
)

type BearerAuthenticator struct {
	Repo metadata.UserRepository
}

func (b *BearerAuthenticator) Authenticate(r *http.Request) (Principal, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return Principal{}, ErrNotApplicable
	}

	parts := strings.Fields(authHeader)
	if len(parts) != 2 {
		return Principal{}, ErrMalformedToken
	}
	scheme, token := parts[0], parts[1]

	if !strings.EqualFold(scheme, "Bearer") {
		return Principal{}, ErrNotApplicable
	}

	accessKeyID, secret, found := strings.Cut(token, ".")
	if !found || accessKeyID == "" || secret == "" {
		return Principal{}, ErrMalformedToken
	}

	user, err := b.Repo.GetByAccessKeyID(r.Context(), accessKeyID)
	if err != nil {
		if errors.Is(err, metadata.ErrUserNotFound) {
			return Principal{}, ErrInvalidCredentials
		}
		return Principal{}, ErrInternal
	}

	if !user.IsActive {
		return Principal{}, ErrInactiveUser
	}

	if !verifySecret(secret, user.SecretHash) {
		return Principal{}, ErrInvalidCredentials
	}

	return Principal{
		UserID:      user.ID,
		DisplayName: user.DisplayName,
		AccessKeyID: user.AccessKeyID,
		Role:        user.Role,
		DevMode:     false,
	}, nil
}
