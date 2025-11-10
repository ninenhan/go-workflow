package fn

func SumMapBy[T any, K comparable](records []T, keyFn func(T) K, addFn func(dst *T, src T)) map[K]T {
	res := make(map[K]T)
	for _, r := range records {
		k := keyFn(r)
		dst, ok := res[k]
		if !ok {
			res[k] = r
			continue
		}
		addFn(&dst, r)
		res[k] = dst
	}
	return res
}

func SumBy[T any](items []T, addFn func(dst *T, src T)) T {
	var res T
	for _, it := range items {
		addFn(&res, it)
	}
	return res
}
