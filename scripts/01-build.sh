#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "${SCRIPT_DIR}/common.sh"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

require_context
require_command git
SOURCE_SHA="$(git -C "${PROJECT_DIR}" rev-parse HEAD)"
[[ -z "$(git -C "${PROJECT_DIR}" status --porcelain)" ]] || die "Working tree belum bersih. Commit dan push perubahan ke GitHub sebelum build."
REMOTE_SHA="$(git -C "${PROJECT_DIR}" ls-remote origin refs/heads/main | cut -f1)"
[[ "${SOURCE_SHA}" == "${REMOTE_SHA}" ]] || die "Checkout lokal tidak sama dengan origin/main di GitHub. Jalankan git pull --ff-only origin main."
[[ "${IMAGE_TAG}" == "${SOURCE_SHA:0:12}" ]] || die "IMAGE_TAG harus 12 karakter awal commit GitHub: ${SOURCE_SHA:0:12}."
info "Menjalankan unit test sebelum build"
(cd "${PROJECT_DIR}" && go test ./...)

info "Build dan push image melalui Cloud Build"
gcloud builds submit "${PROJECT_DIR}" \
    --project="${PROJECT_ID}" \
    --config="${PROJECT_DIR}/cloudbuild.yaml" \
    --substitutions="_REGION=${REGION},_REPOSITORY=${ARTIFACT_REPOSITORY},_TAG=${IMAGE_TAG}"

ok "Image ingest, migrator, API, dan API-key job berhasil dipush dengan tag ${IMAGE_TAG}"
