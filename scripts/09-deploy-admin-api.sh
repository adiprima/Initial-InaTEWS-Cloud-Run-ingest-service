#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "${SCRIPT_DIR}/common.sh"
require_context
require_command openssl

ADMIN_DB_SECRET="inatews-admin-db-password"
if ! gcloud secrets describe "${ADMIN_DB_SECRET}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
    ADMIN_DB_PASSWORD="$(openssl rand -base64 36 | tr -d '\n=/+')"
    gcloud secrets create "${ADMIN_DB_SECRET}" --project="${PROJECT_ID}" --replication-policy=automatic >/dev/null
    printf '%s' "${ADMIN_DB_PASSWORD}" | gcloud secrets versions add "${ADMIN_DB_SECRET}" --project="${PROJECT_ID}" --data-file=- >/dev/null
else
    ADMIN_DB_PASSWORD="$(gcloud secrets versions access latest --secret="${ADMIN_DB_SECRET}" --project="${PROJECT_ID}")"
fi
if gcloud sql users list --instance="${SQL_INSTANCE}" --project="${PROJECT_ID}" --filter='name=inatews_admin' --format='value(name)' | grep -Fxq inatews_admin; then
    gcloud sql users set-password inatews_admin --host='%' --instance="${SQL_INSTANCE}" --project="${PROJECT_ID}" --password="${ADMIN_DB_PASSWORD}" >/dev/null
else
    gcloud sql users create inatews_admin --host='%' --instance="${SQL_INSTANCE}" --project="${PROJECT_ID}" --password="${ADMIN_DB_PASSWORD}" >/dev/null
fi
unset ADMIN_DB_PASSWORD
if [[ "${PREPARE_ONLY:-0}" == "1" ]]; then
    ok "User Cloud SQL dan secret admin siap. Jalankan ops/grants.sql, lalu ulangi script tanpa PREPARE_ONLY."
    exit 0
fi

ADMIN_EMAIL="$(service_account_email "${ADMIN_API_SERVICE_ACCOUNT}")"
DASHBOARD_EMAIL="$(service_account_email "${DASHBOARD_SERVICE_ACCOUNT}")"
for account in "${ADMIN_API_SERVICE_ACCOUNT}" "${DASHBOARD_SERVICE_ACCOUNT}"; do
    if ! service_account_exists "${account}"; then
        gcloud iam service-accounts create "${account}" --project="${PROJECT_ID}" --display-name="InaTEWS ${account}"
    fi
done

gcloud projects add-iam-policy-binding "${PROJECT_ID}" --member="serviceAccount:${ADMIN_EMAIL}" --role=roles/cloudsql.client --condition=None >/dev/null
gcloud projects add-iam-policy-binding "${PROJECT_ID}" --member="serviceAccount:${ADMIN_EMAIL}" --role=roles/monitoring.viewer --condition=None >/dev/null
gcloud projects add-iam-policy-binding "${PROJECT_ID}" --member="serviceAccount:${ADMIN_EMAIL}" --role=roles/run.viewer --condition=None >/dev/null
gcloud secrets add-iam-policy-binding "${ADMIN_DB_SECRET}" --project="${PROJECT_ID}" --member="serviceAccount:${ADMIN_EMAIL}" --role=roles/secretmanager.secretAccessor >/dev/null

ADMIN_IMAGE="${REGION}-docker.pkg.dev/${PROJECT_ID}/${ARTIFACT_REPOSITORY}/inatews-admin-api:${IMAGE_TAG}"
gcloud run deploy "${ADMIN_API_SERVICE}" \
    --project="${PROJECT_ID}" --region="${REGION}" --image="${ADMIN_IMAGE}" \
    --service-account="${ADMIN_EMAIL}" --set-cloudsql-instances="${INSTANCE_CONNECTION_NAME}" \
    --set-env-vars="DB_USER=inatews_admin,DB_NAME=${DATABASE_NAME},DB_SOCKET=/cloudsql/${INSTANCE_CONNECTION_NAME},GCP_PROJECT_ID=${PROJECT_ID},GCP_REGION=${REGION},GCP_SYNC_SUBSCRIPTION_ID=${INGEST_SUBSCRIPTION},GCP_DLQ_SUBSCRIPTION_ID=inatews-sync-v1-dlq-sub,PUBLIC_API_SERVICE=${API_SERVICE},INGEST_SERVICE=${INGEST_SERVICE}" \
    --set-secrets="DB_PASSWORD=${ADMIN_DB_SECRET}:latest" \
    --no-allow-unauthenticated --ingress=all --min-instances=0 --max-instances=2 \
    --concurrency=20 --timeout=30s --memory=512Mi --cpu=1

gcloud run services add-iam-policy-binding "${ADMIN_API_SERVICE}" --project="${PROJECT_ID}" --region="${REGION}" \
    --member="serviceAccount:${DASHBOARD_EMAIL}" --role=roles/run.invoker >/dev/null

ADMIN_URL="$(gcloud run services describe "${ADMIN_API_SERVICE}" --project="${PROJECT_ID}" --region="${REGION}" --format='value(status.url)')"
ok "Private admin API dideploy: ${ADMIN_URL}"
printf 'Buat credential dashboard sekali saja dan simpan dengan permission 600:\n'
printf 'gcloud iam service-accounts keys create inatews-dashboard-sa.json --iam-account=%s --project=%s\n' "${DASHBOARD_EMAIL}" "${PROJECT_ID}"
