package log

import (
	"fmt"
	"sync"
)

var (
	mutex            = &sync.Mutex{}
	currentLogHeader = ""
)

func SetLogHeader(logHeader string) {
	mutex.Lock()
	defer mutex.Unlock()

	if currentLogHeader != logHeader {
		if currentLogHeader != "" {
			fmt.Println()
		}
		fmt.Printf("%s\n", logHeader)
		currentLogHeader = logHeader
	}
}
