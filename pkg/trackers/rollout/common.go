package rollout

import "fmt"

var currentLogHeader = ""

func setLogHeader(logHeader string) {
	if currentLogHeader != logHeader {
		if currentLogHeader != "" {
			fmt.Println()
		}
		fmt.Printf("%s\n", logHeader)
		currentLogHeader = logHeader
	}
}
