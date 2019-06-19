package utils

import (
	"bytes"
	"fmt"
	"golang.org/x/crypto/ssh/terminal"
	"os"
	"strings"

	"github.com/acarl005/stripansi"

	"github.com/flant/logboek"
)

type Table struct {
	width      int
	extraWidth int

	serviceText     string
	serviceRestText string

	columnsRatio []float64

	columnsCount int

	service struct {
		header           string
		headerRest       string
		row              string
		rowRest          string
		lastRow          string
		lastRowRest      string
		extraRow         string
		extraRowRest     string
		lastExtraRow     string
		lastExtraRowRest string
	}

	buf       *bytes.Buffer
	commitBuf *bytes.Buffer
}

func NewTable(columnsRatio ...float64) Table {
	t := Table{}
	t.buf = bytes.NewBuffer([]byte{})
	t.columnsCount = len(columnsRatio)
	t.columnsRatio = columnsRatio

	return t
}

func (t *Table) SubTable(columnsRatio ...float64) Table {
	st := NewTable(columnsRatio...)
	st.commitBuf = t.buf

	tFirstColumnWidth := t.getColumnsContentWidth(t.columnsCount)[0]
	st.width = tFirstColumnWidth
	st.extraWidth = t.width - tFirstColumnWidth

	st.service.header = "│   "
	st.service.headerRest = "│   "
	st.service.row = "├── "
	st.service.rowRest = "│   "
	st.service.lastRow = "└── "
	st.service.lastRowRest = "    "
	st.service.extraRow = "│   ├── "
	st.service.extraRowRest = "│   │   "
	st.service.lastExtraRow = "│   └── "
	st.service.lastExtraRowRest = "│       "

	return st
}

func (t *Table) SetWidth(width int) {
	t.width = width
}

func (t *Table) Header(columns ...interface{}) {
	t.withService(t.service.header, t.service.headerRest, func() {
		t.apply(columns...)
	})
}

func (t *Table) Rows(rows ...[]interface{}) {
	if len(rows) != 0 {
		t.withService(t.service.row, t.service.rowRest, func() {
			for _, rowColumns := range rows[:len(rows)-1] {
				t.Row(rowColumns...)
			}
		})

		t.withService(t.service.lastRow, t.service.lastRowRest, func() {
			t.Row(rows[len(rows)-1]...)
		})
	}
}

func (t *Table) Row(columns ...interface{}) {
	if t.columnsCount == 0 {
		panic("headers should be rendered first")
	}

	var rowColumns []interface{}
	var extraRowColumns []interface{}

	if len(columns) < t.columnsCount {
		panic(fmt.Sprintf("itemFields items count can not be less than headers (got %d, expected %d)", len(columns), t.columnsCount))
	} else {
		rowColumns = columns[:t.columnsCount]
		if len(columns) > t.columnsCount {
			extraRowColumns = columns[t.columnsCount:]
		}
	}

	t.apply(rowColumns...)

	if len(extraRowColumns) != 0 {
		t.withService(t.service.extraRow, t.service.extraRowRest, func() {
			for _, column := range extraRowColumns[:len(extraRowColumns)-1] {
				t.apply(column)
			}
		})

		t.withService(t.service.lastExtraRow, t.service.lastExtraRowRest, func() {
			t.apply(extraRowColumns[len(extraRowColumns)-1])
		})
	}
}

func (t *Table) withService(serviceText, serviceRestText string, f func()) {
	savedServiceText := t.serviceText
	savedServiceRestText := t.serviceRestText

	t.serviceText = serviceText
	t.serviceRestText = serviceRestText
	f()
	t.serviceText = savedServiceText
	t.serviceRestText = savedServiceRestText
}

func (t *Table) apply(columns ...interface{}) {
	columnsContentWidth := t.getColumnsContentWidth(len(columns))

	rowsCount := 0
	columnsContent := make([][]string, len(columnsContentWidth))
	for ind, field := range columns {
		columnsContent[ind] = fitValue(field, columnsContentWidth[ind])

		if len(columnsContent[ind]) > rowsCount {
			rowsCount = len(columnsContent[ind])
		}
	}

	for rowNumber := 0; rowNumber < rowsCount; rowNumber++ {
		var row []string

		if rowNumber == 0 {
			row = append(row, t.serviceText)
		} else {
			row = append(row, t.serviceRestText)
		}

		for ind, columnLines := range columnsContent {
			var columnRowValue string
			if len(columnLines) > rowNumber {
				columnRowValue = columnLines[rowNumber]
			} else {
				columnRowValue = padValue("", columnsContentWidth[ind])
			}

			row = append(row, columnRowValue)
		}

		t.buf.WriteString(strings.Join(row, ""))
		t.buf.WriteString("\n")
	}
}

func fitValue(field interface{}, columnWidth int) []string {
	var lines []string

	columnWidthWithoutSpaces := columnWidth - 1
	value := fmt.Sprintf("%v", field)
	result := logboek.FitText(value, logboek.FitTextOptions{Width: columnWidthWithoutSpaces})

	for _, line := range strings.Split(result, "\n") {
		lines = append(lines, padValue(line, columnWidth))
	}

	return lines
}

func padValue(s string, n int) string {
	rest := n - len([]rune(stripansi.Strip(s)))
	if rest < 0 {
		return s
	}

	return s + strings.Repeat(" ", rest)
}

func (t *Table) getColumnsContentWidth(count int) []int {
	var result []int

	var sum int
	w := t.getWidth() - len([]rune(t.serviceText))

	if count == 1 {
		return []int{w}
	}

	for i := 0; i < count; i++ {
		columnWidth := int(float64(w) * t.columnsRatio[i])
		result = append(result, columnWidth)
		sum += columnWidth
	}

	if w-sum > 0 {
		result[len(result)-1] += w - sum
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

func (t *Table) Commit(extraColumns ...interface{}) {
	lines := strings.Split(t.buf.String(), "\n")

	var extraLines []string
	for _, extraColumn := range extraColumns {
		extraLines = append(extraLines, fitValue(extraColumn, t.extraWidth)...)
	}

	var baseLines []string
	if len(lines) > len(extraLines) {
		baseLines = lines
	} else {
		baseLines = extraLines
	}

	var resultLines []string
	for ind := range baseLines {
		var line, extraLine string

		if ind < len(lines) {
			line = lines[ind]
		}

		if ind < len(extraLines) {
			extraLine = extraLines[ind]
		}

		var resultLine string
		if line != "" && extraLine != "" {
			resultLine += line + extraLine
		} else if extraLine != "" {
			resultLine += padValue("", t.width) + extraLine
		} else {
			resultLine += line + padValue("", t.width-len([]rune(line)))
		}

		resultLines = append(resultLines, resultLine)
	}

	t.commitBuf.WriteString(strings.Join(resultLines, "\n") + "\n")
}

func (t *Table) Render() string {
	return t.buf.String()
}
