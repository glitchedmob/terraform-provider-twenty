// SPDX-License-Identifier: MPL-2.0

package acceptance

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/cpuguy83/dockercfg"
	dockerconfig "github.com/docker/cli/cli/config"
	"github.com/testcontainers/testcontainers-go"
)

func TestLocalDockerHost(t *testing.T) {
	for _, tc := range []struct {
		host  string
		local bool
	}{
		{"unix:///var/run/docker.sock", true},
		{"unix:///home/test/.docker/desktop/docker.sock", true},
		{"", false},
		{"unix:///", false},
		{"unix://remote/socket", false},
		{"tcp://127.0.0.1:2375", false},
		{"tcp://remote.example:2376", false},
		{"ssh://remote.example", false},
		{"unix:///var/run/docker.sock?credentials=secret", false},
	} {
		if got := localDockerHost(tc.host); got != tc.local {
			t.Errorf("local daemon classification returned %t, want %t", got, tc.local)
		}
	}
}

func TestDockerConfigurationIsolation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("acceptance requires a Unix Docker socket")
	}
	for _, mode := range []string{"fresh", "invalid-properties", "cached-environment", "cached-properties"} {
		t.Run(mode, func(t *testing.T) {
			home, configDir, bin := t.TempDir(), t.TempDir(), t.TempDir()
			properties := "session.id=ambient-property-session\n" +
				"docker.host=tcp://private-property-marker:2376\n" +
				"tc.host=tcp://private-property-marker:2376\n" +
				"docker.tls.verify=1\ndocker.cert.path=private-property-marker\n" +
				"hub.image.name.prefix=private-property-marker/\n" +
				"ryuk.disabled=true\nryuk.container.privileged=true\nryuk.verbose=true\n" +
				"ryuk.connection.timeout=7m\nryuk.reconnection.timeout=8m\n"
			if mode == "invalid-properties" {
				properties = "docker.tls.verify=private-property-marker\n"
			}
			for path, content := range map[string]string{
				filepath.Join(home, ".testcontainers.properties"): properties,
				filepath.Join(configDir, "config.json"):           `{"auths":{"https://index.docker.io/v1/":{"username":"private-registry-marker","password":"private-registry-marker"}},"credsStore":"private-registry-marker"}`,
			} {
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal("write synthetic ambient configuration")
				}
			}
			// This command only reads fixture environment values. It cannot contact
			// a daemon and fails if context resolution happens after HOME isolation.
			script := "#!/bin/sh\n" +
				"[ \"$HOME\" = \"$TWENTY_AMBIENT_HOME\" ] || exit 91\n" +
				"[ \"$DOCKER_CONFIG\" = \"$TWENTY_AMBIENT_CONFIG\" ] || exit 92\n" +
				"[ \"$*\" = 'context inspect --format {{.Endpoints.docker.Host}}' ] || exit 93\n" +
				"printf 'unix:///fixture-local-docker.sock\\n'\n"
			if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o700); err != nil {
				t.Fatal("write daemon-free context fixture")
			}
			// A discoverable helper must not become the Docker CLI's credential store.
			if err := os.WriteFile(filepath.Join(bin, "docker-credential-pass"), []byte("#!/bin/sh\nexit 94\n"), 0o700); err != nil {
				t.Fatal("write unused credential-helper fixture")
			}
			for key, value := range map[string]string{
				"TWENTY_DOCKER_ISOLATION_CASE":             mode,
				"TWENTY_AMBIENT_HOME":                      home,
				"TWENTY_AMBIENT_CONFIG":                    configDir,
				"HOME":                                     home,
				"PATH":                                     bin + string(os.PathListSeparator) + os.Getenv("PATH"),
				"DOCKER_CONFIG":                            configDir,
				"DOCKER_AUTH_CONFIG":                       `{"auths":{"https://index.docker.io/v1/":{"username":"private-registry-marker","password":"private-registry-marker"}}}`,
				"DOCKER_CONTEXT":                           "fixture-local-context",
				"DOCKER_HOST":                              "tcp://ambient-remote:2376",
				"DOCKER_TLS":                               "1",
				"DOCKER_TLS_VERIFY":                        "1",
				"DOCKER_CERT_PATH":                         "private-property-marker",
				"DOCKER_CUSTOM_HEADERS":                    "Authorization=private-registry-marker",
				"TESTCONTAINERS_SESSION_ID":                "ambient-environment-session",
				"TESTCONTAINERS_HUB_IMAGE_NAME_PREFIX":     "private-property-marker/",
				"TESTCONTAINERS_HOST_OVERRIDE":             "ambient-remote",
				"TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE":    "/ambient/socket",
				"TESTCONTAINERS_RYUK_DISABLED":             "true",
				"TESTCONTAINERS_RYUK_CONTAINER_PRIVILEGED": "true",
				"TESTCONTAINERS_RYUK_VERBOSE":              "true",
				"TESTCONTAINERS_RYUK_CONNECTION_TIMEOUT":   "7m",
				"TESTCONTAINERS_RYUK_RECONNECTION_TIMEOUT": "8m",
				"TESTCONTAINERS_LOG_LEVEL":                 "debug",
				"RYUK_VERBOSE":                             "true",
				"RYUK_CONNECTION_TIMEOUT":                  "7m",
				"RYUK_RECONNECTION_TIMEOUT":                "8m",
				"RYUK_PORT":                                "12345",
				"TF_ACC":                                   "",
			} {
				t.Setenv(key, value)
			}
			if mode == "cached-properties" {
				t.Setenv("TESTCONTAINERS_SESSION_ID", "")
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal("locate test subprocess")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			output, err := exec.CommandContext(ctx, executable, "-test.run=^TestDockerIsolationProcess$", "-test.v").CombinedOutput()
			for _, marker := range []string{"private-property-marker", "private-registry-marker", "invalid testcontainers properties file"} {
				if bytes.Contains(output, []byte(marker)) {
					t.Fatal("ambient configuration leaked to subprocess output")
				}
			}
			if strings.HasPrefix(mode, "cached-") {
				if err == nil || !bytes.Contains(output, []byte("cached before disposable isolation; refusing Docker access")) {
					t.Fatalf("previously cached configuration was not rejected: %s", output)
				}
			} else if err != nil {
				t.Fatalf("daemon-free isolation regression failed: %s", output)
			}
		})
	}
}

// Only run by the parent test. A new process gives ReadConfig a fresh singleton.
func TestDockerIsolationProcess(t *testing.T) {
	mode := os.Getenv("TWENTY_DOCKER_ISOLATION_CASE")
	if mode == "" {
		t.Skip("subprocess regression helper")
	}
	if strings.HasPrefix(mode, "cached-") {
		_ = testcontainers.ReadConfig()
		requireLocalDocker(t)
		t.Fatal("ambient cached configuration was accepted")
	}
	var sessionID string
	for _, name := range []string{"first start", "later start after cleanup"} {
		t.Run(name, func(t *testing.T) {
			requireLocalDocker(t)
			config := testcontainers.ReadConfig().Config
			decoded, err := hex.DecodeString(config.SessionID)
			if err != nil || len(decoded) != 24 || strings.HasPrefix(config.SessionID, "ambient-") {
				t.Fatal("session ID is not a fresh random disposable ID")
			}
			if sessionID != "" && config.SessionID != sessionID {
				t.Fatal("later Start changed the singleton reaper session")
			}
			sessionID = config.SessionID
			if config.Host != "" || config.TestcontainersHost != "" || config.TLSVerify != 0 || config.CertPath != "" || config.HubImageNamePrefix != "" || config.RyukDisabled || config.RyukPrivileged || config.RyukVerbose || config.RyukConnectionTimeout != time.Minute || config.RyukReconnectionTimeout != 10*time.Second {
				t.Fatal("testcontainers read ambient properties or reaper settings")
			}
			if os.Getenv("DOCKER_HOST") != "unix:///fixture-local-docker.sock" || os.Getenv("TESTCONTAINERS_HOST_OVERRIDE") != "127.0.0.1" {
				t.Fatal("resolved local Docker address was not preserved explicitly")
			}
			for _, key := range []string{"DOCKER_AUTH_CONFIG", "DOCKER_CONTEXT", "DOCKER_TLS", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH", "DOCKER_CUSTOM_HEADERS", "TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE", "TESTCONTAINERS_LOG_LEVEL", "TESTCONTAINERS_RYUK_VERBOSE", "RYUK_PORT"} {
				if _, present := os.LookupEnv(key); present {
					t.Fatalf("ambient setting %s was not removed", key)
				}
			}
			home := os.Getenv("HOME")
			entries, err := os.ReadDir(home)
			if err != nil || home == os.Getenv("TWENTY_AMBIENT_HOME") || len(entries) != 0 {
				t.Fatal("disposable HOME is not empty and isolated")
			}
			if dockerconfig.Dir() != os.Getenv("DOCKER_CONFIG") {
				t.Fatal("Docker CLI retained its previous config directory")
			}
			var diagnostics bytes.Buffer
			cliConfig := dockerconfig.LoadDefaultConfigFile(&diagnostics)
			if diagnostics.Len() != 0 || cliConfig.CredentialsStore != "" || len(cliConfig.CredentialHelpers) != 0 {
				t.Fatal("Docker CLI read ambient registry configuration or discovered a helper")
			}
			registryConfig, err := dockercfg.LoadDefaultConfig()
			if err != nil || registryConfig.CredentialsStore != "" || len(registryConfig.CredentialHelpers) != 0 || len(registryConfig.AuthConfigs) != 1 {
				t.Fatal("registry configuration is not anonymous")
			}
			for _, auth := range registryConfig.AuthConfigs {
				if auth != (dockercfg.AuthConfig{}) {
					t.Fatal("registry configuration contains credentials")
				}
			}
			username, password, err := registryConfig.GetRegistryCredentials("https://index.docker.io/v1/")
			if err != nil || username != "" || password != "" {
				t.Fatal("anonymous registry configuration supplied credentials")
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestDisposableHTTPTransports(t *testing.T) {
	var proxyCalls, defaultCalls, metadataCalls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxyCalls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(key, proxy.URL)
	}
	ambient := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		defaultCalls.Add(1)
		return nil, errors.New("ambient default transport called")
	})
	t.Cleanup(func() { http.DefaultTransport = ambient })
	var origin string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			w.Header().Set("Location", "/metadata")
			w.WriteHeader(http.StatusFound)
			return
		}
		metadataCalls.Add(1)
		if r.Header.Get("Authorization") != "Bearer disposable-test-token" || r.Header.Get("Origin") != origin {
			t.Error("bootstrap request omitted its explicit bearer token or origin")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"safe":true}}`)
	}))
	defer server.Close()
	origin = server.URL
	httpClient := disposableHTTPClient()
	defer httpClient.CloseIdleConnections()
	transport, ok := httpClient.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil || transport.DialContext == nil || transport.TLSHandshakeTimeout <= 0 || transport.ResponseHeaderTimeout <= 0 || transport.IdleConnTimeout <= 0 || httpClient.Timeout <= 0 || httpClient.Timeout > 30*time.Second {
		t.Fatal("disposable HTTP transport does not have explicit proxy isolation and bounded deadlines")
	}
	response, err := httpClient.Get(server.URL + "/redirect")
	if err != nil {
		t.Fatal("disposable direct HTTP request failed")
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusFound || metadataCalls.Load() != 0 {
		t.Fatal("disposable HTTP client followed a redirect")
	}
	fixture := &Stack{Endpoint: server.URL, Origin: origin}
	var data struct{ Safe bool }
	if err := fixture.api("disposable-test-token").MakeRequest(t.Context(), &graphql.Request{Query: "query Fixture { safe }"}, &graphql.Response{Data: &data}); err != nil || !data.Safe {
		t.Fatal("bootstrap GraphQL client did not use its own safe HTTP transport")
	}
	if proxyCalls.Load() != 0 || defaultCalls.Load() != 0 || metadataCalls.Load() != 1 {
		t.Fatal("disposable requests used an ambient proxy or default transport")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal("create canceled disposable request")
	}
	if _, err := httpClient.Do(request); !errors.Is(err, context.Canceled) {
		t.Fatal("disposable HTTP transport did not honor context cancellation")
	}
}
