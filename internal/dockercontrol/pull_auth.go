package dockercontrol

import (
	"context"
	"errors"
	"net/url"
	"strings"

	distref "github.com/distribution/reference"
	dockerregistry "github.com/docker/docker/api/types/registry"

	"phantom-lancer/internal/storage"
)

var ErrRegistryPullCredentialUnavailable = errors.New("no stored registry pull credential is available; rotate or create an active registry.pull credential for this repository")

type parsedImageReference struct {
	RegistryHost string
	Repository   string
}

func (s *Service) registryAuthForPull(ctx context.Context, rawRef string) (string, error) {
	if s.store == nil {
		return "", nil
	}
	settings, err := s.RegistrySettings(ctx)
	if err != nil {
		return "", err
	}
	if !settings.Enabled || settings.PublicURL == "" || settings.AllowAnonymousPull {
		return "", nil
	}
	parsed, ok := parseImageReference(rawRef)
	if !ok {
		return "", nil
	}
	publicHost, ok := registryPublicHost(settings.PublicURL)
	if !ok || !sameRegistryHost(parsed.RegistryHost, publicHost) {
		return "", nil
	}
	if err := validateRepositoryName(parsed.Repository); err != nil {
		return "", err
	}
	return s.encodedRegistryCredentialForPull(ctx, parsed.RegistryHost, parsed.Repository)
}

func (s *Service) encodedRegistryCredentialForPull(ctx context.Context, registryHost, repo string) (string, error) {
	creds, err := s.store.ListDockerRegistryCredentialSecrets(ctx)
	if err != nil {
		return "", err
	}
	for _, cred := range creds {
		if !registryCredentialCanPull(repo, cred) {
			continue
		}
		if cred.Secret == "" {
			continue
		}
		return dockerregistry.EncodeAuthConfig(dockerregistry.AuthConfig{
			Username:      cred.Name,
			Password:      cred.Secret,
			ServerAddress: registryHost,
		})
	}
	return "", ErrRegistryPullCredentialUnavailable
}

func registryCredentialCanPull(repo string, cred storage.DockerRegistryCredential) bool {
	if cred.Status != "active" || !hasScope(cred.Scopes, "registry.pull") {
		return false
	}
	prefix := strings.Trim(strings.TrimSpace(cred.RepositoryPrefix), "/")
	return prefix == "" || repo == prefix || strings.HasPrefix(repo, prefix+"/")
}

func parseImageReference(rawRef string) (parsedImageReference, bool) {
	named, err := distref.ParseNormalizedNamed(strings.TrimSpace(rawRef))
	if err != nil {
		return parsedImageReference{}, false
	}
	out := parsedImageReference{
		RegistryHost: distref.Domain(named),
		Repository:   distref.Path(named),
	}
	return out, out.RegistryHost != "" && out.Repository != ""
}

func registryPublicHost(rawURL string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return "", false
	}
	return strings.ToLower(strings.TrimSuffix(parsed.Host, "/")), true
}

func sameRegistryHost(left, right string) bool {
	return strings.EqualFold(strings.TrimSuffix(left, "/"), strings.TrimSuffix(right, "/"))
}
