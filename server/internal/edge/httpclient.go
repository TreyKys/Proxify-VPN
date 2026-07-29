package edge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/treykys/proxify-vpn/server/internal/model"
)

// HTTPClient talks to the edge-agent daemon (edge/cmd/edge-agent) over HTTPS
// with a per-server bearer token.
type HTTPClient struct {
	hc *http.Client
}

func NewHTTPClient(timeout time.Duration) *HTTPClient {
	return &HTTPClient{hc: &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     90 * time.Second,
		},
	}}
}

func (c *HTTPClient) ApplyPeer(ctx context.Context, srv model.Server, peer Peer) error {
	return c.post(ctx, srv, "/v1/peers", peerPayload{
		PublicKey: peer.PublicKey,
		TunnelIP:  peer.TunnelIP.String(),
		Revision:  peer.Revision,
		Replaces:  peer.Replaces,
	}, nil)
}

func (c *HTTPClient) RemovePeer(ctx context.Context, srv model.Server, publicKey string) error {
	return c.post(ctx, srv, "/v1/peers/remove", removePayload{PublicKey: publicKey}, nil)
}

func (c *HTTPClient) SyncPeers(ctx context.Context, srv model.Server, peers []Peer) error {
	body := syncPayload{Peers: make([]peerPayload, 0, len(peers))}
	for _, p := range peers {
		body.Peers = append(body.Peers, peerPayload{
			PublicKey: p.PublicKey,
			TunnelIP:  p.TunnelIP.String(),
			Revision:  p.Revision,
		})
	}
	return c.post(ctx, srv, "/v1/peers/sync", body, nil)
}

func (c *HTTPClient) Health(ctx context.Context, srv model.Server) (Health, error) {
	var h Health
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(srv.AgentURL, "/")+"/v1/health", nil)
	if err != nil {
		return h, err
	}
	req.Header.Set("Authorization", "Bearer "+srv.AgentToken)
	resp, err := c.hc.Do(req)
	if err != nil {
		return h, fmt.Errorf("%w: %s: %v", ErrUnreachable, srv.Code, err)
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return h, statusError(srv, resp)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&h); err != nil {
		return h, fmt.Errorf("%w: %s: decode health: %v", ErrUnreachable, srv.Code, err)
	}
	return h, nil
}

type peerPayload struct {
	PublicKey string `json:"public_key"`
	TunnelIP  string `json:"tunnel_ip"`
	Revision  int64  `json:"revision"`
	Replaces  string `json:"replaces,omitempty"`
}

type removePayload struct {
	PublicKey string `json:"public_key"`
}

type syncPayload struct {
	Peers []peerPayload `json:"peers"`
}

func (c *HTTPClient) post(ctx context.Context, srv model.Server, path string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := strings.TrimRight(srv.AgentURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+srv.AgentToken)

	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s %s: %v", ErrUnreachable, srv.Code, path, err)
	}
	defer drain(resp)

	if resp.StatusCode/100 != 2 {
		return statusError(srv, resp)
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
	}
	return nil
}

// statusError classifies the failure. 4xx (except 429) is the agent telling us
// the request is wrong — no amount of retrying fixes that, so it maps to
// ErrRejected. Everything else is treated as transient.
func statusError(srv model.Server, resp *http.Response) error {
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	msg := strings.TrimSpace(string(snippet))
	if resp.StatusCode/100 == 4 && resp.StatusCode != http.StatusTooManyRequests {
		return fmt.Errorf("%w: %s: %s: %s", ErrRejected, srv.Code, resp.Status, msg)
	}
	return fmt.Errorf("%w: %s: %s: %s", ErrUnreachable, srv.Code, resp.Status, msg)
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
}
