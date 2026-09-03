#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "${SCRIPT_DIR}/common.sh"

require_context
INVOKER_EMAIL="$(service_account_email "${PUBSUB_INVOKER_SERVICE_ACCOUNT}")"
service_account_exists "${PUBSUB_INVOKER_SERVICE_ACCOUNT}" || die "Service account ${INVOKER_EMAIL} belum ada."

SERVICE_URL="$(gcloud run services describe "${INGEST_SERVICE}" \
    --project="${PROJECT_ID}" --region="${REGION}" --format='value(status.url)')"
[[ "${SERVICE_URL}" == https://* ]] || die "URL Cloud Run ingest tidak ditemukan."
PUSH_ENDPOINT="${SERVICE_URL%/}/pubsub/push"

gcloud run services add-iam-policy-binding "${INGEST_SERVICE}" \
    --project="${PROJECT_ID}" \
    --region="${REGION}" \
    --member="serviceAccount:${INVOKER_EMAIL}" \
    --role=roles/run.invoker >/dev/null

PROJECT_NUMBER="$(gcloud projects describe "${PROJECT_ID}" --format='value(projectNumber)')"
PUBSUB_AGENT="service-${PROJECT_NUMBER}@gcp-sa-pubsub.iam.gserviceaccount.com"

gcloud projects add-iam-policy-binding "${PROJECT_ID}" \
    --member="serviceAccount:${PUBSUB_AGENT}" \
    --role=roles/iam.serviceAccountTokenCreator \
    --condition=None >/dev/null

gcloud pubsub topics add-iam-policy-binding "${DLQ_TOPIC}" \
    --project="${PROJECT_ID}" \
    --member="serviceAccount:${PUBSUB_AGENT}" \
    --role=roles/pubsub.publisher >/dev/null

if gcloud pubsub subscriptions describe "${INGEST_SUBSCRIPTION}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
    EXISTING_ENDPOINT="$(gcloud pubsub subscriptions describe "${INGEST_SUBSCRIPTION}" \
        --project="${PROJECT_ID}" --format='value(pushConfig.pushEndpoint)')"
    [[ "${EXISTING_ENDPOINT}" == "${PUSH_ENDPOINT}" ]] || die "Subscription ada dengan endpoint berbeda: ${EXISTING_ENDPOINT}"
    ok "Subscription sudah ada dengan endpoint yang sesuai"
else
    gcloud pubsub subscriptions create "${INGEST_SUBSCRIPTION}" \
        --project="${PROJECT_ID}" \
        --topic="${SYNC_TOPIC}" \
        --push-endpoint="${PUSH_ENDPOINT}" \
        --push-auth-service-account="${INVOKER_EMAIL}" \
        --push-auth-token-audience="${SERVICE_URL}" \
        --ack-deadline=60 \
        --message-retention-duration=7d \
        --min-retry-delay=10s \
        --max-retry-delay=600s \
        --dead-letter-topic="${DLQ_TOPIC}" \
        --max-delivery-attempts=10 \
        --expiration-period=never
fi

gcloud pubsub subscriptions add-iam-policy-binding "${INGEST_SUBSCRIPTION}" \
    --project="${PROJECT_ID}" \
    --member="serviceAccount:${PUBSUB_AGENT}" \
    --role=roles/pubsub.subscriber >/dev/null

ok "Authenticated push subscription siap"
gcloud pubsub subscriptions describe "${INGEST_SUBSCRIPTION}" --project="${PROJECT_ID}"

