package dockercontrol

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"

	"phantom-lancer/internal/safelog"
)

// Bounds for read-only log/stat retrieval. These cap how much data we ever pull
// from the daemon in a single request so a huge log can never exhaust memory.
const (
	maxLogTailLines = 1000
	maxLogBytes     = 256 * 1024
	defaultLogTail  = 200
)

// ErrInvalidContainerID guards write operations against empty identifiers.
var ErrInvalidContainerID = errors.New("container id is required")

var safeContainerNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)

type CreateContainerRequest struct {
	Name  string `json:"name"`
	Image string `json:"image"`
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
	opCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	created, err := cli.ContainerCreate(opCtx, &container.Config{Image: ref}, &container.HostConfig{}, nil, nil, name)
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
	reader, err := cli.ImagePull(opCtx, ref, image.PullOptions{})
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

// ContainerLogs returns a bounded, redacted tail of a container's logs. It never
// follows the stream and never returns more than maxLogTailLines / maxLogBytes.
func (s *Service) ContainerLogs(ctx context.Context, id string, tail int) ([]LogLine, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrInvalidContainerID
	}
	if tail <= 0 {
		tail = defaultLogTail
	}
	if tail > maxLogTailLines {
		tail = maxLogTailLines
	}
	cli, err := s.engine()
	if err != nil {
		return nil, err
	}
	opCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	reader, err := cli.ContainerLogs(opCtx, id, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       itoaTail(tail),
		Timestamps: false,
	})
	if err != nil {
		s.log.Warn("docker container logs failed", "container", shortID(id), "error", safelog.Error(err, 160))
		return nil, err
	}
	defer reader.Close()
	return readBoundedLogs(reader)
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

// readBoundedLogs decodes the multiplexed docker log stream into redacted,
// length-capped lines, stopping at the byte/line ceiling.
func readBoundedLogs(reader io.Reader) ([]LogLine, error) {
	lines := make([]LogLine, 0, 64)
	scanner := bufio.NewScanner(io.LimitReader(reader, maxLogBytes))
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
	for scanner.Scan() {
		raw := scanner.Text()
		stream, text := demuxLogLine(raw)
		text = safelog.Text(text, 2000)
		lines = append(lines, LogLine{Stream: stream, Text: text})
		if len(lines) >= maxLogTailLines {
			break
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, bufio.ErrTooLong) {
		return lines, err
	}
	return lines, nil
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
