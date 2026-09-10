#!/usr/bin/env bash
# Copy the server's golden API payloads into this repo's testdata.
#
# These files pin the shape of every /api/v1 serializer. The server owns them;
# regenerate there first:
#
#   cd ../saltare
#   WRITE_GOLDEN=1 bin/rails test test/serializers/api/v1/golden_payloads_test.rb
#
# then run this and `go test ./internal/api/` to see what moved.
set -euo pipefail

server="${SALTARE_PATH:-$(dirname "$0")/../../saltare}"
src="$server/test/fixtures/files/api_golden"
dest="$(dirname "$0")/../internal/api/testdata/api_golden"

if [ ! -d "$src" ]; then
  echo "no goldens at $src" >&2
  echo "point SALTARE_PATH at a saltare checkout, or clone it beside this repo." >&2
  exit 1
fi

cp "$src"/*.json "$dest"/
echo "synced $(ls -1 "$dest"/*.json | wc -l | tr -d ' ') goldens from $src"
git -C "$(dirname "$0")/.." status --short -- internal/api/testdata/api_golden
