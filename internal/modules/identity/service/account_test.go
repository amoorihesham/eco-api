package service

import "testing"

func TestWantDefault(t *testing.T) {
	cases := []struct {
		name      string
		count     int
		requested bool
		want      bool
	}{
		{"first address is forced default", 0, false, true},
		{"later address, not requested", 2, false, false},
		{"later address, explicitly requested", 2, true, true},
		{"first address, also requested", 0, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wantDefault(c.count, c.requested); got != c.want {
				t.Fatalf("wantDefault(%d, %v) = %v, want %v", c.count, c.requested, got, c.want)
			}
		})
	}
}
