package dockercontrol

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/go-connections/nat"

	"phantom-lancer/internal/safelog"
)

// Bounds for read-only log/stat retrieval. These cap how much data we ever pull
// from the daemon in a single request so a huge log can never exhaust memory.
const (
	maxLogTailLines   = 1000
	maxLogBytes       = 256 * 1024
	defaultLogTail    = 200
	maxLogStreamLines = 5000
	maxLogStreamBytes = 4 * 1024 * 1024
)

// ErrInvalidContainerID guards write operations against empty identifiers.
var ErrInvalidContainerID = errors.New("container id is required")

var safeContainerNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)

type CreateContainerPortPublish struct {
	ContainerPort int    `json:"containerPort"`
	HostPort      int    `json:"hostPort,omitempty"`
	Protocol      string `json:"protocol,omitempty"`
	HostIP        string `json:"hostIp,omitempty"`
}

type CreateContainerVolumeMount struct {
	VolumeName  string `json:"volumeName"`
	Destination string `json:"destination"`
	ReadOnly    bool   `json:"readOnly,omitempty"`
}

type CreateContainerEnvEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type CreateContainerRequest struct {
	Name          string                       `json:"name"`
	Image         string                       `json:"image"`
	RestartPolicy string                       `json:"restartPolicy,omitempty"`
	Ports         []CreateContainerPortPublish `json:"ports,omitempty"`
	Volumes       []CreateContainerVolumeMount `json:"volumes,omitempty"`
	Env           []CreateContainerEnvEntry    `json:"env,omitempty"`
}

var safeEnvNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var safeVolumeNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)

type ContainerPortSummary struct {
	PrivatePort string `json:"privatePort"`
	Public      string `json:"public,omitempty"`
}

type ContainerMountSummary struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination"`
	Mode        string `json:"mode,omitempty"`
	RW          bool   `json:"rw"`
}

type ContainerNetworkSummary struct {
	Name      string `json:"name"`
	IPAddress string `json:"ipAddress,omitempty"`
}

type ContainerLabelSummary struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type ContainerInspectSummary struct {
	ID           string                    `json:"id"`
	Name         string                    `json:"name"`
	Image        string                    `json:"image"`
	Created      string                    `json:"created,omitempty"`
	State        string                    `json:"state,omitempty"`
	Status       string                    `json:"status,omitempty"`
	Running      bool                      `json:"running"`
	Restarting   bool                      `json:"restarting"`
	ExitCode     int                       `json:"exitCode"`
	StartedAt    string                    `json:"startedAt,omitempty"`
	FinishedAt   string                    `json:"finishedAt,omitempty"`
	Ports        []ContainerPortSummary    `json:"ports,omitempty"`
	Mounts       []ContainerMountSummary   `json:"mounts,omitempty"`
	Networks     []ContainerNetworkSummary `json:"networks,omitempty"`
	Labels       []ContainerLabelSummary   `json:"labels,omitempty"`
	RestartCount int                       `json:"restartCount"`
}

func (s *Service) ContainerInspectSummary(ctx context.Context, id string) (ContainerInspectSummary, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ContainerInspectSummary{}, ErrInvalidContainerID
	}
	cli, err := s.engine()
	if err != nil {
		return ContainerInspectSummary{}, err
	}
	opCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	raw, err := cli.ContainerInspect(opCtx, id)
	if err != nil {
		s.log.Warn("docker container inspect failed", "container", shortID(id), "error", safelog.Error(err, 160))
		return ContainerInspectSummary{}, err
	}
	out := ContainerInspectSummary{
		ID:           shortID(raw.ID),
		Name:         strings.TrimPrefix(raw.Name, "/"),
		Image:        shortID(raw.Image),
		Created:      raw.Created,
		RestartCount: raw.RestartCount,
	}
	if raw.State != nil {
		out.State = raw.State.Status
		out.Status = raw.State.Status
		out.Running = raw.State.Running
		out.Restarting = raw.State.Restarting
		out.ExitCode = raw.State.ExitCode
		out.StartedAt = raw.State.StartedAt
		out.FinishedAt = raw.State.FinishedAt
	}
	if raw.NetworkSettings != nil {
		for port, bindings := range raw.NetworkSettings.Ports {
			row := ContainerPortSummary{PrivatePort: string(port)}
			if len(bindings) > 0 {
				host := strings.TrimSpace(bindings[0].HostIP)
				if host == "" {
					host = "0.0.0.0"
				}
				row.Public = host + ":" + bindings[0].HostPort
			}
			out.Ports = append(out.Ports, row)
		}
		for name, network := range raw.NetworkSettings.Networks {
			out.Networks = append(out.Networks, ContainerNetworkSummary{Name: safelog.Text(name, 80), IPAddress: safelog.Text(network.IPAddress, 80)})
		}
	}
	for _, mount := range raw.Mounts {
		out.Mounts = append(out.Mounts, ContainerMountSummary{
			Type:        string(mount.Type),
			Name:        safelog.Text(mount.Name, 120),
			Source:      safelog.Text(mount.Source, 240),
			Destination: safelog.Text(mount.Destination, 160),
			Mode:        safelog.Text(mount.Mode, 80),
			RW:          mount.RW,
		})
	}
	if raw.Config != nil {
		for key, value := range raw.Config.Labels {
			out.Labels = append(out.Labels, ContainerLabelSummary{Key: safelog.Text(key, 120), Value: redactedLabelValue(key, value)})
		}
	}
	sort.Slice(out.Ports, func(i, j int) bool { return out.Ports[i].PrivatePort < out.Ports[j].PrivatePort })
	sort.Slice(out.Mounts, func(i, j int) bool { return out.Mounts[i].Destination < out.Mounts[j].Destination })
	sort.Slice(out.Networks, func(i, j int) bool { return out.Networks[i].Name < out.Networks[j].Name })
	sort.Slice(out.Labels, func(i, j int) bool { return out.Labels[i].Key < out.Labels[j].Key })
	return out, nil
}

func (s *Service) CreateAndStartContainer(ctx context.Context, req CreateContainerRequest) (string, error) {
	name := strings.TrimSpace(req.Name)
	ref := strings.TrimSpace(req.Image)
	if name == "" || !safeContainerNamePattern.MatchString(name) {
		return "", errors.New("container name is invalid")
	}
	if ref == "" || strings.ContainsAny(ref, " \t\r\n") {
		return "", errors.New("image reference is invalid")
	}
	cli, err := s.engine()
	if err != nil {
		return "", err
	}

	cfg := &container.Config{Image: ref}
	hostCfg := &container.HostConfig{}

	switch strings.ToLower(strings.TrimSpace(req.RestartPolicy)) {
	case "", "no", "none":
		hostCfg.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyDisabled}
	case "always":
		hostCfg.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyAlways}
	case "unless-stopped":
		hostCfg.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyUnlessStopped}
	case "on-failure":
		hostCfg.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyOnFailure, MaximumRetryCount: 5}
	default:
		return "", errors.New("restart policy is invalid")
	}

	if len(req.Ports) > 0 {
		exposed := make(map[nat.Port]struct{}, len(req.Ports))
		bindings := make(map[nat.Port][]nat.PortBinding, len(req.Ports))
		for _, port := range req.Ports {
			if port.ContainerPort <= 0 || port.ContainerPort > 65535 {
				return "", errors.New("container port is out of range")
			}
			if port.HostPort < 0 || port.HostPort > 65535 {
				return "", errors.New("host port is out of range")
			}
			proto := strings.ToLower(strings.TrimSpace(port.Protocol))
			if proto == "" {
				proto = "tcp"
			}
			if proto != "tcp" && proto != "udp" && proto != "sctp" {
				return "", errors.New("port protocol is invalid")
			}
			hostIP := strings.TrimSpace(port.HostIP)
			if hostIP == "" {
				hostIP = "127.0.0.1"
			} else if hostIP != "0.0.0.0" && hostIP != "127.0.0.1" && hostIP != "::" {
				return "", errors.New("host ip is restricted")
			}
			np := nat.Port(fmt.Sprintf("%d/%s", port.ContainerPort, proto))
			exposed[np] = struct{}{}
			bindings[np] = append(bindings[np], nat.PortBinding{
				HostIP:   hostIP,
				HostPort: strconv.Itoa(port.HostPort),
			})
		}
		cfg.ExposedPorts = exposed
		hostCfg.PortBindings = bindings
	}

	if len(req.Volumes) > 0 {
		mounts := make([]mount.Mount, 0, len(req.Volumes))
		for _, vol := range req.Volumes {
			name := strings.TrimSpace(vol.VolumeName)
			dest := strings.TrimSpace(vol.Destination)
			if name == "" || !safeVolumeNamePattern.MatchString(name) {
				return "", errors.New("volume name is invalid")
			}
			if dest == "" || !strings.HasPrefix(dest, "/") {
				return "", errors.New("volume destination is invalid")
			}
			mounts = append(mounts, mount.Mount{
				Type:     mount.TypeVolume,
				Source:   name,
				Target:   dest,
				ReadOnly: vol.ReadOnly,
			})
		}
		hostCfg.Mounts = mounts
	}

	if len(req.Env) > 0 {
		if len(req.Env) > 64 {
			return "", errors.New("too many environment variables")
		}
		environ := make([]string, 0, len(req.Env))
		for _, item := range req.Env {
			name := strings.TrimSpace(item.Name)
			value := strings.TrimSpace(item.Value)
			if name == "" || !safeEnvNamePattern.MatchString(name) {
				return "", errors.New("env name is invalid")
			}
			if len(value) > 4096 {
				return "", errors.New("env value is too long")
			}
			lower := strings.ToLower(name)
			if strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "key") {
				return "", errors.New("env name is restricted: secrets must not be passed via template env")
			}
			environ = append(environ, name+"="+value)
		}
		cfg.Env = environ
	}

	opCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	created, err := cli.ContainerCreate(opCtx, cfg, hostCfg, nil, nil, name)
	if err != nil {
		s.log.Warn("docker container create failed", "image", safelog.Text(ref, 120), "error", safelog.Error(err, 160))
		return "", err
	}
	if err := cli.ContainerStart(opCtx, created.ID, container.StartOptions{}); err != nil {
		s.log.Warn("docker container create start failed", "container", shortID(created.ID), "error", safelog.Error(err, 160))
		return shortID(created.ID), err
	}
	return shortID(created.ID), nil
}

func redactedLabelValue(key, value string) string {
	lower := strings.ToLower(key)
	if strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "key") {
		return "[redacted]"
	}
	return safelog.Text(value, 180)
}

// StartContainer starts an existing container.
func (s *Service) StartContainer(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrInvalidContainerID
	}
	cli, err := s.engine()
	if err != nil {
		return err
	}
	opCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := cli.ContainerStart(opCtx, id, container.StartOptions{}); err != nil {
		s.log.Warn("docker container start failed", "container", shortID(id), "error", safelog.Error(err, 160))
		return err
	}
	return nil
}

// StopContainer gracefully stops a container, allowing the daemon's default
// SIGTERM -> timeout -> SIGKILL sequence.
func (s *Service) StopContainer(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrInvalidContainerID
	}
	cli, err := s.engine()
	if err != nil {
		return err
	}
	opCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := cli.ContainerStop(opCtx, id, container.StopOptions{}); err != nil {
		s.log.Warn("docker container stop failed", "container", shortID(id), "error", safelog.Error(err, 160))
		return err
	}
	return nil
}

// RestartContainer restarts a container.
func (s *Service) RestartContainer(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrInvalidContainerID
	}
	cli, err := s.engine()
	if err != nil {
		return err
	}
	opCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := cli.ContainerRestart(opCtx, id, container.StopOptions{}); err != nil {
		s.log.Warn("docker container restart failed", "container", shortID(id), "error", safelog.Error(err, 160))
		return err
	}
	return nil
}

// KillContainer sends SIGKILL to a container. This is a dangerous operation and
// callers must require explicit confirmation.
func (s *Service) KillContainer(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrInvalidContainerID
	}
	cli, err := s.engine()
	if err != nil {
		return err
	}
	opCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := cli.ContainerKill(opCtx, id, "KILL"); err != nil {
		s.log.Warn("docker container kill failed", "container", shortID(id), "error", safelog.Error(err, 160))
		return err
	}
	return nil
}

// RemoveContainer removes a container. Named volumes are never removed in this
// phase (RemoveVolumes stays false). This is a dangerous operation and callers
// must require explicit confirmation.
func (s *Service) RemoveContainer(ctx context.Context, id string, force bool) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrInvalidContainerID
	}
	cli, err := s.engine()
	if err != nil {
		return err
	}
	opCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := cli.ContainerRemove(opCtx, id, container.RemoveOptions{Force: force, RemoveVolumes: false}); err != nil {
		s.log.Warn("docker container remove failed", "container", shortID(id), "error", safelog.Error(err, 160))
		return err
	}
	return nil
}

// PullImage pulls an image reference and drains the progress stream. The raw
// progress JSON is never persisted; only a completion/failure summary is logged.
func (s *Service) PullImage(ctx context.Context, ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return errors.New("image reference is required")
	}
	cli, err := s.engine()
	if err != nil {
		return err
	}
	opCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	pullOptions := image.PullOptions{}
	registryAuth, err := s.registryAuthForPull(ctx, ref)
	if err != nil {
		s.log.Warn("docker image pull auth selection failed", "ref", safelog.Text(ref, 120), "error", safelog.Error(err, 160))
		return err
	}
	if registryAuth != "" {
		pullOptions.RegistryAuth = registryAuth
	}
	reader, err := cli.ImagePull(opCtx, ref, pullOptions)
	if err != nil {
		s.log.Warn("docker image pull failed", "ref", safelog.Text(ref, 120), "error", safelog.Error(err, 160))
		return err
	}
	defer reader.Close()
	// Drain the progress stream so the pull runs to completion, but cap the
	// total bytes read so a misbehaving daemon cannot stream unbounded output.
	if _, err := io.Copy(io.Discard, io.LimitReader(reader, 32*1024*1024)); err != nil {
		s.log.Warn("docker image pull stream failed", "ref", safelog.Text(ref, 120), "error", safelog.Error(err, 160))
		return err
	}
	return nil
}

// RemoveImage removes a local image by id or reference. Dangerous operation.
func (s *Service) RemoveImage(ctx context.Context, id string, force bool) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("image id is required")
	}
	cli, err := s.engine()
	if err != nil {
		return err
	}
	opCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := cli.ImageRemove(opCtx, id, image.RemoveOptions{Force: force, PruneChildren: true}); err != nil {
		s.log.Warn("docker image remove failed", "image", shortID(id), "error", safelog.Error(err, 160))
		return err
	}
	return nil
}

// LogLine is a single redacted, length-capped container log line.
type LogLine struct {
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

// ContainerLogs returns a bounded, redacted tail of a container's logs. When
// follow is requested, the stream is kept open until ctx completes; otherwise
// it returns a one-shot snapshot capped by maxLogTailLines / maxLogBytes.
func (s *Service) ContainerLogs(ctx context.Context, id string, tail int, follow bool, sink func(LogLine) error) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrInvalidContainerID
	}
	if tail <= 0 {
		tail = defaultLogTail
	}
	if tail > maxLogTailLines {
		tail = maxLogTailLines
	}
	cli, err := s.engine()
	if err != nil {
		return err
	}
	opCtx := ctx
	var cancel context.CancelFunc
	if !follow {
		opCtx, cancel = context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
	}
	reader, err := cli.ContainerLogs(opCtx, id, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Tail:       itoaTail(tail),
		Timestamps: false,
	})
	if err != nil {
		s.log.Warn("docker container logs failed", "container", shortID(id), "error", safelog.Error(err, 160))
		return err
	}
	defer reader.Close()

	limitReader := io.Reader(reader)
	lineCap := maxLogTailLines
	if follow {
		lineCap = maxLogStreamLines
		limitReader = io.LimitReader(reader, maxLogStreamBytes)
	} else {
		limitReader = io.LimitReader(reader, maxLogBytes)
	}
	scanner := bufio.NewScanner(limitReader)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
	count := 0
	for scanner.Scan() {
		raw := scanner.Text()
		stream, text := demuxLogLine(raw)
		text = safelog.Text(text, 2000)
		if err := sink(LogLine{Stream: stream, Text: text}); err != nil {
			return err
		}
		count++
		if count >= lineCap {
			if err := sink(LogLine{Stream: "stdout", Text: "-- reached log read ceiling --"}); err != nil {
				return err
			}
			break
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, bufio.ErrTooLong) && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// ContainerLogsOneShot preserves the historical slice-returning signature for
// callers that want a bounded snapshot rather than a streaming sink.
func (s *Service) ContainerLogsOneShot(ctx context.Context, id string, tail int) ([]LogLine, error) {
	lines := make([]LogLine, 0, 64)
	err := s.ContainerLogs(ctx, id, tail, false, func(line LogLine) error {
		lines = append(lines, line)
		return nil
	})
	return lines, err
}

// Stats is a redacted point-in-time resource snapshot for a container.
type Stats struct {
	CPUPercent    float64 `json:"cpuPercent"`
	MemoryUsage   uint64  `json:"memoryUsageBytes"`
	MemoryLimit   uint64  `json:"memoryLimitBytes"`
	MemoryPercent float64 `json:"memoryPercent"`
}

// ContainerStats returns a one-shot resource snapshot for a container.
func (s *Service) ContainerStats(ctx context.Context, id string) (Stats, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Stats{}, ErrInvalidContainerID
	}
	cli, err := s.engine()
	if err != nil {
		return Stats{}, err
	}
	opCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := cli.ContainerStatsOneShot(opCtx, id)
	if err != nil {
		s.log.Warn("docker container stats failed", "container", shortID(id), "error", safelog.Error(err, 160))
		return Stats{}, err
	}
	defer resp.Body.Close()
	var raw container.StatsResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&raw); err != nil {
		return Stats{}, err
	}
	return computeStats(raw), nil
}

func computeStats(raw container.StatsResponse) Stats {
	stats := Stats{
		MemoryUsage: raw.MemoryStats.Usage,
		MemoryLimit: raw.MemoryStats.Limit,
	}
	cpuDelta := float64(raw.CPUStats.CPUUsage.TotalUsage) - float64(raw.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(raw.CPUStats.SystemUsage) - float64(raw.PreCPUStats.SystemUsage)
	if cpuDelta > 0 && systemDelta > 0 {
		cpus := float64(raw.CPUStats.OnlineCPUs)
		if cpus == 0 {
			cpus = float64(len(raw.CPUStats.CPUUsage.PercpuUsage))
		}
		if cpus == 0 {
			cpus = 1
		}
		stats.CPUPercent = roundTo((cpuDelta/systemDelta)*cpus*100, 2)
	}
	if stats.MemoryLimit > 0 {
		stats.MemoryPercent = roundTo(float64(stats.MemoryUsage)/float64(stats.MemoryLimit)*100, 2)
	}
	return stats
}

// demuxLogLine strips the 8-byte docker multiplexing header when present and
// reports the originating stream.
func demuxLogLine(line string) (string, string) {
	if len(line) >= 8 {
		switch line[0] {
		case 1:
			return "stdout", strings.TrimRight(line[8:], "\r\n")
		case 2:
			return "stderr", strings.TrimRight(line[8:], "\r\n")
		}
	}
	return "stdout", strings.TrimRight(line, "\r\n")
}

func itoaTail(tail int) string {
	if tail <= 0 {
		return "all"
	}
	return strconv.Itoa(tail)
}

func roundTo(value float64, digits int) float64 {
	factor := 1.0
	for i := 0; i < digits; i++ {
		factor *= 10
	}
	return float64(int64(value*factor+0.5)) / factor
}
