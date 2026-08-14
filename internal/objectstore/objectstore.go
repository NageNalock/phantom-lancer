// Package objectstore provides a reusable S3-compatible object storage client
// built from a shared storage.ObjectStorageProfile. It is intentionally module
// agnostic: callers (Images, Docker Registry, ...) own their own key prefixes,
// size limits and fallback policy. Secrets never leave this package except as
// the AWS SDK credentials it constructs.
package objectstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"phantom-lancer/internal/storage"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Client wraps an S3 client bound to one object storage profile.
type Client struct {
	profile storage.ObjectStorageProfile
	client  *s3.Client
}

// New builds a client from a profile after validating required connection
// fields. It never logs or returns secret material.
func New(profile storage.ObjectStorageProfile) (*Client, error) {
	profile = storage.NormalizeObjectStorageProfile(profile)
	if profile.Bucket == "" {
		return nil, errors.New("object storage bucket is required")
	}
	if profile.Endpoint == "" {
		return nil, errors.New("object storage endpoint is required")
	}
	if profile.AccessKeyID == "" || profile.SecretAccessKey == "" {
		return nil, errors.New("object storage credentials are required")
	}
	region := profile.Region
	if region == "" {
		region = "auto"
	}
	options := s3.Options{
		Region:                     region,
		Credentials:                aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(profile.AccessKeyID, profile.SecretAccessKey, profile.SessionToken)),
		BaseEndpoint:               aws.String(profile.Endpoint),
		UsePathStyle:               profile.ForcePathStyle,
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
	}
	return &Client{profile: profile, client: s3.New(options)}, nil
}

// Bucket returns the bound bucket name.
func (c *Client) Bucket() string { return c.profile.Bucket }

// Region returns the bound region.
func (c *Client) Region() string { return c.profile.Region }

// EndpointLabel returns a safe endpoint label (scheme + host, no query) for
// persistence and display.
func (c *Client) EndpointLabel() string { return EndpointLabel(c.profile.Endpoint) }

// Put uploads bytes under key with the given content type and returns the ETag.
func (c *Client) Put(ctx context.Context, key string, data []byte, contentType string) (string, error) {
	if key = strings.TrimSpace(key); key == "" {
		return "", errors.New("object key is required")
	}
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	out, err := c.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.profile.Bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", err
	}
	if out.ETag == nil {
		return "", nil
	}
	return strings.Trim(*out.ETag, `"`), nil
}

// PutReader uploads a stream under key. Callers own size limits and digest
// verification before invoking this method.
func (c *Client) PutReader(ctx context.Context, key string, body io.Reader, size int64, contentType string) (string, error) {
	if key = strings.TrimSpace(key); key == "" {
		return "", errors.New("object key is required")
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	input := &transfermanager.UploadObjectInput{
		Bucket:      aws.String(c.profile.Bucket),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(contentType),
	}
	if size >= 0 {
		input.ContentLength = aws.Int64(size)
	}
	uploader := transfermanager.New(c.client)
	out, err := uploader.UploadObject(ctx, input)
	if err != nil {
		return "", err
	}
	if out.ETag == nil {
		return "", nil
	}
	return strings.Trim(*out.ETag, `"`), nil
}

// PutStream uploads an unknown-length stream with a bounded multipart worker.
// It is intended for producers such as gzip writers that must not spool the
// complete object locally before upload.
func (c *Client) PutStream(ctx context.Context, key string, body io.Reader, contentType string, partSize int64) (string, error) {
	if key = strings.TrimSpace(key); key == "" {
		return "", errors.New("object key is required")
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if partSize <= 0 {
		partSize = 8 << 20
	}
	uploader := transfermanager.New(c.client, func(options *transfermanager.Options) {
		options.PartSizeBytes = partSize
		options.MultipartUploadThreshold = partSize
		options.Concurrency = 1
	})
	out, err := uploader.UploadObject(ctx, &transfermanager.UploadObjectInput{
		Bucket:      aws.String(c.profile.Bucket),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", err
	}
	if out.ETag == nil {
		return "", nil
	}
	return strings.Trim(*out.ETag, `"`), nil
}

// Get downloads an object, reading at most maxBytes (a value <= 0 disables the
// limit). It returns the resolved content type and bytes.
func (c *Client) Get(ctx context.Context, key string, maxBytes int64) (string, []byte, error) {
	out, err := c.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.profile.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", nil, err
	}
	defer out.Body.Close()
	var reader io.Reader = out.Body
	if maxBytes > 0 {
		reader = io.LimitReader(out.Body, maxBytes+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", nil, err
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return "", nil, errors.New("object is larger than the allowed maximum")
	}
	contentType := ""
	if out.ContentType != nil {
		contentType = strings.TrimSpace(*out.ContentType)
	}
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	return contentType, data, nil
}

// Open streams an object. The caller must close the returned body.
func (c *Client) Open(ctx context.Context, key string, rangeHeader string) (string, int64, io.ReadCloser, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(c.profile.Bucket),
		Key:    aws.String(strings.TrimSpace(key)),
	}
	if strings.TrimSpace(rangeHeader) != "" {
		input.Range = aws.String(rangeHeader)
	}
	out, err := c.client.GetObject(ctx, input)
	if err != nil {
		return "", 0, nil, err
	}
	contentType := ""
	if out.ContentType != nil {
		contentType = strings.TrimSpace(*out.ContentType)
	}
	size := int64(0)
	if out.ContentLength != nil {
		size = *out.ContentLength
	}
	return contentType, size, out.Body, nil
}

// Head returns object metadata without downloading the body.
func (c *Client) Head(ctx context.Context, key string) (string, int64, error) {
	out, err := c.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.profile.Bucket),
		Key:    aws.String(strings.TrimSpace(key)),
	})
	if err != nil {
		return "", 0, err
	}
	contentType := ""
	if out.ContentType != nil {
		contentType = strings.TrimSpace(*out.ContentType)
	}
	size := int64(0)
	if out.ContentLength != nil {
		size = *out.ContentLength
	}
	return contentType, size, nil
}

// Delete removes an object by key.
func (c *Client) Delete(ctx context.Context, key string) error {
	_, err := c.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.profile.Bucket),
		Key:    aws.String(key),
	})
	return err
}

type ObjectInfo struct {
	Key      string
	Size     int64
	Modified time.Time
}

// List returns a bounded page of objects below prefix. It is intended for
// module-owned prefixes such as Docker Registry usage calculation and GC.
func (c *Client) List(ctx context.Context, prefix string, limit int32) ([]ObjectInfo, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	var out []ObjectInfo
	var token *string
	for {
		resp, err := c.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(c.profile.Bucket),
			Prefix:            aws.String(strings.Trim(strings.TrimSpace(prefix), "/")),
			MaxKeys:           aws.Int32(limit),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, err
		}
		for _, item := range resp.Contents {
			if item.Key == nil {
				continue
			}
			size := int64(0)
			if item.Size != nil {
				size = *item.Size
			}
			info := ObjectInfo{Key: *item.Key, Size: size}
			if item.LastModified != nil {
				info.Modified = *item.LastModified
			}
			out = append(out, info)
		}
		if resp.IsTruncated == nil || !*resp.IsTruncated || resp.NextContinuationToken == nil {
			break
		}
		token = resp.NextContinuationToken
	}
	return out, nil
}

func IsNotFound(err error) bool {
	var notFound *types.NotFound
	if errors.As(err, &notFound) {
		return true
	}
	code := strings.ToLower(errString(err))
	return strings.Contains(code, "notfound") || strings.Contains(code, "no such key") || strings.Contains(code, "status code: 404")
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return strconv.Quote(err.Error())
}

// Test writes and deletes a short probe object under prefix to validate the
// connection. The probe object is always cleaned up.
func (c *Client) Test(ctx context.Context, prefix string) error {
	key := strings.Trim(strings.TrimSpace(prefix), "/")
	if key == "" {
		key = ".phantom-lancer-test"
	} else {
		key += "/.phantom-lancer-test"
	}
	if _, err := c.Put(ctx, key, []byte("ok"), "text/plain"); err != nil {
		return err
	}
	_ = c.Delete(ctx, key)
	return nil
}

// EndpointLabel strips credentials and query from an endpoint URL, returning a
// safe scheme://host[:port] label suitable for persistence and audit. Inputs
// that do not parse fall back to the trimmed raw value with any query removed.
func EndpointLabel(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		if idx := strings.IndexAny(endpoint, "?#"); idx >= 0 {
			return endpoint[:idx]
		}
		return endpoint
	}
	return parsed.Scheme + "://" + parsed.Host
}
