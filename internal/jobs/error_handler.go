package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/getsentry/sentry-go"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// SentryErrorHandler captures job failures and panics to Sentry.
type SentryErrorHandler struct{}

func (h *SentryErrorHandler) HandleError(ctx context.Context, job *rivertype.JobRow, err error) *river.ErrorHandlerResult {
	sentry.CaptureException(err)
	slog.Error("job error",
		"job_id", job.ID,
		"kind", job.Kind,
		"attempt", job.Attempt,
		"err", err,
	)
	return nil // nil = use River's default retry behaviour
}

func (h *SentryErrorHandler) HandlePanic(ctx context.Context, job *rivertype.JobRow, panicVal any, trace string) *river.ErrorHandlerResult {
	sentry.CaptureMessage(fmt.Sprintf("job panic: %v\n%s", panicVal, trace))
	slog.Error("job panic",
		"job_id", job.ID,
		"kind", job.Kind,
		"panic", panicVal,
	)
	return nil
}
