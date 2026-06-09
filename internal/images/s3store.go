package images

import (
	"context"
	"errors"

	"phantom-lancer/internal/objectstore"
	"phantom-lancer/internal/storage"
)

// objectClientResolver resolves an object storage client for the Images module
// from either the legacy inline "s3" settings or a shared object storage
// profile referenced by "object_storage" backend.
type objectClientResolver interface {
	GetObjectStorageProfile(ctx context.Context, id string) (storage.ObjectStorageProfile, error)
}

// newObjectClient builds an objectstore client for the given Images storage
// settings. It supports both the legacy inline s3 backend and the shared
// object storage profile backend. The resolver may be nil for the legacy path.
func newObjectClient(ctx context.Context, resolver objectClientResolver, settings storage.ImageStorageSettings) (*objectstore.Client, error) {
	settings = storage.NormalizeImageStorageSettings(settings)
	switch settings.Backend {
	case "object_storage":
		if resolver == nil {
			return nil, errors.New("object storage profile resolver is unavailable")
		}
		if settings.ObjectStorageProfileID == "" {
			return nil, errors.New("object storage profile is not selected")
		}
		profile, err := resolver.GetObjectStorageProfile(ctx, settings.ObjectStorageProfileID)
		if err != nil {
			return nil, err
		}
		return objectstore.New(profile)
	case "s3":
		return objectstore.New(storage.ObjectStorageProfile{
			ProviderLabel:   settings.S3ProviderLabel,
			Bucket:          settings.S3Bucket,
			Region:          settings.S3Region,
			Endpoint:        settings.S3Endpoint,
			ForcePathStyle:  settings.S3ForcePathStyle,
			AccessKeyID:     settings.S3AccessKeyID,
			SecretAccessKey: settings.S3SecretAccessKey,
			SessionToken:    settings.S3SessionToken,
		})
	default:
		return nil, errors.New("object storage is not enabled")
	}
}

// newObjectClientForAsset builds an objectstore client for reading or deleting
// an existing asset. When the asset records the object storage profile it was
// written with, the client is built from that profile so historical assets stay
// reachable after the current image storage settings switch to another profile.
// Legacy assets without a recorded profile fall back to the current settings.
func newObjectClientForAsset(ctx context.Context, resolver objectClientResolver, asset storage.ImageAsset, settings storage.ImageStorageSettings) (*objectstore.Client, error) {
	if asset.ObjectStorageProfileID != "" {
		if resolver == nil {
			return nil, errors.New("object storage profile resolver is unavailable")
		}
		profile, err := resolver.GetObjectStorageProfile(ctx, asset.ObjectStorageProfileID)
		if err != nil {
			return nil, err
		}
		return objectstore.New(profile)
	}
	return newObjectClient(ctx, resolver, settings)
}

// objectStorageEndpointLabel returns a safe endpoint label for persistence,
// resolving the effective endpoint for either backend.
func objectStorageEndpointLabel(ctx context.Context, resolver objectClientResolver, settings storage.ImageStorageSettings) string {
	settings = storage.NormalizeImageStorageSettings(settings)
	if settings.Backend == "object_storage" && resolver != nil && settings.ObjectStorageProfileID != "" {
		if profile, err := resolver.GetObjectStorageProfile(ctx, settings.ObjectStorageProfileID); err == nil {
			return objectstore.EndpointLabel(profile.Endpoint)
		}
		return ""
	}
	return objectstore.EndpointLabel(settings.S3Endpoint)
}

// objectStorageBucket returns the effective bucket for persistence on the asset
// record, resolving the profile when needed.
func objectStorageBucket(ctx context.Context, resolver objectClientResolver, settings storage.ImageStorageSettings) string {
	settings = storage.NormalizeImageStorageSettings(settings)
	if settings.Backend == "object_storage" && resolver != nil && settings.ObjectStorageProfileID != "" {
		if profile, err := resolver.GetObjectStorageProfile(ctx, settings.ObjectStorageProfileID); err == nil {
			return profile.Bucket
		}
		return ""
	}
	return settings.S3Bucket
}

// objectStorageRegion returns the effective region for persistence on the asset
// record, resolving the profile when needed.
func objectStorageRegion(ctx context.Context, resolver objectClientResolver, settings storage.ImageStorageSettings) string {
	settings = storage.NormalizeImageStorageSettings(settings)
	if settings.Backend == "object_storage" && resolver != nil && settings.ObjectStorageProfileID != "" {
		if profile, err := resolver.GetObjectStorageProfile(ctx, settings.ObjectStorageProfileID); err == nil {
			return profile.Region
		}
		return ""
	}
	return settings.S3Region
}
