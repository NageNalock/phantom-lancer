package objectstore

import (
	"testing"

	"phantom-lancer/internal/storage"
)

func profileWith(bucket, endpoint, accessKey, secret string) storage.ObjectStorageProfile {
	return storage.ObjectStorageProfile{
		Bucket:          bucket,
		Endpoint:        endpoint,
		AccessKeyID:     accessKey,
		SecretAccessKey: secret,
	}
}

func TestEndpointLabelStripsCredentialsAndQuery(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://s3.example.com", "https://s3.example.com"},
		{"https://s3.example.com:9000/path?token=secret", "https://s3.example.com:9000"},
		{"https://key:pass@s3.example.com/bucket", "https://s3.example.com"},
		{"  https://s3.example.com/  ", "https://s3.example.com"},
		{"", ""},
		{"not a url ? with query", "not a url "},
	}
	for _, tc := range cases {
		if got := EndpointLabel(tc.in); got != tc.want {
			t.Fatalf("EndpointLabel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNewRequiresConnectionFields(t *testing.T) {
	if _, err := New(profileWith("", "https://s3.example.com", "AK", "SK")); err == nil {
		t.Fatal("expected error when bucket is empty")
	}
	if _, err := New(profileWith("bucket", "", "AK", "SK")); err == nil {
		t.Fatal("expected error when endpoint is empty")
	}
	if _, err := New(profileWith("bucket", "https://s3.example.com", "", "")); err == nil {
		t.Fatal("expected error when credentials are missing")
	}
	if _, err := New(profileWith("bucket", "https://s3.example.com", "AK", "SK")); err != nil {
		t.Fatalf("expected client to build, got %v", err)
	}
}
