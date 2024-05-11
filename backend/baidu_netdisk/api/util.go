package api

import (
	"fmt"
	"strconv"
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
func EncryptMd5(e string) string {
	if len(e) != 32 {
		return e
	}

	for _, ch := range e {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return e
		}
	}

	var i = e[8:16] + e[0:8] + e[24:32] + e[16:24]

	r := ""
	for o := 0; o < len(i); o++ {
		digit, _ := strconv.ParseInt(string(i[o]), 16, 0)
		r += strconv.FormatInt(digit^(int64(o))&15, 16)
	}

	s := string('g' + int64(strings.Index("0123456789abcdef", string(r[9]))))

	return r[0:9] + s + r[10:]
}
