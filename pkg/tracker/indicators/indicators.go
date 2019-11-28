package indicators

import (
	"fmt"

	"github.com/fatih/color"
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
}

func (indicator *StringEqualConditionIndicator) IsProgressing(prevIndicator *StringEqualConditionIndicator) bool {
	return (prevIndicator != nil) && (indicator.Value != prevIndicator.Value)
}

func (indicator *StringEqualConditionIndicator) IsReady() bool {
	return (indicator.Value == indicator.TargetValue)
}

func (indicator *StringEqualConditionIndicator) IsFailed() bool {
	return (indicator.Value == indicator.FailedValue)
}

func (indicator *StringEqualConditionIndicator) FormatTableElem(prevIndicator *StringEqualConditionIndicator, opts FormatTableElemOptions) string {
	res := ""

	if opts.ShowProgress && indicator.IsProgressing(prevIndicator) {
		if !opts.IsResourceNew || opts.DisableWarningColors {
			res += prevIndicator.Value
		} else {
			res += color.New(color.FgYellow).Sprintf("%s", prevIndicator.Value)
		}
		res += " -> "
	}

	if !opts.IsResourceNew {
		res += indicator.Value
	} else if indicator.IsReady() {
		res += color.New(color.FgGreen).Sprintf("%s", indicator.Value)
	} else if indicator.IsFailed() {
		if opts.DisableWarningColors {
			res += indicator.Value
		} else {
			res += color.New(color.FgRed).Sprintf("%s", indicator.Value)
		}
	} else {
		if opts.DisableWarningColors {
			res += indicator.Value
		} else {
			res += color.New(color.FgYellow).Sprintf("%s", indicator.Value)
		}
	}

	return res
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
	return (indicator.Value == indicator.TargetValue)
}

func (indicator *Int32EqualConditionIndicator) FormatTableElem(prevIndicator *Int32EqualConditionIndicator, opts FormatTableElemOptions) string {
	res := ""

	if opts.ShowProgress && indicator.IsProgressing(prevIndicator) {
		if prevIndicator.IsReady() {
			res += color.New(color.FgGreen).Sprintf("%d", prevIndicator.Value)
		} else {
			if opts.DisableWarningColors {
				res += fmt.Sprintf("%d", prevIndicator.Value)
			} else {
				res += color.New(color.FgYellow).Sprintf("%d", prevIndicator.Value)
			}
		}
		res += "->"
	}

	if indicator.IsReady() {
		res += color.New(color.FgGreen).Sprintf("%s", indicator.formatValue(opts.WithTargetValue))
	} else {
		if opts.DisableWarningColors {
			res += indicator.formatValue(opts.WithTargetValue)
		} else {
			res += color.New(color.FgYellow).Sprintf("%s", indicator.formatValue(opts.WithTargetValue))
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
	return (indicator.Value >= indicator.TargetValue)
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
			res += color.New(color.FgGreen).Sprintf("%d", prevIndicator.Value)
		} else {
			if opts.DisableWarningColors {
				res += fmt.Sprintf("%d", prevIndicator.Value)
			} else {
				res += color.New(color.FgYellow).Sprintf("%d", prevIndicator.Value)
			}
		}
		res += "->"
	}

	if indicator.IsReady() {
		res += color.New(color.FgGreen).Sprintf("%s", indicator.formatValue(opts.WithTargetValue))
	} else {
		if opts.DisableWarningColors {
			res += indicator.formatValue(opts.WithTargetValue)
		} else {
			res += color.New(color.FgYellow).Sprintf("%s", indicator.formatValue(opts.WithTargetValue))
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
			res += color.New(color.FgYellow).Sprintf("%d", prevIndicator.Value)
		}
		res += "->"
	}

	if indicator.IsReady() {
		res += color.New(color.FgGreen).Sprintf("%d", indicator.Value)
	} else {
		if opts.DisableWarningColors {
			res += fmt.Sprintf("%d", indicator.Value)
		} else {
			res += color.New(color.FgYellow).Sprintf("%d", indicator.Value)
		}
	}

	return res
}
