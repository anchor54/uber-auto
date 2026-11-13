#!/usr/bin/env bash
# Builds the backend's binaries into bin/: server (the API), worker, db
# (migrate / seed / seed-config) and sim (plays the driver in development).
# Render runs this; so does CI.
set -euo pipefail
cd "$(dirname "$0")"
for cmd in server worker db sim; do
  CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o "bin/$cmd" "./cmd/$cmd"
done
echo "built bin/{server,worker,db,sim}"
