package store

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/treykys/proxify-vpn/server/internal/model"
)

// ErrNoAddresses means the server's tunnel subnet is exhausted. Operationally
// this should never fire before capacity_peers is hit; if it does, the subnet
// was sized wrong.
var ErrNoAddresses = errors.New("store: tunnel subnet exhausted")

const peerCols = `
	pa.id, pa.device_id, pa.server_id, pa.public_key, COALESCE(pa.prev_public_key, ''),
	host(pa.tunnel_ip), pa.state, pa.revision, pa.applied_revision, pa.attempts,
	COALESCE(pa.last_error, ''), pa.next_attempt_at, pa.created_at, pa.activated_at`

type peerScan struct {
	p     model.PeerAssignment
	ip    string
	state string
}

func (ps *peerScan) dest() []any {
	return []any{
		&ps.p.ID, &ps.p.DeviceID, &ps.p.ServerID, &ps.p.PublicKey, &ps.p.PrevPublicKey,
		&ps.ip, &ps.state, &ps.p.Revision, &ps.p.AppliedRevision, &ps.p.Attempts,
		&ps.p.LastError, &ps.p.NextAttemptAt, &ps.p.CreatedAt, &ps.p.ActivatedAt,
	}
}

func (ps *peerScan) result() (model.PeerAssignment, error) {
	ps.p.State = model.PeerState(ps.state)
	addr, err := netip.ParseAddr(ps.ip)
	if err != nil {
		return model.PeerAssignment{}, fmt.Errorf("peer %s: parse tunnel_ip %q: %w", ps.p.ID, ps.ip, err)
	}
	ps.p.TunnelIP = addr
	return ps.p, nil
}

func scanPeer(scan func(dest ...any) error) (model.PeerAssignment, error) {
	var ps peerScan
	if err := scan(ps.dest()...); err != nil {
		return model.PeerAssignment{}, err
	}
	return ps.result()
}

// EnsureAssignment is the write half of §7: it makes the desired state of
// (device, server) be "this key, installed".
//
// It is idempotent by construction:
//   - no live row        -> allocate an IP, insert as pending (revision 1)
//   - live row, same key -> returned untouched; a peer already active stays
//     active and the caller does no edge work
//   - live row, new key  -> same IP kept, public_key replaced, revision bumped,
//     state forced back to pending so the reconciler re-pushes
//   - revoking row       -> resurrected to pending on the same IP
//
// Keeping the IP stable across key rotation matters: the client's tun interface
// address doesn't change, so an in-flight reconnect doesn't have to renumber.
func (s *Store) EnsureAssignment(ctx context.Context, deviceID, serverID, publicKey string) (model.PeerAssignment, error) {
	var out model.PeerAssignment
	err := s.InTx(ctx, func(tx pgx.Tx) error {
		// Serialize IP allocation per server. Contention is per-server and the
		// critical section is a single index scan, so this is cheap.
		const lock = `SELECT tunnel_subnet::text, capacity_peers FROM servers WHERE id = $1 FOR UPDATE`
		var subnetText string
		var capacity int
		if err := tx.QueryRow(ctx, lock, serverID).Scan(&subnetText, &capacity); err != nil {
			return err
		}

		const existing = `SELECT ` + peerCols + `
			FROM peer_assignments pa
			WHERE pa.device_id = $1 AND pa.server_id = $2 AND pa.state <> 'revoked'
			FOR UPDATE`
		p, err := scanPeer(tx.QueryRow(ctx, existing, deviceID, serverID).Scan)
		switch {
		case err == nil:
			if p.PublicKey == publicKey && p.State != model.PeerRevoking {
				out = p
				return nil
			}
			const update = `
				UPDATE peer_assignments AS pa
				SET public_key = $2,
				    -- Remember the key being superseded so the edge can drop it.
				    -- COALESCE keeps the oldest un-applied one across repeated
				    -- rotations that happen before any of them reached the box.
				    prev_public_key = CASE
				        WHEN pa.public_key = $2 THEN pa.prev_public_key
				        ELSE COALESCE(pa.prev_public_key, pa.public_key)
				    END,
				    revision = pa.revision + 1,
				    state = 'pending',
				    attempts = 0,
				    last_error = NULL,
				    next_attempt_at = now(),
				    revoked_at = NULL,
				    updated_at = now()
				WHERE pa.id = $1
				RETURNING ` + peerCols
			out, err = scanPeer(tx.QueryRow(ctx, update, p.ID, publicKey).Scan)
			return err
		case errors.Is(err, pgx.ErrNoRows):
			ip, err := allocateIP(ctx, tx, serverID, subnetText)
			if err != nil {
				return err
			}
			const insert = `
				INSERT INTO peer_assignments AS pa
				    (device_id, server_id, public_key, tunnel_ip, state)
				VALUES ($1, $2, $3, $4::inet, 'pending')
				RETURNING ` + peerCols
			out, err = scanPeer(tx.QueryRow(ctx, insert, deviceID, serverID, publicKey, ip.String()).Scan)
			return err
		default:
			return err
		}
	})
	return out, mapErr(err)
}

// allocateIP picks the lowest free host address in the server's subnet.
// .1 is the server itself, so allocation starts at offset 2.
func allocateIP(ctx context.Context, tx pgx.Tx, serverID, subnetText string) (netip.Addr, error) {
	prefix, err := netip.ParsePrefix(subnetText)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parse subnet %q: %w", subnetText, err)
	}
	if !prefix.Addr().Is4() {
		return netip.Addr{}, fmt.Errorf("tunnel subnet %s is not IPv4", subnetText)
	}
	hostBits := 32 - prefix.Bits()
	maxOffset := int64(1)<<hostBits - 2 // exclude network and broadcast
	if maxOffset < 1 {
		return netip.Addr{}, ErrNoAddresses
	}

	const q = `
		SELECT host(candidate)
		FROM (
			SELECT set_masklen($1::cidr::inet, 32) + g AS candidate
			FROM generate_series(2, $2::bigint) g
		) c
		WHERE NOT EXISTS (
			SELECT 1 FROM peer_assignments pa
			WHERE pa.server_id = $3 AND pa.state <> 'revoked' AND pa.tunnel_ip = c.candidate
		)
		LIMIT 1`
	var ip string
	if err := tx.QueryRow(ctx, q, subnetText, maxOffset, serverID).Scan(&ip); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return netip.Addr{}, ErrNoAddresses
		}
		return netip.Addr{}, err
	}
	return netip.ParseAddr(ip)
}

// MarkApplied records that the edge confirmed the given revision. The revision
// guard means a slow reply about an old key can't mark a newer key live.
func (s *Store) MarkApplied(ctx context.Context, id string, revision int64) error {
	const q = `
		UPDATE peer_assignments
		SET state = 'active',
		    applied_revision = GREATEST(applied_revision, $2),
		    -- The superseded key is gone from the box now; forget it so a later
		    -- resync doesn't keep asking for a removal that already happened.
		    prev_public_key = NULL,
		    attempts = 0,
		    last_error = NULL,
		    activated_at = COALESCE(activated_at, now()),
		    updated_at = now()
		WHERE id = $1 AND state = 'pending' AND revision <= $2`
	_, err := s.pool.Exec(ctx, q, id, revision)
	return mapErr(err)
}

// MarkRevoked is the terminal transition. The row is kept (not deleted) so the
// IP stays out of circulation until a later sweep reclaims it, which avoids
// handing a fresh peer an address an old client is still sending from.
func (s *Store) MarkRevoked(ctx context.Context, id string, revision int64) error {
	const q = `
		UPDATE peer_assignments
		SET state = 'revoked',
		    applied_revision = GREATEST(applied_revision, $2),
		    revoked_at = now(),
		    attempts = 0,
		    last_error = NULL,
		    updated_at = now()
		WHERE id = $1 AND state = 'revoking' AND revision <= $2`
	_, err := s.pool.Exec(ctx, q, id, revision)
	return mapErr(err)
}

// MarkAttemptFailed records a failed edge push and schedules the retry.
func (s *Store) MarkAttemptFailed(ctx context.Context, id string, cause string, retryAt time.Time) error {
	const q = `
		UPDATE peer_assignments
		SET attempts = attempts + 1,
		    last_error = left($2, 500),
		    next_attempt_at = $3,
		    updated_at = now()
		WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, id, cause, retryAt)
	return mapErr(err)
}

// BeginRevokeForUser flips every live assignment belonging to a user to
// 'revoking'. Called on expiry, suspension, and account deletion.
func (s *Store) BeginRevokeForUser(ctx context.Context, userID string) (int, error) {
	const q = `
		UPDATE peer_assignments pa
		SET state = 'revoking', revision = revision + 1, attempts = 0,
		    next_attempt_at = now(), updated_at = now()
		FROM devices d
		WHERE d.id = pa.device_id AND d.user_id = $1 AND pa.state IN ('pending', 'active')`
	tag, err := s.pool.Exec(ctx, q, userID)
	return int(tag.RowsAffected()), mapErr(err)
}

func (s *Store) BeginRevokeForDevice(ctx context.Context, deviceID string) (int, error) {
	const q = `
		UPDATE peer_assignments
		SET state = 'revoking', revision = revision + 1, attempts = 0,
		    next_attempt_at = now(), updated_at = now()
		WHERE device_id = $1 AND state IN ('pending', 'active')`
	tag, err := s.pool.Exec(ctx, q, deviceID)
	return int(tag.RowsAffected()), mapErr(err)
}

// BeginRevokeOtherServers keeps one device to one live server. A user switching
// location gets the new peer provisioned first, then the old ones torn down, so
// a failed switch never leaves them with no tunnel at all.
func (s *Store) BeginRevokeOtherServers(ctx context.Context, deviceID, keepServerID string) (int, error) {
	const q = `
		UPDATE peer_assignments
		SET state = 'revoking', revision = revision + 1, attempts = 0,
		    next_attempt_at = now(), updated_at = now()
		WHERE device_id = $1 AND server_id <> $2 AND state IN ('pending', 'active')`
	tag, err := s.pool.Exec(ctx, q, deviceID, keepServerID)
	return int(tag.RowsAffected()), mapErr(err)
}

// DueAssignment pairs an assignment with the server it belongs to.
type DueAssignment struct {
	Peer   model.PeerAssignment
	Server model.Server
}

// DueAssignments claims a batch of reconciler work with SKIP LOCKED so several
// control-plane instances can run the reconciler without stepping on each other.
func (s *Store) DueAssignments(ctx context.Context, limit int) ([]DueAssignment, error) {
	q := `
		SELECT ` + peerCols + `, ` + serverCols + `
		FROM peer_assignments pa
		JOIN servers s ON s.id = pa.server_id
		WHERE pa.state IN ('pending', 'revoking') AND pa.next_attempt_at <= now()
		ORDER BY pa.next_attempt_at
		LIMIT $1
		FOR UPDATE OF pa SKIP LOCKED`

	var out []DueAssignment
	err := s.InTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, q, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				ps peerScan
				ss serverScan
			)
			dest := append(ps.dest(), ss.dest()...)
			if err := rows.Scan(dest...); err != nil {
				return err
			}
			peer, err := ps.result()
			if err != nil {
				return err
			}
			srv, err := ss.result()
			if err != nil {
				return err
			}
			out = append(out, DueAssignment{Peer: peer, Server: srv})
		}
		return rows.Err()
	})
	return out, mapErr(err)
}

func (s *Store) AssignmentsForDevice(ctx context.Context, deviceID string) ([]model.PeerAssignment, error) {
	q := `SELECT ` + peerCols + ` FROM peer_assignments pa
	      WHERE pa.device_id = $1 AND pa.state <> 'revoked' ORDER BY pa.created_at`
	rows, err := s.pool.Query(ctx, q, deviceID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	var out []model.PeerAssignment
	for rows.Next() {
		p, err := scanPeer(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Assignment(ctx context.Context, id string) (model.PeerAssignment, error) {
	q := `SELECT ` + peerCols + ` FROM peer_assignments pa WHERE pa.id = $1`
	p, err := scanPeer(s.pool.QueryRow(ctx, q, id).Scan)
	return p, mapErr(err)
}

// LivePeersForServer is the full desired peer set for one edge box, used by the
// resync path when an edge server has been rebuilt and lost its wg state.
func (s *Store) LivePeersForServer(ctx context.Context, serverID string) ([]model.PeerAssignment, error) {
	q := `SELECT ` + peerCols + ` FROM peer_assignments pa
	      WHERE pa.server_id = $1 AND pa.state IN ('pending', 'active') ORDER BY pa.tunnel_ip`
	rows, err := s.pool.Query(ctx, q, serverID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	var out []model.PeerAssignment
	for rows.Next() {
		p, err := scanPeer(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkServerPeersStale forces a re-push of every live peer on a server. Called
// when an edge box reports a cold start (rebuilt host, wiped wg config).
func (s *Store) MarkServerPeersStale(ctx context.Context, serverID string) (int, error) {
	const q = `
		UPDATE peer_assignments
		SET state = 'pending', revision = revision + 1, attempts = 0,
		    next_attempt_at = now(), updated_at = now()
		WHERE server_id = $1 AND state IN ('pending', 'active')`
	tag, err := s.pool.Exec(ctx, q, serverID)
	return int(tag.RowsAffected()), mapErr(err)
}
