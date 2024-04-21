package fs

import (
	"encoding/json"
	"strconv"
)

type StringValue struct {
	Raw string
}

func (sv *StringValue) UnmarshalJSON(b []byte) error {
	var i int
	if err := json.Unmarshal(b, &i); err == nil {
		sv.Raw = strconv.Itoa(i)
		return nil
	}

	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	sv.Raw = s
	return nil
}
