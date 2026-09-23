-- Run as the Cloud SQL root user in Cloud SQL Studio after migrations exist.
-- These statements intentionally grant access only inside inatews_public.

GRANT ALL PRIVILEGES ON inatews_public.* TO 'inatews_migrator'@'%';

GRANT SELECT, INSERT, UPDATE, DELETE
    ON inatews_public.sync_receipts TO 'inatews_ingest'@'%';
GRANT SELECT, INSERT, UPDATE, DELETE
    ON inatews_public.sync_entity_versions TO 'inatews_ingest'@'%';
GRANT SELECT, INSERT, UPDATE, DELETE
    ON inatews_public.replicated_entities TO 'inatews_ingest'@'%';
GRANT SELECT, INSERT, UPDATE, DELETE
    ON inatews_public.earthquake_archive_events TO 'inatews_ingest'@'%';

GRANT SELECT ON inatews_public.earthquake_archive_events TO 'inatews_api'@'%';
GRANT SELECT ON inatews_public.replicated_entities TO 'inatews_api'@'%';
GRANT SELECT ON inatews_public.api_clients TO 'inatews_api'@'%';
GRANT SELECT, UPDATE ON inatews_public.api_keys TO 'inatews_api'@'%';
GRANT SELECT, INSERT, UPDATE, DELETE ON inatews_public.api_usage_minutes TO 'inatews_api'@'%';
GRANT SELECT, INSERT, UPDATE, DELETE ON inatews_public.api_request_minutes TO 'inatews_api'@'%';

GRANT SELECT ON inatews_public.sync_receipts TO 'inatews_admin'@'%';
GRANT SELECT ON inatews_public.sync_entity_versions TO 'inatews_admin'@'%';
GRANT SELECT ON inatews_public.replicated_entities TO 'inatews_admin'@'%';
GRANT SELECT ON inatews_public.earthquake_archive_events TO 'inatews_admin'@'%';
GRANT SELECT, INSERT, UPDATE ON inatews_public.api_clients TO 'inatews_admin'@'%';
GRANT SELECT, INSERT, UPDATE ON inatews_public.api_keys TO 'inatews_admin'@'%';
GRANT SELECT ON inatews_public.api_usage_minutes TO 'inatews_admin'@'%';
GRANT SELECT ON inatews_public.api_request_minutes TO 'inatews_admin'@'%';
GRANT SELECT, INSERT ON inatews_public.admin_audit_logs TO 'inatews_admin'@'%';

FLUSH PRIVILEGES;
