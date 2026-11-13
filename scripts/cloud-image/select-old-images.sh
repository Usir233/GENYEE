#!/bin/sh
# Select old image tags only from the configured GENYEE registry.
set -eu

registry=${1:?registry is required}
keep_tag=${2:?current tag is required}

while IFS= read -r reference; do
  case "$reference" in
    "$registry"/genyee-*)
      if [ "${reference##*:}" != "$keep_tag" ]; then
        printf '%s\n' "$reference"
      fi
      ;;
  esac
done
