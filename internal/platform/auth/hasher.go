package auth

import "golang.org/x/crypto/bcrypt"

// Hasher hashes and verifies passwords. The service depends on this port, not on bcrypt.
type Hasher interface {
	Hash(plaintext string) (string, error)
	Compare(hash, plaintext string) error
}

// BcryptHasher is the MVP adapter (ARCHITECTURE §10: bcrypt/argon2).
type BcryptHasher struct{ cost int }

// NewBcryptHasher builds a BcryptHasher with the given cost, falling back
// to bcrypt.DefaultCost when cost is 0.
func NewBcryptHasher(cost int) BcryptHasher {
	if cost == 0 {
		cost = bcrypt.DefaultCost
	}
	return BcryptHasher{cost: cost}
}

// Hash bcrypt-hashes plaintext.
func (h BcryptHasher) Hash(plaintext string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plaintext), h.cost)
	return string(b), err
}

// Compare returns nil if plaintext matches hash, or an error otherwise.
func (h BcryptHasher) Compare(hash, plaintext string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext))
}
