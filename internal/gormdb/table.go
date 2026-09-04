package gormdb

import "fmt"

// ValidateTablePrefix restricts prefixes to portable SQL identifier characters.
// The empty prefix preserves the default table names.
func ValidateTablePrefix(prefix string) error {
	for _, character := range prefix {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '_' {
			continue
		}
		return fmt.Errorf("table prefix %q contains unsupported character %q", prefix, character)
	}
	return nil
}

func TableName(prefix, name string) string {
	return prefix + name
}
