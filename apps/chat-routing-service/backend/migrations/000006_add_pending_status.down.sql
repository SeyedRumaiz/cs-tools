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

-- Postgres can't drop an enum value, so we recreate the type without it.
-- Rows currently PENDING map back to BUSY first so nothing is left
-- pointing at a value that's about to disappear.
UPDATE chat_routing.engineers SET status = 'BUSY' WHERE status = 'PENDING';

ALTER TABLE chat_routing.engineers ALTER COLUMN status DROP DEFAULT;

CREATE TYPE chat_routing.engineer_status_old AS ENUM ('AVAILABLE', 'BUSY', 'OFFLINE');

ALTER TABLE chat_routing.engineers
  ALTER COLUMN status TYPE chat_routing.engineer_status_old
  USING status::text::chat_routing.engineer_status_old;

DROP TYPE chat_routing.engineer_status;
ALTER TYPE chat_routing.engineer_status_old RENAME TO engineer_status;

ALTER TABLE chat_routing.engineers ALTER COLUMN status SET DEFAULT 'OFFLINE';
