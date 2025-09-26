package util

import "testing"

func TestTimestampFormat(t *testing.T) {
	stamp := Timestamp()
	if len(stamp) != len("2006-01-02T15-04-05Z") {
		t.Fatalf("unexpected length: %d", len(stamp))
	}
	if stamp[4] != '-' || stamp[7] != '-' {
		t.Fatalf("timestamp malformed: %s", stamp)
	}
}
