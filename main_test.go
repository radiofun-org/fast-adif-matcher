package main

import "testing"

func TestWithinThirtyMinutes(t *testing.T) {
	var tests = []struct {
		name   string
		a      Contact
		b      Contact
		answer bool
	}{
		{"5 minute difference", Contact{Date: 25000, Time: 1005}, Contact{Date: 25000, Time: 1000}, true},
		{"2 day difference", Contact{Date: 25002, Time: 1005}, Contact{Date: 25000, Time: 1000}, false},
		{"30 minute difference", Contact{Date: 25000, Time: 1005}, Contact{Date: 25000, Time: 1035}, true},
		{"31 minute difference", Contact{Date: 25000, Time: 1005}, Contact{Date: 25000, Time: 1036}, false},
		{"11 minute difference on previous day", Contact{Date: 25000, Time: 10}, Contact{Date: 24999, Time: 1439}, true},
		{"31 minute difference on previous day", Contact{Date: 25000, Time: 30}, Contact{Date: 24999, Time: 1439}, false},
		{"20 minute difference on next day", Contact{Date: 25000, Time: 1430}, Contact{Date: 25001, Time: 10}, true},
		{"40 minute difference on next day", Contact{Date: 25000, Time: 1430}, Contact{Date: 25001, Time: 30}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if withinThirtyMinutes(tt.a, tt.b) != tt.answer {
				t.Errorf("got %t, want %t", !tt.answer, tt.answer)
			}
		})
	}
}
