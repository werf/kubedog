package pod

import (
	"fmt"
	"math"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/werf/kubedog/pkg/tracker/debug"
)

type ReadinessProbe struct {
	corev1.Probe

	startedAtTime          *time.Time
	ignoreFailuresDuration time.Duration

	startupProbe *corev1.Probe
}

func NewReadinessProbe(readinessProbe, startupProbe *corev1.Probe, isStartedNow *bool, ignoreFailuresDurationOverride *time.Duration) ReadinessProbe {
	probe := ReadinessProbe{
		Probe:        *readinessProbe,
		startupProbe: startupProbe,
	}
	probe.SetupStartedAtTime(isStartedNow)
	probe.setIgnoreFailuresDuration(ignoreFailuresDurationOverride)

	return probe
}

func (p *ReadinessProbe) SetupStartedAtTime(isStartedNow *bool) {
	var startedAtTime *time.Time
	if isStartedNow != nil && *isStartedNow {
		now := time.Now()
		startedAtTime = &now
	}
	p.startedAtTime = startedAtTime
}

func (p *ReadinessProbe) IsFailureShouldBeIgnoredNow() bool {
	if p.FailureThreshold == 1 {
		return false
	}

	if p.startedAtTime == nil {
		return true
	}

	ignoreFailuresUntilTime := p.startedAtTime.Add(p.ignoreFailuresDuration)
	if debug.Debug() {
		fmt.Printf("startedAtTime time is %q and ignoreFailuresUntilTime is %q for probe: %+v\n",
			p.startedAtTime, ignoreFailuresUntilTime, p)
	}

	return time.Now().Before(ignoreFailuresUntilTime)
}

func (p *ReadinessProbe) setIgnoreFailuresDuration(ignoreFailuresDurationOverride *time.Duration) {
	if ignoreFailuresDurationOverride != nil {
		p.ignoreFailuresDuration = *ignoreFailuresDurationOverride
	} else {
		p.ignoreFailuresDuration = p.calculateIgnoreFailuresDuration()
	}
}

func (p *ReadinessProbe) calculateIgnoreFailuresDuration() time.Duration {
	// Since we can't detect succeeded probes, but only the failed ones, we have to
	// make some assumptions in our formula, e.g. we don't account for the
	// possibility of breaking the chain of succeeded probes with failed ones and vice
	// versa. This means this formula provides an approximate duration, which might
	// be somewhat higher or lower than needed.
	ignoreFailuresDuration := time.Duration(float32(math.Max(float64(
		p.calculateRealInitialDelay()+
			// Wait for the first probe to finish.
			p.TimeoutSeconds+
			// Ignore until we need to perform only the last failure check.
			(p.FailureThreshold+p.SuccessThreshold-3)*p.PeriodSeconds+
			// Ignore for additional half of a period to account for possible delays in
			// events processing.
			p.PeriodSeconds/2+
			// Ignore for timeout of a ReadinessProbe to make sure the last possible ignored
			// probe completed.
			p.TimeoutSeconds,
		// And after all of this the first failed ReadinessProbe should fail the rollout.
	), 0))) * time.Second

	if debug.Debug() {
		fmt.Printf("ignoreFailuresDuration calculated as %q for probe: %+v\n", ignoreFailuresDuration, p)
	}

	return ignoreFailuresDuration
}

// ReadinessProbe initialDelaySeconds counts from the container creation, not
// from startup probe finish. Because of this we have to determine what will
// take longer — startup probe completion or readiness probe initialDelaySeconds
// and make sure we waited from the container creation for as long as the
// biggest time duration of these two.
func (p *ReadinessProbe) calculateRealInitialDelay() int32 {
	var initialDelaySeconds int32
	if p.startupProbe != nil {
		// Maximum possible time between container creation and readiness probe starting.
		readinessProbeInitialDelay := p.PeriodSeconds + p.InitialDelaySeconds

		// Another maximum possible time between container creation and readiness probe
		// starting. Won't respect failed probe breaking chain of successful probes and
		// vice versa.
		startupProbeInitialDelay :=
			// Between container creation and first startup probe there is a delay in the
			// range of 0-"periodSeconds".
			p.startupProbe.PeriodSeconds +
				// Add initialDelaySeconds.
				p.startupProbe.InitialDelaySeconds +
				// Calculate maximum time it might take to iterate over all success/failureThresholds.
				(p.startupProbe.FailureThreshold+p.startupProbe.SuccessThreshold-1)*p.startupProbe.PeriodSeconds +
				// Make sure last possible startup probe finished.
				p.startupProbe.TimeoutSeconds +
				// Add 1 extra periodSeconds to account for delay between last startup probe
				// finished and first readiness probe started.
				p.PeriodSeconds

		if startupProbeInitialDelay > readinessProbeInitialDelay {
			// If startup delay more than readiness delay, then we won't need to wait any
			// extra and just wait for a single periodSeconds (correction for delay between
			// startup probe finished and first readiness probe started).
			initialDelaySeconds = p.PeriodSeconds
		} else {
			// Else wait for the time left for readiness probe to initialize.
			initialDelaySeconds = readinessProbeInitialDelay - startupProbeInitialDelay
		}
	} else {
		initialDelaySeconds = p.PeriodSeconds + p.InitialDelaySeconds
	}
	return initialDelaySeconds
}
