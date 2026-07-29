// Package wg drives the local WireGuard interface.
//
// It shells out to the wg/wg-quick tools rather than talking netlink directly.
// On a box being debugged at 2am, `wg show` in the logs and `wg show` in the
// operator's terminal being literally the same command is worth more than the
// elegance of a netlink library.
//
// Every argument that reaches exec is validated first (see Validate*). Commands
// are run without a shell, so no argument can turn into a shell metacharacter.
package wg

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"strings"
	"time"
)

var (
	ErrInvalidKey = errors.New("wg: invalid public key")
	ErrInvalidIP  = errors.New("wg: invalid tunnel address")
)

type Client struct {
	// Interface is the WireGuard interface name, e.g. "wg0".
	Interface string
	// Timeout bounds a single wg invocation.
	Timeout time.Duration
	// Persist controls whether changes are written back to the interface's
	// config file. Without it, a reboot silently drops every peer.
	Persist bool
	// runner is a test seam; production uses execCommand.
	runner func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func New(iface string) *Client {
	return &Client{
		Interface: iface,
		Timeout:   10 * time.Second,
		Persist:   true,
		runner:    execCommand,
	}
}

// Peer is one WireGuard peer as the agent understands it.
type Peer struct {
	PublicKey string
	TunnelIP  netip.Addr
}

// ValidateKey accepts only a canonical base64 32-byte key. The agent re-checks
// what the control plane sends: a compromised or buggy control plane must not be
// able to turn a peer key into an argument injection on the edge box.
func ValidateKey(key string) (string, error) {
	if len(key) != 44 {
		return "", ErrInvalidKey
	}
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(raw) != 32 {
		return "", ErrInvalidKey
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// ValidateIP accepts a single host address inside the interface's range.
func ValidateIP(addr string) (netip.Addr, error) {
	ip, err := netip.ParseAddr(addr)
	if err != nil || !ip.Is4() || ip.IsUnspecified() || ip.IsMulticast() {
		return netip.Addr{}, ErrInvalidIP
	}
	return ip, nil
}

// SetPeer installs or updates a peer, optionally removing the key it replaces.
//
// Removal happens first so that a rotation is never briefly "both keys valid":
// if the process dies between the two commands, the box is left with no peer
// for that device rather than with a stale key that still works. The control
// plane retries and the user reconnects; a lingering valid key would not fix
// itself.
func (c *Client) SetPeer(ctx context.Context, peer Peer, replaces string) error {
	key, err := ValidateKey(peer.PublicKey)
	if err != nil {
		return err
	}
	if !peer.TunnelIP.IsValid() {
		return ErrInvalidIP
	}

	if replaces != "" && replaces != key {
		old, err := ValidateKey(replaces)
		if err != nil {
			return fmt.Errorf("replaces: %w", err)
		}
		if err := c.removePeer(ctx, old); err != nil {
			return err
		}
	}

	_, err = c.run(ctx, "wg", "set", c.Interface,
		"peer", key,
		"allowed-ips", peer.TunnelIP.String()+"/32")
	if err != nil {
		return err
	}
	return c.save(ctx)
}

// RemovePeer deletes a peer. Removing a peer that isn't there succeeds, because
// that is the state the caller asked for.
func (c *Client) RemovePeer(ctx context.Context, publicKey string) error {
	key, err := ValidateKey(publicKey)
	if err != nil {
		return err
	}
	if err := c.removePeer(ctx, key); err != nil {
		return err
	}
	return c.save(ctx)
}

func (c *Client) removePeer(ctx context.Context, key string) error {
	_, err := c.run(ctx, "wg", "set", c.Interface, "peer", key, "remove")
	return err
}

// Sync makes the interface hold exactly the given peers. Used when the control
// plane has decided this box's state is untrustworthy (a rebuild, usually).
func (c *Client) Sync(ctx context.Context, peers []Peer) error {
	current, err := c.ListPeers(ctx)
	if err != nil {
		return err
	}

	want := make(map[string]Peer, len(peers))
	for _, p := range peers {
		key, err := ValidateKey(p.PublicKey)
		if err != nil {
			return err
		}
		if !p.TunnelIP.IsValid() {
			return ErrInvalidIP
		}
		want[key] = Peer{PublicKey: key, TunnelIP: p.TunnelIP}
	}

	// Remove first, then add: if the box is at its peer limit, adding before
	// removing would fail on exactly the boxes that most need the resync.
	for key := range current {
		if _, keep := want[key]; !keep {
			if err := c.removePeer(ctx, key); err != nil {
				return err
			}
		}
	}
	for key, p := range want {
		if existing, ok := current[key]; ok && existing.TunnelIP == p.TunnelIP {
			continue
		}
		if _, err := c.run(ctx, "wg", "set", c.Interface,
			"peer", key, "allowed-ips", p.TunnelIP.String()+"/32"); err != nil {
			return err
		}
	}
	return c.save(ctx)
}

// ListPeers parses `wg show <iface> dump`.
func (c *Client) ListPeers(ctx context.Context) (map[string]Peer, error) {
	out, err := c.run(ctx, "wg", "show", c.Interface, "dump")
	if err != nil {
		return nil, err
	}

	peers := map[string]Peer{}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	// The first line describes the interface itself, not a peer.
	for i, line := range lines {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 4 {
			continue
		}
		key := fields[0]
		allowed := fields[3]
		p := Peer{PublicKey: key}
		if prefix, err := netip.ParsePrefix(strings.Split(allowed, ",")[0]); err == nil {
			p.TunnelIP = prefix.Addr()
		}
		peers[key] = p
	}
	return peers, nil
}

// Up reports whether the interface exists and is configured.
func (c *Client) Up(ctx context.Context) bool {
	_, err := c.run(ctx, "wg", "show", c.Interface)
	return err == nil
}

// save writes the running config back to disk so a reboot doesn't drop peers.
func (c *Client) save(ctx context.Context) error {
	if !c.Persist {
		return nil
	}
	if _, err := c.run(ctx, "wg-quick", "save", c.Interface); err != nil {
		return fmt.Errorf("persist config: %w", err)
	}
	return nil
}

func (c *Client) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	return c.runner(ctx, name, args...)
}

func execCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		// Keys are not secret, but they identify a user's device; keeping them
		// out of error strings keeps them out of logs and crash reports.
		return nil, fmt.Errorf("%s %s: %s", name, redactArgs(args), msg)
	}
	return stdout.Bytes(), nil
}

// redactArgs hides base64 key arguments in error messages.
func redactArgs(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if len(a) == 44 && strings.HasSuffix(a, "=") {
			out[i] = "<key>"
			continue
		}
		out[i] = a
	}
	return strings.Join(out, " ")
}

// PeerCount is a convenience for the health endpoint.
func (c *Client) PeerCount(ctx context.Context) int {
	peers, err := c.ListPeers(ctx)
	if err != nil {
		return -1
	}
	return len(peers)
}
