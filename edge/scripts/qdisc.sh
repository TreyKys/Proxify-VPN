#!/usr/bin/env bash
#
# qdisc.sh — fair queueing on the edge box.
#
# This is the server half of the app-profiles system. The client marks packets
# by class (see android/core/.../TrafficClassifier.kt); this is what honours the
# marks and, more importantly, keeps one heavy flow from starving everything
# else on the box.
#
# What it actually buys, stated honestly: it does not create bandwidth. It
# decides who waits. Without it, one user's file download fills the queue and
# every other user's call, tap and page load queues behind it — which they
# experience as "the VPN is slow". With it, the download waits and the call
# doesn't. That is the whole trick, and it is worth more on a congested link
# than any amount of tuning elsewhere.
set -eu

WG_IF="${1:-wg0}"
PUB_IF="$(ip -4 route show default | awk '/default/ {print $5; exit}')"

log() { printf '==> %s\n' "$*"; }

apply() {
    local dev="$1"; shift
    tc qdisc replace dev "$dev" root "$@" >/dev/null 2>&1
}

# cake is the right tool and does everything in one qdisc; fq_codel is the
# fallback for kernels without sch_cake, which is common on cheap VPS images.
#
#   diffserv4    - four priority tins, which is what the client's DSCP marks select
#   dual-dsthost - fairness BETWEEN clients as well as between flows, so one
#                  user cannot degrade the box for everyone else
configure() {
    local dev="$1" hostmode="$2"
    if apply "$dev" cake diffserv4 "$hostmode"; then
        log "$dev: cake (diffserv4, $hostmode)"
    elif apply "$dev" fq_codel; then
        log "$dev: fq_codel (cake unavailable — no per-class priority, still fair between flows)"
    else
        log "$dev: could not set a queue discipline; leaving the kernel default"
    fi
}

# Traffic leaving wg0 is heading TO clients: fairness per destination host.
configure "$WG_IF" dual-dsthost

# Traffic leaving the public interface is heading to the internet on behalf of
# clients: fairness per source host.
[ -n "$PUB_IF" ] && configure "$PUB_IF" dual-srchost

exit 0
