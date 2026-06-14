package dockercontrol

import (
	"context"
	"errors"
)

var ErrContainerImageDenied = errors.New("image is outside the allowed Docker Registry source")

// ValidateContainerImageSource keeps template-based container creation bounded
// to the configured first-party registry when a registry public URL is set.
// The policy intentionally checks the registry host rather than a hard-coded
// repository prefix so owner-created repositories such as stock-pulse/app work
// without retagging to a fixed personal/* namespace.
func (s *Service) ValidateContainerImageSource(ctx context.Context, rawRef string) error {
	if s.store == nil {
		return nil
	}
	settings, err := s.RegistrySettings(ctx)
	if err != nil {
		return err
	}
	return validateContainerImageSource(settings.PublicURL, rawRef)
}

func validateContainerImageSource(publicURL, rawRef string) error {
	if publicURL == "" {
		return nil
	}
	publicHost, ok := registryPublicHost(publicURL)
	if !ok {
		return nil
	}
	parsed, ok := parseImageReference(rawRef)
	if !ok || !sameRegistryHost(parsed.RegistryHost, publicHost) {
		return ErrContainerImageDenied
	}
	if err := validateRepositoryName(parsed.Repository); err != nil {
		return ErrContainerImageDenied
	}
	return nil
}
