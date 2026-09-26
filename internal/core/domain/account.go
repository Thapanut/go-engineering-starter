package domain

import "time"

// AccountStatus is the lifecycle state of an account.
type AccountStatus string

// Account statuses. Only ACTIVE accounts can be debited or credited.
const (
	AccountActive AccountStatus = "ACTIVE"
	AccountFrozen AccountStatus = "FROZEN"
	AccountClosed AccountStatus = "CLOSED"
)

// Account is a deposit account. ID is an opaque UUID, not the account number (PII).
type Account struct {
	ID         string
	CustomerID string
	Balance    Money
	Status     AccountStatus
	UpdatedAt  time.Time
}

// OwnedBy reports whether customerID owns the account.
func (a Account) OwnedBy(customerID string) bool { return a.CustomerID == customerID }

// Debit removes amount from the balance (spec AC-04, AC-09, AC-10).
func (a *Account) Debit(amount Money) error {
	if a.Status != AccountActive {
		return ErrAccountInactive
	}
	next, err := a.Balance.Sub(amount)
	if err != nil {
		return err
	}
	if next.Amount < 0 {
		return ErrInsufficientFunds
	}
	a.Balance = next
	return nil
}

// Credit adds amount to the balance (spec AC-09, AC-10).
func (a *Account) Credit(amount Money) error {
	if a.Status != AccountActive {
		return ErrAccountInactive
	}
	next, err := a.Balance.Add(amount)
	if err != nil {
		return err
	}
	a.Balance = next
	return nil
}
