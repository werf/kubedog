package indicators

import (
	"fmt"

	"github.com/fatih/color"
)

type FormatTableElemOptions struct {
	ShowProgress bool

	// disables yellow and red colors for resources with fail-mode=IgnoreAndContinueDeployProcess
	DisableWarningColors bool
}

type StringEqualConditionIndicator struct {
	Value       string
	TargetValue string
}

func (indicator *StringEqualConditionIndicator) IsProgressing(prevIndicator *StringEqualConditionIndicator) bool {
	return (prevIndicator != nil) && (indicator.Value != prevIndicator.Value)
}

func (indicator *StringEqualConditionIndicator) IsReady() bool {
	return (indicator.Value == indicator.TargetValue)
}

func (indicator *StringEqualConditionIndicator) FormatTableElem(prevIndicator *StringEqualConditionIndicator, opts FormatTableElemOptions) string {
	res := ""

	if opts.ShowProgress && indicator.IsProgressing(prevIndicator) {
		if opts.DisableWarningColors {
			res += prevIndicator.Value
		} else {
			res += color.New(color.FgYellow).Sprintf("%s", prevIndicator.Value)
		}
		res += "=>"
	}

	if indicator.IsReady() {
		res += color.New(color.FgGreen).Sprintf("%s", indicator.Value)
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

func (indicator *Int32EqualConditionIndicator) IsProgressing(prevIndicator *Int32EqualConditionIndicator) bool {
	return (prevIndicator != nil) && (indicator.Value != prevIndicator.Value)
}

func (indicator *Int32EqualConditionIndicator) IsReady() bool {
	return (indicator.Value == indicator.TargetValue)
}

func (indicator *Int32EqualConditionIndicator) FormatTableElem(prevIndicator *Int32EqualConditionIndicator, opts FormatTableElemOptions) string {
	res := ""

	if opts.ShowProgress && indicator.IsProgressing(prevIndicator) {
		if opts.DisableWarningColors {
			res += fmt.Sprintf("%d/%d", prevIndicator.Value, prevIndicator.TargetValue)
		} else {
			res += color.New(color.FgYellow).Sprintf("%d/%d", prevIndicator.Value, prevIndicator.TargetValue)
		}
		res += "=>"
	}

	if indicator.IsReady() {
		res += color.New(color.FgGreen).Sprintf("%d/%d", indicator.Value, indicator.TargetValue)
	} else {
		if opts.DisableWarningColors {
			res += fmt.Sprintf("%d/%d", indicator.Value, indicator.TargetValue)
		} else {
			res += color.New(color.FgYellow).Sprintf("%d/%d", indicator.Value, indicator.TargetValue)
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
