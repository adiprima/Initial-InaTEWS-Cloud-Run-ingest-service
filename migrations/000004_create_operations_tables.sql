ALTER TABLE sync_receipts
    ADD COLUMN delivery_count INT UNSIGNED NOT NULL DEFAULT 1 AFTER status,
    ADD COLUMN last_received_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) AFTER received_at,
    ADD COLUMN processing_duration_ms INT UNSIGNED NULL AFTER processed_at;

ALTER TABLE api_keys
    ADD COLUMN suspended_at DATETIME(6) NULL AFTER last_used_at;

CREATE TABLE IF NOT EXISTS api_request_minutes (
    minute_bucket DATETIME NOT NULL,
    api_key_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
    method VARCHAR(8) NOT NULL,
    route VARCHAR(191) NOT NULL,
    status_code SMALLINT UNSIGNED NOT NULL,
    request_count BIGINT UNSIGNED NOT NULL DEFAULT 0,
    total_duration_ms BIGINT UNSIGNED NOT NULL DEFAULT 0,
    max_duration_ms INT UNSIGNED NOT NULL DEFAULT 0,
    updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (minute_bucket, api_key_id, method, route, status_code),
    INDEX idx_api_request_route_time (route, minute_bucket),
    INDEX idx_api_request_status_time (status_code, minute_bucket)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS admin_audit_logs (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    actor_id VARCHAR(64) NULL,
    actor_email VARCHAR(191) NULL,
    action VARCHAR(64) NOT NULL,
    target_type VARCHAR(32) NOT NULL,
    target_id VARCHAR(191) NULL,
    metadata JSON NOT NULL,
    created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    INDEX idx_admin_audit_created (created_at),
    INDEX idx_admin_audit_target (target_type, target_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
