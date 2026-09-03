CREATE TABLE IF NOT EXISTS schema_migrations (
    version VARCHAR(191) PRIMARY KEY,
    checksum CHAR(64) NOT NULL,
    applied_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS sync_receipts (
    message_id VARCHAR(191) PRIMARY KEY,
    pubsub_message_id VARCHAR(191) NULL,
    entity_type VARCHAR(32) NOT NULL,
    entity_key VARCHAR(191) NOT NULL,
    operation ENUM('upsert', 'delete') NOT NULL,
    status ENUM('processing', 'applied', 'stale') NOT NULL,
    reason VARCHAR(255) NULL,
    received_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    processed_at TIMESTAMP(6) NULL,
    INDEX idx_sync_receipts_received (received_at),
    INDEX idx_sync_receipts_entity (entity_type, entity_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS sync_entity_versions (
    entity_type VARCHAR(32) NOT NULL,
    entity_key VARCHAR(191) NOT NULL,
    source_updated_at DATETIME(6) NOT NULL,
    source_sequence BIGINT UNSIGNED NOT NULL DEFAULT 0,
    message_id VARCHAR(191) NOT NULL,
    operation ENUM('upsert', 'delete') NOT NULL,
    updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (entity_type, entity_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS replicated_entities (
    entity_type VARCHAR(32) NOT NULL,
    entity_key VARCHAR(191) NOT NULL,
    event_id VARCHAR(191) NULL,
    source_row_id BIGINT NULL,
    source_updated_at DATETIME(6) NOT NULL,
    source_sequence BIGINT UNSIGNED NOT NULL DEFAULT 0,
    payload JSON NULL,
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    received_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (entity_type, entity_key),
    INDEX idx_replicated_event (event_id),
    INDEX idx_replicated_source_row (entity_type, source_row_id),
    INDEX idx_replicated_updated (entity_type, source_updated_at),
    CHECK ((is_deleted = TRUE AND payload IS NULL) OR (is_deleted = FALSE AND payload IS NOT NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
