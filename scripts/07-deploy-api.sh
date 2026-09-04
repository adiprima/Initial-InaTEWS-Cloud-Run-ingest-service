#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "${SCRIPT_DIR}/common.sh"

require_context
API_EMAIL="$(service_account_email "${API_SERVICE_ACCOUNT}")"
service_account_exists "${API_SERVICE_ACCOUNT}" || die "Service account ${API_EMAIL} belum ada."

gcloud projects add-iam-policy-binding "${PROJECT_ID}" \
    --member="serviceAccount:${API_EMAIL}" \
    --role=roles/cloudsql.client \
    --condition=None >/dev/null
gcloud secrets add-iam-policy-binding inatews-api-db-password \
    --project="${PROJECT_ID}" \
    --member="serviceAccount:${API_EMAIL}" \
    --role=roles/secretmanager.secretAccessor >/dev/null

API_IMAGE="${REGION}-docker.pkg.dev/${PROJECT_ID}/${ARTIFACT_REPOSITORY}/inatews-api:${IMAGE_TAG}"
info "Deploy public Cloud Run API dengan autentikasi X-API-Key pada aplikasi"
gcloud run deploy "${API_SERVICE}" \
    --project="${PROJECT_ID}" \
    --region="${REGION}" \
    --image="${API_IMAGE}" \
    --service-account="${API_EMAIL}" \
    --set-cloudsql-instances="${INSTANCE_CONNECTION_NAME}" \
    --set-env-vars="DB_USER=inatews_api,DB_NAME=${DATABASE_NAME},DB_SOCKET=/cloudsql/${INSTANCE_CONNECTION_NAME},DB_MAX_OPEN_CONNS=10,DB_MAX_IDLE_CONNS=5,API_DEFAULT_PAGE_SIZE=20,API_MAX_PAGE_SIZE=100,API_ALLOWED_ORIGINS=*" \
    --set-secrets="DB_PASSWORD=inatews-api-db-password:latest" \
    --allow-unauthenticated \
    --ingress=all \
    --min-instances=0 \
    --max-instances=5 \
    --concurrency=40 \
    --timeout=30s \
    --memory=512Mi \
    --cpu=1

SERVICE_URL="$(gcloud run services describe "${API_SERVICE}" \
    --project="${PROJECT_ID}" --region="${REGION}" --format='value(status.url)')"
curl --fail --silent --show-error "${SERVICE_URL}/health"
printf '\n'
curl --fail --silent --show-error "${SERVICE_URL}/ready"
printf '\n'
ok "Public API dideploy: ${SERVICE_URL}"
