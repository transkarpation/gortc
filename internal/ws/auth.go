package ws

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// App is a configured API consumer: an apiKey resolves to an appId and the
// HMAC secret used to sign and verify that app's JWTs.
type App struct {
	AppID     string
	APISecret []byte
}

// Principal is the authenticated identity resolved from a handshake.
type Principal struct {
	AppID  string
	UserID string
}

// Authenticator validates the websocket handshake against a set of apps keyed
// by API key. The "authorization" JWT is verified with the matched app's
// secret, and its "userId" claim must equal the "userId" query parameter.
type Authenticator struct {
	apps map[string]App // keyed by API key
}

// NewAuthenticator builds an Authenticator from apps keyed by their API key.
func NewAuthenticator(appsByKey map[string]App) Authenticator {
	return Authenticator{apps: appsByKey}
}

// AppID returns the appId mapped to apiKey, and whether it is known.
func (a Authenticator) AppID(apiKey string) (string, bool) {
	app, ok := a.apps[apiKey]
	return app.AppID, ok
}

// Mint signs and returns an HMAC JWT for userID under the app identified by
// apiKey, valid for ttl. It uses the same secret and algorithm as authenticate,
// so the result passes validation. Intended for development and testing only.
func (a Authenticator) Mint(apiKey, userID string, ttl time.Duration) (string, error) {
	app, ok := a.apps[apiKey]
	if !ok {
		return "", fmt.Errorf("unknown apiKey")
	}
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"userId": userID,
		"iat":    now.Unix(),
		"exp":    now.Add(ttl).Unix(),
	})
	return token.SignedString(app.APISecret)
}

// authenticate resolves the app from the "apiKey" query parameter, verifies the
// "authorization" JWT with that app's secret, and confirms the JWT's "userId"
// claim equals the "userId" query parameter. On success it returns the
// authenticated principal.
func (a Authenticator) authenticate(r *http.Request) (Principal, error) {
	q := r.URL.Query()

	app, ok := a.apps[q.Get("apiKey")]
	if !ok {
		return Principal{}, errors.New("unknown apiKey")
	}

	userID := q.Get("userId")
	if userID == "" {
		return Principal{}, errors.New("missing userId")
	}

	token, err := jwt.Parse(q.Get("authorization"), func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return app.APISecret, nil
	})
	if err != nil {
		return Principal{}, fmt.Errorf("invalid authorization token: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return Principal{}, errors.New("invalid token claims")
	}
	claimUserID, ok := claims["userId"].(string)
	if !ok {
		return Principal{}, errors.New("token missing string userId claim")
	}
	if subtle.ConstantTimeCompare([]byte(claimUserID), []byte(userID)) != 1 {
		return Principal{}, errors.New("userId does not match token")
	}

	return Principal{AppID: app.AppID, UserID: userID}, nil
}
