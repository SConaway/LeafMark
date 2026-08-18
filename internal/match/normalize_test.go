package match

import "testing"

func TestNormalize(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Project Hail Mary: A Novel", "project hail mary"},
		{"The Fifth Season (The Broken Earth, #1)", "the fifth season"},
		{"Dune (Kindle Edition)", "dune"},
		{"  Extra   Spaces   Here  ", "extra spaces here"},
		{"Colon: Subtitle, Comma!", "colon subtitle comma"},
		{"", ""},
	}
	for _, c := range cases {
		if got := Normalize(c.in); got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
