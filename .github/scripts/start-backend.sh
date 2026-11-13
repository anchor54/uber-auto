#!/usr/bin/env bash
# Starts the backend for end-to-end runs: migrated, seeded with the demo city,
# with the fixed demo code, the dev driver simulator, and the worker jobs in
# the same process (so they share the simulator's clock), on port 8080.
# Leaves it running in the background and waits until /health answers.
set -euo pipefail

mkdir -p "${OUT:-/tmp}"
cd "$(dirname "$0")/../../backend"
bash build.sh
./bin/db seed
MAPS_PROVIDER=demo FIXED_OTP=123456 ENABLE_DRIVER_SIM=true RUN_JOBS_IN_API=true AUTO_ACCEPT_COMMUTES="${AUTO_ACCEPT_COMMUTES:-false}" PORT=8080 \
  nohup ./bin/server > backend.log 2>&1 &
for _ in $(seq 60); do
  curl -sf http://localhost:8080/health >/dev/null && { echo "backend up"; exit 0; }
  sleep 1
done
echo "backend did not start" >&2
cat backend.log >&2
exit 1
