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

-- One row per reason a user ticked alongside their rating. option_value and
-- reason are kept as recorded, with no FK to work_item_feedback_metric_option,
-- so a reason survives its option being renamed or removed.
CREATE TABLE IF NOT EXISTS work_item_feedback_reason (
    id UUID PRIMARY KEY,
    feedback_id UUID NOT NULL REFERENCES work_item_feedback(id) ON DELETE CASCADE,
    metric_id UUID NOT NULL REFERENCES work_item_feedback_metric(id),
    option_value INTEGER NOT NULL,
    reason VARCHAR(255) NOT NULL,
    created_on TIMESTAMPTZ NOT NULL,
    updated_on TIMESTAMPTZ NOT NULL,
    created_by VARCHAR(255) NOT NULL,
    updated_by VARCHAR(255) NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_work_item_feedback_reason_feedback_id ON work_item_feedback_reason (feedback_id);
CREATE INDEX IF NOT EXISTS idx_work_item_feedback_reason_metric_id ON work_item_feedback_reason (metric_id);
