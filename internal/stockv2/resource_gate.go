package stockv2

import (
	"bufio"
	"errors"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"syscall"
)

type ResourceGateState string

const (
	ResourceGateNormal    ResourceGateState = "normal"
	ResourceGateThrottled ResourceGateState = "throttled"
	ResourceGatePaused    ResourceGateState = "paused"

	resourceGateNormalMemoryBytes               = uint64(1 << 30)
	resourceGatePauseMemoryBytes                = uint64(768 << 20)
	resourceGateHardMemoryBytes                 = uint64(512 << 20)
	resourceGateMinDiskBytes                    = uint64(10 << 30)
	resourceGateMaxLoad1                        = 1.5
	resourceGateNormalMaintenanceConcurrency    = 2
	resourceGateThrottledMaintenanceConcurrency = 1

	resourceGateMeminfoPath = "/proc/meminfo"
	resourceGateLoadavgPath = "/proc/loadavg"
)

type ResourceGateMetrics struct {
	MemAvailableBytes  uint64  `json:"memAvailableBytes,omitempty"`
	MemoryKnown        bool    `json:"memoryKnown"`
	Load1              float64 `json:"load1,omitempty"`
	LoadKnown          bool    `json:"loadKnown"`
	DiskAvailableBytes uint64  `json:"diskAvailableBytes,omitempty"`
	DiskKnown          bool    `json:"diskKnown"`
}

type ResourceGateStatus struct {
	State       ResourceGateState   `json:"state"`
	Reasons     []string            `json:"reasons,omitempty"`
	Diagnostics []string            `json:"diagnostics,omitempty"`
	Metrics     ResourceGateMetrics `json:"metrics"`
}

// EvaluateResourceGate is deterministic so scheduling code can share the same
// thresholds without performing I/O. Unknown metrics are deliberately ignored:
// ponytail: a transient /proc or statfs read failure must not disable all
// background work; callers still receive a diagnostic from ReadResourceGate.
func EvaluateResourceGate(metrics ResourceGateMetrics) ResourceGateStatus {
	status := ResourceGateStatus{State: ResourceGateNormal, Metrics: metrics}
	paused := false
	throttled := false

	if metrics.MemoryKnown {
		switch {
		case metrics.MemAvailableBytes < resourceGateHardMemoryBytes:
			paused = true
			status.Reasons = append(status.Reasons, "memory_critical")
		case metrics.MemAvailableBytes < resourceGatePauseMemoryBytes:
			paused = true
			status.Reasons = append(status.Reasons, "memory_low")
		case metrics.MemAvailableBytes < resourceGateNormalMemoryBytes:
			throttled = true
			status.Reasons = append(status.Reasons, "memory_throttled")
		}
	}
	if metrics.LoadKnown && metrics.Load1 >= resourceGateMaxLoad1 {
		throttled = true
		status.Reasons = append(status.Reasons, "load_high")
	}
	if metrics.DiskKnown && metrics.DiskAvailableBytes < resourceGateMinDiskBytes {
		paused = true
		status.Reasons = append(status.Reasons, "disk_low")
	}

	if paused {
		status.State = ResourceGatePaused
	} else if throttled {
		status.State = ResourceGateThrottled
	}
	return status
}

// ReadResourceGate reads the Linux host metrics used by bounded background
// workers. It has no logging side effects, so frequent callers cannot create a
// diagnostic log loop.
func ReadResourceGate(diskPath string) ResourceGateStatus {
	return readResourceGate(resourceGateMeminfoPath, resourceGateLoadavgPath, diskPath)
}

func (s *Service) currentResourceGate() ResourceGateStatus {
	if s != nil && s.resourceGateReader != nil {
		return s.resourceGateReader()
	}
	diskPath := "."
	if s != nil && s.store != nil {
		if path := strings.TrimSpace(s.store.MarketDBPath()); path != "" && path != ":memory:" {
			diskPath = path
		}
	}
	return ReadResourceGate(diskPath)
}

func maintenanceConcurrencyForResourceGate(status ResourceGateStatus) int {
	if status.State == ResourceGatePaused {
		return 0
	}
	if status.State == ResourceGateThrottled {
		return resourceGateThrottledMaintenanceConcurrency
	}
	return resourceGateNormalMaintenanceConcurrency
}

func resourceGatePauseMessage(status ResourceGateStatus) string {
	details := status.Reasons
	if len(details) == 0 {
		details = status.Diagnostics
	}
	if len(details) == 0 {
		return "resource gate paused"
	}
	return "resource gate paused: " + strings.Join(details, ",")
}

func readResourceGate(meminfoPath, loadavgPath, diskPath string) ResourceGateStatus {
	var metrics ResourceGateMetrics
	diagnostics := make([]string, 0, 3)

	if file, err := os.Open(meminfoPath); err != nil {
		diagnostics = append(diagnostics, "meminfo_unavailable")
	} else {
		available, parseErr := parseMemAvailable(file)
		_ = file.Close()
		if parseErr != nil {
			diagnostics = append(diagnostics, "meminfo_invalid")
		} else {
			metrics.MemAvailableBytes = available
			metrics.MemoryKnown = true
		}
	}

	if file, err := os.Open(loadavgPath); err != nil {
		diagnostics = append(diagnostics, "loadavg_unavailable")
	} else {
		load1, parseErr := parseLoad1(file)
		_ = file.Close()
		if parseErr != nil {
			diagnostics = append(diagnostics, "loadavg_invalid")
		} else {
			metrics.Load1 = load1
			metrics.LoadKnown = true
		}
	}

	if strings.TrimSpace(diskPath) == "" {
		diskPath = "."
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(diskPath, &stat); err != nil || stat.Bsize <= 0 {
		diagnostics = append(diagnostics, "disk_space_unavailable")
	} else {
		blockSize := uint64(stat.Bsize)
		if stat.Bavail > ^uint64(0)/blockSize {
			diagnostics = append(diagnostics, "disk_space_invalid")
		} else {
			metrics.DiskAvailableBytes = stat.Bavail * blockSize
			metrics.DiskKnown = true
		}
	}

	status := EvaluateResourceGate(metrics)
	status.Diagnostics = diagnostics
	return status
}

func parseMemAvailable(r io.Reader) (uint64, error) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || fields[0] != "MemAvailable:" {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, err
		}
		if len(fields) >= 3 && !strings.EqualFold(fields[2], "kB") {
			return 0, errors.New("unsupported MemAvailable unit")
		}
		if value > ^uint64(0)/1024 {
			return 0, errors.New("MemAvailable overflows bytes")
		}
		return value * 1024, nil
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, errors.New("MemAvailable is missing")
}

func parseLoad1(r io.Reader) (float64, error) {
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return 0, err
		}
		return 0, errors.New("load average is missing")
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) == 0 {
		return 0, errors.New("load average is missing")
	}
	load1, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || load1 < 0 || math.IsNaN(load1) || math.IsInf(load1, 0) {
		return 0, errors.New("invalid one-minute load average")
	}
	return load1, nil
}
