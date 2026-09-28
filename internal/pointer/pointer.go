package pointer

func To[T any](v T) *T { return &v }
func ToWithZeroAsNil[T comparable](v T) *T {
	var zero T
	if v == zero {
		return nil
	}
	return &v
}

func Indirect[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
