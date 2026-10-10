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

-- Question definitions of the case feedback form (the rating question, the
-- per-rating "reasons" checkbox questions, the free-text comment question).
-- depends_on_metric_id links a reasons question to the rating question; it is
-- deferrable so a batch holding both parent and child commits in any row order.
-- selected_image / unselected_image are the frontend asset paths of the rating
-- icon in its selected and unselected state; set only on the "<rating> - Reasons"
-- rows (NULL elsewhere), and written by the sync itself from the metric name.
CREATE TABLE IF NOT EXISTS work_item_feedback_metric (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    question TEXT,
    datatype VARCHAR(64) NOT NULL,
    display_order INTEGER,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    is_mandatory BOOLEAN NOT NULL DEFAULT FALSE,
    depends_on_metric_id UUID REFERENCES work_item_feedback_metric(id) ON DELETE SET NULL DEFERRABLE INITIALLY DEFERRED,
    selected_image VARCHAR(255),
    unselected_image VARCHAR(255),
    created_on TIMESTAMPTZ NOT NULL,
    updated_on TIMESTAMPTZ NOT NULL,
    created_by VARCHAR(255) NOT NULL,
    updated_by VARCHAR(255) NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_work_item_feedback_metric_depends_on_metric_id ON work_item_feedback_metric (depends_on_metric_id);

-- Selectable choices of a checkbox question. value is the number a recorded
-- reason carries as option_value.
CREATE TABLE IF NOT EXISTS work_item_feedback_metric_option (
    id UUID PRIMARY KEY,
    metric_id UUID NOT NULL REFERENCES work_item_feedback_metric(id) ON DELETE CASCADE,
    label VARCHAR(255) NOT NULL,
    value INTEGER NOT NULL,
    display_order INTEGER,
    created_on TIMESTAMPTZ NOT NULL,
    updated_on TIMESTAMPTZ NOT NULL,
    created_by VARCHAR(255) NOT NULL,
    updated_by VARCHAR(255) NOT NULL,
    CONSTRAINT uq_work_item_feedback_metric_option_metric_value UNIQUE (metric_id, value)
);
