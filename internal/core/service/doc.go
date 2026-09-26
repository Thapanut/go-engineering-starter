// Package service implements the inbound ports (use cases).
//
// Services depend on internal/core/domain and internal/core/port only. They own
// the business rules and the unit of work (port.TxManager.WithinTx). Unit-test
// them against the in-memory adapter; prove transactional/concurrency guarantees
// with -tags=integration tests on PostgreSQL.
package service
