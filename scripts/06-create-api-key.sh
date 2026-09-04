#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "${SCRIPT_DIR}/common.sh"

require_context
require_command openssl
API_KEY_SECRET="${API_KEY_SECRET:-inatews-public-api-key}"
MIGRATOR_EMAIL="$(service_account_email "${MIGRATOR_SERVICE_ACCOUNT}")"

service_account_exists "${MIGRATOR_SERVICE_ACCOUNT}" || die "Service account ${MIGRATOR_EMAIL} belum ada."

if ! gcloud secrets describe "${API_KEY_SECRET}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
    info "Membuat secret API key publik"
    gcloud secrets create "${API_KEY_SECRET}" --project="${PROJECT_ID}" --replication-policy=automatic
fi

ENABLED_VERSION="$(gcloud secrets versions list "${API_KEY_SECRET}" \
    --project="${PROJECT_ID}" --filter='state=ENABLED' --format='value(name)' --limit=1)"
if [[ -z "${ENABLED_VERSION}" ]]; then
    info "Membuat API key acak dan menyimpannya langsung ke Secret Manager"
    RAW_API_KEY="inatews_live_$(openssl rand -hex 24)"
    printf '%s' "${RAW_API_KEY}" | gcloud secrets versions add "${API_KEY_SECRET}" \
        --project="${PROJECT_ID}" --data-file=- >/dev/null
    unset RAW_API_KEY
fi

gcloud secrets add-iam-policy-binding "${API_KEY_SECRET}" \
    --project="${PROJECT_ID}" \
    --member="serviceAccount:${MIGRATOR_EMAIL}" \
    --role=roles/secretmanager.secretAccessor >/dev/null

APIKEY_IMAGE="${REGION}-docker.pkg.dev/${PROJECT_ID}/${ARTIFACT_REPOSITORY}/inatews-apikey:${IMAGE_TAG}"
info "Deploy job idempoten untuk menyimpan hash API key"
gcloud run jobs deploy inatews-seed-api-key \
    --project="${PROJECT_ID}" \
    --region="${REGION}" \
    --image="${APIKEY_IMAGE}" \
    --service-account="${MIGRATOR_EMAIL}" \
    --set-cloudsql-instances="${INSTANCE_CONNECTION_NAME}" \
    --set-env-vars="DB_USER=inatews_migrator,DB_NAME=${DATABASE_NAME},DB_SOCKET=/cloudsql/${INSTANCE_CONNECTION_NAME},API_CLIENT_NAME=InaTEWS Initial Public Client,API_KEY_NAME=initial-public-key" \
    --set-secrets="DB_PASSWORD=inatews-migrator-db-password:latest,API_KEY=${API_KEY_SECRET}:latest" \
    --max-retries=0 \
    --task-timeout=5m \
    --memory=512Mi \
    --cpu=1

gcloud run jobs execute inatews-seed-api-key --project="${PROJECT_ID}" --region="${REGION}" --wait
ok "Hash API key tersimpan. Nilai mentah tetap hanya berada di Secret Manager ${API_KEY_SECRET}."
