package wg

import (
	"context"
	"net/netip"
	"strings"
	"testing"
)

// recorder captures the commands the client would run, so the tests assert on
// the exact argv reaching exec — the place where a validation gap would turn
// into a command running on a production box.
type recorder struct {
	calls  [][]string
	output map[string]string
	fail   map[string]bool
}

func newRecorder() *recorder {
	return &recorder{output: map[string]string{}, fail: map[string]bool{}}
}

func (r *recorder) run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	r.calls = append(r.calls, call)
	joined := strings.Join(call, " ")
	if r.fail[joined] {
		return nil, context.DeadlineExceeded
	}
	for prefix, out := range r.output {
		if strings.HasPrefix(joined, prefix) {
			return []byte(out), nil
		}
	}
	return nil, nil
}

func newTestClient() (*Client, *recorder) {
	rec := newRecorder()
	c := New("wg0")
	c.runner = rec.run
	c.Persist = false
	return c, rec
}

const (
	keyA = "QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE="
	keyB = "QkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkI="
)

func TestValidateKeyRejectsInjection(t *testing.T) {
	bad := []string{
		"",
		"not-a-key",
		"; rm -rf / #",
		"QUFB QUFB",
		strings.Repeat("A", 44), // right length, but not valid base64 padding
	}
	for _, k := range bad {
		if _, err := ValidateKey(k); err == nil {
			t.Errorf("ValidateKey(%q) accepted a bad key", k)
		}
	}
	if _, err := ValidateKey(keyA); err != nil {
		t.Errorf("ValidateKey rejected a valid key: %v", err)
	}
}

func TestSetPeerRunsExpectedCommand(t *testing.T) {
	c, rec := newTestClient()
	ip := netip.MustParseAddr("10.77.0.5")

	if err := c.SetPeer(context.Background(), Peer{PublicKey: keyA, TunnelIP: ip}, ""); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("calls = %v, want exactly one", rec.calls)
	}
	got := strings.Join(rec.calls[0], " ")
	want := "wg set wg0 peer " + keyA + " allowed-ips 10.77.0.5/32"
	if got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
}

// A rotation must remove the old key before installing the new one: the failure
// mode we refuse to allow is "both keys work".
func TestSetPeerRemovesReplacedKeyFirst(t *testing.T) {
	c, rec := newTestClient()
	ip := netip.MustParseAddr("10.77.0.5")

	if err := c.SetPeer(context.Background(), Peer{PublicKey: keyB, TunnelIP: ip}, keyA); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 2 {
		t.Fatalf("calls = %v, want remove then set", rec.calls)
	}
	if first := strings.Join(rec.calls[0], " "); !strings.Contains(first, keyA) || !strings.HasSuffix(first, "remove") {
		t.Errorf("first command = %q, want removal of the old key", first)
	}
	if second := strings.Join(rec.calls[1], " "); !strings.Contains(second, keyB) {
		t.Errorf("second command = %q, want the new key installed", second)
	}
}

func TestSetPeerRejectsBadInput(t *testing.T) {
	c, rec := newTestClient()
	ip := netip.MustParseAddr("10.77.0.5")

	if err := c.SetPeer(context.Background(), Peer{PublicKey: "bogus", TunnelIP: ip}, ""); err == nil {
		t.Error("accepted an invalid key")
	}
	if err := c.SetPeer(context.Background(), Peer{PublicKey: keyA}, ""); err == nil {
		t.Error("accepted an invalid address")
	}
	if len(rec.calls) != 0 {
		t.Errorf("invalid input still reached exec: %v", rec.calls)
	}
}

func TestListPeersParsesDump(t *testing.T) {
	c, rec := newTestClient()
	// Line 1 is the interface; the rest are peers.
	rec.output["wg show wg0 dump"] = strings.Join([]string{
		"privkey\tpubkey\t51820\toff",
		keyA + "\t(none)\t102.89.1.1:1234\t10.77.0.2/32\t1699999999\t1024\t2048\t25",
		keyB + "\t(none)\t(none)\t10.77.0.3/32\t0\t0\t0\toff",
	}, "\n")

	peers, err := c.ListPeers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 2 {
		t.Fatalf("peers = %v, want 2", peers)
	}
	if got := peers[keyA].TunnelIP.String(); got != "10.77.0.2" {
		t.Errorf("peer A address = %q", got)
	}
}

func TestSyncRemovesUnknownPeersBeforeAdding(t *testing.T) {
	c, rec := newTestClient()
	rec.output["wg show wg0 dump"] = strings.Join([]string{
		"privkey\tpubkey\t51820\toff",
		keyA + "\t(none)\t(none)\t10.77.0.2/32\t0\t0\t0\toff",
	}, "\n")

	// Desired state has only keyB, so keyA must go.
	err := c.Sync(context.Background(), []Peer{
		{PublicKey: keyB, TunnelIP: netip.MustParseAddr("10.77.0.3")},
	})
	if err != nil {
		t.Fatal(err)
	}

	var removeIdx, addIdx = -1, -1
	for i, call := range rec.calls {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, keyA) && strings.HasSuffix(joined, "remove") {
			removeIdx = i
		}
		if strings.Contains(joined, keyB) && strings.Contains(joined, "allowed-ips") {
			addIdx = i
		}
	}
	if removeIdx == -1 {
		t.Fatal("stale peer was not removed")
	}
	if addIdx == -1 {
		t.Fatal("new peer was not added")
	}
	if removeIdx > addIdx {
		t.Error("removal must happen before addition so a full box can still resync")
	}
}

func TestRedactArgsHidesKeys(t *testing.T) {
	got := redactArgs([]string{"set", "wg0", "peer", keyA, "allowed-ips", "10.77.0.2/32"})
	if strings.Contains(got, keyA) {
		t.Errorf("redactArgs leaked a key: %q", got)
	}
	if !strings.Contains(got, "10.77.0.2/32") {
		t.Errorf("redactArgs removed useful context: %q", got)
	}
}
