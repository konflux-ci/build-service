package git

import (
	"testing"
)

func TestBuildEndpoint(t *testing.T) {

	tests := []struct {
		name         string
		endpointType string
		host         string
		wantEndpoint string
	}{
		{
			name:         "Github SAAS",
			endpointType: "github",
			host:         "github.com",
			wantEndpoint: "https://api.github.com/",
		},
		{
			name:         "Github On-Prem",
			endpointType: "github",
			host:         "github.umbrella.com",
			wantEndpoint: "https://api.github.umbrella.com/",
		},
		{
			name:         "Gitlab SAAS",
			endpointType: "gitlab",
			host:         "gitlab.com",
			wantEndpoint: "https://gitlab.com/api/v4/",
		},
		{
			name:         "Gitlab On-Prem",
			endpointType: "gitlab",
			host:         "gitlab.umbrella.com",
			wantEndpoint: "https://gitlab.umbrella.com/api/v4/",
		},
		{
			name:         "Unknown provider",
			endpointType: "bibi",
			host:         "bibi.umbrella.com",
			wantEndpoint: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildAPIEndpoint(tt.endpointType).APIEndpoint(tt.host); got != tt.wantEndpoint {
				t.Errorf("BuildAPIEndpoint() = %v, want %v", got, tt.wantEndpoint)
			}
		})
	}
}
