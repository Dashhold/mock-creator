#!/bin/sh
# Writes the parts of the nginx configuration that depend on where the container
# runs. The nginx image's entrypoint runs every executable script in
# /docker-entrypoint.d/ in name order before it starts nginx. This one runs after
# 20-envsubst-on-templates.sh has rendered nginx.conf, and a non-zero exit stops
# the container instead of starting nginx half configured.
#
#   PORT                  port to listen on; 80 unless the platform sets it
#   API_UPSTREAM          host:port of the API; api:8080 unless set
#   BASIC_AUTH_USER       set both to require a login for the whole app
#   BASIC_AUTH_PASSWORD
#   API_KEY               the API's shared secret, added to every forwarded call
set -eu

ME=$(basename "$0")
OUT=/etc/nginx/mock-creator

log() { echo "$ME: $*"; }
fail() {
    echo "$ME: error: $*" >&2
    exit 1
}

mkdir -p "$OUT"

# --- the values rendered into the configuration --------------------------------
# Checked here because nginx would otherwise start from a broken configuration,
# or be handed something other than a port and an address.
port=${PORT:-80}
log "PORT=$port"
case "$port" in
    '' | *[!0-9]*) fail "PORT must be a port number, got '$port'" ;;
esac

upstream=${API_UPSTREAM:-api:8080}
log "API_UPSTREAM=$upstream"
upstream_host=${upstream%:*}
upstream_port=${upstream##*:}
bad_upstream="API_UPSTREAM must be host:port, for example api:8080; got '$upstream'"
case "$upstream" in
    *:*) ;;
    *) fail "$bad_upstream" ;;
esac
case "$upstream_host" in
    '' | *[!A-Za-z0-9.-]*) fail "$bad_upstream" ;;
esac
case "$upstream_port" in
    '' | *[!0-9]*) fail "$bad_upstream" ;;
esac

# --- IPv6 ------------------------------------------------------------------------
# Listen on both address families where the container has IPv6, as the stock
# nginx image does for its own default site.
if [ -f /proc/net/if_inet6 ]; then
    printf 'listen [::]:%s;\n' "$port" > "$OUT/listen-ipv6.conf"
else
    : > "$OUT/listen-ipv6.conf"
fi

# --- DNS for finding the API ---------------------------------------------------------
# The container's own nameservers: Docker's embedded DNS under compose, Railway's
# internal DNS on Railway. nginx wants IPv6 addresses in brackets.
resolvers=$(awk 'BEGIN { ORS = " " } $1 == "nameserver" { if ($2 ~ /:/) print "[" $2 "]"; else print $2 }' /etc/resolv.conf 2>/dev/null || true)
resolvers=${resolvers% }
if [ -n "$resolvers" ]; then
    printf 'resolver %s valid=10s;\n' "$resolvers" > "$OUT/resolver.conf"
else
    : > "$OUT/resolver.conf"
    log "warning: no nameserver in /etc/resolv.conf, so API_UPSTREAM only works as an IP address"
fi

# --- a login for the whole app ---------------------------------------------------------
user=${BASIC_AUTH_USER:-}
password=${BASIC_AUTH_PASSWORD:-}
newline='
'
if [ -n "$user" ] || [ -n "$password" ]; then
    if [ -z "$user" ] || [ -z "$password" ]; then
        fail "set both BASIC_AUTH_USER and BASIC_AUTH_PASSWORD, or neither"
    fi
    case "$user" in
        *:* | *"$newline"*) fail "BASIC_AUTH_USER cannot contain ':' or a line break" ;;
    esac
    case "$password" in
        *"$newline"*) fail "BASIC_AUTH_PASSWORD cannot contain a line break" ;;
    esac

    # apr1 is checked by nginx's own code rather than the C library's, so it
    # works on any base image. The hash never leaves this container, which holds
    # the password in its environment anyway, so a slower hash would add nothing.
    hash=$(printf '%s\n' "$password" | openssl passwd -apr1 -stdin) ||
        fail "could not hash BASIC_AUTH_PASSWORD"
    printf '%s:%s\n' "$user" "$hash" > "$OUT/htpasswd"
    # nginx's worker processes read this file on every request, as user nginx.
    if chown root:nginx "$OUT/htpasswd" 2>/dev/null; then
        chmod 640 "$OUT/htpasswd"
    else
        # Not running as root, so the workers run as this same user.
        chmod 600 "$OUT/htpasswd"
    fi

    printf 'auth_basic "Mock Creator";\nauth_basic_user_file %s/htpasswd;\n' "$OUT" > "$OUT/auth.conf"
    log "a login is required (user '$user')"
else
    : > "$OUT/auth.conf"
    log "warning: BASIC_AUTH_USER and BASIC_AUTH_PASSWORD are not set, so anyone who can reach this address can use the app; set both before exposing it"
fi

# --- the API's shared secret -----------------------------------------------------------
# Added to every forwarded API call, so it never reaches the browser and the
# app's plain download links carry it too.
key=${API_KEY:-}
if [ -n "$key" ]; then
    # Nothing that could end or escape the quoted string it is written into.
    case "$key" in
        *[!A-Za-z0-9._~+/=-]*) fail "API_KEY may only contain letters, digits and . _ ~ + / = -" ;;
    esac
    (
        umask 077
        printf 'proxy_set_header X-API-Key "%s";\n' "$key" > "$OUT/api-key.conf"
    )
    # Same fingerprint the API logs at startup; if the two differ, every API
    # call is answered 401.
    fingerprint=$(printf '%s' "$key" | sha256sum | cut -c1-8)
    log "the API key is added to forwarded API calls, fingerprint=$fingerprint"
else
    : > "$OUT/api-key.conf"
fi
