package dockercontrol

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	settingInstallEnabled         = "docker.install_enabled"
	settingDaemonControlEnabled   = "docker.daemon_control_enabled"
	settingContainerCreateEnabled = "docker.container_create_enabled"

	maxCommandEventLines = 300
)

// Settings are Docker-domain safety switches. These high-privilege extensions
// default to disabled and can only be toggled from the Docker capability page.
type Settings struct {
	InstallEnabled         bool   `json:"installEnabled"`
	DaemonControlEnabled   bool   `json:"daemonControlEnabled"`
	ContainerCreateEnabled bool   `json:"containerCreateEnabled"`
	UpdatedAt              string `json:"updatedAt,omitempty"`
}

type ControlStatus struct {
	Settings        Settings      `json:"settings"`
	Install         InstallStatus `json:"install"`
	Systemd         SystemdStatus `json:"systemd"`
	PrivilegeMethod string        `json:"privilegeMethod,omitempty"`
	ActiveJob       *Job          `json:"activeJob,omitempty"`
	LatestJob       *Job          `json:"latestJob,omitempty"`
}

type InstallStatus struct {
	Supported       bool     `json:"supported"`
	Installed       bool     `json:"installed"`
	CanInstall      bool     `json:"canInstall"`
	DistroID        string   `json:"distroId,omitempty"`
	DistroName      string   `json:"distroName,omitempty"`
	Family          string   `json:"family,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	CommandPreview  []string `json:"commandPreview,omitempty"`
	DockerVersion   string   `json:"dockerVersion,omitempty"`
	InstallEnabled  bool     `json:"installEnabled"`
	PrivilegeMethod string   `json:"privilegeMethod,omitempty"`
}

type SystemdStatus struct {
	Available       bool   `json:"available"`
	CanControl      bool   `json:"canControl"`
	ActiveState     string `json:"activeState,omitempty"`
	Reason          string `json:"reason,omitempty"`
	ControlEnabled  bool   `json:"controlEnabled"`
	PrivilegeMethod string `json:"privilegeMethod,omitempty"`
}

type osRelease struct {
	ID          string
	Name        string
	VersionCode string
	IDLike      []string
}

func (s *Service) Settings(ctx context.Context) Settings {
	settings := Settings{}
	if s.store == nil {
		return settings
	}
	values, err := s.store.GetSettingsByPrefix(ctx, "docker.")
	if err != nil {
		return settings
	}
	settings.InstallEnabled = truthy(values[settingInstallEnabled])
	settings.DaemonControlEnabled = truthy(values[settingDaemonControlEnabled])
	settings.ContainerCreateEnabled = truthy(values[settingContainerCreateEnabled])
	return settings
}

func (s *Service) UpdateSettings(ctx context.Context, settings Settings) (Settings, error) {
	if s.store == nil {
		return Settings{}, errors.New("storage is unavailable")
	}
	if err := s.store.PutSettings(ctx, map[string]string{
		settingInstallEnabled:         boolText(settings.InstallEnabled),
		settingDaemonControlEnabled:   boolText(settings.DaemonControlEnabled),
		settingContainerCreateEnabled: boolText(settings.ContainerCreateEnabled),
	}); err != nil {
		return Settings{}, err
	}
	return s.Settings(ctx), nil
}

func (s *Service) ControlStatus(ctx context.Context) ControlStatus {
	settings := s.Settings(ctx)
	privilege := detectPrivilege(ctx)
	install := detectInstall(ctx, settings, privilege)
	systemd := detectSystemd(ctx, settings, privilege)
	return ControlStatus{
		Settings:        settings,
		Install:         install,
		Systemd:         systemd,
		PrivilegeMethod: privilege,
		ActiveJob:       s.ActiveJob(),
		LatestJob:       s.LatestJob(),
	}
}

func (s *Service) InstallDockerJob(ctx context.Context) (OperationResult, error) {
	settings := s.Settings(ctx)
	privilege := detectPrivilege(ctx)
	status := detectInstall(ctx, settings, privilege)
	if !settings.InstallEnabled {
		return OperationResult{}, errors.New("docker install is disabled")
	}
	if !status.CanInstall && !status.Installed {
		if status.Reason != "" {
			return OperationResult{}, errors.New(status.Reason)
		}
		return OperationResult{}, errors.New("docker install is not available on this host")
	}
	return s.StartJob(ctx, "docker.install", "安装 Docker daemon", "critical", status.Family, map[string]any{"family": status.Family, "distroId": status.DistroID}, func(runCtx context.Context, emit func(string, map[string]any)) error {
		current := detectInstall(runCtx, settings, privilege)
		if current.Installed {
			emit("docker.job.output", map[string]any{"stream": "stdout", "message": "Docker 已安装，跳过安装步骤"})
			return nil
		}
		cmd, args, err := installCommand(current.Family, privilege)
		if err != nil {
			return err
		}
		emit("docker.job.output", map[string]any{"stream": "stdout", "message": "开始执行 Docker 官方源安装流程"})
		return runCommand(runCtx, cmd, args, emit)
	})
}

func (s *Service) DaemonControlJob(ctx context.Context, action string) (OperationResult, error) {
	action = strings.TrimSpace(action)
	settings := s.Settings(ctx)
	privilege := detectPrivilege(ctx)
	systemd := detectSystemd(ctx, settings, privilege)
	if !settings.DaemonControlEnabled {
		return OperationResult{}, errors.New("docker daemon control is disabled")
	}
	if !systemd.CanControl {
		if systemd.Reason != "" {
			return OperationResult{}, errors.New(systemd.Reason)
		}
		return OperationResult{}, errors.New("docker daemon control is not available")
	}
	if action != "start" && action != "stop" && action != "restart" {
		return OperationResult{}, errors.New("unsupported docker daemon action")
	}
	title := map[string]string{"start": "启动 Docker daemon", "stop": "停止 Docker daemon", "restart": "重启 Docker daemon"}[action]
	return s.StartJob(ctx, "docker.daemon."+action, title, "critical", "docker.service", map[string]any{"action": action}, func(runCtx context.Context, emit func(string, map[string]any)) error {
		cmd, args := privilegedCommand(privilege, "systemctl", action, "docker")
		emit("docker.job.output", map[string]any{"stream": "stdout", "message": title + "：影响本机所有 Docker 容器"})
		return runCommand(runCtx, cmd, args, emit)
	})
}

func detectInstall(ctx context.Context, settings Settings, privilege string) InstallStatus {
	release := readOSRelease("/etc/os-release")
	status := InstallStatus{
		Supported:       runtime.GOOS == "linux",
		DistroID:        release.ID,
		DistroName:      release.Name,
		Family:          dockerFamily(release),
		InstallEnabled:  settings.InstallEnabled,
		PrivilegeMethod: privilege,
	}
	if runtime.GOOS != "linux" {
		status.Reason = "仅 Linux 主机支持一键安装 Docker daemon"
		return status
	}
	if out, err := shortCommand(ctx, "docker", "--version"); err == nil {
		status.Installed = true
		status.DockerVersion = safelog.Text(out, 120)
	}
	if status.Family == "" {
		status.Reason = "当前发行版暂未内置 Docker 官方源安装流程"
		return status
	}
	status.CommandPreview = installPreview(status.Family, privilege)
	if !settings.InstallEnabled {
		status.Reason = "Docker 安装开关未开启"
		return status
	}
	if privilege == "" {
		status.Reason = "当前进程没有 root 权限，也没有可用的 sudo -n"
		return status
	}
	status.CanInstall = true
	return status
}

func detectSystemd(ctx context.Context, settings Settings, privilege string) SystemdStatus {
	status := SystemdStatus{ControlEnabled: settings.DaemonControlEnabled, PrivilegeMethod: privilege}
	if runtime.GOOS != "linux" {
		status.Reason = "仅 Linux 主机支持 systemd 控制"
		return status
	}
	if !commandAvailable("systemctl") {
		status.Reason = "未检测到 systemctl"
		return status
	}
	status.Available = true
	if out, err := shortCommand(ctx, "systemctl", "is-active", "docker"); err == nil {
		status.ActiveState = strings.TrimSpace(out)
	} else {
		status.ActiveState = "unknown"
	}
	if !settings.DaemonControlEnabled {
		status.Reason = "Docker daemon 控制开关未开启"
		return status
	}
	if privilege == "" {
		status.Reason = "当前进程没有 root 权限，也没有可用的 sudo -n"
		return status
	}
	status.CanControl = true
	return status
}

func detectPrivilege(ctx context.Context) string {
	if runtime.GOOS != "linux" {
		return ""
	}
	if os.Geteuid() == 0 {
		return "root"
	}
	if commandAvailable("sudo") {
		if err := exec.CommandContext(ctx, "sudo", "-n", "true").Run(); err == nil {
			return "sudo"
		}
	}
	return ""
}

func readOSRelease(path string) osRelease {
	file, err := os.Open(path)
	if err != nil {
		return osRelease{}
	}
	defer file.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			continue
		}
		values[key] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	return osRelease{ID: values["ID"], Name: values["NAME"], VersionCode: values["VERSION_CODENAME"], IDLike: strings.Fields(values["ID_LIKE"])}
}

func dockerFamily(release osRelease) string {
	ids := append([]string{release.ID}, release.IDLike...)
	for _, id := range ids {
		switch strings.ToLower(id) {
		case "ubuntu", "debian":
			return "debian"
		case "rhel", "centos", "fedora", "rocky", "almalinux", "opencloudos":
			return "rhel"
		}
	}
	return ""
}

func installPreview(family, privilege string) []string {
	prefix := ""
	if privilege == "sudo" {
		prefix = "sudo -n "
	}
	switch family {
	case "debian":
		return []string{prefix + "apt-get update", prefix + "apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin"}
	case "rhel":
		return []string{prefix + "dnf/yum config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo", prefix + "dnf/yum install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin"}
	default:
		return nil
	}
}

func installCommand(family, privilege string) (string, []string, error) {
	var script string
	switch family {
	case "debian":
		script = `set -eu
. /etc/os-release
apt-get update
apt-get install -y ca-certificates curl gnupg
install -m 0755 -d /etc/apt/keyrings
curl -fsSL "https://download.docker.com/linux/${ID}/gpg" -o /etc/apt/keyrings/docker.asc
chmod a+r /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/${ID} ${VERSION_CODENAME} stable" > /etc/apt/sources.list.d/docker.list
apt-get update
apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
systemctl enable --now docker || true`
	case "rhel":
		script = `set -eu
if command -v dnf >/dev/null 2>&1; then PM=dnf; else PM=yum; fi
"$PM" install -y dnf-plugins-core yum-utils || true
if command -v dnf >/dev/null 2>&1; then dnf config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo; else yum-config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo; fi
"$PM" install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
systemctl enable --now docker || true`
	default:
		return "", nil, errors.New("unsupported docker install family")
	}
	name, args := privilegedCommand(privilege, "sh", "-c", script)
	return name, args, nil
}

func privilegedCommand(privilege, name string, args ...string) (string, []string) {
	if privilege == "sudo" {
		return "sudo", append([]string{"-n", name}, args...)
	}
	return name, args
}

func runCommand(ctx context.Context, name string, args []string, emit func(string, map[string]any)) error {
	cmd := exec.CommandContext(ctx, name, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	lines := make(chan map[string]string, 32)
	var scanners sync.WaitGroup
	scan := func(stream string, reader io.Reader) {
		defer scanners.Done()
		scanner := bufio.NewScanner(io.LimitReader(reader, 512*1024))
		scanner.Buffer(make([]byte, 0, 16*1024), 64*1024)
		for scanner.Scan() {
			select {
			case lines <- map[string]string{"stream": stream, "message": safelog.Text(scanner.Text(), 500)}:
			case <-ctx.Done():
				return
			}
		}
	}
	scanners.Add(2)
	go scan("stdout", stdout)
	go scan("stderr", stderr)
	go func() {
		scanners.Wait()
		close(lines)
	}()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	count := 0
	suppressed := false
	var waitErr error
	for lines != nil || done != nil {
		select {
		case line, ok := <-lines:
			if !ok {
				lines = nil
				continue
			}
			if count < maxCommandEventLines {
				emit("docker.job.output", map[string]any{"stream": line["stream"], "message": line["message"]})
				count++
			} else if !suppressed {
				emit("docker.job.output", map[string]any{"stream": "stderr", "message": "输出过多，后续 stdout/stderr 已折叠"})
				suppressed = true
			}
		case err := <-done:
			waitErr = err
			done = nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return waitErr
}

func shortCommand(ctx context.Context, name string, args ...string) (string, error) {
	if !commandAvailable(name) {
		return "", fmt.Errorf("%s not found", name)
	}
	runCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(runCtx, name, args...).CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(out)), nil
}

func truthy(value string) bool {
	return value == "1" || strings.EqualFold(value, "true")
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
