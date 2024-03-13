package webhook

import (
	"errors"
	"reflect"
	"testing"
)

func TestConfigWebhookURLLoader_Load(t *testing.T) {
	type fields struct {
		mapping map[string]string
	}
	type args struct {
		repositoryUrl string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   string
	}{
		{
			name: "No match should return an empty string",
			fields: fields{
				mapping: map[string]string{},
			},
			args: args{
				repositoryUrl: "https://github.com/org/repo",
			},
			want: "",
		},
		{
			name: "Multiple prefix' with an exact match",
			fields: fields{
				mapping: map[string]string{
					"https://github.com/org/repo": "chosenTarget",
					"https://github.com/org/":     "otherTarget1",
					"https://github.com/":         "otherTarget2",
					"https://gitlab.com/":         "otherTarget3",
				},
			},
			args: args{
				repositoryUrl: "https://github.com/org/repo",
			},
			want: "chosenTarget",
		},
		{
			name: "No exact match, the longest prefix is chosen",
			fields: fields{
				mapping: map[string]string{
					"https://github.com/org/": "chosenTarget",
					"https://github.com/":     "otherTarget1",
					"https://gitlab.com/":     "otherTarget2",
				},
			},
			args: args{
				repositoryUrl: "https://github.com/org/repo",
			},
			want: "chosenTarget",
		},
		{
			name: "Match on an empty string",
			fields: fields{
				mapping: map[string]string{
					"":                    "chosenTarget",
					"https://gitlab.com/": "otherTarget2",
				},
			},
			args: args{
				repositoryUrl: "https://github.com/org/repo",
			},
			want: "chosenTarget",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewConfigWebhookURLLoader(tt.fields.mapping)
			if got := c.Load(tt.args.repositoryUrl); got != tt.want {
				t.Errorf("ConfigWebhookURLLoader.Load() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoadMappingFromFile(t *testing.T) {
	type args struct {
		path string
	}
	tests := []struct {
		name       string
		args       args
		fileReader FileReader
		want       map[string]string
		wantErr    bool
	}{
		{
			name: "Load empty file",
			args: args{path: "file"},
			fileReader: func(name string) ([]byte, error) {
				return []byte("{}"), nil
			},
			want:    map[string]string{},
			wantErr: false,
		},
		{
			name: "Load non empty file",
			args: args{path: "file"},
			fileReader: func(name string) ([]byte, error) {
				return []byte(`
					{
						"a": "1",
						"b": "2"
					}
				`), nil
			},
			want: map[string]string{
				"a": "1",
				"b": "2",
			},
			wantErr: false,
		},
		{
			name: "The given path is an empty string",
			args: args{path: ""},
			fileReader: func(name string) ([]byte, error) {
				return nil, nil
			},
			want:    map[string]string{},
			wantErr: false,
		},
		{
			name: "Load file with broken json",
			args: args{path: "file"},
			fileReader: func(name string) ([]byte, error) {
				return []byte("abc"), nil
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "Load file random error",
			args: args{path: "file"},
			fileReader: func(name string) ([]byte, error) {
				return nil, errors.New("Random Error")
			},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LoadMappingFromFile(tt.args.path, tt.fileReader)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadMappingFromFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("LoadMappingFromFile() = %v, want %v", got, tt.want)
			}
		})
	}
}
