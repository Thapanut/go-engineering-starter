package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// TransferStatus is the state of a transfer.
type TransferStatus string

// TransferCompleted is the only status in this synchronous, single-ledger flow.
const TransferCompleted TransferStatus = "COMPLETED"

// MaxReferenceLen is the maximum length of a transfer reference, in characters.
const MaxReferenceLen = 140

var (
	uuidRe           = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	idempotencyKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// IsValidID reports whether s is a UUID.
func IsValidID(s string) bool { return uuidRe.MatchString(s) }

// NormalizeID returns the canonical (lower-case) form of a UUID, as stored.
func NormalizeID(s string) string { return strings.ToLower(s) }

// Transfer is a completed movement of money between two accounts.
type Transfer struct {
	ID             string
	RequestedBy    string // customer id of the caller
	IdempotencyKey string
	RequestHash    string
	FromAccountID  string
	ToAccountID    string
	Amount         Money
	Reference      string
	Status         TransferStatus
	CreatedAt      time.Time
}

// TransferRequest is what a customer asks for. It is validated before any I/O.
type TransferRequest struct {
	CustomerID     string
	IdempotencyKey string
	FromAccountID  string
	ToAccountID    string
	Amount         Money
	Reference      string
}

// Normalized returns r with canonical ids, so equal requests fingerprint equally.
func (r TransferRequest) Normalized() TransferRequest {
	r.FromAccountID, r.ToAccountID = NormalizeID(r.FromAccountID), NormalizeID(r.ToAccountID)
	return r
}

// Validate checks request shape and static business rules (spec AC-05).
func (r TransferRequest) Validate() error {
	switch {
	case r.CustomerID == "":
		return Invalid("customer is required")
	case !idempotencyKeyRe.MatchString(r.IdempotencyKey):
		return Invalid("Idempotency-Key must be 1-64 characters of [A-Za-z0-9_-]")
	case !IsValidID(r.FromAccountID):
		return Invalid("fromAccountId must be a UUID")
	case !IsValidID(r.ToAccountID):
		return Invalid("toAccountId must be a UUID")
	case r.FromAccountID == r.ToAccountID:
		return Invalid("fromAccountId and toAccountId must differ")
	case r.Amount.Amount <= 0:
		return Invalid("amount must be greater than 0")
	case !r.Amount.Currency.IsSupported():
		return Invalid("currency is not supported")
	case utf8.RuneCountInString(r.Reference) > MaxReferenceLen:
		return Invalid("reference must be at most 140 characters")
	}
	return nil
}

// Fingerprint identifies the request body, so a reused Idempotency-Key with a
// different payload is detected (spec AC-03).
func (r TransferRequest) Fingerprint() string {
	h := sha256.New()
	for _, part := range []string{
		r.FromAccountID, r.ToAccountID,
		strconv.FormatInt(r.Amount.Amount, 10), string(r.Amount.Currency),
		r.Reference,
	} {
		h.Write([]byte(strconv.Itoa(len(part)))) // length-prefix to avoid ambiguity
		h.Write([]byte{':'})
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}
