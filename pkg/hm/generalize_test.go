package hm

import "testing"

// TestSimpleFresherUniqueAfterGreekLetters guards against an earlier
// regression where Fresh fell back to `'0' + n % 10`, recycling the same
// 10 ASCII digit names after the 24 Greek letters were exhausted. Two
// independent fresh variables would then alias and unrelated parts of an
// inferred type could unify incorrectly.
func TestSimpleFresherUniqueAfterGreekLetters(t *testing.T) {
	f := NewSimpleFresher()
	seen := make(map[TypeVariable]struct{})
	const n = 100
	for i := range n {
		tv := f.Fresh()
		if _, dup := seen[tv]; dup {
			t.Fatalf("Fresh() returned duplicate %q after %d allocations", tv, i)
		}
		seen[tv] = struct{}{}
	}
	if got := len(seen); got != n {
		t.Fatalf("expected %d unique fresh vars, got %d", n, got)
	}
}
