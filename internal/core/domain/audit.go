package domain

import "time"

// AuditTransferCreated is the audit action for a completed transfer.
const AuditTransferCreated = "TRANSFER_CREATED"

// AuditEntry records who did what, when, with before/after state (spec AC-11).
// It must be written in the same transaction as the change it describes.
type AuditEntry struct {
	ID           string
	Actor        string
	Action       string
	ResourceType string
	ResourceID   string
	TraceID      string
	Before       map[string]any
	After        map[string]any
	OccurredAt   time.Time
}
