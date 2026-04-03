package wrapper

import (
	"encoding/json"
	"strings"
)

type StringFlags []string

func (f *StringFlags) String() string {
	b, _ := json.Marshal(*f)
	return string(b)
}

func (f *StringFlags) Set(value string) error {
	for _, str := range strings.Split(value, ",") {
		*f = append(*f, str)
	}
	return nil
}

type StringWrapper struct {
	Value string // set by Cli
}
