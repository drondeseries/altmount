#!/usr/bin/env bash
# Local image build guardrail for docker/Dockerfile.ci.
#
# Dockerfile.ci consumes a PRE-BUILT frontend (it copies ./frontend/dist from
# the build host; CI builds it in a separate job and uploads it as an
# artifact). Forgetting `npm run build` locally ships a stale SPA silently —
# backend changes apply but frontend changes never appear, even in a fresh
# browser session. This script makes that mistake impossible:
#
#   1. builds the frontend (fails hard on any build error)
#   2. builds the image
#   3. verifies the baked dist inside the image contains any feature marker
#      passed as the second argument (a raw string expected in the built JS)
#
# Usage: scripts/build-image.sh <image-tag> [feature-marker ...]
set -euo pipefail

tag="${1:?usage: build-image.sh <image-tag> [feature-marker ...]}"
shift || true
markers=("$@")

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

echo "==> building frontend (tsc -b && vite build)"
(cd "$root/frontend" && npm run build) || { echo "FRONTEND BUILD FAILED — not building image" >&2; exit 1; }

if [ "${#markers[@]}" -gt 0 ]; then
  bundle="$(ls "$root/frontend/dist/assets/"index-*.js | head -1)"
  for marker in "${markers[@]}"; do
    if ! grep -Fq "$marker" "$bundle"; then
      echo "MARKER MISSING from local dist: $marker" >&2
      exit 1
    fi
  done
  echo "==> all markers present in local dist: ${markers[*]}"
fi

echo "==> building image $tag"
docker build -f "$root/docker/Dockerfile.ci" -t "$tag" "$root"

if [ "${#markers[@]}" -gt 0 ]; then
  # No shell is involved when grep is the entrypoint, so globs cannot expand;
  # run a tiny sh and pass the marker via env (avoids quoting regex chars).
  for marker in "${markers[@]}"; do
    if ! docker run --rm -e MARKER="$marker" --entrypoint sh "$tag" \
         -c 'grep -Fq "$MARKER" /app/frontend/dist/assets/index-*.js'; then
      echo "MARKER NOT BAKED INTO IMAGE $tag: $marker" >&2
      exit 1
    fi
  done
  echo "==> verified in-image dist contains: ${markers[*]}"
fi

echo "==> OK: $tag"
docker inspect --format 'sha256={{.Id}}' "$tag"
