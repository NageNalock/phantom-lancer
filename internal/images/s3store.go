package images

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"phantom-lancer/internal/storage"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3ObjectStore struct {
	settings storage.ImageStorageSettings
	client   *s3.Client
}

func NewS3ObjectStore(settings storage.ImageStorageSettings) (*S3ObjectStore, error) {
	settings = storage.NormalizeImageStorageSettings(settings)
	if settings.Backend != "s3" {
		return nil, errors.New("S3 storage is not enabled")
	}
	if settings.S3Bucket == "" {
		return nil, errors.New("S3 bucket is required")
	}
	if settings.S3Endpoint == "" {
		return nil, errors.New("S3 compatible endpoint is required")
	}
	if settings.S3Region == "" {
		settings.S3Region = "auto"
	}
	if settings.S3AccessKeyID == "" || settings.S3SecretAccessKey == "" {
		return nil, errors.New("S3 credentials are required")
	}
	options := s3.Options{
		Region:       settings.S3Region,
		Credentials:  aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(settings.S3AccessKeyID, settings.S3SecretAccessKey, settings.S3SessionToken)),
		BaseEndpoint: aws.String(settings.S3Endpoint),
		UsePathStyle: settings.S3ForcePathStyle,
	}
	return &S3ObjectStore{settings: settings, client: s3.New(options)}, nil
}

func (s *S3ObjectStore) Put(ctx context.Context, key string, data []byte, mimeType string) (string, error) {
	if key = strings.TrimSpace(key); key == "" {
		return "", errors.New("S3 object key is required")
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	out, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.settings.S3Bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String(mimeType),
	})
	if err != nil {
		return "", err
	}
	if out.ETag == nil {
		return "", nil
	}
	return strings.Trim(*out.ETag, `"`), nil
}

func (s *S3ObjectStore) Get(ctx context.Context, key string) (string, []byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.settings.S3Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", nil, err
	}
	defer out.Body.Close()
	data, err := io.ReadAll(io.LimitReader(out.Body, maxStoredImageBytes+1))
	if err != nil {
		return "", nil, err
	}
	if len(data) > maxStoredImageBytes {
		return "", nil, errors.New("S3 object is too large")
	}
	mimeType := ""
	if out.ContentType != nil {
		mimeType = strings.TrimSpace(*out.ContentType)
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	return mimeType, data, nil
}

func (s *S3ObjectStore) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.settings.S3Bucket),
		Key:    aws.String(key),
	})
	return err
}

func (s *S3ObjectStore) Test(ctx context.Context) error {
	key := strings.Trim(s.settings.S3Prefix, "/") + "/.phantom-lancer-test"
	if key == "/.phantom-lancer-test" || key == "" {
		key = ".phantom-lancer-test"
	}
	if _, err := s.Put(ctx, key, []byte("ok"), "text/plain"); err != nil {
		return err
	}
	_ = s.Delete(ctx, key)
	return nil
}
