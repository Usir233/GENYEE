#!/bin/sh
set -eu

validate_port() {
  case "$1" in
    ''|*[!0-9]*)
      echo "WEBSITE_NGINX_PORT must be an integer between 1 and 65535" >&2
      exit 1
      ;;
  esac
  value=$(printf '%s' "$1" | sed 's/^0*//')
  if [ -z "$value" ] || [ "$value" -lt 1 ] 2>/dev/null || [ "$value" -gt 65535 ] 2>/dev/null; then
    echo "WEBSITE_NGINX_PORT must be an integer between 1 and 65535" >&2
    exit 1
  fi
  printf '%s' "$value"
}

WEBSITE_NGINX_PORT=$(validate_port "${WEBSITE_NGINX_PORT:-80}")
export WEBSITE_NGINX_PORT

# Substitute only the port; preserve Nginx variables such as $uri.
# shellcheck disable=SC2016
envsubst '${WEBSITE_NGINX_PORT}' \
  < /etc/nginx/templates/default.conf.template \
  > /etc/nginx/conf.d/default.conf

# Match the frontend entrypoint: configure and start Nginx even with no arguments.
exec nginx -g 'daemon off;'
