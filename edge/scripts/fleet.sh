#!/usr/bin/env bash
#
# fleet.sh — drive the whole edge fleet from infra/fleet.json.
#
# Run this from your machine, not from a box. It SSHes out, runs bootstrap.sh
# with the right flags for each location, and registers the result with the
# control plane.
#
# Adding location #7 is: one entry in fleet.json, then `fleet.sh provision <code>`.
#
# Usage:
#   fleet.sh list                    show the inventory
#   fleet.sh provision <code|all>    bootstrap and register
#   fleet.sh status                  what the control plane currently believes
#   fleet.sh promote <code>          draining -> active
#   fleet.sh drain <code>            active -> draining (stops new users)
#   fleet.sh resync <code>           re-push the full peer set to a box
#
# Environment:
#   CONTROL_PLANE          e.g. https://api.proxify.ng
#   PROXIFY_ADMIN_TOKEN    the admin bearer token
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
FLEET="${FLEET_FILE:-$REPO_ROOT/infra/fleet.json}"

log()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mxx\033[0m %s\n' "$*" >&2; exit 1; }

command -v jq >/dev/null || die "jq is required"
[[ -f "$FLEET" ]] || die "no fleet file at $FLEET"

need_control_plane() {
  [[ -n "${CONTROL_PLANE:-}" ]] || die "set CONTROL_PLANE (e.g. https://api.proxify.ng)"
  [[ -n "${PROXIFY_ADMIN_TOKEN:-}" ]] || die "set PROXIFY_ADMIN_TOKEN"
}

# location <code> — the inventory entry with defaults merged in.
location() {
  jq -e --arg code "$1" '
    .defaults as $d
    | .locations[]
    | select(.code == $code)
    | $d + .
  ' "$FLEET" || die "no location \"$1\" in $FLEET"
}

codes() { jq -r '.locations[].code' "$FLEET"; }

# enabled_codes — only locations that should actually have a box behind them.
enabled_codes() { jq -r '.locations[] | select(.enabled != false) | .code' "$FLEET"; }

cmd_list() {
  printf '%-10s %-12s %-14s %-4s %-16s %-8s %-9s %s\n' STATE CODE NAME CC SUBNET PRIORITY CAPACITY PROVIDER
  jq -r '.locations[] | [(if .enabled == false then "deferred" else "live" end),
                         .code, .display_name, .country_code, .tunnel_subnet,
                         (.priority|tostring), (.capacity_peers|tostring), .provider]
         | @tsv' "$FLEET" |
    while IFS=$'\t' read -r state code name cc subnet prio cap provider; do
      printf '%-10s %-12s %-14s %-4s %-16s %-8s %-9s %s\n' "$state" "$code" "$name" "$cc" "$subnet" "$prio" "$cap" "$provider"
    done
}

cmd_provision() {
  local code="${1:-}"
  [[ -n "$code" ]] || die "usage: fleet.sh provision <code|all>"
  need_control_plane

  if [[ "$code" == "all" ]]; then
    # Sequential on purpose. Bootstrapping in parallel means interleaved output
    # and, when something fails, no idea which box it was.
    for c in $(enabled_codes); do cmd_provision "$c"; done
    return
  fi

  local entry ssh_host hostname subnet cc region wg_port agent_port sni
  entry="$(location "$code")"

  # A deferred location is a decision we wrote down, not a box. Provisioning one
  # by name is allowed — you may be turning it on — but say so out loud.
  if [[ "$(jq -r '.enabled // true' <<<"$entry")" == "false" ]]; then
    warn "$code is marked enabled:false in $FLEET — $(jq -r '.provider' <<<"$entry")"
    warn "Provisioning it anyway. Set enabled:true there so the fleet file matches reality."
  fi
  ssh_host="$(jq -r '.ssh_host' <<<"$entry")"
  hostname="$(jq -r '.hostname' <<<"$entry")"
  subnet="$(jq -r '.tunnel_subnet' <<<"$entry")"
  cc="$(jq -r '.country_code' <<<"$entry")"
  region="$(jq -r '.region' <<<"$entry")"
  wg_port="$(jq -r '.wg_port' <<<"$entry")"
  agent_port="$(jq -r '.agent_port' <<<"$entry")"
  sni="$(jq -r '.reality_sni' <<<"$entry")"

  log "provisioning $code ($ssh_host)"

  log "  building the agent"
  make -C "$REPO_ROOT/edge" build >/dev/null

  log "  copying scripts and binary"
  ssh "$ssh_host" 'mkdir -p /opt/proxify-edge/{scripts,config,systemd,bin}'
  scp -q "$REPO_ROOT/edge/scripts/bootstrap.sh"                "$ssh_host:/opt/proxify-edge/scripts/"
  scp -q "$REPO_ROOT/edge/config/xray-reality.json.tmpl"       "$ssh_host:/opt/proxify-edge/config/"
  scp -q "$REPO_ROOT/edge/systemd/proxify-edge-agent.service"  "$ssh_host:/opt/proxify-edge/systemd/"
  scp -q "$REPO_ROOT/edge/bin/edge-agent"                      "$ssh_host:/opt/proxify-edge/bin/"

  # The control plane's public address, so the box can firewall the agent port
  # down to just us.
  local cp_ip
  cp_ip="$(getent hosts "$(printf '%s' "${CONTROL_PLANE#*://}" | cut -d/ -f1 | cut -d: -f1)" | awk '{print $1; exit}' || true)"

  log "  running bootstrap.sh"
  # shellcheck disable=SC2029  # deliberate client-side expansion of the inventory values
  ssh "$ssh_host" "chmod +x /opt/proxify-edge/scripts/bootstrap.sh && \
    /opt/proxify-edge/scripts/bootstrap.sh \
      --code '$code' --subnet '$subnet' --country '$cc' --region '$region' \
      --wg-port '$wg_port' --agent-port '$agent_port' --reality-sni '$sni' \
      --hostname '$hostname' ${cp_ip:+--control-plane-ip "$cp_ip"}"

  log "  registering with the control plane"
  cmd_register "$code"
}

# cmd_register reads the box's own state and posts it to the control plane. It
# reads from the box rather than from the inventory so what we register is what
# actually exists — a keypair the box generated, not one we assumed.
cmd_register() {
  local code="${1:-}"
  [[ -n "$code" ]] || die "usage: fleet.sh register <code>"
  need_control_plane

  local entry ssh_host hostname
  entry="$(location "$code")"
  ssh_host="$(jq -r '.ssh_host' <<<"$entry")"
  hostname="$(jq -r '.hostname' <<<"$entry")"

  local pubkey agent_token reality
  pubkey="$(ssh "$ssh_host" 'cat /etc/wireguard/public.key')"
  agent_token="$(ssh "$ssh_host" 'cat /etc/proxify/agent.token')"
  reality="$(ssh "$ssh_host" 'cat /etc/proxify/reality.env')"

  local reality_public reality_uuid reality_shortid reality_sni
  reality_public="$(grep '^REALITY_PUBLIC='  <<<"$reality" | cut -d= -f2-)"
  reality_uuid="$(grep '^REALITY_UUID='      <<<"$reality" | cut -d= -f2-)"
  reality_shortid="$(grep '^REALITY_SHORTID=' <<<"$reality" | cut -d= -f2-)"
  reality_sni="$(grep '^REALITY_SNI='        <<<"$reality" | cut -d= -f2-)"

  local payload
  payload="$(jq -cn \
    --argjson e "$entry" \
    --arg pubkey "$pubkey" \
    --arg agent "https://$hostname:$(jq -r '.agent_port' <<<"$entry")" \
    --arg token "$agent_token" \
    --arg rpub "$reality_public" \
    --arg ruuid "$reality_uuid" \
    --arg rshort "$reality_shortid" \
    --arg rsni "$reality_sni" \
    '{
       code: $e.code,
       display_name: $e.display_name,
       country_code: $e.country_code,
       region: $e.region,
       endpoint_host: $e.hostname,
       endpoint_port: $e.wg_port,
       public_key: $pubkey,
       tunnel_subnet: $e.tunnel_subnet,
       agent_url: $agent,
       agent_token: $token,
       capacity_peers: $e.capacity_peers,
       priority: $e.priority,
       # Always draining. A box goes live only when a human has checked it.
       status: "draining",
       obfuscation: {
         type: "reality", tcp_port: 443, uuid: $ruuid,
         public_key: $rpub, short_id: $rshort, sni: $rsni,
         flow: "xtls-rprx-vision"
       }
     }')"

  curl -fsS -X POST "$CONTROL_PLANE/v1/admin/servers" \
    -H "Authorization: Bearer $PROXIFY_ADMIN_TOKEN" \
    -H 'Content-Type: application/json' \
    -d "$payload" | jq .

  warn "$code registered as DRAINING — verify it, then: fleet.sh promote $code"
}

cmd_status() {
  need_control_plane
  log "servers the control plane will hand to users:"
  curl -fsS "$CONTROL_PLANE/v1/servers" | jq -r '.servers[] | "  \(.code)\t\(.country_code)\t\(.load)"'
  echo
  log "expected but not active (provisioned? promoted?):"
  local active
  active="$(curl -fsS "$CONTROL_PLANE/v1/servers" | jq -r '.servers[].code')"
  for c in $(enabled_codes); do
    grep -qx "$c" <<<"$active" || echo "  $c"
  done

  log "deferred by decision (see $FLEET):"
  jq -r '.locations[] | select(.enabled == false) | "  \(.code)\t\(.provider)"' "$FLEET"
}

cmd_set_status() {
  local code="$1" status="$2"
  need_control_plane
  curl -fsS -X POST "$CONTROL_PLANE/v1/admin/servers/$code/status" \
    -H "Authorization: Bearer $PROXIFY_ADMIN_TOKEN" \
    -H 'Content-Type: application/json' \
    -d "{\"status\":\"$status\"}" | jq .
}

cmd_resync() {
  local code="${1:-}"
  [[ -n "$code" ]] || die "usage: fleet.sh resync <code>"
  need_control_plane
  curl -fsS -X POST "$CONTROL_PLANE/v1/admin/servers/$code/resync" \
    -H "Authorization: Bearer $PROXIFY_ADMIN_TOKEN" | jq .
}

case "${1:-}" in
  list)      cmd_list ;;
  provision) cmd_provision "${2:-}" ;;
  register)  cmd_register "${2:-}" ;;
  status)    cmd_status ;;
  promote)   cmd_set_status "${2:?usage: fleet.sh promote <code>}" active ;;
  drain)     cmd_set_status "${2:?usage: fleet.sh drain <code>}" draining ;;
  resync)    cmd_resync "${2:-}" ;;
  *)         sed -n '2,26p' "$0"; exit 1 ;;
esac
