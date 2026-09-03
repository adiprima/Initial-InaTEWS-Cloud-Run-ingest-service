#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "${SCRIPT_DIR}/common.sh"

require_context
TEST_SUFFIX="$(date -u +%Y%m%dT%H%M%SZ)"
TEST_EVENT_ID="gcp-test-${TEST_SUFFIX}"
TEST_MESSAGE_ID="manual-${TEST_SUFFIX}"
UPDATED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

read -r -d '' PAYLOAD <<JSON || true
{
  "schema_version": 1,
  "message_id": "${TEST_MESSAGE_ID}",
  "entity_type": "earthquake",
  "operation": "upsert",
  "source_row_id": 1,
  "source_sequence": 1,
  "event_id": "${TEST_EVENT_ID}",
  "entity_key": "${TEST_EVENT_ID}",
  "source_updated_at": "${UPDATED_AT}",
  "payload": {
    "eventid": "${TEST_EVENT_ID}",
    "mag": 5.1,
    "depth_km": 10.0,
    "latitude": -6.2,
    "longitude": 106.8,
    "place": "GCP integration test",
    "datetime_utc": "${UPDATED_AT}",
    "status": "test",
    "type": "earthquake",
    "source": "manual-test",
    "properties": {"test": true}
  }
}
JSON

info "Publish event uji ${TEST_EVENT_ID}"
gcloud pubsub topics publish "${SYNC_TOPIC}" \
    --project="${PROJECT_ID}" \
    --message="${PAYLOAD}"

info "Publish ulang envelope yang sama untuk menguji idempotency"
gcloud pubsub topics publish "${SYNC_TOPIC}" \
    --project="${PROJECT_ID}" \
    --message="${PAYLOAD}"

printf '\nEvent ID   : %s\nMessage ID : %s\n' "${TEST_EVENT_ID}" "${TEST_MESSAGE_ID}"
printf 'Tunggu beberapa detik, lalu periksa log:\n'
printf 'gcloud run services logs read %s --region=%s --limit=50\n' "${INGEST_SERVICE}" "${REGION}"
printf '\nLog harus memperlihatkan satu hasil applied dan satu duplicate untuk message_id yang sama.\n'
