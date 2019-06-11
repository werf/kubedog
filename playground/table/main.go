package main

import (
	"bytes"
	"fmt"
	"golang.org/x/crypto/ssh/terminal"
	"os"
)

type Table struct {
	firstColumnWidth int
	headersCount     int

	buf *bytes.Buffer
}

func NewTable() Table {
	t := Table{}
	t.buf = bytes.NewBuffer([]byte{})
	return t
}

func (t *Table) SetFirstColumnWidth(size int) {
	t.firstColumnWidth = size
}

func (t *Table) Headers(headers ...interface{}) {
	t.headersCount = len(headers)
	t.buf.WriteString(fmt.Sprintf("%+v\n", headers))
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

	t.buf.WriteString(fmt.Sprintf("%+v\n", itemBaseFields))

	if len(itemExtraFields) != 0 {
		t.buf.WriteString(fmt.Sprintf("%+v\n", itemExtraFields))
	}
}

func (t *Table) SubTable() Table {
	return Table{
		firstColumnWidth: t.firstColumnWidth,
		buf:              t.buf,
	}
}

func (t *Table) Render() {
	fmt.Println(t.buf.String())
}

func main() {
	t := NewTable()
	t.SetFirstColumnWidth(20)
	t.Headers("NAME", "DESIRED", "CURRENT", "UP-TO-DATE", "AVAILABLE")
	t.Item("deploy/name1", 1, 1, 1, 1, "ERRO1", "ERROR2")
	st := t.SubTable()
	st.Headers("PODS")
	st.Headers("NAME", "READY", "STATUS", "RESTARTS", "AGE")
	st.Item("pod/name1", "3/3", "Pulling", "0", "31 minutes", "ERROR1", "ERRRO2")
	st.Item("pod/name2", "3/3", "Pulling", "0", "31 minutes")
	t.Item("deploy/name2", 1, 1, 1, 1)
	t.Render()
}

func TerminalWidth() int {
	if terminal.IsTerminal(int(os.Stdout.Fd())) {
		w, _, err := terminal.GetSize(int(os.Stdout.Fd()))
		if err != nil {
			panic(fmt.Sprintf("get terminal size failed: %s", err))
		}

		return w
	} else {
		return 120
	}
}
