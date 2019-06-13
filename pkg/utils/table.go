package utils

import (
	"bytes"
	"fmt"
	"github.com/acarl005/stripansi"
	"github.com/flant/logboek"
	"golang.org/x/crypto/ssh/terminal"
	"os"
	"strings"
)

type Table struct {
	width int

	serviceText     string
	serviceRestText string

	columnsRatio []float64

	columnsCount int

	service struct {
		header           string
		headerRest       string
		raw              string
		rawRest          string
		lastRaw          string
		lastRawRest      string
		extraRaw         string
		extraRawRest     string
		lastExtraRaw     string
		lastExtraRawRest string
	}

	buf *bytes.Buffer
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
	st.buf = t.buf

	st.SetWidth(t.getColumnsContentWidth(t.columnsCount)[0])

	st.service.header = "│   "
	st.service.headerRest = "│   "
	st.service.raw = "├── "
	st.service.rawRest = "│   "
	st.service.lastRaw = "└── "
	st.service.lastRawRest = "    "
	st.service.extraRaw = "│   ├── "
	st.service.extraRawRest = "│   │   "
	st.service.lastExtraRaw = "│   └── "
	st.service.lastExtraRawRest = "│       "

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

func (t *Table) Raws(raws ...[]interface{}) {
	if len(raws) != 0 {
		t.withService(t.service.raw, t.service.rawRest, func() {
			for _, rawColumns := range raws[:len(raws)-1] {
				t.Raw(rawColumns...)
			}
		})

		t.withService(t.service.lastRaw, t.service.lastRawRest, func() {
			t.Raw(raws[len(raws)-1]...)
		})
	}
}

func (t *Table) Raw(columns ...interface{}) {
	if t.columnsCount == 0 {
		panic("headers should be rendered first")
	}

	var rawColumns []interface{}
	var extraRawColumns []interface{}

	if len(columns) < t.columnsCount {
		panic(fmt.Sprintf("itemFields items count can not be less than headers (got %d, expected %d)", len(columns), t.columnsCount))
	} else {
		rawColumns = columns[:t.columnsCount]
		if len(columns) > t.columnsCount {
			extraRawColumns = columns[t.columnsCount:]
		}
	}

	t.apply(rawColumns...)

	if len(extraRawColumns) != 0 {
		t.withService(t.service.extraRaw, t.service.extraRawRest, func() {
			for _, column := range extraRawColumns[:len(extraRawColumns)-1] {
				t.apply(column)
			}
		})

		t.withService(t.service.lastExtraRaw, t.service.lastExtraRawRest, func() {
			t.apply(extraRawColumns[len(extraRawColumns)-1])
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

	rawsCount := 0
	columnsContent := make([][]string, len(columnsContentWidth))
	for ind, field := range columns {
		value := fmt.Sprintf("%v", field)

		columnsContent[ind] = []string{}
		columnWidthWithoutSpaces := columnsContentWidth[ind] - 1

		result := logboek.FitText(value, logboek.FitTextOptions{Width: columnWidthWithoutSpaces})
		lines := strings.Split(result, "\n")

		for _, line := range lines {
			columnsContent[ind] = append(columnsContent[ind], formatCellContent(line, columnsContentWidth[ind]))
		}

		if len(columnsContent[ind]) > rawsCount {
			rawsCount = len(columnsContent[ind])
		}
	}

	for rawNumber := 0; rawNumber < rawsCount; rawNumber++ {
		var raw []string

		if rawNumber == 0 {
			raw = append(raw, t.serviceText)
		} else {
			raw = append(raw, t.serviceRestText)
		}

		for ind, columnLines := range columnsContent {
			var columnRawValue string
			if len(columnLines) > rawNumber {
				columnRawValue = columnLines[rawNumber]
			} else {
				columnRawValue = strings.Repeat(" ", columnsContentWidth[ind])
			}

			raw = append(raw, columnRawValue)
		}

		t.buf.WriteString(strings.Join(raw, ""))
		t.buf.WriteString("\n")
	}
}

func formatCellContent(s string, n int) string {
	rest := n - len(stripansi.Strip(s))
	rightDiv := rest

	return s + strings.Repeat(" ", rightDiv)
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

	if sum-w > 0 {
		result[len(result)-1] += sum - w
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
	t.apply("")
	fmt.Printf(t.buf.String())
}
