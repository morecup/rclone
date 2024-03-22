package api

import (
	"fmt"
	"strings"
)

func BodyList(slice []string) string {
	if len(slice) > 0 {
		nospace := strings.Join(slice, "\",\"")
		quoted := fmt.Sprintf("[\"%s\"]", nospace)
		return quoted
	} else {
		return "[]"
	}
}
func BoolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
func BoolToStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
