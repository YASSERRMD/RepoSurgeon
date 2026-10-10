package main

import "testing"

func TestAdd(t *testing.T) {
	cases := []struct {
		a, b, want int
	}{
		{2, 2, 4},
		{-1, 1, 0},
		{40, 2, 42},
	}
	for _, tc := range cases {
		if got := Add(tc.a, tc.b); got != tc.want {
			t.Errorf("Add(%d, %d) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
