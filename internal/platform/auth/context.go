// Package auth provides password hashing, JWT issuance/verification, and
// RBAC middleware shared by every module that needs authentication.
package auth

import "context"

type ctxKey int

const claimsKey ctxKey = iota

func withClaims(ctx context.Context, c Claims) context.Context {
	return context.WithValue(ctx, claimsKey, c)
}

// ClaimsFrom returns the verified claims placed by Authn, if present.
func ClaimsFrom(ctx context.Context) (Claims, bool) {
	c, ok := ctx.Value(claimsKey).(Claims)
	return c, ok
}
