package dockercontrol

import (
	"net/http"
	"testing"

	"github.com/docker/docker/api/types/container"

	"phantom-lancer/internal/storage"
)

func TestShortID(t *testing.T) {
	cases := map[string]string{
		"sha256:abcdef0123456789": "abcdef012345",
		"abcdef0123456789":        "abcdef012345",
		"short":                   "short",
		"":                        "",
	}
	for in, want := range cases {
		if got := shortID(in); got != want {
			t.Fatalf("shortID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatPort(t *testing.T) {
	if got := formatPort("0.0.0.0", 8080, 80, "tcp"); got != "0.0.0.0:8080->80/tcp" {
		t.Fatalf("published port = %q", got)
	}
	if got := formatPort("", 8080, 80, "tcp"); got != "0.0.0.0:8080->80/tcp" {
		t.Fatalf("published port default host = %q", got)
	}
	if got := formatPort("", 0, 5432, "tcp"); got != "5432/tcp" {
		t.Fatalf("internal-only port = %q", got)
	}
}

func TestCleanContainerNames(t *testing.T) {
	got := cleanContainerNames([]string{"/web", "/db"})
	if len(got) != 2 || got[0] != "web" || got[1] != "db" {
		t.Fatalf("cleanContainerNames = %v", got)
	}
}

func TestComputeStats(t *testing.T) {
	var raw container.StatsResponse
	raw.CPUStats.CPUUsage.TotalUsage = 200
	raw.PreCPUStats.CPUUsage.TotalUsage = 100
	raw.CPUStats.SystemUsage = 2000
	raw.PreCPUStats.SystemUsage = 1000
	raw.CPUStats.OnlineCPUs = 2
	raw.MemoryStats.Usage = 50 * 1024 * 1024
	raw.MemoryStats.Limit = 100 * 1024 * 1024

	stats := computeStats(raw)
	// cpuDelta=100, systemDelta=1000, cpus=2 => 0.1*2*100 = 20%
	if stats.CPUPercent != 20 {
		t.Fatalf("CPUPercent = %v, want 20", stats.CPUPercent)
	}
	if stats.MemoryPercent != 50 {
		t.Fatalf("MemoryPercent = %v, want 50", stats.MemoryPercent)
	}
}

func TestComputeStatsHandlesZeroSystemDelta(t *testing.T) {
	var raw container.StatsResponse
	raw.MemoryStats.Usage = 10
	raw.MemoryStats.Limit = 0
	stats := computeStats(raw)
	if stats.CPUPercent != 0 || stats.MemoryPercent != 0 {
		t.Fatalf("expected zero percentages, got %+v", stats)
	}
}

func TestDockerFamily(t *testing.T) {
	cases := []struct {
		release osRelease
		want    string
	}{
		{release: osRelease{ID: "ubuntu"}, want: "debian"},
		{release: osRelease{ID: "opencloudos"}, want: "rhel"},
		{release: osRelease{ID: "custom", IDLike: []string{"rhel", "fedora"}}, want: "rhel"},
		{release: osRelease{ID: "custom"}, want: ""},
	}
	for _, tc := range cases {
		if got := dockerFamily(tc.release); got != tc.want {
			t.Fatalf("dockerFamily(%+v) = %q, want %q", tc.release, got, tc.want)
		}
	}
}

func TestPrivilegedCommandUsesNonInteractiveSudo(t *testing.T) {
	name, args := privilegedCommand("sudo", "systemctl", "restart", "docker")
	if name != "sudo" {
		t.Fatalf("name = %q, want sudo", name)
	}
	want := []string{"-n", "systemctl", "restart", "docker"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args = %v, want %v", args, want)
		}
	}
}

func TestInstallPreviewUsesPublicDockerSource(t *testing.T) {
	lines := installPreview("rhel", "sudo")
	if len(lines) == 0 {
		t.Fatal("expected install preview")
	}
	found := false
	for _, line := range lines {
		if line == "sudo -n dnf/yum config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected public Docker source in preview, got %v", lines)
	}
}

func TestValidateRegistrySettingsRejectsTokenURL(t *testing.T) {
	err := validateRegistrySettings(storage.DockerRegistrySettings{PublicURL: "https://registry.example.com?token=secret", RequireTLS: true, StorageBackend: "local", ObjectPrefix: "phantom-lancer/docker-registry"})
	if err == nil {
		t.Fatal("expected token-bearing public URL to be rejected")
	}
}

func TestValidateRegistrySettingsRejectsInsecurePublicHost(t *testing.T) {
	err := validateRegistrySettings(storage.DockerRegistrySettings{PublicURL: "http://registry.example.com", RequireTLS: false, AllowInsecureLocal: true, StorageBackend: "local", ObjectPrefix: "phantom-lancer/docker-registry"})
	if err == nil {
		t.Fatal("expected public insecure registry URL to be rejected")
	}
	if err := validateRegistrySettings(storage.DockerRegistrySettings{PublicURL: "http://127.0.0.1:5443", RequireTLS: false, AllowInsecureLocal: true, StorageBackend: "local", ObjectPrefix: "phantom-lancer/docker-registry"}); err != nil {
		t.Fatalf("local insecure registry should be allowed with explicit flag: %v", err)
	}
}

func TestValidateRepositoryName(t *testing.T) {
	if err := validateRepositoryName("personal/my-app"); err != nil {
		t.Fatalf("valid repository rejected: %v", err)
	}
	if err := validateRepositoryName("../secret"); err == nil {
		t.Fatal("expected invalid repository name")
	}
}

func TestSafeContainerNamePattern(t *testing.T) {
	if !safeContainerNamePattern.MatchString("managed-app_01") {
		t.Fatal("expected safe container name")
	}
	if safeContainerNamePattern.MatchString("../host") {
		t.Fatal("expected unsafe container name to be rejected")
	}
}

func TestRegistryObjectKeysUseDockerPrefix(t *testing.T) {
	settings := storage.DockerRegistrySettings{ObjectPrefix: "phantom-lancer/docker-registry"}
	got := blobKey(settings, "sha256:abcdef")
	if got != "phantom-lancer/docker-registry/blobs/sha256/ab/abcdef" {
		t.Fatalf("blob key = %q", got)
	}
}

func TestParseByteRange(t *testing.T) {
	start, length, ok := parseByteRange("bytes=10-19", 100)
	if !ok || start != 10 || length != 10 {
		t.Fatalf("unexpected range: start=%d length=%d ok=%v", start, length, ok)
	}
	if _, _, ok := parseByteRange("bytes=200-300", 100); ok {
		t.Fatal("out-of-bounds range should be rejected")
	}
}

func TestRegistryReadOnlyRequest(t *testing.T) {
	if !registryReadOnlyRequest(&http.Request{Method: http.MethodGet}) || !registryReadOnlyRequest(&http.Request{Method: http.MethodHead}) {
		t.Fatal("GET and HEAD should be read-only")
	}
	if registryReadOnlyRequest(&http.Request{Method: http.MethodPut}) {
		t.Fatal("PUT must not be read-only")
	}
}
