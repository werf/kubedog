package generic

import (
	"context"
	"fmt"
	"io"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/cache"

	"github.com/werf/logboek"
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

func SetWatchErrorHandler(cancelFn context.CancelFunc, resName string, setWatchErrorHandler func(handler cache.WatchErrorHandler) error, opts SetWatchErrorHandlerOptions) error {
	return setWatchErrorHandler(
		func(r *cache.Reflector, err error) {
			isExpiredError := func(err error) bool {
				// In Kubernetes 1.17 and earlier, the api server returns both apierrors.StatusReasonExpired and
				// apierrors.StatusReasonGone for HTTP 410 (Gone) status code responses. In 1.18 the kube server is more consistent
				// and always returns apierrors.StatusReasonExpired. For backward compatibility we can only remove the apierrors.IsGone
				// check when we fully drop support for Kubernetes 1.17 servers from reflectors.
				return apierrors.IsResourceExpired(err) || apierrors.IsGone(err)
			}

			switch {
			case isExpiredError(err):
				// Don't set LastSyncResourceVersionUnavailable - LIST call with ResourceVersion=RV already
				// has a semantic that it returns data at least as fresh as provided RV.
				// So first try to LIST with setting RV to resource version of last observed object.
				logboek.Context(context.Background()).Info().LogF("watch of %q closed with: %s\n", resName, err)
			case err == io.EOF:
				// watch closed normally
			case err == io.ErrUnexpectedEOF:
				logboek.Context(context.Background()).Info().LogF("watch of %q closed with unexpected EOF: %s\n", resName, err)
			default:
				logboek.Context(context.Background()).Warn().LogF("failed to watch %q: %s\n", resName, err)
				if opts.FatalWatchErr != nil {
					*opts.FatalWatchErr = UnrecoverableWatchError{ResName: resName, Err: err}
				}
				cancelFn()
			}
		},
	)
}
