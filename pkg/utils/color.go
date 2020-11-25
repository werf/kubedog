package utils

import (
	"github.com/fatih/color"

	"github.com/werf/logboek"
	"github.com/werf/logboek/pkg/style"
)

func BoldString(format string, a ...interface{}) string {
	return colorString(colorStyle(color.Bold), format, a...)
}

func BlueString(format string, a ...interface{}) string {
	return colorString(colorStyle(color.FgBlue), format, a...)
}

func YellowString(format string, a ...interface{}) string {
	return colorString(colorStyle(color.FgYellow), format, a...)
}

func GreenString(format string, a ...interface{}) string {
	return colorString(colorStyle(color.FgGreen), format, a...)
}

func RedString(format string, a ...interface{}) string {
	return colorString(colorStyle(color.FgRed), format, a...)
}

func colorStyle(attrs ...color.Attribute) *style.Style {
	return &style.Style{Attributes: attrs}
}

func colorString(style *style.Style, format string, a ...interface{}) string {
	return logboek.Colorize(style, format, a...)
}
