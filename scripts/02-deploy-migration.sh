#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "${SCRIPT_DIR}/common.sh"

require_context
MIGRATOR_EMAIL="$(service_account_email "${MIGRATOR_SERVICE_ACCOUNT}")"

if ! service_account_exists "${MIGRATOR_SERVICE_ACCOUNT}"; then
    info "Membuat service account migrator"
    gcloud iam service-accounts create "${MIGRATOR_SERVICE_ACCOUNT}" \
        --project="${PROJECT_ID}" \
        --display-name="InaTEWS Database Migrator" \
        --description="Runs versioned InaTEWS Cloud SQL migrations"
fi

gcloud projects add-iam-policy-binding "${PROJECT_ID}" \
    --member="serviceAccount:${MIGRATOR_EMAIL}" \
    --role=roles/cloudsql.client \
    --condition=None >/dev/null

gcloud secrets add-iam-policy-binding inatews-migrator-db-password \
    --project="${PROJECT_ID}" \
    --member="serviceAccount:${MIGRATOR_EMAIL}" \
    --role=roles/secretmanager.secretAccessor >/dev/null

MIGRATION_IMAGE="${REGION}-docker.pkg.dev/${PROJECT_ID}/${ARTIFACT_REPOSITORY}/inatews-migrate:${IMAGE_TAG}"

info "Deploy Cloud Run migration job"
gcloud run jobs deploy inatews-migrate \
    --project="${PROJECT_ID}" \
    --region="${REGION}" \
    --image="${MIGRATION_IMAGE}" \
    --service-account="${MIGRATOR_EMAIL}" \
    --set-cloudsql-instances="${INSTANCE_CONNECTION_NAME}" \
    --set-env-vars="DB_USER=inatews_migrator,DB_NAME=${DATABASE_NAME},DB_SOCKET=/cloudsql/${INSTANCE_CONNECTION_NAME},DB_MAX_OPEN_CONNS=2,DB_MAX_IDLE_CONNS=1" \
    --set-secrets="DB_PASSWORD=inatews-migrator-db-password:latest" \
    --max-retries=0 \
    --task-timeout=10m \
    --memory=512Mi \
    --cpu=1

info "Menjalankan migration job dan menunggu hasil"
gcloud run jobs execute inatews-migrate \
    --project="${PROJECT_ID}" \
    --region="${REGION}" \
    --wait

ok "Migration selesai"
printf '\nSelanjutnya buka Cloud SQL Studio sebagai root dan jalankan ops/grants.sql.\n'

