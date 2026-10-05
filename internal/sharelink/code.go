// Package sharelink generates and validates share link codes and paths.
package sharelink

import (
	"crypto/rand"
	"errors"
	"math/big"
	"net/url"
	"regexp"
)

// GeneratedCodeLength is the length of random codes. 10 base62 characters
// give about 59 bits of entropy, which is enough to make guessing impractical.
const GeneratedCodeLength = 10

// RoutePrefix is the public path prefix that serves share links. It is a
// single character so it can never collide with an S3 bucket name, which must
// be at least three characters long.
const RoutePrefix = "/s/"

const codeAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// ErrInvalidAlias is returned when a custom alias does not match AliasPattern.
var ErrInvalidAlias = errors.New("alias must be 3-64 characters of letters, digits, '-' or '_', starting with a letter or digit")

var aliasPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{2,63}$`)

// GenerateCode returns a random base62 code of GeneratedCodeLength characters.
func GenerateCode() (string, error) {
	alphabetSize := big.NewInt(int64(len(codeAlphabet)))
	code := make([]byte, GeneratedCodeLength)
	for i := range code {
		n, err := rand.Int(rand.Reader, alphabetSize)
		if err != nil {
			return "", err
		}
		code[i] = codeAlphabet[n.Int64()]
	}
	return string(code), nil
}

// ValidateAlias reports whether a user-chosen alias is an acceptable code.
func ValidateAlias(alias string) error {
	if !aliasPattern.MatchString(alias) {
		return ErrInvalidAlias
	}
	return nil
}

// Path returns the public path for a share link code.
func Path(code string) string {
	return RoutePrefix + url.PathEscape(code)
}
