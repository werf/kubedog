package display

import (
	"fmt"
	"os"
	"sync"
)

var (
	mutex            = &sync.Mutex{}
	currentLogHeader = ""
)

type LogLine struct {
	Timestamp string
	Message   string
}

func SetLogHeader(logHeader string) {
	mutex.Lock()
	defer mutex.Unlock()

	if currentLogHeader != logHeader {
		if currentLogHeader != "" {
			fmt.Println()
		}
		fmt.Printf(">> %s\n", logHeader)
		currentLogHeader = logHeader
	}
}

func OutputLogLines(header string, logLines []LogLine) {
	if inline() {
		for _, line := range logLines {
			fmt.Printf(">> %s: %s\n", header, line.Message)
		}
	} else {
		SetLogHeader(header)
		for _, line := range logLines {
			fmt.Println(line.Message)
		}
	}
}

func inline() bool {
	return os.Getenv("KUBEDOG_LOG_INLINE") == "1"
}
