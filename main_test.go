package main

import "testing"

func TestVersionString(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })

	version = "v1.2.3"
	if got, want := versionString(), "awxtui v1.2.3"; got != want {
		t.Errorf("versionString() = %q, want %q", got, want)
	}
}
