#!/usr/bin/env bash
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "${SCRIPT_DIR}/common.sh"
require_context
require_command jq
ADMIN_URL="$(gcloud run services describe "${ADMIN_API_SERVICE}" --project="${PROJECT_ID}" --region="${REGION}" --format='value(status.url)')"
STATUS="$(curl --silent --output /dev/null --write-out '%{http_code}' "${ADMIN_URL}/v1/overview")"
[[ "${STATUS}" == "401" || "${STATUS}" == "403" ]] || die "Anonymous admin API returned ${STATUS}."
DASHBOARD_EMAIL="$(service_account_email "${DASHBOARD_SERVICE_ACCOUNT}")"
TOKEN="$(gcloud auth print-identity-token --impersonate-service-account="${DASHBOARD_EMAIL}" --audiences="${ADMIN_URL}")" || \
    die "Akun aktif harus memiliki roles/iam.serviceAccountTokenCreator pada ${DASHBOARD_EMAIL}."
AUTH_HEADER="Authorization: Bearer ${TOKEN}"
ACTOR_HEADER="X-InaTEWS-Admin-Email: smoke-test@inatews.bmkg.go.id"

OVERVIEW="$(curl --fail --silent --show-error --header "${AUTH_HEADER}" "${ADMIN_URL}/v1/overview")"
jq -e '.gcp.available != null and (.entities | length == 8)' <<<"${OVERVIEW}" >/dev/null
curl --fail --silent --show-error --header "${AUTH_HEADER}" "${ADMIN_URL}/v1/api/metrics?window=1h" | jq -e '.summary.requests >= 0' >/dev/null

CLIENT_NAME="smoke-$(date -u +%Y%m%dT%H%M%SZ)"
CLIENT="$(curl --fail --silent --show-error --request POST --header "${AUTH_HEADER}" --header "${ACTOR_HEADER}" --header 'Content-Type: application/json' \
    --data "$(jq -nc --arg name "${CLIENT_NAME}" '{name:$name,status:"active",default_rate_limit_per_minute:60}')" "${ADMIN_URL}/v1/api-clients")"
CLIENT_ID="$(jq -er '.id' <<<"${CLIENT}")"
KEY_RESPONSE="$(curl --fail --silent --show-error --request POST --header "${AUTH_HEADER}" --header "${ACTOR_HEADER}" --header 'Content-Type: application/json' \
    --data '{"name":"smoke-key","privileges":["earthquakes:read"],"rate_limit_per_minute":60,"expires_at":null}' "${ADMIN_URL}/v1/api-clients/${CLIENT_ID}/keys")"
KEY_ID="$(jq -er '.id' <<<"${KEY_RESPONSE}")"
RAW_KEY="$(jq -er '.api_key' <<<"${KEY_RESPONSE}")"
API_URL="$(gcloud run services describe "${API_SERVICE}" --project="${PROJECT_ID}" --region="${REGION}" --format='value(status.url)')"
curl --fail --silent --show-error --header "X-API-Key: ${RAW_KEY}" "${API_URL}/v1/earthquakes?limit=1" >/dev/null
curl --fail --silent --show-error --request POST --header "${AUTH_HEADER}" --header "${ACTOR_HEADER}" "${ADMIN_URL}/v1/api-keys/${KEY_ID}/revoke" >/dev/null
REVOKED_STATUS="$(curl --silent --output /dev/null --write-out '%{http_code}' --header "X-API-Key: ${RAW_KEY}" "${API_URL}/v1/earthquakes?limit=1")"
unset RAW_KEY KEY_RESPONSE
[[ "${REVOKED_STATUS}" == "401" ]] || die "Revoked key returned HTTP ${REVOKED_STATUS}, expected 401."
curl --fail --silent --show-error --request PATCH --header "${AUTH_HEADER}" --header "${ACTOR_HEADER}" --header 'Content-Type: application/json' \
    --data "$(jq -nc --arg name "${CLIENT_NAME}" '{name:$name,status:"revoked",default_rate_limit_per_minute:60}')" "${ADMIN_URL}/v1/api-clients/${CLIENT_ID}" >/dev/null
ok "Private endpoint, Monitoring/Pub/Sub, key create/use/revoke, dan audit lulus smoke test."
