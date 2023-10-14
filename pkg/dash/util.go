package dash

func sliceOf[T any](val any) []T {
	anys := val.([]any)
	ts := make([]T, len(anys))
	for i, node := range anys {
		ts[i] = node.(T)
	}
	return ts
}
