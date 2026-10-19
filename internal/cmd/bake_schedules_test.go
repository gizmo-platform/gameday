package cmd

import (
	"reflect"
	"testing"
)

func TestParseIntRange(t *testing.T) {
	cases := []struct {
		spec string
		want []int
	}{
		{"8", []int{8}},
		{"8,12,16", []int{8, 12, 16}},
		{"8-12", []int{8, 9, 10, 11, 12}},
		{"24-40:8", []int{24, 32, 40}},
		{"8-12,16,24-40:8", []int{8, 9, 10, 11, 12, 16, 24, 32, 40}},
		{"10-10", []int{10}},
		{" 8 - 10 ", []int{8, 9, 10}},
		{"8,8,9", []int{8, 9}},
	}
	for _, c := range cases {
		got, err := parseIntRange(c.spec)
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.spec, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %v want %v", c.spec, got, c.want)
		}
	}
}

func TestParseIntRangeErrors(t *testing.T) {
	for _, spec := range []string{
		"",
		"abc",
		"10-5",
		"0",
		"-4",
		"8-12:0",
		"8-12:x",
	} {
		if _, err := parseIntRange(spec); err == nil {
			t.Errorf("%q: expected an error", spec)
		}
	}
}
