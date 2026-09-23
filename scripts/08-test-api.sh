#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "${SCRIPT_DIR}/common.sh"

require_context
require_command curl
require_command jq
API_KEY_SECRET="${API_KEY_SECRET:-inatews-public-api-key}"
SERVICE_URL="$(gcloud run services describe "${API_SERVICE}" \
    --project="${PROJECT_ID}" --region="${REGION}" --format='value(status.url)')"
RAW_API_KEY="$(gcloud secrets versions access latest --secret="${API_KEY_SECRET}" --project="${PROJECT_ID}")"
trap 'unset RAW_API_KEY' EXIT

info "Memastikan request tanpa API key ditolak"
STATUS="$(curl --silent --output /dev/null --write-out '%{http_code}' "${SERVICE_URL}/v1/earthquakes?limit=1")"
[[ "${STATUS}" == "401" ]] || die "Request tanpa API key menghasilkan HTTP ${STATUS}, bukan 401."

info "Memastikan OpenAPI dan Swagger UI tersedia"
OPENAPI="$(curl --fail --silent --show-error "${SERVICE_URL}/openapi.json")"
jq -e '.openapi == "3.1.0" and .paths["/v1/earthquakes/{eventid}"] != null' <<<"${OPENAPI}" >/dev/null \
    || die "Dokumen OpenAPI tidak valid atau endpoint detail tidak terdokumentasi."
STATUS="$(curl --silent --output /dev/null --write-out '%{http_code}' "${SERVICE_URL}/docs")"
[[ "${STATUS}" == "200" ]] || die "Swagger UI menghasilkan HTTP ${STATUS}, bukan 200."

info "Mengambil data gempa melalui public API"
RESPONSE="$(curl --fail --silent --show-error \
    --header "X-API-Key: ${RAW_API_KEY}" \
    "${SERVICE_URL}/v1/earthquakes?limit=5")"
printf '%s\n' "${RESPONSE}" | jq .

if [[ "${REQUIRE_DATA:-0}" == "1" ]]; then
    ITEM_COUNT="$(jq '.data | length' <<<"${RESPONSE}")"
    [[ "${ITEM_COUNT}" -gt 0 ]] || die "API aktif tetapi belum berisi data hasil sinkronisasi."

    EVENT_ID="$(jq -r '.data[0].event_id' <<<"${RESPONSE}")"
    DETAIL="$(curl --fail --silent --show-error \
        --header "X-API-Key: ${RAW_API_KEY}" \
        "${SERVICE_URL}/v1/earthquakes/${EVENT_ID}")"
    jq -e '.data.source_payload != null
        and (.data.related | has("tsunami"))
        and (.data.related | has("moment_tensor"))
        and (.data.related | has("felt"))
        and (.data.related | has("damage"))
        and (.data.related | has("narasi"))
        and (.data.related | has("m5"))
        and (.data.related | has("eq_phase"))' <<<"${DETAIL}" >/dev/null \
        || die "Endpoint detail belum mengembalikan source_payload dan seluruh grup related."
    ok "API menampilkan ${ITEM_COUNT} data gempa pada halaman pertama"
fi

ok "API publik dapat diakses dan autentikasi API key bekerja: ${SERVICE_URL}"
