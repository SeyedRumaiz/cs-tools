-- Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
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

DO $$ BEGIN
    CREATE TYPE comment_attachment_state_enum AS ENUM (
        'AVAILABLE', 'AVAILABLE_CONDITIONALLY', 'NOT_AVAILABLE', 'PENDING'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

CREATE TABLE IF NOT EXISTS comment_attachment (
    id UUID PRIMARY KEY,
    created_on TIMESTAMPTZ NOT NULL,
    updated_on TIMESTAMPTZ NOT NULL,
    created_by VARCHAR(255) NOT NULL,
    updated_by VARCHAR(255) NOT NULL,
    name VARCHAR(255),
    content_type VARCHAR(255),
    comment_id UUID NOT NULL REFERENCES comment(id) ON DELETE CASCADE,
    size_bytes BIGINT,
    compressed_size_bytes BIGINT,
    is_compressed BOOLEAN,
    hash VARCHAR(100),
    state comment_attachment_state_enum,
    chunk_size_bytes INTEGER
);

CREATE INDEX IF NOT EXISTS idx_comment_attachment_comment_id ON comment_attachment (comment_id);
