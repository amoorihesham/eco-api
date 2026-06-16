package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// ErrInvalidToken is returned for any malformed, mis-signed, or expired access token.
var ErrInvalidToken = errors.New("invalid token")

// Claims is the verified identity carried by an access token.
type Claims struct {
	UserID uuid.UUID
	Role   string
}

// TokenIssuer mints access tokens; Verifier validates them. Two ports so handlers can depend narrowly.
type TokenIssuer interface {
	Issue(userID uuid.UUID, role string) (token string, expiresIn int, err error)
}

// Verifier validates an access token and returns the claims it carries.
type Verifier interface {
	Verify(token string) (Claims, error)
}

// JWT is the HS256 adapter satisfying both ports.
type JWT struct {
	secret    []byte
	accessTTL time.Duration
}

// NewJWT builds a JWT adapter that signs and verifies HS256 tokens with
// secret, issuing access tokens valid for accessTTL.
func NewJWT(secret string, accessTTL time.Duration) JWT {
	return JWT{secret: []byte(secret), accessTTL: accessTTL}
}

// Issue mints a signed access token for userID and role.
func (j JWT) Issue(userID uuid.UUID, role string) (string, int, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":  userID.String(),
		"role": role,
		"iat":  now.Unix(),
		"exp":  now.Add(j.accessTTL).Unix(),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(j.secret)
	if err != nil {
		return "", 0, fmt.Errorf("sign token: %w", err)
	}
	return signed, int(j.accessTTL.Seconds()), nil
}

// Verify parses and validates token, returning the embedded claims.
func (j JWT) Verify(token string) (Claims, error) {
	parsed, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return j.secret, nil
	})
	if err != nil || !parsed.Valid {
		return Claims{}, ErrInvalidToken
	}
	mc, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return Claims{}, ErrInvalidToken
	}
	sub, _ := mc["sub"].(string)
	uid, err := uuid.Parse(sub)
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	role, _ := mc["role"].(string)
	return Claims{UserID: uid, Role: role}, nil
}
