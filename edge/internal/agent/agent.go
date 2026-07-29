// Package agent is the HTTP surface the control plane uses to manage peers on
// this box.
//
// Trust model: the control plane authenticates with a per-server bearer token
// over TLS, and the agent still validates every value it receives. The agent
// binds to the machine's private/management address, never to the public
// internet — see edge/scripts/bootstrap.sh.
package agent

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"

	"github.com/treykys/proxify-vpn/edge/internal/wg"
)

const maxBody = 1 << 20

type Agent struct {
	wg    *wg.Client
	token string
	log   *slog.Logger

	// instanceID identifies this installation. It survives reboots (it's on
	// disk) but not a rebuild, which is exactly the signal the control plane
	// needs: a new ID means "my peer table is empty, send me everything".
	instanceID string
	version    string

	// mu serializes mutations. wg itself is safe, but a resync racing an
	// individual peer add can otherwise interleave into a state that matches
	// neither request.
	mu sync.Mutex
}

type Options struct {
	Interface string
	Token     string
	StateDir  string
	Version   string
	Logger    *slog.Logger
}

func New(opts Options) (*Agent, error) {
	if opts.Token == "" {
		return nil, errors.New("agent: token is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	id, err := loadOrCreateInstanceID(opts.StateDir)
	if err != nil {
		return nil, err
	}
	return &Agent{
		wg:         wg.New(opts.Interface),
		token:      opts.Token,
		log:        opts.Logger,
		instanceID: id,
		version:    opts.Version,
	}, nil
}

func (a *Agent) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /v1/peers", a.authed(a.handleSetPeer))
	mux.Handle("POST /v1/peers/remove", a.authed(a.handleRemovePeer))
	mux.Handle("POST /v1/peers/sync", a.authed(a.handleSync))
	mux.Handle("GET /v1/health", a.authed(a.handleHealth))
	return mux
}

func (a *Agent) authed(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if len(h) < 7 || !strings.EqualFold(h[:7], "bearer ") ||
			subtle.ConstantTimeCompare([]byte(strings.TrimSpace(h[7:])), []byte(a.token)) != 1 {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	})
}

type peerRequest struct {
	PublicKey string `json:"public_key"`
	TunnelIP  string `json:"tunnel_ip"`
	Revision  int64  `json:"revision"`
	Replaces  string `json:"replaces"`
}

func (a *Agent) handleSetPeer(w http.ResponseWriter, r *http.Request) {
	var req peerRequest
	if !decode(w, r, &req) {
		return
	}
	ip, err := wg.ValidateIP(req.TunnelIP)
	if err != nil {
		badRequest(w, "invalid tunnel_ip")
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.wg.SetPeer(r.Context(), wg.Peer{PublicKey: req.PublicKey, TunnelIP: ip}, req.Replaces); err != nil {
		if errors.Is(err, wg.ErrInvalidKey) || errors.Is(err, wg.ErrInvalidIP) {
			badRequest(w, "invalid peer")
			return
		}
		a.log.Error("set peer", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "apply failed"})
		return
	}
	a.log.Info("peer applied", "tunnel_ip", ip.String(), "revision", req.Revision)
	writeJSON(w, http.StatusOK, map[string]any{"applied_revision": req.Revision})
}

func (a *Agent) handleRemovePeer(w http.ResponseWriter, r *http.Request) {
	var req peerRequest
	if !decode(w, r, &req) {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.wg.RemovePeer(r.Context(), req.PublicKey); err != nil {
		if errors.Is(err, wg.ErrInvalidKey) {
			badRequest(w, "invalid public_key")
			return
		}
		a.log.Error("remove peer", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "remove failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": true})
}

type syncRequest struct {
	Peers []peerRequest `json:"peers"`
}

func (a *Agent) handleSync(w http.ResponseWriter, r *http.Request) {
	var req syncRequest
	if !decode(w, r, &req) {
		return
	}

	peers := make([]wg.Peer, 0, len(req.Peers))
	for _, p := range req.Peers {
		ip, err := wg.ValidateIP(p.TunnelIP)
		if err != nil {
			badRequest(w, "invalid tunnel_ip in peer set")
			return
		}
		peers = append(peers, wg.Peer{PublicKey: p.PublicKey, TunnelIP: ip})
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.wg.Sync(r.Context(), peers); err != nil {
		if errors.Is(err, wg.ErrInvalidKey) || errors.Is(err, wg.ErrInvalidIP) {
			badRequest(w, "invalid peer set")
			return
		}
		a.log.Error("sync peers", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "sync failed"})
		return
	}
	a.log.Info("peer set synced", "peers", len(peers))
	writeJSON(w, http.StatusOK, map[string]any{"synced": len(peers)})
}

func (a *Agent) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	up := a.wg.Up(ctx)
	count := a.wg.PeerCount(ctx)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            up,
		"boot_id":       a.instanceID,
		"peer_count":    count,
		"interface":     a.wg.Interface,
		"agent_version": a.version,
		"wireguard_up":  up,
		"obfuscator_up": obfuscatorUp(),
	})
}

// obfuscatorUp reports whether the Xray/Reality service is running. It is a
// best-effort check: the control plane uses it for alerting, not for routing
// decisions.
func obfuscatorUp() bool {
	_, err := os.Stat("/run/xray.pid")
	return err == nil
}

// loadOrCreateInstanceID persists a random ID under the state directory.
func loadOrCreateInstanceID(dir string) (string, error) {
	if dir == "" {
		dir = "/var/lib/proxify-edge"
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "instance-id")

	if b, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(b)); id != "" {
			return id, nil
		}
	}

	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	id := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", err
	}
	return id, nil
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	if err := dec.Decode(dst); err != nil {
		badRequest(w, "could not parse body")
		return false
	}
	return true
}

func badRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// ReadTokenFile loads the shared secret from disk, trimming whitespace. Keeping
// it in a 0600 file rather than an environment variable keeps it out of `ps`
// output and out of any crash dump that includes the environment.
func ReadTokenFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(b))
	if len(token) < 32 {
		return "", errors.New("agent: token must be at least 32 characters")
	}
	return token, nil
}

// DefaultTimeouts for the agent's HTTP server.
var (
	ReadTimeout  = 15 * time.Second
	WriteTimeout = 30 * time.Second
	IdleTimeout  = 60 * time.Second
)
