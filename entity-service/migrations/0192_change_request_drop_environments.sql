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

-- A deployment already IS an environment instance of its project (its role --
-- Primary production, Staging, QA, ... -- is deployment.type), so the change
-- request form's separate Environments field is gone, and with it the
-- environment catalogue and the change_request_environment join table that
-- migration 0191 added for it. deployment.type stays as data; it is simply no
-- longer an "environment" concept in the change request API.
--
-- Migration 0191 itself is left untouched (it may already be applied
-- somewhere); this migration undoes the environment half of it. The other
-- tables of 0191 (change_request_deployment, change_request_deployed_product)
-- and the new category labels stay.
--
-- Also removes project_customer_group, an association table a first draft of the
-- Customer Group rule created: the Customer Group is now derived live from the
-- change request's project (its REGISTERED portal-user contacts), so nothing
-- needs to store it. DROP ... IF EXISTS makes this a no-op wherever that draft
-- never ran. change_request.customer_group_id (migration 0075) is kept but no
-- longer written or read.
--
-- Idempotent: IF EXISTS throughout, so a re-run is a no-op.

DROP TABLE IF EXISTS change_request_environment;
DROP TABLE IF EXISTS environment;
DROP TABLE IF EXISTS project_customer_group;
