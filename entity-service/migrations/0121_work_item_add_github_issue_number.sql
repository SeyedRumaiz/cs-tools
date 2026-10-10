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

-- work_item.github_issue_number: the GitHub issue a work item is linked to.
--
-- *** THE COLUMN IS ALREADY LIVE, DECLARED IN THIS REPOSITORY. ***
-- This repo's own 0114_github_issue_on_work_item.sql already adds this
-- column (plus a backfill from "case" and the GitHub outbound triggers), so
-- this migration is a no-op here -- ADD COLUMN IF NOT EXISTS and CREATE
-- INDEX IF NOT EXISTS against objects that already exist.
--
-- It exists anyway so this migration's number mirrors csm-sync-service's own
-- 0121_work_item_add_github_issue_number.sql exactly: work_item is a table
-- csm-sync-service creates (its own 0021_work_item_table.sql) and this
-- column is one it needs too, for any environment built by csm-sync alone.
-- Same convention as every other shared-table migration mirrored between
-- the two repos -- see this file's own CLAUDE.md "Database migrations"
-- section.

ALTER TABLE work_item
  ADD COLUMN IF NOT EXISTS github_issue_number INTEGER;

CREATE INDEX IF NOT EXISTS idx_work_item_github_issue_number
    ON work_item (github_issue_number) WHERE github_issue_number IS NOT NULL;
