package api

import (
	"fmt"
	"strings"
	"testing"
)

func TestGetFileMetas(t *testing.T) {
	list := []string{}

	nospace := strings.Join(list, "\",\"")
	quoted := fmt.Sprintf("[\"%s\"]", nospace)

	fmt.Println(quoted)
}
