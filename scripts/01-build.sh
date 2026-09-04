#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "${SCRIPT_DIR}/common.sh"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

require_context
info "Menjalankan unit test sebelum build"
(cd "${PROJECT_DIR}" && go test ./...)

info "Build dan push image melalui Cloud Build"
gcloud builds submit "${PROJECT_DIR}" \
    --project="${PROJECT_ID}" \
    --config="${PROJECT_DIR}/cloudbuild.yaml" \
    --substitutions="_REGION=${REGION},_REPOSITORY=${ARTIFACT_REPOSITORY},_TAG=${IMAGE_TAG}"

ok "Image ingest, migrator, API, dan API-key job berhasil dipush dengan tag ${IMAGE_TAG}"
