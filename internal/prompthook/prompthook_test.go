package prompthook

import (
	"testing"
	"time"
)

func TestDecide(t *testing.T) {
	cases := []struct {
		name      string
		in        DecideInput
		wantMsg   string
		wantNudge bool
	}{
		{"first message forces toolsearch", DecideInput{FirstMessage: true}, toolSearchMsg, false},
		{"young session is silent", DecideInput{SessionAge: 2 * time.Minute}, "", false},
		{"within cooldown is silent", DecideInput{SessionAge: 10 * time.Minute, HasLastNudge: true, SinceLastNudge: 5 * time.Minute, HasLastSave: true, SinceLastSave: 30 * time.Minute}, "", false},
		{"no facts is silent", DecideInput{SessionAge: 10 * time.Minute, HasLastSave: false}, "", false},
		{"recent save is silent", DecideInput{SessionAge: 10 * time.Minute, HasLastSave: true, SinceLastSave: 5 * time.Minute}, "", false},
		{"stale save nudges", DecideInput{SessionAge: 10 * time.Minute, HasLastSave: true, SinceLastSave: 30 * time.Minute}, nudgeMsg, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decide(c.in)
			if got.Message != c.wantMsg || got.DidNudge != c.wantNudge {
				t.Fatalf("decide(%+v) = {%q,%v}; want {%q,%v}", c.in, got.Message, got.DidNudge, c.wantMsg, c.wantNudge)
			}
		})
	}
}
