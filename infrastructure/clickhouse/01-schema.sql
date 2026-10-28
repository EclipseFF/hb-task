CREATE DATABASE IF NOT EXISTS vacation;
CREATE TABLE IF NOT EXISTS vacation.decisions (
    event_id UUID,
    type LowCardinality(String),
    version UInt16,
    request_id UUID,
    employee_id UUID,
    days UInt16,
    result LowCardinality(String),
    occurred_at DateTime64(3, 'UTC')
) ENGINE = ReplacingMergeTree
ORDER BY event_id;

-- Kafka delivery is at-least-once. FINAL removes duplicates before background merges.
CREATE VIEW IF NOT EXISTS vacation.decision_results AS
SELECT * FROM vacation.decisions FINAL;
