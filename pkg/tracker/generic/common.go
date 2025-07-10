package generic

import (
	"context"
	"fmt"
	"io"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/cache"

	"github.com/werf/kubedog/pkg/tracker/debug"
)

func init() {
	initResourceStatusJSONPathsByPriority()
}

type UnrecoverableWatchError struct {
	ResName string
	Err     error
}

func (e UnrecoverableWatchError) Error() string {
	return fmt.Sprintf("unrecoverable watch error for %q: %s", e.ResName, e.Err.Error())
}

func (e UnrecoverableWatchError) Unwrap() error {
	return e.Err
}

type SetWatchErrorHandlerOptions struct {
	FatalWatchErr *UnrecoverableWatchError // If unrecoverable watch error occurred it will be saved here.
}

func SetWatchErrorHandler(cancelFn context.CancelCauseFunc, resName string, setWatchErrorHandler func(handler cache.WatchErrorHandler) error, opts SetWatchErrorHandlerOptions) error {
	return setWatchErrorHandler(
		func(r *cache.Reflector, err error) {
			isExpiredError := func(err error) bool {
				return apierrors.IsResourceExpired(err) || apierrors.IsGone(err)
			}

			// Based on: k8s.io/client-go@v0.30.11/tools/cache/reflector.go
			switch {
			case isExpiredError(err):
				if debug.Debug() {
					fmt.Printf("[SetWatchErrorHandler] Resource %q watch closed with expired error: %s\n", resName, err)
				}
			case err == io.EOF:
				// watch closed normally
			case err == io.ErrUnexpectedEOF:
				if debug.Debug() {
					fmt.Printf("[SetWatchErrorHandler] Resource %q watch closed with unexpected EOF error: %s\n", resName, err)
				}
			default:
				if debug.Debug() {
					fmt.Printf("[SetWatchErrorHandler] Resource %q watch closed with an error: %s\n", resName, err)
				}

				if opts.FatalWatchErr != nil {
					*opts.FatalWatchErr = UnrecoverableWatchError{ResName: resName, Err: err}
				}

				cancelFn(fmt.Errorf("in watch error handler context canceled: %w", err))
			}
		},
	)
}
