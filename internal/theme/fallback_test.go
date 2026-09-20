package theme

import "testing"

func TestFallbackDistinctFromBatroun(t *testing.T) {
	d := darkFallback()
	if d["accent"] == d["danger"] {
		t.Fatal("fallback accent must not equal danger (Batroun trap)")
	}
	l := lightFallback()
	if l["accent"] == l["danger"] {
		t.Fatal("light fallback accent == danger")
	}
}
