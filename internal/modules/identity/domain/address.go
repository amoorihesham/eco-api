package domain

import (
	"time"

	"github.com/google/uuid"
)

// Address is a buyer's saved shipping address. Optional fields (Line2, Region, Phone) are the empty
// string when absent. Exactly one Address per user may carry IsDefault (enforced in service + DB).
type Address struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Recipient  string
	Line1      string
	Line2      string
	City       string
	Region     string
	PostalCode string
	Country    string // ISO 3166-1 alpha-2
	Phone      string
	IsDefault  bool
	CreatedAt  time.Time
}
