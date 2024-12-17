package indicators

import (
	"fmt"

	"github.com/werf/kubedog-for-werf-helm/pkg/utils"
)

type FormatTableElemOptions struct {
	// Show previous value as progressing OLD_VALUE->NEW_VALUE if the value has changed
	ShowProgress bool

	// Disable yellow and red colors for resources with fail-mode=IgnoreAndContinueDeployProcess
	DisableWarningColors bool

	// Show target value in format VALUE/TARGET, show only VALUE by default
	WithTargetValue bool

	// Do not use colors for old resources. Old resource is the resource
	// related to old ReplicaSets of Deployment for example
	IsResourceNew bool
}

type StringEqualConditionIndicator struct {
	Value       string
	TargetValue string
	FailedValue string

	forceReady  *bool
	forceFailed *bool
}

func (indicator *StringEqualConditionIndicator) IsProgressing(prevIndicator *StringEqualConditionIndicator) bool {
	return (prevIndicator != nil) && (indicator.Value != prevIndicator.Value)
}

func (indicator *StringEqualConditionIndicator) SetReady(ready bool) {
	indicator.forceReady = &ready
}

func (indicator *StringEqualConditionIndicator) IsReady() bool {
	if indicator.forceReady != nil {
		return *indicator.forceReady
	}

	return indicator.Value == indicator.TargetValue
}

func (indicator *StringEqualConditionIndicator) SetFailed(failed bool) {
	indicator.forceFailed = &failed
}

func (indicator *StringEqualConditionIndicator) IsFailed() bool {
	if indicator.forceFailed != nil {
		return *indicator.forceFailed
	}

	return indicator.Value == indicator.FailedValue
}

func (indicator *StringEqualConditionIndicator) FormatTableElem(prevIndicator *StringEqualConditionIndicator, opts FormatTableElemOptions) string {
	res := ""

	if opts.ShowProgress && indicator.IsProgressing(prevIndicator) {
		if !opts.IsResourceNew || opts.DisableWarningColors {
			res += prevIndicator.Value
		} else {
			res += utils.YellowF("%s", prevIndicator.Value)
		}
		res += "->"
	}

	switch {
	case !opts.IsResourceNew:
		res += indicator.formatValue(opts.WithTargetValue)
	case indicator.IsReady():
		res += utils.GreenF("%s", indicator.formatValue(opts.WithTargetValue))
	case indicator.IsFailed():
		if opts.DisableWarningColors {
			res += indicator.formatValue(opts.WithTargetValue)
		} else {
			res += utils.RedF("%s", indicator.formatValue(opts.WithTargetValue))
		}
	default:
		if opts.DisableWarningColors {
			res += indicator.formatValue(opts.WithTargetValue)
		} else {
			res += utils.YellowF("%s", indicator.formatValue(opts.WithTargetValue))
		}
	}

	return res
}

func (indicator *StringEqualConditionIndicator) formatValue(withTargetValue bool) string {
	if withTargetValue {
		return fmt.Sprintf("%s (%s)", indicator.Value, indicator.TargetValue)
	} else {
		return fmt.Sprintf("%s", indicator.Value)
	}
}

type Int32EqualConditionIndicator struct {
	Value       int32
	TargetValue int32
}

func (indicator *Int32EqualConditionIndicator) formatValue(withTargetValue bool) string {
	if withTargetValue {
		return fmt.Sprintf("%d/%d", indicator.Value, indicator.TargetValue)
	} else {
		return fmt.Sprintf("%d", indicator.Value)
	}
}

func (indicator *Int32EqualConditionIndicator) IsProgressing(prevIndicator *Int32EqualConditionIndicator) bool {
	return (prevIndicator != nil) && (indicator.Value != prevIndicator.Value)
}

func (indicator *Int32EqualConditionIndicator) IsReady() bool {
	return indicator.Value == indicator.TargetValue
}

func (indicator *Int32EqualConditionIndicator) FormatTableElem(prevIndicator *Int32EqualConditionIndicator, opts FormatTableElemOptions) string {
	res := ""

	if opts.ShowProgress && indicator.IsProgressing(prevIndicator) {
		if prevIndicator.IsReady() {
			res += utils.GreenF("%d", prevIndicator.Value)
		} else {
			if opts.DisableWarningColors {
				res += fmt.Sprintf("%d", prevIndicator.Value)
			} else {
				res += utils.YellowF("%d", prevIndicator.Value)
			}
		}
		res += "->"
	}

	if indicator.IsReady() {
		res += utils.GreenF("%s", indicator.formatValue(opts.WithTargetValue))
	} else {
		if opts.DisableWarningColors {
			res += indicator.formatValue(opts.WithTargetValue)
		} else {
			res += utils.YellowF("%s", indicator.formatValue(opts.WithTargetValue))
		}
	}

	return res
}

type Int64GreaterOrEqualConditionIndicator struct {
	Value       int64
	TargetValue int64
}

func (indicator *Int64GreaterOrEqualConditionIndicator) IsProgressing(prevIndicator *Int64GreaterOrEqualConditionIndicator) bool {
	return (prevIndicator != nil) && (indicator.Value != prevIndicator.Value)
}

func (indicator *Int64GreaterOrEqualConditionIndicator) IsReady() bool {
	return indicator.Value >= indicator.TargetValue
}

func (indicator *Int64GreaterOrEqualConditionIndicator) formatValue(withTargetValue bool) string {
	if withTargetValue {
		return fmt.Sprintf("%d/%d", indicator.Value, indicator.TargetValue)
	} else {
		return fmt.Sprintf("%d", indicator.Value)
	}
}

func (indicator *Int64GreaterOrEqualConditionIndicator) FormatTableElem(prevIndicator *Int64GreaterOrEqualConditionIndicator, opts FormatTableElemOptions) string {
	res := ""

	if opts.ShowProgress && indicator.IsProgressing(prevIndicator) {
		if prevIndicator.IsReady() {
			res += utils.GreenF("%d", prevIndicator.Value)
		} else {
			if opts.DisableWarningColors {
				res += fmt.Sprintf("%d", prevIndicator.Value)
			} else {
				res += utils.YellowF("%d", prevIndicator.Value)
			}
		}
		res += "->"
	}

	if indicator.IsReady() {
		res += utils.GreenF("%s", indicator.formatValue(opts.WithTargetValue))
	} else {
		if opts.DisableWarningColors {
			res += indicator.formatValue(opts.WithTargetValue)
		} else {
			res += utils.YellowF("%s", indicator.formatValue(opts.WithTargetValue))
		}
	}

	return res
}

type Int32MultipleEqualConditionIndicator struct {
	Value        int32
	TargetValues []int32
}

func (indicator *Int32MultipleEqualConditionIndicator) IsReady() bool {
	for _, val := range indicator.TargetValues {
		if val == indicator.Value {
			return true
		}
	}
	return false
}

func (indicator *Int32MultipleEqualConditionIndicator) IsProgressing(prevIndicator *Int32MultipleEqualConditionIndicator) bool {
	return (prevIndicator != nil) && (indicator.Value != prevIndicator.Value)
}

func (indicator *Int32MultipleEqualConditionIndicator) FormatTableElem(prevIndicator *Int32MultipleEqualConditionIndicator, opts FormatTableElemOptions) string {
	res := ""

	if opts.ShowProgress && indicator.IsProgressing(prevIndicator) {
		if opts.DisableWarningColors {
			res += fmt.Sprintf("%d", prevIndicator.Value)
		} else {
			res += utils.YellowF("%d", prevIndicator.Value)
		}
		res += "->"
	}

	if indicator.IsReady() {
		res += utils.GreenF("%d", indicator.Value)
	} else {
		if opts.DisableWarningColors {
			res += fmt.Sprintf("%d", indicator.Value)
		} else {
			res += utils.YellowF("%d", indicator.Value)
		}
	}

	return res
}
