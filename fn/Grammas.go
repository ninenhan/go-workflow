package fn

// Ternary 三元表达式
func Ternary[T any](cond bool, trueVal T, falseVal T) T {
	if cond {
		return trueVal
	}
	return falseVal
}
