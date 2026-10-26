//go:build linux || darwin

package kamera

import (
	"os"
	"path/filepath"
)

// secretPaths — то, что в одиночке прячется, даже когда домашний каталог
// виден: ключи, токены и сокеты, через которые можно выйти за стены.
// Список не исчерпывающий; спрятать дом целиком — флаг --bez-doma.
func secretPaths(home string) []string {
	var out []string
	if home != "" {
		for _, p := range []string{
			".ssh", ".gnupg", ".aws", ".azure", ".kube", ".docker",
			".netrc", ".git-credentials", ".password-store", ".pgpass",
			".npmrc", ".pypirc", ".gem/credentials", ".cargo/credentials", ".cargo/credentials.toml",
			".config/gh", ".config/gcloud", ".config/hub", ".config/rclone",
			".local/share/keyrings", ".mozilla", ".config/google-chrome", ".config/chromium",
			".terraform.d/credentials.tfrc.json", ".vault-token",
		} {
			out = append(out, filepath.Join(home, p))
		}
	}
	return append(out,
		"/run/docker.sock", "/var/run/docker.sock",
		"/run/podman/podman.sock", "/run/containerd/containerd.sock",
	)
}

// secretEnv — переменные окружения, которые в одиночке убираются.
var secretEnv = []string{"SSH_AUTH_SOCK", "GPG_AGENT_INFO", "DBUS_SESSION_BUS_ADDRESS"}

// cleanEnv убирает из окружения адреса агентов и шины.
func cleanEnv(env []string) []string {
	out := env[:0:0]
outer:
	for _, kv := range env {
		for _, k := range secretEnv {
			if len(kv) > len(k) && kv[:len(k)+1] == k+"=" {
				continue outer
			}
		}
		out = append(out, kv)
	}
	return out
}

func userHome() string {
	h, _ := os.UserHomeDir()
	return h
}
