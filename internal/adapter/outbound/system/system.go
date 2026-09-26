// Package system provides production implementations of port.Clock and port.IDGenerator.
package system

import (
	"crypto/rand"
	"fmt"
	"time"
)

// Clock is the production port.Clock.
type Clock struct{}

// Now returns the current time.
func (Clock) Now() time.Time { return time.Now() }

// UUIDGenerator is the production port.IDGenerator (random UUID v4, stdlib only).
type UUIDGenerator struct{}

// NewID returns a random UUID v4. It panics if the OS random source fails,
// which the Go runtime treats as unrecoverable anyway.
func (UUIDGenerator) NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("crypto/rand: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
