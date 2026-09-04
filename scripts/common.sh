#!/usr/bin/env bash
set -Eeuo pipefail

PROJECT_ID="${PROJECT_ID:-gempa-integration-prod}"
REGION="${REGION:-asia-southeast2}"
SQL_INSTANCE="${SQL_INSTANCE:-inatews-mysql-prod}"
DATABASE_NAME="${DATABASE_NAME:-inatews_public}"
ARTIFACT_REPOSITORY="${ARTIFACT_REPOSITORY:-inatews-containers}"
SYNC_TOPIC="${SYNC_TOPIC:-inatews-sync-v1}"
DLQ_TOPIC="${DLQ_TOPIC:-inatews-sync-v1-dlq}"
INGEST_SUBSCRIPTION="${INGEST_SUBSCRIPTION:-inatews-sync-v1-ingest-sub}"
INGEST_SERVICE="${INGEST_SERVICE:-inatews-ingest}"
API_SERVICE="${API_SERVICE:-inatews-api}"
INGEST_SERVICE_ACCOUNT="${INGEST_SERVICE_ACCOUNT:-inatews-ingest-sa}"
API_SERVICE_ACCOUNT="${API_SERVICE_ACCOUNT:-inatews-api-sa}"
MIGRATOR_SERVICE_ACCOUNT="${MIGRATOR_SERVICE_ACCOUNT:-inatews-migrator-sa}"
PUBSUB_INVOKER_SERVICE_ACCOUNT="${PUBSUB_INVOKER_SERVICE_ACCOUNT:-inatews-pubsub-invoker}"
IMAGE_TAG="${IMAGE_TAG:-latest}"
INSTANCE_CONNECTION_NAME="${PROJECT_ID}:${REGION}:${SQL_INSTANCE}"

info() { printf '\n[INFO] %s\n' "$*"; }
ok() { printf '[OK] %s\n' "$*"; }
die() { printf '[ERROR] %s\n' "$*" >&2; exit 1; }

require_command() {
    command -v "$1" >/dev/null 2>&1 || die "Perintah '$1' tidak ditemukan. Jalankan dari Google Cloud Shell."
}

require_context() {
    require_command gcloud
    local active_project active_account
    active_project="$(gcloud config get-value project 2>/dev/null)"
    active_account="$(gcloud auth list --filter=status:ACTIVE --format='value(account)' | head -n 1)"
    [[ "${active_project}" == "${PROJECT_ID}" ]] || die "Project aktif '${active_project}' bukan '${PROJECT_ID}'."
    [[ -n "${active_account}" ]] || die "Tidak ada akun gcloud aktif."
    ok "Akun ${active_account}; project ${PROJECT_ID}; region ${REGION}"
}

service_account_email() {
    printf '%s@%s.iam.gserviceaccount.com' "$1" "${PROJECT_ID}"
}

service_account_exists() {
    gcloud iam service-accounts describe "$(service_account_email "$1")" --project="${PROJECT_ID}" >/dev/null 2>&1
}
