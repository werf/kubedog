package utils

import (
	"bytes"
	"fmt"
	"strings"

	"k8s.io/client-go/util/jsonpath"
)

func JSONPath(tmpl string, input interface{}) (result string, found bool, err error) {
	jsonPath := jsonpath.New("")

	if err := jsonPath.Parse(fmt.Sprintf("{%s}", tmpl)); err != nil {
		return "", false, fmt.Errorf("error parsing jsonpath: %w", err)
	}

	resultBuf := &bytes.Buffer{}
	if err := jsonPath.Execute(resultBuf, input); err != nil {
		if strings.HasSuffix(err.Error(), " is not found") {
			return "", false, nil
		} else {
			return "", false, fmt.Errorf("error executing jsonpath: %w", err)
		}
	}
	if strings.TrimSpace(resultBuf.String()) == "" {
		return "", false, nil
	}

	return resultBuf.String(), true, nil
}
