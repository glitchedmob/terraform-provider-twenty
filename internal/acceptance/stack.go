// SPDX-License-Identifier: MPL-2.0

// Package acceptance provides disposable Twenty fixtures for acceptance tests.
// It must not be imported by provider configuration or managed resources.
package acceptance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	dockerconfig "github.com/docker/cli/cli/config"
	"github.com/testcontainers/testcontainers-go"
	containerlog "github.com/testcontainers/testcontainers-go/log"
	"github.com/testcontainers/testcontainers-go/modules/compose"
)

// Stack contains addresses of a fresh, loopback-only Compose project.
type Stack struct {
	Endpoint string
	Origin   string
	MailURL  string
}

// Start registers cleanup before starting containers. No credential or endpoint
// environment variables are inherited by the Compose application.
func Start(t *testing.T) *Stack {
	t.Helper()
	if os.Getenv("TF_ACC") != "1" {
		t.Skip("set TF_ACC=1 to run disposable acceptance tests")
	}
	requireLocalDocker(t)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal("reserve acceptance loopback port")
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal("release acceptance loopback port")
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate acceptance compose file")
	}
	stack, err := compose.NewDockerComposeWith(
		compose.WithStackFiles(filepath.Join(filepath.Dir(source), "../../integration/compose.yml")),
		compose.StackIdentifier("twenty-acc-"+randomString(t)),
		compose.WithLogger(log.New(io.Discard, "", 0)),
	)
	if err != nil {
		t.Fatal("create disposable Twenty compose project")
	}
	t.Cleanup(func() {
		if t.Failed() {
			captureStatus(t, stack)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := stack.Down(ctx, compose.RemoveOrphans(true), compose.RemoveVolumes(true)); err != nil {
			t.Error("remove disposable Twenty project and volumes failed")
		}
		if err := stack.Close(); err != nil {
			t.Error("close disposable Twenty Docker clients failed")
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	stack.WithEnv(map[string]string{
		"TWENTY_TEST_PORT":           strconv.Itoa(port),
		"TWENTY_TEST_DB_PASSWORD":    randomString(t),
		"TWENTY_TEST_APP_SECRET":     randomString(t),
		"TWENTY_TEST_ENCRYPTION_KEY": randomString(t),
	})
	if err := stack.Up(ctx, compose.Wait(true)); err != nil {
		t.Fatal("disposable Twenty startup or bounded migration/readiness failed; see sanitized status artifact")
	}
	server, err := stack.ServiceContainer(ctx, "server")
	if err != nil {
		t.Fatal("locate disposable server after migration")
	}
	logs, err := server.Logs(ctx)
	if err != nil {
		t.Fatal("check disposable migration result")
	}
	migrationLog, readErr := io.ReadAll(io.LimitReader(logs, 8<<20))
	_ = logs.Close()
	if readErr != nil || !strings.Contains(string(migrationLog), "Successfully migrated DB!") || strings.Contains(string(migrationLog), "Upgrade completed with errors") {
		t.Fatal("disposable image entrypoint did not complete migrations cleanly")
	}
	mail, err := stack.ServiceContainer(ctx, "mail")
	if err != nil {
		t.Fatal("locate disposable mail sink")
	}
	mailPort, err := mail.MappedPort(ctx, "8025/tcp")
	if err != nil {
		t.Fatal("locate disposable mail sink port")
	}
	origin := "http://127.0.0.1:" + strconv.Itoa(port)
	return &Stack{Endpoint: origin, Origin: origin, MailURL: "http://127.0.0.1:" + mailPort.Port()}
}

// Refuse remote daemon contexts before any daemon call. Both Compose's Docker
// CLI client and testcontainers must target the same local Unix socket.
func requireLocalDocker(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	host := os.Getenv("DOCKER_HOST")
	if host == "" || os.Getenv("DOCKER_CONTEXT") != "" {
		output, err := exec.CommandContext(ctx, "docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}").Output()
		if err != nil {
			t.Fatal("resolve disposable local Docker context")
		}
		host = strings.TrimSpace(string(output))
	}
	if !localDockerHost(host) {
		t.Fatal("acceptance tests require a local Docker Unix socket; remote daemon contexts are not allowed")
	}
	isolateDockerConfiguration(t, host)
}

// Testcontainers caches configuration and reuses its reaper within a process.
// All Starts must share our random session, never one supplied by the caller.
var disposableDocker struct {
	sync.Mutex
	sessionID string
	host      string
}

// This function does not contact Docker. Resolve the local context first, then
// hide ambient properties, registry credentials, reaper settings, and logging.
func isolateDockerConfiguration(t *testing.T, host string) {
	t.Helper()
	disposableDocker.Lock()
	defer disposableDocker.Unlock()
	if disposableDocker.sessionID == "" {
		disposableDocker.sessionID = randomString(t)
		disposableDocker.host = host
	} else if disposableDocker.host != host {
		t.Fatal("disposable acceptance stacks must use the same local Docker socket within a process")
	}
	previousDockerConfig := dockerconfig.Dir()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "DOCKER_") || strings.HasPrefix(key, "TESTCONTAINERS_") || strings.HasPrefix(key, "RYUK_") {
			t.Setenv(key, "")
			if err := os.Unsetenv(key); err != nil {
				t.Fatal("clear ambient Docker and testcontainers settings")
			}
		}
	}
	// HOME must change before the first ReadConfig: invalid properties can print
	// their raw values, and session.id can broaden Ryuk cleanup to unrelated tests.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DOCKER_HOST", host)
	dockerConfig := t.TempDir()
	// An anonymous Hub entry also prevents Docker CLI auto-discovery of a
	// credential helper from PATH. An empty directory alone does not do that.
	if err := os.WriteFile(filepath.Join(dockerConfig, "config.json"), []byte(`{"auths":{"https://index.docker.io/v1/":{}}}`), 0o600); err != nil {
		t.Fatal("write anonymous disposable Docker configuration")
	}
	t.Setenv("DOCKER_CONFIG", dockerConfig)
	// Docker CLI also caches its config directory. Refresh it for later Starts.
	dockerconfig.SetDir(dockerConfig)
	t.Cleanup(func() { dockerconfig.SetDir(previousDockerConfig) })
	t.Setenv("TESTCONTAINERS_HOST_OVERRIDE", "127.0.0.1")
	// Leave DOCKER_SOCKET_OVERRIDE absent so local Docker Desktop detection works.
	t.Setenv("TESTCONTAINERS_SESSION_ID", disposableDocker.sessionID)
	t.Setenv("TESTCONTAINERS_HUB_IMAGE_NAME_PREFIX", "")
	t.Setenv("TESTCONTAINERS_RYUK_DISABLED", "false")
	t.Setenv("TESTCONTAINERS_RYUK_CONTAINER_PRIVILEGED", "false")
	t.Setenv("RYUK_VERBOSE", "false")
	t.Setenv("RYUK_CONNECTION_TIMEOUT", "1m")
	t.Setenv("RYUK_RECONNECTION_TIMEOUT", "10s")
	logger := containerlog.Default()
	containerlog.SetDefault(containerlog.NewNoopLogger())
	t.Cleanup(func() { containerlog.SetDefault(logger) })

	want := testcontainers.TestcontainersConfig{}
	want.Config.SessionID = disposableDocker.sessionID
	want.Config.RyukConnectionTimeout = time.Minute
	want.Config.RyukReconnectionTimeout = 10 * time.Second
	if testcontainers.ReadConfig() != want {
		// Never adopt a singleton initialized before our isolation, even when its
		// daemon host is local. Its session can still own unrelated containers.
		t.Fatal("testcontainers configuration was cached before disposable isolation; refusing Docker access")
	}
}

func localDockerHost(host string) bool {
	return strings.HasPrefix(host, "unix:///") && len(host) > len("unix:///") && !strings.ContainsAny(host, "?#")
}

func randomString(t *testing.T) string {
	t.Helper()
	var data [24]byte
	if _, err := rand.Read(data[:]); err != nil {
		t.Fatal("generate disposable secret")
	}
	return hex.EncodeToString(data[:])
}

// captureStatus deliberately emits an allowlist, not raw container logs,
// configuration, healthcheck output, mail messages, or GraphQL request bodies.
func captureStatus(t *testing.T, stack compose.ComposeStack) {
	t.Helper()
	type serviceStatus struct {
		Service  string         `json:"service"`
		Running  bool           `json:"running"`
		ExitCode int            `json:"exit_code"`
		Health   string         `json:"health"`
		Events   map[string]int `json:"events"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	statuses := []serviceStatus{}
	for _, service := range []string{"postgres", "redis", "mail", "server", "worker"} {
		status := serviceStatus{Service: service, Health: "unavailable", Events: map[string]int{}}
		container, err := stack.ServiceContainer(ctx, service)
		if err == nil {
			state, inspectErr := container.Inspect(ctx)
			if inspectErr == nil && state.State != nil {
				status.Running = state.State.Running
				status.ExitCode = state.State.ExitCode
				if state.State.Health != nil {
					// Docker's health status is an enum; no healthcheck output.
					switch state.State.Health.Status {
					case "healthy", "unhealthy", "starting":
						status.Health = string(state.State.Health.Status)
					}
				}
			}
			logs, logErr := container.Logs(ctx)
			if logErr == nil {
				data, _ := io.ReadAll(io.LimitReader(logs, 8<<20))
				_ = logs.Close()
				for name, marker := range map[string]string{
					"migration_started":  "Running database setup and migrations",
					"migration_complete": "Successfully migrated DB",
					"migration_warning":  "Upgrade completed with errors",
					"server_started":     "Nest application successfully started",
					"exception":          "Exception",
					"error":              "ERROR",
				} {
					status.Events[name] = strings.Count(string(data), marker)
				}
			}
		}
		statuses = append(statuses, status)
	}
	data, err := json.MarshalIndent(statuses, "", "  ")
	if err != nil {
		t.Error("encode sanitized acceptance status")
		return
	}
	t.Logf("sanitized disposable status: %s", data)
	if err := os.WriteFile(filepath.Join(t.ArtifactDir(), "stack-status.json"), data, 0o600); err != nil {
		t.Error("save sanitized acceptance status")
	}
}

func disposableHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 15 * time.Second,
			IdleConnTimeout:       30 * time.Second,
			ExpectContinueTimeout: time.Second,
			MaxIdleConns:          10,
			MaxIdleConnsPerHost:   2,
		},
		Timeout:       30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}
