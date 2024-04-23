package renovate

import (
	"context"
	"reflect"
	"testing"

	"github.com/redhat-appstudio/build-service/pkg/git"
	"github.com/redhat-appstudio/build-service/pkg/git/credentials"
)

var staticCredentials = &credentials.BasicAuthCredentials{}
var StaticCredentialsFunc credentials.BasicAuthCredentialsProviderFunc = func(ctx context.Context, component *git.ScmComponent) (*credentials.BasicAuthCredentials, error) {
	return staticCredentials, nil
}

func TestNewTasks(t *testing.T) {
	tests := []struct {
		name            string
		credentialsFunc credentials.BasicAuthCredentialsProviderFunc
		components      []*git.ScmComponent
		expected        []Task
	}{
		{
			name:            "No components - no tasks",
			credentialsFunc: StaticCredentialsFunc,
		},
		{
			name:            "Simple one component case",
			credentialsFunc: StaticCredentialsFunc,
			components:      []*git.ScmComponent{ignoreError(git.NewScmComponent("github", "https://github.com/umbrellacorp/devfile-sample-go-basic", "main", "devfile-sample-go-basic", "umbrellacorp-tenant")).(*git.ScmComponent)},
			expected: []Task{
				NewBasicAuthTask("github", "github.com", staticCredentials, []Repository{
					{
						Repository:   "umbrellacorp/devfile-sample-go-basic",
						BaseBranches: []string{"main"},
					},
				}),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			//given
			taskProvider := NewBasicAuthTaskProvider(tt.credentialsFunc)
			//when
			got := taskProvider.GetNewTasks(context.TODO(), tt.components)
			//then
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("got %v, want %v", got, tt.expected)
			}
		})
	}

}

func ignoreError(val interface{}, err error) interface{} {
	return val
}
