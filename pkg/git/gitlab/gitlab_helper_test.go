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
