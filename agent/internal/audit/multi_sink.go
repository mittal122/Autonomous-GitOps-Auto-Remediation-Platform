package audit

import (
	"context"
	"errors"
)

// MultiSink fans out every Record call to multiple sinks in order.
// Query delegates to the first sink that supports it (typically the FileSink).
// A failing sink does not prevent delivery to subsequent sinks; all errors
// are joined and returned.
type MultiSink []AuditSink

// NewMultiSink returns a MultiSink wrapping the given sinks.
// nil sinks are silently skipped.
func NewMultiSink(sinks ...AuditSink) MultiSink {
	out := make(MultiSink, 0, len(sinks))
	for _, s := range sinks {
		if s != nil {
			out = append(out, s)
		}
	}
	return out
}

// Record writes ev to every sink. All errors are joined into a single error.
func (m MultiSink) Record(ctx context.Context, ev AuditEvent) error {
	var errs []error
	for _, s := range m {
		if err := s.Record(ctx, ev); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Query returns events from the first sink that returns a non-nil result.
func (m MultiSink) Query(ctx context.Context, f QueryFilter) ([]AuditEvent, error) {
	for _, s := range m {
		evs, err := s.Query(ctx, f)
		if err != nil || len(evs) > 0 {
			return evs, err
		}
	}
	return nil, nil
}

// compile-time interface assertion
var _ AuditSink = MultiSink{}
