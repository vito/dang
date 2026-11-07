package dang

import "testing"

func TestSortStableOrdersNumericScalarsNumerically(t *testing.T) {
	scalar := func(v string) Value { return ScalarValue{Val: v, Encoded: true} }
	elems := []Value{scalar("10"), scalar("9"), scalar("1e2"), scalar("-3.5")}
	sorted, err := sortStable(elems, elems, false)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range sorted {
		got = append(got, v.(ScalarValue).Val)
	}
	want := []string{"-3.5", "9", "10", "1e2"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
