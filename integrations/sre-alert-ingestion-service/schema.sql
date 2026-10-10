-- Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
--
-- WSO2 LLC. licenses this file to you under the Apache License,
-- Version 2.0 (the "License"); you may not use this file except
-- in compliance with the License.
-- You may obtain a copy of the License at
--
-- http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing,
-- software distributed under the License is distributed on an
-- "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
-- KIND, either express or implied.  See the License for the
-- specific language governing permissions and limitations
-- under the License.

-- The tables this service writes; identical to their definitions in sre-alert-core-service/schema.sql, which owns the rest.

CREATE TABLE IF NOT EXISTS alerts (
  id             text PRIMARY KEY,
  source         text NOT NULL,
  alert          jsonb NOT NULL,
  fingerprint    text,
  created_at     timestamptz NOT NULL DEFAULT now(),
  claimed_by     text,
  claimed_until  timestamptz,
  processed_at   timestamptz
) WITH (fillfactor = 85, autovacuum_vacuum_scale_factor = 0.05, autovacuum_vacuum_insert_scale_factor = 0.05);

CREATE INDEX IF NOT EXISTS alerts_unclaimed_idx ON alerts (id) WHERE processed_at IS NULL;
CREATE INDEX IF NOT EXISTS alerts_unclaimed_fp_idx ON alerts (fingerprint) WHERE processed_at IS NULL;

CREATE SEQUENCE IF NOT EXISTS alert_seq AS bigint START 1;

-- Raw webhook bodies exactly as received, before any transform; sre-alert-ingestion-service writes them in batches.
CREATE TABLE IF NOT EXISTS raw_alerts (
  id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  received_at  timestamptz NOT NULL,
  payload      jsonb NOT NULL
);
-- Retention deletes by age, oldest first.
CREATE INDEX IF NOT EXISTS raw_alerts_received_at_idx ON raw_alerts (received_at);
