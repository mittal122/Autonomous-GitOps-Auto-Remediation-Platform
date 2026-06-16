# Error Log

| Date/Time | Error Description | Root Cause | Resolution | Files Affected | Status |
|-----------|-------------------|------------|------------|----------------|--------|
| 2026-06-15 | `go mod init` failed — Go not installed on dev machine | Go toolchain not present in local environment | Created `go.mod` manually with `module github.com/autosre/agent` and `go 1.22`; CI installs Go via `actions/setup-go@v5` | `agent/go.mod` | Resolved |
| 2026-06-16 | `go.sum` absent — cannot be generated without Go toolchain | Go not installed on dev machine; `go mod tidy` cannot run | Hand-authored `go.mod` with known direct and indirect deps from `k8s.io/client-go v0.29.3`; added `go mod tidy` step to CI before lint and test jobs; CI generates `go.sum` transiently each run. Run `go mod tidy` locally once Go 1.22 is available. | `agent/go.mod`, `.github/workflows/ci.yml` | Resolved (CI workaround active) |
| 2026-06-16 (P5) | Test signals pre-populated before `Verify()` window opened had `ReceivedAt` before `windowStart` so `RecentSignalsFor` filtered them out — FAILED tests would have incorrectly produced RECOVERED | `RecentSignalsFor` filters by `since=windowStart`; signals stamped at test-setup time are before that | Added `futureSig()` helper (`ReceivedAt = now+1h`) so pre-populated signals are always visible inside the window; RECOVERED tests use `sig()` (current time, filtered out) | `agent/internal/verifier/verifier_test.go` | Resolved |
| 2026-06-16 (P6) | Test called `result.EscalationNeeded()` as a method — `ApprovalResult` struct has no such method | `ApprovalResult` is a plain struct; `EscalationNeeded` is only a field on `VerificationResult`, not on `ApprovalResult` | Removed the invalid method call; replaced with direct comparison `result.Decision == contracts.ApprovalApproved` to verify the fail-closed invariant | `agent/internal/notifier/notifier_test.go` | Resolved |

---

> **Instructions:** Add one row per error encountered during any prompt.
> Date/Time format: `YYYY-MM-DD HH:MM UTC`.
> Status: `Resolved` or `Pending`.
