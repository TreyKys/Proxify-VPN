package agent

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testToken = "0123456789abcdef0123456789abcdef"

func newTestAgent(t *testing.T) *Agent {
	t.Helper()
	a, err := New(Options{
		Interface: "wg-test",
		Token:     testToken,
		StateDir:  t.TempDir(),
		Version:   "test",
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

// The agent can add and remove peers on a production box. Every route must
// require the token — including the read-only health route, which otherwise
// leaks the peer count to anyone who can reach the port.
func TestEveryRouteRequiresTheToken(t *testing.T) {
	srv := httptest.NewServer(newTestAgent(t).Handler())
	defer srv.Close()

	routes := []struct{ method, path string }{
		{http.MethodPost, "/v1/peers"},
		{http.MethodPost, "/v1/peers/remove"},
		{http.MethodPost, "/v1/peers/sync"},
		{http.MethodGet, "/v1/health"},
	}
	headers := map[string]string{
		"no header":    "",
		"empty bearer": "Bearer ",
		"wrong token":  "Bearer wrong-token-wrong-token-wrong",
		"wrong scheme": "Basic " + testToken,
		"token as-is":  testToken,
		"near-miss":    "Bearer " + testToken[:len(testToken)-1] + "0",
	}

	for _, route := range routes {
		for name, header := range headers {
			req, err := http.NewRequest(route.method, srv.URL+route.path, strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			if header != "" {
				req.Header.Set("Authorization", header)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s %s with %s: status = %d, want 401",
					route.method, route.path, name, resp.StatusCode)
			}
		}
	}
}

func TestSetPeerRejectsBadAddressBeforeTouchingWireGuard(t *testing.T) {
	srv := httptest.NewServer(newTestAgent(t).Handler())
	defer srv.Close()

	body := `{"public_key":"QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=","tunnel_ip":"not-an-ip"}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/peers", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// The instance ID is how the control plane distinguishes a rebuilt box from a
// rebooted one. It must be stable across restarts and unique per install.
func TestInstanceIDIsStablePerStateDir(t *testing.T) {
	dir := t.TempDir()

	first, err := loadOrCreateInstanceID(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadOrCreateInstanceID(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("instance ID changed across restarts: %s -> %s", first, second)
	}

	other, err := loadOrCreateInstanceID(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if other == first {
		t.Error("a fresh install reused an existing instance ID")
	}

	info, err := os.Stat(filepath.Join(dir, "instance-id"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("instance-id permissions = %o, want 600", perm)
	}
}

func TestReadTokenFileRejectsWeakTokens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.token")

	if err := os.WriteFile(path, []byte("short\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTokenFile(path); err == nil {
		t.Error("accepted a short token")
	}

	if err := os.WriteFile(path, []byte("  "+testToken+"  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadTokenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != testToken {
		t.Errorf("token = %q, want the trimmed value", got)
	}
}
