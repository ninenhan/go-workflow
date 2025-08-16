package fn

import (
	"context"
)

type PageQueryFunc[T any] func(ctx context.Context, pageNum, pageSize int) ([]T, error)
type KeyFunc[T any] func(item T) string
type HandlerFunc[T any] func(items []T)

func ScanAllPages[T any](
	ctx context.Context,
	pageSize int,
	query PageQueryFunc[T],
	keyFunc KeyFunc[T],
	handle HandlerFunc[T],
) (*SafeHashSet[string], error) {
	set := NewSafeHashSet[string]()
	page := 1
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		items, err := query(ctx, page, pageSize)
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			break
		}
		handle(items)
		for _, item := range items {
			key := keyFunc(item)
			if set.Has(key) {
				continue
			}
			set.Add(key)
		}
		page++
	}
	return set, nil
}
