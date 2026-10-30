package hm

import "testing"

func TestSimpleFresherNeverRepeats(t *testing.T) {
	fresh := NewSimpleFresher()
	seen := map[TypeVariable]bool{}
	names := map[string]bool{}
	for i := 0; i < 1000; i++ {
		tv := fresh.Fresh()
		if seen[tv] {
			t.Fatalf("fresh variable #%d (%s) was already minted", i, tv)
		}
		seen[tv] = true
		if names[tv.String()] {
			t.Fatalf("fresh variable #%d is named %s, like an earlier one", i, tv)
		}
		names[tv.String()] = true
		if tv.IsRigid() {
			t.Fatalf("fresh variable #%d (%s) is rigid", i, tv)
		}
		r := Rigid(tv)
		if !r.IsRigid() || r == tv || r.String() != tv.String() {
			t.Fatalf("rigid counterpart of %s: %s (rigid: %v)", tv, r, r.IsRigid())
		}
	}

	for i, want := range map[int]string{0: "a", 25: "z", 26: "a1", 51: "z1", 52: "a2"} {
		if got := FreshVar(i).String(); got != want {
			t.Errorf("FreshVar(%d) = %s, want %s", i, got, want)
		}
	}
}

func TestFreshVariablesAreNeverAnothers(t *testing.T) {
	written := TypeVariable('a')
	one, other := NewSimpleFresher().Fresh(), NewSimpleFresher().Fresh()
	if one == written || other == written || one == other {
		t.Fatalf("fresh variables %v and %v and written %v are not all distinct", one, other, written)
	}
	if one.String() != "a" || other.String() != "a" {
		t.Fatalf("first fresh variables are named %s and %s, want a", one, other)
	}

	copied := Freshen(written)
	if copied == written || copied.String() != "a" || copied.IsRigid() {
		t.Fatalf("Freshen(%v) = %v", written, copied)
	}
	if Freshen(Rigid(written)).IsRigid() {
		t.Fatalf("Freshen keeps a variable rigid")
	}
}

func TestEqIgnoringIDs(t *testing.T) {
	one, other := NewSimpleFresher().Fresh(), NewSimpleFresher().Fresh()
	if one.Eq(other) || !EqIgnoringIDs(one, other) {
		t.Fatalf("%v and %v: Eq %v, EqIgnoringIDs %v", one, other, one.Eq(other), EqIgnoringIDs(one, other))
	}
	if EqIgnoringIDs(Rigid(one), Rigid(other)) {
		t.Fatalf("rigid %v and %v compare equal ignoring ids", one, other)
	}
}
