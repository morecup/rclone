package baidu_netdisk

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
func TestIntToDigitString(f *testing.T) {
	digitString := intToDigitString(9999, 4)
	fmt.Println(digitString)
}
