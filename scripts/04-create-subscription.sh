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
    info "Sinkronkan kembali endpoint dan autentikasi OIDC subscription"
    # A deploy can preserve an old or incomplete pushConfig. Always update all
    # push authentication fields so Pub/Sub sends a Google-signed ID token with
    # the Cloud Run service URL as its audience.
    gcloud pubsub subscriptions update "${INGEST_SUBSCRIPTION}" \
        --project="${PROJECT_ID}" \
        --push-endpoint="${PUSH_ENDPOINT}" \
        --push-auth-service-account="${INVOKER_EMAIL}" \
        --push-auth-token-audience="${SERVICE_URL}" \
        --ack-deadline=60 \
        --min-retry-delay=10s \
        --max-retry-delay=600s >/dev/null
    ok "Push endpoint dan OIDC subscription diperbarui"
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
ACTUAL_ENDPOINT="$(gcloud pubsub subscriptions describe "${INGEST_SUBSCRIPTION}" \
    --project="${PROJECT_ID}" --format='value(pushConfig.pushEndpoint)')"
ACTUAL_INVOKER="$(gcloud pubsub subscriptions describe "${INGEST_SUBSCRIPTION}" \
    --project="${PROJECT_ID}" --format='value(pushConfig.oidcToken.serviceAccountEmail)')"
ACTUAL_AUDIENCE="$(gcloud pubsub subscriptions describe "${INGEST_SUBSCRIPTION}" \
    --project="${PROJECT_ID}" --format='value(pushConfig.oidcToken.audience)')"

[[ "${ACTUAL_ENDPOINT}" == "${PUSH_ENDPOINT}" ]] || die "Push endpoint tidak sesuai: ${ACTUAL_ENDPOINT}"
[[ "${ACTUAL_INVOKER}" == "${INVOKER_EMAIL}" ]] || die "OIDC service account tidak sesuai: ${ACTUAL_INVOKER}"
[[ "${ACTUAL_AUDIENCE}" == "${SERVICE_URL}" ]] || die "OIDC audience tidak sesuai: ${ACTUAL_AUDIENCE}"

printf 'Push endpoint : %s\n' "${ACTUAL_ENDPOINT}"
printf 'OIDC invoker  : %s\n' "${ACTUAL_INVOKER}"
printf 'OIDC audience : %s\n' "${ACTUAL_AUDIENCE}"
