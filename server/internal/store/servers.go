package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/treykys/proxify-vpn/server/internal/model"
)

const serverCols = `
	s.id, s.code, s.display_name, s.country_code, s.region, s.endpoint_host,
	s.endpoint_port, s.public_key, s.obfuscation, s.tunnel_subnet::text,
	s.agent_url, s.agent_token, s.status, s.capacity_peers, s.priority, s.last_seen_at,
	(SELECT count(*) FROM peer_assignments pa
	 WHERE pa.server_id = s.id AND pa.state IN ('pending', 'active'))::int AS live_peers`

// serverScan holds the raw column values for one server row. Columns that need
// conversion (jsonb, cidr, enum-ish text) land in the side fields and are turned
// into domain types by result().
type serverScan struct {
	srv    model.Server
	obf    []byte
	subnet string
	status string
}

func (ss *serverScan) dest() []any {
	return []any{
		&ss.srv.ID, &ss.srv.Code, &ss.srv.DisplayName, &ss.srv.CountryCode,
		&ss.srv.Region, &ss.srv.EndpointHost, &ss.srv.EndpointPort, &ss.srv.PublicKey,
		&ss.obf, &ss.subnet, &ss.srv.AgentURL, &ss.srv.AgentToken, &ss.status,
		&ss.srv.CapacityPeers, &ss.srv.Priority, &ss.srv.LastSeenAt, &ss.srv.LivePeers,
	}
}

func (ss *serverScan) result() (model.Server, error) {
	ss.srv.Status = model.ServerStatus(ss.status)
	if len(ss.obf) > 0 {
		if err := json.Unmarshal(ss.obf, &ss.srv.Obfuscation); err != nil {
			return model.Server{}, fmt.Errorf("server %s: decode obfuscation: %w", ss.srv.Code, err)
		}
	}
	p, err := netip.ParsePrefix(ss.subnet)
	if err != nil {
		return model.Server{}, fmt.Errorf("server %s: parse tunnel_subnet %q: %w", ss.srv.Code, ss.subnet, err)
	}
	ss.srv.TunnelSubnet = p
	return ss.srv, nil
}

func scanServer(scan func(dest ...any) error) (model.Server, error) {
	var ss serverScan
	if err := scan(ss.dest()...); err != nil {
		return model.Server{}, err
	}
	return ss.result()
}

func (s *Store) ServerByID(ctx context.Context, id string) (model.Server, error) {
	q := `SELECT ` + serverCols + ` FROM servers s WHERE s.id = $1`
	srv, err := scanServer(s.pool.QueryRow(ctx, q, id).Scan)
	return srv, mapErr(err)
}

func (s *Store) ServerByCode(ctx context.Context, code string) (model.Server, error) {
	q := `SELECT ` + serverCols + ` FROM servers s WHERE s.code = $1`
	srv, err := scanServer(s.pool.QueryRow(ctx, q, code).Scan)
	return srv, mapErr(err)
}

// Servers lists servers. onlyActive filters to those accepting new peers;
// draining servers stay in the list for existing peers but never get new ones.
func (s *Store) Servers(ctx context.Context, onlyActive bool) ([]model.Server, error) {
	q := `SELECT ` + serverCols + ` FROM servers s`
	if onlyActive {
		q += ` WHERE s.status = 'active'`
	}
	q += ` ORDER BY s.priority, s.code`

	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	var out []model.Server
	for rows.Next() {
		srv, err := scanServer(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, srv)
	}
	return out, rows.Err()
}

type UpsertServerParams struct {
	Code          string
	DisplayName   string
	CountryCode   string
	Region        string
	EndpointHost  string
	EndpointPort  int
	PublicKey     string
	Obfuscation   map[string]any
	TunnelSubnet  string
	AgentURL      string
	AgentToken    string
	Status        string
	CapacityPeers int
	Priority      int
}

// UpsertServer registers or updates an edge server. Used by the admin CLI after
// bash-provisioning a box (see edge/scripts/bootstrap.sh).
func (s *Store) UpsertServer(ctx context.Context, p UpsertServerParams) (model.Server, error) {
	obf, err := json.Marshal(p.Obfuscation)
	if err != nil {
		return model.Server{}, err
	}
	if p.Obfuscation == nil {
		obf = []byte(`{}`)
	}
	const q = `
		INSERT INTO servers (code, display_name, country_code, region, endpoint_host,
		                     endpoint_port, public_key, obfuscation, tunnel_subnet,
		                     agent_url, agent_token, status, capacity_peers, priority)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9::cidr,$10,$11,$12,$13,$14)
		ON CONFLICT (code) DO UPDATE SET
			display_name = EXCLUDED.display_name,
			country_code = EXCLUDED.country_code,
			region = EXCLUDED.region,
			endpoint_host = EXCLUDED.endpoint_host,
			endpoint_port = EXCLUDED.endpoint_port,
			public_key = EXCLUDED.public_key,
			obfuscation = EXCLUDED.obfuscation,
			tunnel_subnet = EXCLUDED.tunnel_subnet,
			agent_url = EXCLUDED.agent_url,
			agent_token = EXCLUDED.agent_token,
			status = EXCLUDED.status,
			capacity_peers = EXCLUDED.capacity_peers,
			priority = EXCLUDED.priority,
			updated_at = now()
		RETURNING id`
	var id string
	err = s.pool.QueryRow(ctx, q, p.Code, p.DisplayName, p.CountryCode, p.Region,
		p.EndpointHost, p.EndpointPort, p.PublicKey, obf, p.TunnelSubnet,
		p.AgentURL, p.AgentToken, p.Status, p.CapacityPeers, p.Priority).Scan(&id)
	if err != nil {
		return model.Server{}, mapErr(err)
	}
	return s.ServerByID(ctx, id)
}

func (s *Store) SetServerStatus(ctx context.Context, code string, status model.ServerStatus) error {
	const q = `UPDATE servers SET status = $2, updated_at = now() WHERE code = $1`
	tag, err := s.pool.Exec(ctx, q, code, string(status))
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) MarkServerSeen(ctx context.Context, id string) error {
	const q = `UPDATE servers SET last_seen_at = now() WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, id)
	return mapErr(err)
}
