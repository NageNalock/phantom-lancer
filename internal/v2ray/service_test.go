package v2ray

import (
	"strings"
	"testing"

	"phantom-lancer/internal/storage"
)

func TestValidateSettingsAllowsPrivilegedPorts(t *testing.T) {
	settings := validTestSettings()
	settings.Port = 443

	if err := validateSettings(settings); err != nil {
		t.Fatalf("expected port 443 to be valid, got %v", err)
	}
}

func TestValidateSettingsRejectsInvalidPorts(t *testing.T) {
	for _, port := range []int{0, 65536} {
		settings := validTestSettings()
		settings.Port = port

		err := validateSettings(settings)
		if err == nil {
			t.Fatalf("expected port %d to be invalid", port)
		}
		if !strings.Contains(err.Error(), "1-65535") {
			t.Fatalf("expected range error for port %d, got %v", port, err)
		}
	}
}

func validTestSettings() storage.V2RaySettings {
	return storage.V2RaySettings{
		Port:       10086,
		Protocol:   "vmess",
		Transport:  "tcp",
		Security:   "none",
		ConfigMode: "guided",
	}
}
