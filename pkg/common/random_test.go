package common

import "testing"

func TestGetRandomString(t *testing.T) {
	tests := []struct {
		name   string
		length int
	}{
		{
			name:   "should be able to generate one symbol rangom string",
			length: 1,
		},
		{
			name:   "should be able to generate rangom string",
			length: 5,
		},
		{
			name:   "should be able to generate long rangom string",
			length: 100,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RandomString(tt.length)
			if len(got) != tt.length {
				t.Errorf("Got string %s has lenght %d but expected length is %d", got, len(got), tt.length)
			}
		})
	}
}
