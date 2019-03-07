package display

import (
	"fmt"
	"io"
	"os"
	"sync"
)

var (
	Out io.Writer = os.Stdout
	Err io.Writer = os.Stderr

	mutex            = &sync.Mutex{}
	currentLogHeader = ""
)

func SetOut(out io.Writer) {
	Out = out
}

func SetErr(err io.Writer) {
	Err = err
}

type LogLine struct {
	Timestamp string
	Message   string
}

func SetLogHeader(logHeader string) {
	mutex.Lock()
	defer mutex.Unlock()

	if currentLogHeader != logHeader {
		if currentLogHeader != "" {
			fmt.Fprintln(Out)
		}
		fmt.Fprintf(Out, ">> %s\n", logHeader)
		currentLogHeader = logHeader
	}
}

func OutputLogLines(header string, logLines []LogLine) {
	if inline() {
		for _, line := range logLines {
			fmt.Fprintf(Out, ">> %s: %s\n", header, line.Message)
		}
	} else {
		SetLogHeader(header)
		for _, line := range logLines {
			fmt.Fprintln(Out, line.Message)
		}
	}
}

func inline() bool {
	return os.Getenv("KUBEDOG_LOG_INLINE") == "1"
}
