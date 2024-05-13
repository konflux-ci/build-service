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

func TestIsPaCApplicationConfigured(t *testing.T) {
	tests := []struct {
		name        string
		gitProvider string
		config      map[string][]byte
		want        bool
	}{
		{
			name:        "should detect github application configured",
			gitProvider: "github",
			config: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
			},
			want: true,
		},
		{
			name:        "should prefer github application if both github application and webhook configured",
			gitProvider: "github",
			config: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
				"password":                      []byte("ghp_token"),
			},
			want: true,
		},
		{
			name:        "should not detect application if it is not configured",
			gitProvider: "github",
			config: map[string][]byte{
				"password": []byte("ghp_token"),
			},
			want: false,
		},
		{
			name:        "should not detect application if configuration empty",
			gitProvider: "github",
			config:      map[string][]byte{},
			want:        false,
		},
		{
			name:        "should not detect GitHub application if gilab webhook configured",
			gitProvider: "gitlab",
			config: map[string][]byte{
				PipelinesAsCodeGithubAppIdKey:   []byte("12345"),
				PipelinesAsCodeGithubPrivateKey: []byte("private-key"),
				"password":                      []byte("glpat-token"),
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsPaCApplicationConfigured(tt.gitProvider, tt.config); got != tt.want {
				t.Errorf("want %t, but got %t", tt.want, got)
			}
		})
	}
}
