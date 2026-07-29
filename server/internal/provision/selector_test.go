package provision

import (
	"testing"

	"github.com/treykys/proxify-vpn/server/internal/model"
)

func srv(code, country string, priority, live, capacity int) model.Server {
	return model.Server{
		ID: code, Code: code, CountryCode: country,
		Priority: priority, LivePeers: live, CapacityPeers: capacity,
		Status: model.ServerActive,
	}
}

func codes(servers []model.Server) []string {
	out := make([]string, len(servers))
	for i, s := range servers {
		out[i] = s.Code
	}
	return out
}

func TestSortServersPrefersPriorityThenLoad(t *testing.T) {
	servers := []model.Server{
		srv("us-nyc-1", "US", 200, 10, 500),
		srv("de-fsn-2", "DE", 100, 400, 500),
		srv("de-fsn-1", "DE", 100, 50, 500),
	}
	sortServers(servers, "")

	want := []string{"de-fsn-1", "de-fsn-2", "us-nyc-1"}
	got := codes(servers)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestSortServersDeprioritizesFullBoxes(t *testing.T) {
	servers := []model.Server{
		srv("de-fsn-1", "DE", 10, 500, 500), // best priority but full
		srv("ng-lag-1", "NG", 90, 5, 500),
	}
	sortServers(servers, "")

	if codes(servers)[0] != "ng-lag-1" {
		t.Errorf("order = %v; a full server must not be picked first", codes(servers))
	}
}

func TestSortServersBreaksTiesOnClientCountry(t *testing.T) {
	servers := []model.Server{
		srv("de-fsn-1", "DE", 100, 10, 500),
		srv("ng-lag-1", "NG", 100, 10, 500),
	}
	sortServers(servers, "NG")

	if codes(servers)[0] != "ng-lag-1" {
		t.Errorf("order = %v, want the in-country server first", codes(servers))
	}
}

func TestBackoffGrowsAndIsCapped(t *testing.T) {
	first := Backoff(1)
	if first < backoffBase/2 || first > backoffBase*2 {
		t.Errorf("Backoff(1) = %v, want roughly %v", first, backoffBase)
	}

	// Must grow between early attempts...
	if Backoff(5) <= Backoff(1) {
		t.Error("backoff did not grow with attempts")
	}
	// ...and never exceed the cap, jitter included. A user's tunnel is what is
	// waiting on this; an unbounded backoff would mean a peer that never lands.
	for attempt := range 40 {
		if d := Backoff(attempt); d > backoffMax*12/10 {
			t.Errorf("Backoff(%d) = %v, exceeds the cap", attempt, d)
		}
	}
}
