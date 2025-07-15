package dang

func sliceOf[T any](val any) []T {
	anys := val.([]any)
	ts := make([]T, len(anys))
	for i, node := range anys {
		ts[i] = node.(T)
	}
	return ts
}

func sliceOfAppend[T any](val any, last any) []T {
	anys := val.([]any)
	ts := make([]T, len(anys))
	for i, node := range anys {
		ts[i] = node.(T)
	}
	if last != nil {
		ts = append(ts, last.(T))
	}
	return ts
}
