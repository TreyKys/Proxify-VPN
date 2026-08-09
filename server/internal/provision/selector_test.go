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

// Country matching only breaks ties between servers of EQUAL priority. That
// ordering is load-bearing for the bill, not a detail: the Lagos box runs on
// metered Nigerian bandwidth at the worst priority in the fleet, and almost
// every user is in Nigeria. If country matching were ever compared before
// priority, every single user on "auto" would land on the most expensive box we
// own and the egress bill would follow.
//
// The Lagos IP is a feature people choose deliberately, never a default.
func TestAutoSelectionNeverDefaultsNigerianUsersToTheMeteredLagosBox(t *testing.T) {
	// Priorities and capacities mirror infra/fleet.json.
	fleet := []model.Server{
		srv("ng-lag-1", "NG", 90, 0, 50),
		srv("uk-lon-1", "GB", 10, 0, 500),
		srv("de-fsn-1", "DE", 20, 0, 500),
		srv("za-jnb-1", "ZA", 50, 0, 80),
	}
	sortServers(fleet, "NG")

	if got := codes(fleet)[0]; got != "uk-lon-1" {
		t.Fatalf("a Nigerian user on auto landed on %q, want uk-lon-1", got)
	}
	if last := codes(fleet)[len(fleet)-1]; last != "ng-lag-1" {
		t.Errorf("Lagos should be the last resort in auto-selection, got order %v", codes(fleet))
	}
}

// Explicitly asking for Lagos must still work — that is the whole point of
// having it. Capacity is what protects the box, not obscurity.
func TestFullLagosBoxYieldsToOthersInAutoSelection(t *testing.T) {
	fleet := []model.Server{
		srv("ng-lag-1", "NG", 90, 50, 50), // at capacity
		srv("uk-lon-1", "GB", 10, 400, 500),
	}
	sortServers(fleet, "NG")

	if codes(fleet)[0] != "uk-lon-1" {
		t.Errorf("order = %v, want the full metered box last", codes(fleet))
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
