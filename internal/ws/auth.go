package ws

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Authenticator validates the websocket handshake against a static API key and
// an HMAC-signed JWT supplied as query parameters.
type Authenticator struct {
	// APIKey is the expected value of the "apiKey" query parameter.
	APIKey string
	// JWTSecret is the HMAC secret the "authorization" JWT is verified with.
	JWTSecret []byte
}

// Mint signs and returns an HMAC JWT for userID, valid for ttl. It uses the
// same secret and algorithm as authenticate, so the result passes validation.
// Intended for development and testing only.
func (a Authenticator) Mint(userID string, ttl time.Duration) (string, error) {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"userId": userID,
		"iat":    now.Unix(),
		"exp":    now.Add(ttl).Unix(),
	})
	return token.SignedString(a.JWTSecret)
}

// authenticate checks the request's "apiKey", "authorization" (a JWT) and
// "userId" query parameters. It returns nil only when the API key matches, the
// JWT is valid (signature + standard claims), and the JWT's "userId" claim
// equals the "userId" query parameter.
func (a Authenticator) authenticate(r *http.Request) error {
	q := r.URL.Query()

	if subtle.ConstantTimeCompare([]byte(q.Get("apiKey")), []byte(a.APIKey)) != 1 {
		return errors.New("invalid apiKey")
	}

	userID := q.Get("userId")
	if userID == "" {
		return errors.New("missing userId")
	}

	token, err := jwt.Parse(q.Get("authorization"), func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return a.JWTSecret, nil
	})
	if err != nil {
		return fmt.Errorf("invalid authorization token: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return errors.New("invalid token claims")
	}
	claimUserID, ok := claims["userId"].(string)
	if !ok {
		return errors.New("token missing string userId claim")
	}
	if subtle.ConstantTimeCompare([]byte(claimUserID), []byte(userID)) != 1 {
		return errors.New("userId does not match token")
	}

	return nil
}
