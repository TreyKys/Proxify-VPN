package provision

import (
	"context"
	"sort"
	"strings"

	"github.com/treykys/proxify-vpn/server/internal/model"
)

// candidates returns the servers to try, best first.
//
// v1 selection is deliberately simple (brief §4: "nearest / auto" plus an
// explicit picker). The ordering rules, in priority order:
//
//  1. an explicitly requested server always wins — a user picking a Nigerian IP
//     for local content must get one, never a "better" German box
//  2. servers below capacity beat servers at capacity
//  3. lower priority number wins (we set this per box: Lagos and Falkenstein
//     ahead of overflow boxes)
//  4. a server in the caller's own country wins ties (relevant once we have
//     more than one Lagos box)
//  5. least loaded wins remaining ties
//
// Even when the request names a server, we append the other active servers as
// failover candidates so a single dead box doesn't mean a dead app.
func (s *Service) candidates(ctx context.Context, req Request) ([]model.Server, error) {
	active, err := s.store.Servers(ctx, true)
	if err != nil {
		return nil, err
	}

	code := strings.TrimSpace(strings.ToLower(req.ServerCode))
	if code != "" && code != "auto" {
		chosen, err := s.store.ServerByCode(ctx, code)
		if err != nil {
			return nil, ErrNoServer
		}
		if chosen.Status != model.ServerActive {
			return nil, ErrNoServer
		}
		rest := make([]model.Server, 0, len(active))
		for _, srv := range active {
			if srv.ID != chosen.ID {
				rest = append(rest, srv)
			}
		}
		sortServers(rest, req.ClientCountry)
		return append([]model.Server{chosen}, rest...), nil
	}

	if len(active) == 0 {
		return nil, ErrNoServer
	}
	sortServers(active, req.ClientCountry)
	return active, nil
}

func sortServers(servers []model.Server, clientCountry string) {
	cc := strings.ToUpper(strings.TrimSpace(clientCountry))
	sort.SliceStable(servers, func(i, j int) bool {
		a, b := servers[i], servers[j]

		aFull, bFull := atCapacity(a), atCapacity(b)
		if aFull != bFull {
			return !aFull
		}
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		if cc != "" {
			aHome := strings.EqualFold(a.CountryCode, cc)
			bHome := strings.EqualFold(b.CountryCode, cc)
			if aHome != bHome {
				return aHome
			}
		}
		if a.LivePeers != b.LivePeers {
			return a.LivePeers < b.LivePeers
		}
		return a.Code < b.Code
	})
}

func atCapacity(s model.Server) bool {
	return s.CapacityPeers > 0 && s.LivePeers >= s.CapacityPeers
}
