// Package contract defines the remote REST/WS v1 wire contract.
//
// This package contains PURE CONTRACT TYPES, CONSTANTS AND VALIDATION
// FUNCTIONS. It must not import the remote, session, pty or app service
// packages, and must not register routes, start servers, build stores or
// implement business state machines. The production Decode*/Validate*/Marshal*
// entry points are pure functions callable from tests and future v1
// handler/producers, but NO handler, WebSocket consumer, store or route is
// wired in M0 — the runtime enforcement boundary is the contract package only.
// M1/M2/M3 producers must call these functions; they must not json.Marshal a
// raw DTO or json.Unmarshal directly into one.
//
// The normative wire document is docs/developer/remote-api-v1-contract.md.
// The single machine-readable wire example set consumed by both the Go and TS
// tests is
// mobile/src/lib/contract/testdata/v1-wire-fixtures.json.
//
// Field existence rules (see design §6.1):
//   - R (required): present, never null.
//   - O (optional): omitted when N/A; never null when present.
//   - C (conditional): required when the condition holds, otherwise omitted.
//   - v1 has NO nullable fields; null/missing-required/unknown-client-field is
//     bad_request.
//
// Time is carried as a pre-formatted RFC3339 string (UTC, 'Z'); business
// adapters are responsible for formatting/parsing. Seq is a per-session
// replay cursor (uint64) capped at the JS safe-integer ceiling.
package contract
