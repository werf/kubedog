package log

import "fmt"

var currentLogHeader = ""

func SetLogHeader(logHeader string) {
	if currentLogHeader != logHeader {
		if currentLogHeader != "" {
			fmt.Println()
		}
		fmt.Printf("%s\n", logHeader)
		currentLogHeader = logHeader
	}
}
