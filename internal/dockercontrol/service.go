// Package dockercontrol provides a controlled plane over the local Docker
// daemon for Phantom Lancer's Docker Host capability. It wraps the Docker
// Engine SDK, exposes redacted summaries only, and routes high-risk host
// operations through bounded jobs and audit-friendly events.
package dockercontrol

import (
	"context"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"

	"phantom-lancer/internal/authlimiter"
	"phantom-lancer/internal/events"
	"phantom-lancer/internal/logsampler"
	"phantom-lancer/internal/safelog"
	"phantom-lancer/internal/storage"
)

// Daemon availability states surfaced to the UI.
const (
	StateAvailable   = "available"
	StateUnavailable = "unavailable"
)

// Service is the read-only Docker Host control plane. The Docker client is
// created lazily and cached; callers never receive the raw client.
type Service struct {
	store           *storage.Store
	hub             *events.Hub
	log             *slog.Logger
	registryDataDir string

	mu      sync.Mutex
	client  *client.Client
	jobs    map[string]Job
	cancels map[string]context.CancelFunc

	// registryGC serializes registry writes against garbage collection.
	// Blob/manifest writes take a read lock (so concurrent pushes are allowed),
	// while GC takes the write lock to run exclusively and avoid reclaiming a
	// blob that an in-flight push has just committed but not yet referenced.
	registryGC sync.RWMutex

	// registryAuthBackoff rate-limits repeated Basic Auth failures on the
	// `/v2/` native registry endpoint. It is keyed by username (for the
	// targeted credential) and by remote IP (for blanket spray attacks).
	// Successes clear the per-credential counter so a legitimate user
	// never backoff after one bad password.
	registryAuthBackoff *authlimiter.Backoff

	// registryAuthSuccessSampler rate-limits the low-severity
	// docker.registry.auth.succeeded audit row. Docker daemon + pullers
	// call /v2/ on every layer fetch; recording every successful auth
	// blows past the interesting (failed/backoff/forbidden) events on the
	// Audit page. Key = credential ID, allow once per hour.
	registryAuthSuccessSampler *logsampler.Sampler

	// logSampler gates hot-path warnings (job event append failures,
	// repeated daemon probe / List* errors) so transient DB or daemon
	// outages never flood the logs with per-iteration Warn entries.
	logSampler *logsampler.Sampler

	// pullSem limits concurrent image pull jobs to avoid starving the
	// host CPU / containerd during layer decompression. Jobs beyond the
	// limit stay in "queued" state until a slot frees up.
	pullSem chan struct{}
}

// DefaultMaxConcurrentPulls is the default maximum number of image pull
// jobs allowed to run simultaneously. Pulling is CPU-intensive due to
// layer decompression in containerd, so a low default protects small hosts.
const DefaultMaxConcurrentPulls = 1

// NewService builds the Docker control service. The Docker client is not
// created until first use so startup never blocks on a missing daemon.
func NewService(store *storage.Store, hub *events.Hub, dataDir string, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	svc := &Service{
		store:                      store,
		hub:                        hub,
		log:                        logger,
		registryDataDir:            dataDir,
		jobs:                       make(map[string]Job),
		cancels:                    make(map[string]context.CancelFunc),
		registryAuthBackoff:        authlimiter.NewBackoff(0),
		registryAuthSuccessSampler: logsampler.New(1 * time.Hour),
		logSampler:                 logsampler.New(2 * time.Second),
		pullSem:                    make(chan struct{}, DefaultMaxConcurrentPulls),
	}
	if store != nil {
		if values, err := store.GetSettingsByPrefix(context.Background(), "docker."); err == nil {
			if v := strings.TrimSpace(values["docker.pull_concurrency"]); v != "" {
				if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 10 {
					svc.pullSem = make(chan struct{}, n)
				}
			}
		}
	}
	return svc
}

// Close releases the cached Docker client.
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		_ = s.client.Close()
		s.client = nil
	}
}

func (s *Service) engine() (*client.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		return s.client, nil
	}
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	s.client = cli
	return cli, nil
}

// Status is the redacted daemon summary surfaced on the Docker Overview.
type Status struct {
	State         string `json:"state"`
	Available     bool   `json:"available"`
	ServerVersion string `json:"serverVersion,omitempty"`
	APIVersion    string `json:"apiVersion,omitempty"`
	OS            string `json:"os,omitempty"`
	Architecture  string `json:"architecture,omitempty"`
	StorageDriver string `json:"storageDriver,omitempty"`
	Rootless      bool   `json:"rootless"`
	Containers    int    `json:"containers"`
	ContainersRun int    `json:"containersRunning"`
	Images        int    `json:"images"`
	LastError     string `json:"lastError,omitempty"`
	LastCheckedAt string `json:"lastCheckedAt"`
}

// Status probes the daemon and returns a redacted summary. A missing or
// unreachable daemon is reported as unavailable with a sanitized reason rather
// than an error, so the UI can render an actionable empty state.
func (s *Service) Status(ctx context.Context) Status {
	status := Status{State: StateUnavailable, LastCheckedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	cli, err := s.engine()
	if err != nil {
		status.LastError = safelog.Error(err, 200)
		return status
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	info, err := cli.Info(probeCtx)
	if err != nil {
		status.LastError = safelog.Error(err, 200)
		if s.logSampler.Allow("docker:daemon-probe") {
			s.log.Warn("docker daemon probe failed", "error", safelog.Error(err, 160))
		}
		return status
	}
	status.State = StateAvailable
	status.Available = true
	status.ServerVersion = info.ServerVersion
	status.APIVersion = cli.ClientVersion()
	status.OS = info.OperatingSystem
	status.Architecture = info.Architecture
	status.StorageDriver = info.Driver
	status.Containers = info.Containers
	status.ContainersRun = info.ContainersRunning
	status.Images = info.Images
	for _, pair := range info.SecurityOptions {
		if strings.Contains(pair, "rootless") {
			status.Rootless = true
		}
	}
	return status
}

// ContainerSummary is a redacted container list row.
type ContainerSummary struct {
	ID      string   `json:"id"`
	Names   []string `json:"names"`
	Image   string   `json:"image"`
	State   string   `json:"state"`
	Status  string   `json:"status"`
	Created int64    `json:"created"`
	Ports   []string `json:"ports,omitempty"`
}

// ListContainers returns all containers (running and stopped) as redacted rows.
func (s *Service) ListContainers(ctx context.Context) ([]ContainerSummary, error) {
	cli, err := s.engine()
	if err != nil {
		return nil, err
	}
	listCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	items, err := cli.ContainerList(listCtx, container.ListOptions{All: true})
	if err != nil {
		if s.logSampler.Allow("docker:list-containers") {
			s.log.Warn("docker container list failed", "error", safelog.Error(err, 160))
		}
		return nil, err
	}
	out := make([]ContainerSummary, 0, len(items))
	for _, item := range items {
		row := ContainerSummary{
			ID:      shortID(item.ID),
			Names:   cleanContainerNames(item.Names),
			Image:   item.Image,
			State:   item.State,
			Status:  item.Status,
			Created: item.Created,
		}
		for _, port := range item.Ports {
			row.Ports = append(row.Ports, formatPort(port.IP, port.PublicPort, port.PrivatePort, port.Type))
		}
		out = append(out, row)
	}
	return out, nil
}

// ImageSummary is a redacted image list row.
type ImageSummary struct {
	ID        string   `json:"id"`
	Tags      []string `json:"tags,omitempty"`
	Created   int64    `json:"created"`
	SizeBytes int64    `json:"sizeBytes"`
	UsedBy    []string `json:"usedBy,omitempty"`
}

type containerRef struct {
	ShortID string
	Name    string
	Image   string
	ImageID string
	State   string
}

func (s *Service) listContainerRefs(ctx context.Context) ([]containerRef, error) {
	cli, err := s.engine()
	if err != nil {
		return nil, err
	}
	listCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	items, err := cli.ContainerList(listCtx, container.ListOptions{All: true})
	if err != nil {
		if s.logSampler.Allow("docker:list-containers") {
			s.log.Warn("docker container list failed", "error", safelog.Error(err, 160))
		}
		return nil, err
	}
	out := make([]containerRef, 0, len(items))
	for _, item := range items {
		name := ""
		if len(item.Names) > 0 {
			name = strings.TrimPrefix(item.Names[0], "/")
		}
		out = append(out, containerRef{
			ShortID: shortID(item.ID),
			Name:    name,
			Image:   item.Image,
			ImageID: shortID(item.ImageID),
			State:   item.State,
		})
	}
	return out, nil
}

// ListImages returns local images as redacted rows, including references to
// containers that reference each image (used for delete confirmation and
// network-object relationships in the UI).
func (s *Service) ListImages(ctx context.Context) ([]ImageSummary, error) {
	cli, err := s.engine()
	if err != nil {
		return nil, err
	}
	listCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	items, err := cli.ImageList(listCtx, image.ListOptions{})
	if err != nil {
		if s.logSampler.Allow("docker:list-images") {
			s.log.Warn("docker image list failed", "error", safelog.Error(err, 160))
		}
		return nil, err
	}
	containers, _ := s.listContainerRefs(ctx)
	imageIDIndex := make(map[string][]string)
	imageTagIndex := make(map[string][]string)
	for _, c := range containers {
		label := c.Name
		if label == "" {
			label = c.ShortID
		}
		if c.ImageID != "" {
			imageIDIndex[c.ImageID] = append(imageIDIndex[c.ImageID], label)
		}
		if c.Image != "" {
			imageTagIndex[c.Image] = append(imageTagIndex[c.Image], label)
		}
	}
	out := make([]ImageSummary, 0, len(items))
	for _, item := range items {
		row := ImageSummary{
			ID:        shortID(item.ID),
			Tags:      item.RepoTags,
			Created:   item.Created,
			SizeBytes: item.Size,
		}
		seen := make(map[string]struct{})
		for _, name := range imageIDIndex[shortID(item.ID)] {
			seen[name] = struct{}{}
		}
		for _, tag := range item.RepoTags {
			for _, name := range imageTagIndex[tag] {
				seen[name] = struct{}{}
			}
		}
		for name := range seen {
			row.UsedBy = append(row.UsedBy, name)
		}
		sort.Strings(row.UsedBy)
		out = append(out, row)
	}
	return out, nil
}

// VolumeSummary is a redacted volume list row.
type VolumeSummary struct {
	Name       string `json:"name"`
	Driver     string `json:"driver"`
	Mountpoint string `json:"mountpoint,omitempty"`
	CreatedAt  string `json:"createdAt,omitempty"`
}

// ListVolumes returns volumes as redacted rows.
func (s *Service) ListVolumes(ctx context.Context) ([]VolumeSummary, error) {
	cli, err := s.engine()
	if err != nil {
		return nil, err
	}
	listCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	resp, err := cli.VolumeList(listCtx, volume.ListOptions{})
	if err != nil {
		if s.logSampler.Allow("docker:list-volumes") {
			s.log.Warn("docker volume list failed", "error", safelog.Error(err, 160))
		}
		return nil, err
	}
	out := make([]VolumeSummary, 0, len(resp.Volumes))
	for _, item := range resp.Volumes {
		if item == nil {
			continue
		}
		out = append(out, VolumeSummary{
			Name:       item.Name,
			Driver:     item.Driver,
			Mountpoint: item.Mountpoint,
			CreatedAt:  item.CreatedAt,
		})
	}
	return out, nil
}

// NetworkSummary is a redacted network list row.
type NetworkSummary struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Driver string   `json:"driver"`
	Scope  string   `json:"scope"`
	UsedBy []string `json:"usedBy,omitempty"`
}

// ListNetworks returns networks as redacted rows, including the list of
// containers currently attached to each network.
func (s *Service) ListNetworks(ctx context.Context) ([]NetworkSummary, error) {
	cli, err := s.engine()
	if err != nil {
		return nil, err
	}
	listCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	items, err := cli.NetworkList(listCtx, network.ListOptions{})
	if err != nil {
		if s.logSampler.Allow("docker:list-networks") {
			s.log.Warn("docker network list failed", "error", safelog.Error(err, 160))
		}
		return nil, err
	}
	containers, _ := s.listContainerRefs(ctx)
	networkToContainers := make(map[string]map[string]struct{})
	for _, c := range containers {
		label := c.Name
		if label == "" {
			label = c.ShortID
		}
		inspectCtx, inspectCancel := context.WithTimeout(ctx, 3*time.Second)
		raw, inspectErr := cli.ContainerInspect(inspectCtx, c.ShortID)
		inspectCancel()
		if inspectErr != nil || raw.NetworkSettings == nil {
			continue
		}
		for name := range raw.NetworkSettings.Networks {
			if networkToContainers[name] == nil {
				networkToContainers[name] = make(map[string]struct{})
			}
			networkToContainers[name][label] = struct{}{}
		}
	}
	out := make([]NetworkSummary, 0, len(items))
	for _, item := range items {
		row := NetworkSummary{
			ID:     shortID(item.ID),
			Name:   item.Name,
			Driver: item.Driver,
			Scope:  item.Scope,
		}
		seen := networkToContainers[item.Name]
		for label := range seen {
			row.UsedBy = append(row.UsedBy, label)
		}
		sort.Strings(row.UsedBy)
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func cleanContainerNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, strings.TrimPrefix(name, "/"))
	}
	return out
}

func formatPort(ip string, public, private uint16, proto string) string {
	if public > 0 {
		host := ip
		if host == "" {
			host = "0.0.0.0"
		}
		return host + ":" + strconv.Itoa(int(public)) + "->" + strconv.Itoa(int(private)) + "/" + proto
	}
	return strconv.Itoa(int(private)) + "/" + proto
}
