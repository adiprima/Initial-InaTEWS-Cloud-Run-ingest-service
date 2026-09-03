#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "${SCRIPT_DIR}/common.sh"

require_context
INGEST_EMAIL="$(service_account_email "${INGEST_SERVICE_ACCOUNT}")"
service_account_exists "${INGEST_SERVICE_ACCOUNT}" || die "Service account ${INGEST_EMAIL} belum ada."

INGEST_IMAGE="${REGION}-docker.pkg.dev/${PROJECT_ID}/${ARTIFACT_REPOSITORY}/inatews-ingest:${IMAGE_TAG}"

info "Deploy private Cloud Run ingest service"
gcloud run deploy "${INGEST_SERVICE}" \
    --project="${PROJECT_ID}" \
    --region="${REGION}" \
    --image="${INGEST_IMAGE}" \
    --service-account="${INGEST_EMAIL}" \
    --set-cloudsql-instances="${INSTANCE_CONNECTION_NAME}" \
    --set-env-vars="DB_USER=inatews_ingest,DB_NAME=${DATABASE_NAME},DB_SOCKET=/cloudsql/${INSTANCE_CONNECTION_NAME},DB_MAX_OPEN_CONNS=7,DB_MAX_IDLE_CONNS=5,MAX_REQUEST_BYTES=15728640" \
    --set-secrets="DB_PASSWORD=inatews-ingest-db-password:latest" \
    --no-allow-unauthenticated \
    --min-instances=0 \
    --max-instances=3 \
    --concurrency=20 \
    --timeout=60s \
    --memory=512Mi \
    --cpu=1

SERVICE_URL="$(gcloud run services describe "${INGEST_SERVICE}" \
    --project="${PROJECT_ID}" --region="${REGION}" --format='value(status.url)')"
ok "Ingest dideploy: ${SERVICE_URL}"

info "Health check terautentikasi"
curl --fail --silent --show-error \
    -H "Authorization: Bearer $(gcloud auth print-identity-token)" \
    "${SERVICE_URL}/healthz"
printf '\n'
