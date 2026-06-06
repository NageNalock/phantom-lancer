package selfupdate

import "testing"

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name       string
		current    string
		latest     string
		want       int
		comparable bool
	}{
		{name: "newer latest", current: "v0.1.0", latest: "v0.1.1", want: -1, comparable: true},
		{name: "equal", current: "v1.2.3", latest: "v1.2.3", want: 0, comparable: true},
		{name: "current newer", current: "v1.2.4", latest: "v1.2.3", want: 1, comparable: true},
		{name: "dev current", current: "0.0.0-dev+abc123", latest: "v1.2.3", comparable: false},
		{name: "invalid latest", current: "v1.2.3", latest: "latest", comparable: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, comparable := compareVersions(test.current, test.latest)
			if comparable != test.comparable || got != test.want {
				t.Fatalf("compareVersions(%q, %q) = %d/%v, want %d/%v", test.current, test.latest, got, comparable, test.want, test.comparable)
			}
		})
	}
}
