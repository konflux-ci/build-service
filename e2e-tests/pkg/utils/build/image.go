package build

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	corev1 "k8s.io/api/core/v1"
)

type DockerRegistryAuth struct {
	Auth     string `json:"auth"`               // Base64 encoded username:password string
	Username string `json:"username,omitempty"` // Used when auth is not set
	Password string `json:"password,omitempty"` // Used when auth is not set
}

type DockerConfig map[string]DockerRegistryAuth

type DockerConfigJSON struct {
	Auths DockerConfig `json:"auths"`
}

// Creates a mock image and pushes it to the targetRef
// targetRef Format: quay.io/<namespace>/<repository>:<tag>
func BuildMockImageAndPush(secret *corev1.Secret, targetRef string) error {
	keychain, err := NewKeychainFromPullSecret(secret)
	if err != nil {
		return err
	}

	// Prepare the mock image containing a single text file.
	imgContents := map[string][]byte{
		"/app/hello.txt": []byte("Hello, I am a sample text!\n"),
	}

	img, err := crane.Image(imgContents)
	if err != nil {
		return fmt.Errorf("Failed to create image: %v", err)
	}
	fmt.Printf("Pushing image to %s...", targetRef)

	// Authenticate and push using crane options
	err = crane.Push(img, targetRef, crane.WithAuthFromKeychain(keychain))
	if err != nil {
		return fmt.Errorf("Failed to push image: %v", err)
	}

	fmt.Println("Successfully authenticated and pushed the image to the registry!")
	return nil
}

// IsImagePullableAnonymously checks whether the image can be pulled without credentials.
func IsImagePullableAnonymously(imageRef string) (bool, error) {
	if _, err := crane.Digest(imageRef, crane.WithAuth(authn.Anonymous)); err != nil {
		if isAuthError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// PullImageWithSecret pulls the image using credentials from the given kubernetes
// pull secret (kubernetes.io/dockerconfigjson or kubernetes.io/dockercfg).
// The image is fully pulled, i.e. its manifest, config and all the layers are fetched,
// so a nil error means the image content is really available with the given credentials.
// imageRef format: quay.io/<namespace>/<repository>:<tag>
func PullImageWithSecret(secret *corev1.Secret, imageRef string) error {
	keychain, err := NewKeychainFromPullSecret(secret)
	if err != nil {
		return err
	}

	img, err := crane.Pull(imageRef, crane.WithAuthFromKeychain(keychain))
	if err != nil {
		return fmt.Errorf("failed to pull image %s: %w", imageRef, err)
	}

	if _, err := img.RawConfigFile(); err != nil {
		return fmt.Errorf("failed to pull config of the image %s: %w", imageRef, err)
	}

	layers, err := img.Layers()
	if err != nil {
		return fmt.Errorf("failed to read layers of the image %s: %w", imageRef, err)
	}
	for _, layer := range layers {
		if err := readLayer(layer); err != nil {
			return fmt.Errorf("failed to pull layer of the image %s: %w", imageRef, err)
		}
	}
	return nil
}

// IsImagePullableWithSecret checks whether the image can be pulled with the credentials
// from the given kubernetes pull secret.
// Returns false without an error if the registry rejects the credentials, any other
// failure (e.g. malformed secret, network issue) is returned as an error.
func IsImagePullableWithSecret(secret *corev1.Secret, imageRef string) (bool, error) {
	if err := PullImageWithSecret(secret, imageRef); err != nil {
		if isAuthError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// NewKeychainFromPullSecret builds a keychain that resolves registry credentials
// from the docker config stored in the given kubernetes pull secret.
func NewKeychainFromPullSecret(secret *corev1.Secret) (authn.Keychain, error) {
	auths, err := getDockerConfigFromSecret(secret)
	if err != nil {
		return nil, err
	}
	return &pullSecretKeychain{auths: auths}, nil
}

type pullSecretKeychain struct {
	auths DockerConfig
}

// Resolve returns credentials for the target from the pull secret.
// Credentials can be stored under the registry host (quay.io), or under a repository
// path (quay.io/my-org/my-repo), the most specific matching entry wins.
func (k *pullSecretKeychain) Resolve(target authn.Resource) (authn.Authenticator, error) {
	matchedKey := ""
	var matchedAuth DockerRegistryAuth
	for key, auth := range k.auths {
		normalizedKey := normalizeRegistryKey(key)
		if !registryKeyMatches(normalizedKey, target) {
			continue
		}
		if len(normalizedKey) > len(matchedKey) {
			matchedKey, matchedAuth = normalizedKey, auth
		}
	}
	if matchedKey == "" {
		return authn.Anonymous, nil
	}

	if matchedAuth.Auth == "" {
		if matchedAuth.Username == "" {
			return nil, fmt.Errorf("empty auth for the registry: %s", matchedKey)
		}
		return &authn.Basic{Username: matchedAuth.Username, Password: matchedAuth.Password}, nil
	}
	username, password, err := decodeRegistryAuth(matchedAuth.Auth, matchedKey)
	if err != nil {
		return nil, err
	}
	return &authn.Basic{Username: username, Password: password}, nil
}

// getDockerConfigFromSecret reads registry credentials from a pull / push secret.
func getDockerConfigFromSecret(secret *corev1.Secret) (DockerConfig, error) {
	if secret == nil {
		return nil, fmt.Errorf("secret is nil")
	}
	if dockerConfigJSONBytes, ok := secret.Data[corev1.DockerConfigJsonKey]; ok {
		var configJSON DockerConfigJSON
		if err := json.Unmarshal(dockerConfigJSONBytes, &configJSON); err != nil {
			return nil, fmt.Errorf("failed to unmarshal %s from secret %s: %w", corev1.DockerConfigJsonKey, secret.Name, err)
		}
		return configJSON.Auths, nil
	}
	// Legacy kubernetes.io/dockercfg secret has no "auths" wrapper.
	if dockerCfgBytes, ok := secret.Data[corev1.DockerConfigKey]; ok {
		var config DockerConfig
		if err := json.Unmarshal(dockerCfgBytes, &config); err != nil {
			return nil, fmt.Errorf("failed to unmarshal %s from secret %s: %w", corev1.DockerConfigKey, secret.Name, err)
		}
		return config, nil
	}
	return nil, fmt.Errorf("failed to read data from secret %s, it's not a docker config secret", secret.Name)
}

// normalizeRegistryKey strips the parts of a docker config key that are not a part
// of an image reference, e.g. https://quay.io/ becomes quay.io
func normalizeRegistryKey(key string) string {
	if _, rest, found := strings.Cut(key, "://"); found {
		key = rest
	}
	key = strings.TrimSuffix(key, "/")
	// Docker Hub is referenced by several aliases, but images resolve to the default registry.
	switch key {
	case "docker.io", "registry-1.docker.io", "index.docker.io/v1", "index.docker.io/v2":
		return name.DefaultRegistry
	}
	return key
}

func registryKeyMatches(normalizedKey string, target authn.Resource) bool {
	if normalizedKey == target.RegistryStr() || normalizedKey == target.String() {
		return true
	}
	// The key can be a repository path prefix, e.g. quay.io/my-org for quay.io/my-org/my-repo
	return strings.HasPrefix(target.String(), normalizedKey+"/")
}

func decodeRegistryAuth(auth, registry string) (string, string, error) {
	decodedAuth, err := base64.StdEncoding.DecodeString(auth)
	if err != nil {
		return "", "", fmt.Errorf("failed to decode auth for the registry %s: %v", registry, err)
	}
	// decoded auth has username:password format
	username, password, found := strings.Cut(string(decodedAuth), ":")
	if !found {
		return "", "", fmt.Errorf("invalid auth format for the registry %s, expected username:password", registry)
	}
	return username, password, nil
}

func readLayer(layer v1.Layer) error {
	reader, err := layer.Compressed()
	if err != nil {
		return err
	}
	defer reader.Close()
	if _, err := io.Copy(io.Discard, reader); err != nil {
		return err
	}
	return nil
}

// isAuthError tells whether the registry refused the request because of missing or insufficient permissions.
func isAuthError(err error) bool {
	var tErr *transport.Error
	if errors.As(err, &tErr) {
		for _, e := range tErr.Errors {
			if e.Code == transport.UnauthorizedErrorCode || e.Code == transport.DeniedErrorCode {
				return true
			}
		}
	}
	return false
}
