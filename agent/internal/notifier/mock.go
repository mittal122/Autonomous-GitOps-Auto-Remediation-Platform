package notifier

import (
	"context"
	"sync"
	"time"

	"github.com/autosre/agent/internal/contracts"
)

// MockNotifier satisfies contracts.Notifier for use in tests.
// It records all calls and returns configurable outcomes.
// No network calls are made.
type MockNotifier struct {
	mu sync.Mutex

	// Recorded calls.
	Notified  []NotifyCall
	Approvals []ApprovalCall
	Escalated []EscalateCall

	// Configurable outcomes.
	// ApprovalResult is returned by RequestApproval. Defaults to DENIED if zero.
	ApprovalResult contracts.ApprovalResult
	// NotifyErr, if non-nil, is returned by Notify.
	NotifyErr error
	// EscalateErr, if non-nil, is returned by Escalate.
	EscalateErr error
}

// NotifyCall records one call to Notify.
type NotifyCall struct {
	Subject string
	Body    string
}

// ApprovalCall records one call to RequestApproval.
type ApprovalCall struct {
	Proposal contracts.RemediationProposal
}

// EscalateCall records one call to Escalate.
type EscalateCall struct {
	Incident contracts.Incident
	Reason   string
}

func (m *MockNotifier) Notify(_ context.Context, subject, body string) error {
	m.mu.Lock()
	m.Notified = append(m.Notified, NotifyCall{Subject: subject, Body: body})
	m.mu.Unlock()
	return m.NotifyErr
}

func (m *MockNotifier) RequestApproval(_ context.Context, proposal contracts.RemediationProposal) (contracts.ApprovalResult, error) {
	m.mu.Lock()
	m.Approvals = append(m.Approvals, ApprovalCall{Proposal: proposal})
	result := m.ApprovalResult
	m.mu.Unlock()

	if result.Decision == "" {
		result = contracts.ApprovalResult{
			Decision:  contracts.ApprovalDenied,
			Approver:  "mock",
			DecidedAt: time.Now(),
			Reason:    "mock default: fail-closed DENIED",
		}
	}
	return result, nil
}

func (m *MockNotifier) Escalate(_ context.Context, incident contracts.Incident, reason string) error {
	m.mu.Lock()
	m.Escalated = append(m.Escalated, EscalateCall{Incident: incident, Reason: reason})
	m.mu.Unlock()
	return m.EscalateErr
}
