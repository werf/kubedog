package main

import (
	"bytes"
	"fmt"
	"golang.org/x/crypto/ssh/terminal"
	"os"
	"strings"
)

type Table struct {
	width            int
	firstColumnWidth int
	headersCount     int

	lastAppliedFields []interface{}

	buf *bytes.Buffer

	border Border
}

type Border struct {
	rightBottom     rune
	rightTop        rune
	leftTop         rune
	leftBottom      rune
	vertical        rune
	horizontal      rune
	leftRightTop    rune
	leftRightBottom rune
	leftTopBottom   rune
	rightTopBottom  rune
	cross           rune
}

func defaultBorder() Border {
	return Border{'┌', '└', '┘', '┐', '│', '─', '┴', '┬', '┤', '├', '┼'}
}

func NewTable() Table {
	t := Table{}
	t.buf = bytes.NewBuffer([]byte{})
	t.border = defaultBorder()

	return t
}

func (t *Table) WithCustomBorder(f func()) {
	savedBorder := t.border

	t.border = defaultBorder()
	t.border.horizontal = '-'
	f()
	t.border = savedBorder
}

func (t *Table) SetWidth(width int) {
	t.width = width
}

func (t *Table) SetFirstColumnWidth(size int) {
	t.firstColumnWidth = size
}

func (t *Table) Headers(headers ...interface{}) {
	t.headersCount = len(headers)
	t.Apply(headers...)
}

func (t *Table) Item(fields ...interface{}) {
	if t.headersCount == 0 {
		panic("headers should be rendered first")
	}

	var itemBaseFields []interface{}
	var itemExtraFields []interface{}

	if len(fields) < t.headersCount {
		panic(fmt.Sprintf("item fields count can not be less than headers (got %d, expected %d)", len(fields), t.headersCount))
	} else {
		itemBaseFields = fields[:t.headersCount]
		if len(fields) > t.headersCount {
			itemExtraFields = fields[t.headersCount:]
		}
	}

	t.Apply(itemBaseFields...)
	t.ApplySub(itemExtraFields...)
}

func (t *Table) Apply(fields ...interface{}) {
	t.ApplyLine(false, fields...)
	t.ApplyData(fields...)
}

func (t *Table) ApplySub(fields ...interface{}) {
	for _, field := range fields {
		t.ApplyLine(true, "", field)
		t.ApplySubData(field)
	}
}

func (t *Table) ApplyLine(isApplySub bool, fields ...interface{}) {
	var leftBorder, rightBorder rune

	isStartedLine := len(t.lastAppliedFields) == 0
	isFinishLine := len(fields) == 0

	if isStartedLine || isFinishLine || len(t.lastAppliedFields) == len(fields) {
		var columnsSeparatorBorder rune
		var targetFields []interface{}

		if isStartedLine {
			leftBorder = t.border.rightBottom
			rightBorder = t.border.leftBottom
			columnsSeparatorBorder = t.border.leftRightBottom
			targetFields = fields
		} else if isFinishLine {
			leftBorder = t.border.rightTop
			rightBorder = t.border.leftTop
			columnsSeparatorBorder = t.border.leftRightTop
			targetFields = t.lastAppliedFields
		} else {
			leftBorder = t.border.rightTopBottom
			rightBorder = t.border.leftTopBottom
			columnsSeparatorBorder = t.border.cross
			targetFields = fields
		}

		if isApplySub {
			leftBorder = t.border.vertical
		}

		t.buf.WriteRune(leftBorder)

		columnsContentWidth := t.CalculateColumnsContentWidth(len(targetFields))
		columnsBorder := make([]string, len(columnsContentWidth))
		for ind, columnContentWidth := range columnsContentWidth {
			lineBorder := t.border.horizontal
			if ind == 0 && isApplySub {
				lineBorder = ' '
			}

			columnsBorder[ind] = strings.Repeat(string(lineBorder), columnContentWidth)
		}

		if isApplySub {
			t.buf.WriteString(columnsBorder[0] + string(t.border.rightTopBottom))
			t.buf.WriteString(strings.Join(columnsBorder[1:], string(columnsSeparatorBorder)))
		} else {
			t.buf.WriteString(strings.Join(columnsBorder, string(columnsSeparatorBorder)))
		}

		t.buf.WriteRune(rightBorder)
		t.buf.WriteString("\n")
	} else {
		leftBorder = t.border.rightTopBottom
		rightBorder = t.border.leftTopBottom

		if isApplySub {
			leftBorder = t.border.vertical
		}

		var moreFieldsThanWas bool
		var crossColumnsCount int
		var columnsCount int
		if len(t.lastAppliedFields) > len(fields) {
			columnsCount = len(t.lastAppliedFields)
			crossColumnsCount = len(fields) - 1
			moreFieldsThanWas = false
		} else {
			columnsCount = len(fields)
			crossColumnsCount = len(t.lastAppliedFields) - 1
			moreFieldsThanWas = true
		}

		columnsSeparatorBorder := make([]rune, columnsCount)
		for ind, _ := range columnsSeparatorBorder[:len(columnsSeparatorBorder)-1] {
			if ind == 0 && isApplySub {
				columnsSeparatorBorder[ind] = t.border.rightTopBottom
			} else if ind < crossColumnsCount {
				columnsSeparatorBorder[ind] = t.border.cross
			} else if moreFieldsThanWas {
				columnsSeparatorBorder[ind] = t.border.leftRightBottom
			} else {
				columnsSeparatorBorder[ind] = t.border.leftRightTop
			}
		}

		columnsSeparatorBorder[len(columnsSeparatorBorder)-1] = rightBorder

		t.buf.WriteRune(leftBorder)

		columnsContentWidth := t.CalculateColumnsContentWidth(columnsCount)
		for ind, columnContentWidth := range columnsContentWidth[:len(columnsContentWidth)-1] {
			separatorBorder := t.border.horizontal
			if ind == 0 && isApplySub {
				separatorBorder = ' '
			}

			t.buf.WriteString(strings.Repeat(string(separatorBorder), columnContentWidth))
			t.buf.WriteString(string(columnsSeparatorBorder[ind]))
		}
		t.buf.WriteString(strings.Repeat(string(t.border.horizontal), columnsContentWidth[len(columnsContentWidth)-1]))
		t.buf.WriteRune(rightBorder)
		t.buf.WriteString("\n")
	}
}

func (t *Table) ApplyData(fields ...interface{}) {
	leftBorder := t.border.vertical
	rightBorder := t.border.vertical
	separatorBorder := t.border.vertical

	columnsContentWidth := t.CalculateColumnsContentWidth(len(fields))

	rawsCount := 0
	columnsContent := make([][]string, len(columnsContentWidth))
	for ind, field := range fields {
		value := fmt.Sprintf("%v", field)

		columnsContent[ind] = []string{}
		columnWidthWithoutSpaces := columnsContentWidth[ind] - 2 // wrap by one space
		if len(value) > columnWidthWithoutSpaces {
			for i := 0; i <= len(value)/columnWidthWithoutSpaces; i++ {
				var slice string
				if len(value[i*columnWidthWithoutSpaces:]) > columnWidthWithoutSpaces {
					slice = value[i*columnWidthWithoutSpaces : (i+1)*columnWidthWithoutSpaces]
				} else if len(value[i*columnWidthWithoutSpaces:]) != 0 {
					slice = value[i*columnWidthWithoutSpaces:]
				} else {
					continue
				}

				columnsContent[ind] = append(columnsContent[ind], formatCellContent(slice, columnsContentWidth[ind]))
			}
		} else {
			columnsContent[ind] = append(columnsContent[ind], formatCellContent(value, columnsContentWidth[ind]))
		}

		if len(columnsContent[ind]) > rawsCount {
			rawsCount = len(columnsContent[ind])
		}
	}

	for rawNumber := 0; rawNumber < rawsCount; rawNumber++ {
		var raw []string
		for ind, columnLines := range columnsContent {
			var columnRawValue string
			if len(columnLines) > rawNumber {
				columnRawValue = columnLines[rawNumber]
			} else {
				columnRawValue = strings.Repeat(" ", columnsContentWidth[ind])
			}

			raw = append(raw, columnRawValue)
		}

		t.buf.WriteRune(leftBorder)
		t.buf.WriteString(strings.Join(raw, string(separatorBorder)))
		t.buf.WriteRune(rightBorder)
		t.buf.WriteString("\n")
	}

	t.lastAppliedFields = fields
}

func (t *Table) ApplySubData(field interface{}) {
	t.ApplyData("", field)
}

func formatCellContent(s string, n int) string {
	rest := n - len(s)
	leftDiv := 1
	rightDiv := rest - 1

	return strings.Repeat(" ", leftDiv) + s + strings.Repeat(" ", rightDiv)
}

func (t *Table) CalculateColumnsContentWidth(count int) []int {
	var result []int

	leftBorder := 1
	rightBorder := 1

	rest := t.getWidth() - leftBorder - rightBorder

	if count <= 1 {
		result = append(result, rest)
	} else {
		result = append(result, t.firstColumnWidth)
		rest -= t.firstColumnWidth + count - 1
		sum := 0
		x := rest / (count - 1)

		for i := 0; i < count-1; i++ {
			result = append(result, x)
			sum += x
		}

		result[len(result)-1] += rest - sum
	}

	return result
}

func (t *Table) getWidth() int {
	if t.width != 0 {
		return t.width
	}

	defaultWidth := 140
	minWidth := 60

	tw := terminalWidth()
	if tw == 0 {
		return defaultWidth
	} else if tw < minWidth {
		return minWidth
	} else {
		return tw
	}
}

func terminalWidth() int {
	if terminal.IsTerminal(int(os.Stdout.Fd())) {
		w, _, err := terminal.GetSize(int(os.Stdout.Fd()))
		if err != nil {
			panic(fmt.Sprintf("get terminal size failed: %s", err))
		}

		return w
	}

	return 0
}

func (t *Table) Render() {
	t.Apply()
	fmt.Printf(t.buf.String())
}

func main() {
	t := NewTable()
	t.SetFirstColumnWidth(40)
	//t.SetWidth(100)
	t.Headers("NAME", "DESIRED", "CURRENT", "UP-TO-DATE", "AVAILABLE")
	t.Item("deployment.apps/tiller-deploy", 1, 1, 1, 1, "See the server log for details. BUILD FAILED (total time: 1 second)", "An individual language user's deviations from standard language norms in grammar, pronunciation and orthography are sometimes referred to as errors")
	t.WithCustomBorder(func() {
		t.Headers("PODS")
		t.Headers("NAME", "READY", "STATUS", "RESTARTS", "AGE")
		t.Item("pod/coredns-fb8b8dccf-nmr6t", "0/1", "CreateContainerError", "0", "55d", "An individual language user's deviations from standard language norms in grammar, pronunciation and orthography are sometimes referred to as errors", "ERRRO2")
		t.Item("pod/coredns-fb8b8dccf-vchzj", "1/1", "Pulling", "0", "31d")
	})
	t.Item("deployment.apps/coredns", 1, 1, 1, 1)
	t.Render()
}
