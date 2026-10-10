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

-- Team Schedule: the ENG tag, for SRE engineers on customer engagement.
--
-- The SaaS SRE rota (Apollo and Artemis) keeps some engineers on customer
-- engagement only: they take no rota turn. The leads' sheet marked them
-- "Customer-Engagement-only"; on the roster they are marked with this tag over
-- the span, and the month generator keeps anyone tagged off every turn of
-- those days, as it does anyone on leave or another allocation.
--
-- An allocation, not an exclusion: the roster's picker offers leave and
-- allocation kinds, and an allocation can say who the time is for (the
-- customer) in its "For" field. Offered on the SRE rotas only.
--
-- Data only: one catalogue row, no table, column or type changes. Created by
-- 'migration' so it reads as one of the catalogue's own kinds, which a lead
-- cannot delete. Safe to re-run: an existing ENG row is left as it is.
INSERT INTO team_schedule_absence_kind
    (code, short_code, label, bucket, colour_token, sort_order, is_active, family, created_by, updated_by)
VALUES
    ('ENG', 'ENG', 'Customer engagement', 'ALLOCATION', 'EXT', 45, TRUE, 'SRE', 'migration', 'migration')
ON CONFLICT (code) DO NOTHING;
