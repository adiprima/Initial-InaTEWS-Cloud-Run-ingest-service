# InaTEWS Cloud Run

Repository terpisah untuk komponen InaTEWS yang berjalan di Google Cloud:

- `inatews-ingest`: penerima authenticated Pub/Sub push dan penulis Cloud SQL.
- `inatews-migrate`: Cloud Run Job untuk menjalankan migration berurutan.
- Public API akan ditambahkan setelah jalur ingest lulus pengujian end-to-end.

Project Laravel lokal tetap berada di repository `api-inatews` dan tidak dicampur dengan repository ini.

## Status Implementasi

Tahap ini sudah menyediakan:

- endpoint `GET /healthz`, `GET /readyz`, dan `POST /pubsub/push`;
- validasi envelope sinkronisasi versi 1;
- penyimpanan raw payload delapan tipe entitas;
- receipt idempoten berdasarkan `message_id`;
- perlindungan terhadap update lama menggunakan waktu dan sequence sumber;
- proyeksi terindeks pertama untuk earthquake;
- migration job, container, Cloud Build, dan script deployment;
- schema awal API key untuk tahap public API berikutnya.
- GitHub Actions untuk test, race detector, vet, format, dan syntax shell.

Tipe entitas yang diterima:

```text
earthquake
tsunami
moment_tensor
felt
damage
narasi
m5
eq_phase
```

Selain `earthquake`, tipe-tipe tersebut pada tahap ini disimpan di `replicated_entities` sebagai raw JSON. Proyeksi tabel khusus masing-masing tipe ditambahkan setelah payload aktualnya dikunci melalui contract test.

## Prasyarat GCP

Fondasi berikut harus sudah tersedia:

```text
Project                  gempa-integration-prod
Region                   asia-southeast2
Cloud SQL                inatews-mysql-prod
Database                 inatews_public
Sync topic               inatews-sync-v1
DLQ topic                inatews-sync-v1-dlq
Artifact Registry        inatews-containers
Ingest service account   inatews-ingest-sa
Pub/Sub invoker          inatews-pubsub-invoker
```

Secret berikut harus memiliki versi `ENABLED`:

```text
inatews-migrator-db-password
inatews-ingest-db-password
```

Semua script deployment dijalankan dari Google Cloud Shell, bukan server produksi.

## Urutan Deployment Pertama

### 1. Masukkan repository ke Cloud Shell

Push repository ini ke repository Git terpisah, lalu clone dari Cloud Shell. Alternatif sementara adalah mengunggah folder ini melalui Cloud Shell Editor.

```bash
cd /path/ke/inatews-cloud-run
gcloud config set project gempa-integration-prod
```

### 2. Jalankan tes

```bash
go test ./...
go vet ./...
```

### 3. Build container

```bash
./scripts/01-build.sh
```

Script menjalankan unit test, membangun image ingest dan migrator, lalu mengunggah keduanya ke Artifact Registry dengan tag `latest`.

Untuk tag immutable:

```bash
IMAGE_TAG="$(git rev-parse --short HEAD)" ./scripts/01-build.sh
```

Gunakan tag yang sama pada semua langkah deployment berikutnya.

### 4. Pastikan migrator memiliki hak schema

Buka **Cloud SQL > inatews-mysql-prod > Cloud SQL Studio**, login sebagai `root`, pilih database `inatews_public`, lalu jalankan terlebih dahulu:

```sql
GRANT ALL PRIVILEGES ON inatews_public.* TO 'inatews_migrator'@'%';
FLUSH PRIVILEGES;
```

Password tidak boleh ditempel ke source code atau commit Git.

### 5. Deploy dan jalankan migration

```bash
./scripts/02-deploy-migration.sh
```

Periksa hasil job:

```bash
gcloud run jobs executions list \
  --job=inatews-migrate \
  --region=asia-southeast2
```

### 6. Terapkan hak database minimum

Setelah migration berhasil, buka Cloud SQL Studio sebagai `root` dan jalankan [ops/grants.sql](ops/grants.sql).

Verifikasi:

```sql
SHOW GRANTS FOR 'inatews_migrator'@'%';
SHOW GRANTS FOR 'inatews_ingest'@'%';
SHOW GRANTS FOR 'inatews_api'@'%';
```

### 7. Deploy private ingest

```bash
./scripts/03-deploy-ingest.sh
```

Service dideploy dengan:

- unauthenticated access dinonaktifkan;
- koneksi melalui `/cloudsql/...`;
- password dari Secret Manager;
- maksimal tiga instance;
- maksimal tujuh koneksi database per instance.

### 8. Buat authenticated push subscription

```bash
./scripts/04-create-subscription.sh
```

Script memberikan `roles/run.invoker` hanya kepada Pub/Sub invoker dan menghubungkan subscription dengan retry serta DLQ.

### 9. Publish satu event uji

```bash
./scripts/05-publish-test.sh
```

Periksa log:

```bash
gcloud run services logs read inatews-ingest \
  --region=asia-southeast2 \
  --limit=100
```

Script mengirim envelope yang sama dua kali. Log sukses harus mengandung `result=applied` untuk message pertama dan `result=duplicate` untuk message kedua tanpa menambah record baru.

## Envelope Versi 1

```json
{
  "schema_version": 1,
  "message_id": "outbox-12345",
  "entity_type": "earthquake",
  "operation": "upsert",
  "source_row_id": 109220,
  "source_sequence": 12345,
  "event_id": "20260903123456",
  "entity_key": "20260903123456",
  "source_updated_at": "2026-09-03T10:15:30Z",
  "payload": {}
}
```

Ketentuan penting:

- `message_id` nantinya berasal dari ID outbox lokal dan harus unik.
- `entity_key` adalah natural key record; untuk earthquake sama dengan `eventid`.
- `source_sequence` harus meningkat mengikuti ID outbox.
- `source_updated_at` selalu RFC3339 UTC.
- operasi yang didukung adalah `upsert` dan `delete`.
- `payload` wajib untuk `upsert` dan boleh kosong untuk `delete`.

## Status HTTP Pub/Sub

| Status | Makna |
|---|---|
| `204` | Berhasil, duplikat, atau update lama telah ditangani |
| `400` | Envelope permanen tidak valid; Pub/Sub retry lalu mengirim ke DLQ |
| `503` | Database/kegagalan sementara; Pub/Sub harus retry |

Autentikasi OIDC diverifikasi oleh lapisan IAM Cloud Run karena service tidak mengizinkan akses anonim.

## Pengembangan Lokal

Gunakan MySQL lokal atau Cloud SQL Auth Proxy. Jangan memakai database produksi untuk unit test.

```bash
cp .env.example .env
```

Untuk MySQL lokal, sesuaikan tanpa menyimpan password produksi:

```text
DB_SOCKET=
DB_HOST=127.0.0.1
DB_PORT=3306
DB_USER=inatews_ingest
DB_PASSWORD=local-only-password
DB_NAME=inatews_public
```

Jalankan:

```bash
set -a
source .env
set +a
go run ./cmd/ingest
```

## Batas Tahap Ini

Jangan melakukan backfill data produksi sebelum semua kondisi berikut terpenuhi:

- migration job berhasil;
- private ingest dapat diakses oleh Pub/Sub;
- event uji masuk ke Cloud SQL;
- duplikat tidak membuat record baru;
- versi lama tidak menimpa versi baru;
- invalid message berhasil masuk DLQ setelah retry;
- log tidak mengandung password atau raw credential.

Setelah kondisi tersebut terpenuhi, tahap berikutnya adalah membuat transactional outbox dan asset uploader pada server lokal, lalu menjalankan backfill bertahap.
