package fn

import "strings"

func IsEmpty(s string) bool {
	return s == "" || strings.TrimSpace(s) == ""
}
