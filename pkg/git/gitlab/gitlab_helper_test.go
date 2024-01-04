package gitlab

import (
	"errors"
	"testing"
)

func TestGetProjectPathFromRepoUrl(t *testing.T) {
	var params = []struct {
		in  string
		out string
		err error
	}{
		{
			"https://gitlab.com/namespace/projectname",
			"namespace/projectname",
			nil,
		},
		{
			"https://gitlab.com/projectname",
			"projectname",
			nil,
		},
		{
			"https://gitlab.com/redhat/rhel/containers/rhtap-prototype",
			"redhat/rhel/containers/rhtap-prototype",
			nil,
		},
		{
			"https://gitlab.com/redhat/rhel/containers/rhtap-prototype.git",
			"redhat/rhel/containers/rhtap-prototype",
			nil,
		},
		{
			"",
			"",
			nil,
		},
		{
			"http!!://abc",
			"",
			errors.New(""),
		},
	}

	for _, tt := range params {
		actual, err := getProjectPathFromRepoUrl(tt.in)
		if err != nil && tt.err == nil {
			t.Fatalf("Expected call to succeed, found error %s", err.Error())
		}
		if err == nil && tt.err != nil {
			t.Fatalf("Expected call to end with an error, but it succeed")
		}
		if actual != tt.out {
			t.Fatalf("Expected %s found %s", tt.out, actual)
		}
	}
}

func TestGetHostFromRepoUrl(t *testing.T) {
	var params = []struct {
		in  string
		out string
		err error
	}{
		{
			"https://gitlab.host.com/rhtap/tenants-config.git",
			"https://gitlab.host.com/",
			nil,
		},
		{
			"https://gitlab.com/projectname",
			"https://gitlab.com/",
			nil,
		},
		{
			"http!!://abc",
			"",
			FailedToParseUrlError{},
		},
	}

	for _, tt := range params {
		t.Run(tt.in, func(t *testing.T) {
			actual, err := GetBaseUrl(tt.in)
			if err != nil && tt.err == nil {
				t.Fatalf("Expected call to succeed, found error %s", err.Error())
			}
			if err == nil && tt.err != nil {
				t.Fatalf("Expected call to end with an error, but it succeed")
			}
			if actual != tt.out {
				t.Fatalf("Expected %s found %s", tt.out, actual)
			}
		})
	}
}

func TestGetHostFromRepoUrlFailToParseUrl(t *testing.T) {
	var params = []struct {
		in  string
		out string
	}{
		{
			"http!!://abc",
			"",
		},
	}

	for _, tt := range params {
		t.Run(tt.in, func(t *testing.T) {
			actual, err := GetBaseUrl(tt.in)
			if actual != "" {
				t.Fatalf("Expected an empty string result")
			}
			if _, ok := err.(FailedToParseUrlError); !ok {
				t.Fatalf("Expected to received error of type FailedToParseUrlError got %T", err)
			}
		})
	}
}

func TestGetHostFromRepoUrlMissingSchema(t *testing.T) {
	var params = []struct {
		in  string
		out string
	}{
		{
			"gitlab.com",
			"",
		},
		{
			"gitlab.cee.redhat.com",
			"",
		},
	}

	for _, tt := range params {
		t.Run(tt.in, func(t *testing.T) {
			actual, err := GetBaseUrl(tt.in)
			if actual != "" {
				t.Fatalf("Expected an empty string result")
			}
			if _, ok := err.(MissingSchemaError); !ok {
				t.Fatalf("Expected to received error of type MissingSchemaError got %T", err)
			}
		})
	}
}

func TestGetHostFromRepoUrlMissingHost(t *testing.T) {
	var params = []struct {
		in  string
		out string
	}{
		{
			"http:///abc",
			"",
		},
	}

	for _, tt := range params {
		t.Run(tt.in, func(t *testing.T) {
			actual, err := GetBaseUrl(tt.in)
			if actual != "" {
				t.Fatalf("Expected an empty string result")
			}
			if _, ok := err.(MissingHostError); !ok {
				t.Fatalf("Expected to received error of type MissingHostError got %T", err)
			}
		})
	}
}
