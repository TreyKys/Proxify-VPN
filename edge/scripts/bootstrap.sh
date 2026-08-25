#!/usr/bin/env bash
#
# bootstrap.sh — turn a fresh Debian/Ubuntu box into a Proxify edge server.
#
# Installs WireGuard, the obfuscation layer (Xray + VLESS/XTLS Reality), and the
# Proxify edge agent, then prints the JSON needed to register the box with the
# control plane.
#
# Bash is the right tool here on purpose (brief §8): below ~5 servers, a script
# an operator can read top to bottom beats a configuration-management system
# nobody has debugged at 2am. Run it again any time — every step is idempotent.
#
# Usage:
#   ./bootstrap.sh --code de-fsn-1 --subnet 10.77.0.0/16 [--country DE] \
#                  [--region eu-central] [--wg-port 51820] [--reality-sni www.microsoft.com]
set -euo pipefail

CODE=""
SUBNET=""
COUNTRY=""
REGION=""
WG_PORT=51820
AGENT_PORT=8443
REALITY_SNI="www.microsoft.com"
WG_IF="wg0"
# HOSTNAME_FQDN enables a real TLS certificate for the agent. Without it the
# agent stays on loopback, because an unauthenticated-transport peer manager
# exposed to the internet is not something we ship.
HOSTNAME_FQDN=""
CONTROL_PLANE_IP=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --code)             CODE="$2"; shift 2 ;;
    --subnet)           SUBNET="$2"; shift 2 ;;
    --country)          COUNTRY="$2"; shift 2 ;;
    --region)           REGION="$2"; shift 2 ;;
    --wg-port)          WG_PORT="$2"; shift 2 ;;
    --agent-port)       AGENT_PORT="$2"; shift 2 ;;
    --reality-sni)      REALITY_SNI="$2"; shift 2 ;;
    --interface)        WG_IF="$2"; shift 2 ;;
    --hostname)         HOSTNAME_FQDN="$2"; shift 2 ;;
    --control-plane-ip) CONTROL_PLANE_IP="$2"; shift 2 ;;
    -h|--help)          sed -n '2,24p' "$0"; exit 0 ;;
    *) echo "unknown flag: $1" >&2; exit 1 ;;
  esac
done

[[ -n "$CODE"   ]] || { echo "--code is required (e.g. de-fsn-1)" >&2; exit 1; }
[[ -n "$SUBNET" ]] || { echo "--subnet is required (e.g. 10.77.0.0/16)" >&2; exit 1; }
[[ $EUID -eq 0  ]] || { echo "run as root" >&2; exit 1; }

log() { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$*" >&2; }

# The server takes .1 of the tunnel subnet; the control plane hands out .2 up.
SERVER_TUNNEL_IP="$(python3 - "$SUBNET" <<'PY'
import ipaddress, sys
net = ipaddress.ip_network(sys.argv[1], strict=False)
print(f"{net.network_address + 1}/{net.prefixlen}")
PY
)"

# --------------------------------------------------------------------- packages

log "installing packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq wireguard wireguard-tools nftables curl jq ca-certificates python3 unzip >/dev/null

# ------------------------------------------------------------------ kernel/sysctl

log "enabling IPv4 forwarding"
cat > /etc/sysctl.d/99-proxify.conf <<'EOF'
net.ipv4.ip_forward = 1
# Larger connection-tracking table: one box carries hundreds of users behind a
# single public IP, and the default table fills long before the CPU does.
net.netfilter.nf_conntrack_max = 262144
# BBR helps on the long, lossy Nigeria<->Europe path more than any other single
# kernel knob. It does not make anyone's link faster; it stops a lossy path from
# collapsing throughput the way CUBIC does.
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
EOF
sysctl -q --system || warn "some sysctl values were not applied (expected on some VPS kernels)"

# -------------------------------------------------------------------- WireGuard

install -d -m 0700 /etc/wireguard

if [[ ! -f /etc/wireguard/private.key ]]; then
  log "generating WireGuard server keypair"
  umask 077
  wg genkey > /etc/wireguard/private.key
  wg pubkey < /etc/wireguard/private.key > /etc/wireguard/public.key
  chmod 600 /etc/wireguard/private.key
else
  log "reusing existing WireGuard keypair"
fi

SERVER_PRIVKEY="$(cat /etc/wireguard/private.key)"
SERVER_PUBKEY="$(cat /etc/wireguard/public.key)"
PUBLIC_IF="$(ip -4 route show default | awk '/default/ {print $5; exit}')"
PUBLIC_IP="$(curl -fsS --max-time 10 https://api.ipify.org || hostname -I | awk '{print $1}')"

if [[ ! -f "/etc/wireguard/${WG_IF}.conf" ]]; then
  log "writing /etc/wireguard/${WG_IF}.conf"
  cat > "/etc/wireguard/${WG_IF}.conf" <<EOF
# Managed by Proxify. Peers are added by the edge agent and persisted here with
# 'wg-quick save', so a reboot does not disconnect every user on the box.
[Interface]
Address = ${SERVER_TUNNEL_IP}
ListenPort = ${WG_PORT}
PrivateKey = ${SERVER_PRIVKEY}
SaveConfig = false

# NAT tunnel traffic out of the public interface, and clamp MSS so TCP sessions
# survive the smaller tunnel MTU instead of stalling on paths that swallow the
# "fragmentation needed" messages — which is most mobile paths.
PostUp   = nft add table ip proxify 2>/dev/null || true
PostUp   = nft add chain ip proxify postrouting { type nat hook postrouting priority 100 \; } 2>/dev/null || true
PostUp   = nft add rule ip proxify postrouting ip saddr ${SUBNET} oifname "${PUBLIC_IF}" masquerade
PostUp   = nft add chain ip proxify forward { type filter hook forward priority 0 \; } 2>/dev/null || true
PostUp   = nft add rule ip proxify forward tcp flags syn tcp option maxseg size set rt mtu
PostDown = nft delete table ip proxify 2>/dev/null || true
EOF
  chmod 600 "/etc/wireguard/${WG_IF}.conf"
else
  log "keeping existing /etc/wireguard/${WG_IF}.conf (peers preserved)"
fi

systemctl enable --now "wg-quick@${WG_IF}" >/dev/null 2>&1 || systemctl restart "wg-quick@${WG_IF}"

# ------------------------------------------------------------------ queueing

# Fair queueing is the single highest-value knob on a congested box: it stops
# one user's download from starving every other user's call. See qdisc.sh.
log "installing queue discipline"
install -m 0755 "$(dirname "$0")/qdisc.sh" /usr/local/bin/proxify-qdisc.sh
install -m 0644 "$(dirname "$0")/../systemd/proxify-qdisc.service" \
  /etc/systemd/system/proxify-qdisc.service
sed -i "s|__WG_IF__|${WG_IF}|g" /etc/systemd/system/proxify-qdisc.service
systemctl daemon-reload
systemctl enable proxify-qdisc >/dev/null 2>&1 || true
systemctl restart proxify-qdisc || warn "queue discipline failed to apply; check 'tc qdisc show'"

# ------------------------------------------------------------------------- Xray

# Xray provides the DPI-resistant path: clients that cannot get UDP out reach a
# VLESS+Reality endpoint on 443 which forwards to the local WireGuard port.
#
# The routing rules below are the important part: this inbound can ONLY reach
# 127.0.0.1:${WG_PORT}. It is not a general-purpose proxy, so a leaked UUID
# cannot be used to relay arbitrary traffic through our IP.
if ! command -v xray >/dev/null 2>&1; then
  log "installing Xray"
  bash -c "$(curl -fsSL https://github.com/XTLS/Xray-install/raw/main/install-release.sh)" @ install >/dev/null
else
  log "Xray already installed"
fi

install -d -m 0755 /usr/local/etc/xray

if [[ ! -f /etc/proxify/reality.env ]]; then
  log "generating Reality keypair and client UUID"
  install -d -m 0700 /etc/proxify
  REALITY_KEYS="$(xray x25519)"
  REALITY_PRIVATE="$(echo "$REALITY_KEYS" | awk '/[Pp]rivate/ {print $NF}')"
  REALITY_PUBLIC="$(echo "$REALITY_KEYS" | awk '/[Pp]ublic/ {print $NF}')"
  REALITY_UUID="$(xray uuid)"
  REALITY_SHORTID="$(openssl rand -hex 8)"
  cat > /etc/proxify/reality.env <<EOF
REALITY_PRIVATE=${REALITY_PRIVATE}
REALITY_PUBLIC=${REALITY_PUBLIC}
REALITY_UUID=${REALITY_UUID}
REALITY_SHORTID=${REALITY_SHORTID}
REALITY_SNI=${REALITY_SNI}
EOF
  chmod 600 /etc/proxify/reality.env
fi
# shellcheck disable=SC1091
source /etc/proxify/reality.env

log "writing Xray config"
sed -e "s|__UUID__|${REALITY_UUID}|g" \
    -e "s|__PRIVATE_KEY__|${REALITY_PRIVATE}|g" \
    -e "s|__SHORT_ID__|${REALITY_SHORTID}|g" \
    -e "s|__SNI__|${REALITY_SNI}|g" \
    -e "s|__WG_PORT__|${WG_PORT}|g" \
    "$(dirname "$0")/../config/xray-reality.json.tmpl" > /usr/local/etc/xray/config.json
chmod 600 /usr/local/etc/xray/config.json

systemctl enable --now xray >/dev/null 2>&1 || systemctl restart xray

# ------------------------------------------------------------------ edge agent

install -d -m 0700 /etc/proxify /var/lib/proxify-edge

if [[ ! -f /etc/proxify/agent.token ]]; then
  log "generating edge agent token"
  openssl rand -hex 32 > /etc/proxify/agent.token
  chmod 600 /etc/proxify/agent.token
fi
AGENT_TOKEN="$(cat /etc/proxify/agent.token)"

if [[ -f "$(dirname "$0")/../bin/edge-agent" ]]; then
  log "installing edge-agent binary"
  install -m 0755 "$(dirname "$0")/../bin/edge-agent" /usr/local/bin/edge-agent
elif [[ ! -x /usr/local/bin/edge-agent ]]; then
  warn "edge-agent binary not found — build it with 'make -C edge build' and re-run"
fi

# TLS for the agent. The control plane talks to this port from the public
# internet, so it gets a real certificate — no self-signed cert plus a disabled
# verification flag, which is how "encrypted" quietly becomes "not".
TLS_ARGS=""
LISTEN_ADDR="127.0.0.1:${AGENT_PORT}"
AGENT_URL="http://127.0.0.1:${AGENT_PORT}"

if [[ -n "$HOSTNAME_FQDN" ]]; then
  log "obtaining a TLS certificate for ${HOSTNAME_FQDN}"
  apt-get install -y -qq certbot >/dev/null
  # http-01 on port 80; 443 belongs to Xray. Renewal reloads the agent.
  if [[ ! -d "/etc/letsencrypt/live/${HOSTNAME_FQDN}" ]]; then
    certbot certonly --standalone --non-interactive --agree-tos \
      --register-unsafely-without-email -d "${HOSTNAME_FQDN}" \
      --deploy-hook "systemctl restart proxify-edge-agent" \
      || warn "certbot failed — the agent will stay on loopback until a certificate exists"
  fi
  if [[ -d "/etc/letsencrypt/live/${HOSTNAME_FQDN}" ]]; then
    TLS_ARGS="--tls-cert /etc/letsencrypt/live/${HOSTNAME_FQDN}/fullchain.pem --tls-key /etc/letsencrypt/live/${HOSTNAME_FQDN}/privkey.pem"
    LISTEN_ADDR="0.0.0.0:${AGENT_PORT}"
    AGENT_URL="https://${HOSTNAME_FQDN}:${AGENT_PORT}"
  fi
else
  warn "no --hostname given: the agent is bound to loopback and the control plane cannot reach it."
  warn "Pass --hostname <fqdn pointing at this box> for a production install."
fi

log "installing systemd unit"
install -m 0644 "$(dirname "$0")/../systemd/proxify-edge-agent.service" \
  /etc/systemd/system/proxify-edge-agent.service
sed -i "s|__LISTEN_ADDR__|${LISTEN_ADDR}|; s|__WG_IF__|${WG_IF}|; s|__TLS_ARGS__|${TLS_ARGS}|" \
  /etc/systemd/system/proxify-edge-agent.service
systemctl daemon-reload
systemctl enable proxify-edge-agent >/dev/null 2>&1 || true
systemctl restart proxify-edge-agent

# ---------------------------------------------------------------------- firewall

log "applying firewall rules"

# The agent port is only opened when the agent is actually listening publicly,
# and it is restricted to the control plane's address when we know it. Defence
# in depth: the bearer token and TLS are the real controls, this is the backstop.
AGENT_FW_RULE="    # agent port closed: agent is on loopback"
if [[ "$LISTEN_ADDR" != 127.0.0.1:* ]]; then
  if [[ -n "$CONTROL_PLANE_IP" ]]; then
    AGENT_FW_RULE="    ip saddr ${CONTROL_PLANE_IP} tcp dport ${AGENT_PORT} accept"
  else
    AGENT_FW_RULE="    tcp dport ${AGENT_PORT} accept"
    warn "agent port ${AGENT_PORT} is open to the internet; pass --control-plane-ip to restrict it"
  fi
fi

cat > /etc/nftables.conf <<EOF
#!/usr/sbin/nft -f
flush ruleset

table inet filter {
  chain input {
    type filter hook input priority 0; policy drop;

    ct state established,related accept
    iif lo accept
    ip protocol icmp accept

    tcp dport 22 accept
    udp dport ${WG_PORT} accept
    tcp dport 443 accept
    # Port 80 is only needed for certificate issuance and renewal.
    tcp dport 80 accept
${AGENT_FW_RULE}
  }
  chain forward { type filter hook forward priority 0; policy accept; }
  chain output  { type filter hook output priority 0; policy accept; }
}
EOF
systemctl enable nftables >/dev/null 2>&1 || true
nft -f /etc/nftables.conf || warn "nftables rules failed to apply; check the ruleset before exposing this box"

# ------------------------------------------------------------------ registration

cat <<EOF

$(log "bootstrap complete")

Register this box with the control plane:

  curl -sS -X POST "\$CONTROL_PLANE/v1/admin/servers" \\
    -H "Authorization: Bearer \$PROXIFY_ADMIN_TOKEN" \\
    -H 'Content-Type: application/json' \\
    -d '$(jq -cn \
      --arg code "$CODE" \
      --arg display "$CODE" \
      --arg country "${COUNTRY:-XX}" \
      --arg region "${REGION:-unknown}" \
      --arg host "$PUBLIC_IP" \
      --argjson port "$WG_PORT" \
      --arg pubkey "$SERVER_PUBKEY" \
      --arg subnet "$SUBNET" \
      --arg agent "$AGENT_URL" \
      --arg token "$AGENT_TOKEN" \
      --arg uuid "$REALITY_UUID" \
      --arg realitypub "$REALITY_PUBLIC" \
      --arg shortid "$REALITY_SHORTID" \
      --arg sni "$REALITY_SNI" \
      '{code:$code, display_name:$display, country_code:$country, region:$region,
        endpoint_host:$host, endpoint_port:$port, public_key:$pubkey,
        tunnel_subnet:$subnet, agent_url:$agent, agent_token:$token,
        status:"draining", capacity_peers:500, priority:100,
        obfuscation:{type:"reality", tcp_port:443, uuid:$uuid,
                     public_key:$realitypub, short_id:$shortid, sni:$sni, flow:"xtls-rprx-vision"}}')'

The box registers as 'draining' — it accepts no new users until you verify it
and flip it to 'active':

  curl -sS -X POST "\$CONTROL_PLANE/v1/admin/servers/$CODE/status" \\
    -H "Authorization: Bearer \$PROXIFY_ADMIN_TOKEN" \\
    -d '{"status":"active"}'

EOF
