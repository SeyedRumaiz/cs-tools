-- Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com). All Rights Reserved.
--
-- This software is the property of WSO2 LLC. and its suppliers, if any.
-- Dissemination of any information or reproduction of any material contained
-- herein in any form is strictly forbidden, unless permitted by WSO2 expressly.
-- You may not alter or remove any copyright or other notice from copies of this content.

-- sf_id (Salesforce id) is neither unique nor always populated in production
-- Salesforce data - both NOT NULL and UNIQUE reject real rows, so every sf_id
-- column across the schema is relaxed to a plain nullable, non-unique column.
-- account/project had NOT NULL UNIQUE inline (default constraint name
-- <table>_sf_id_key); account_contact/project_contact/"user" were already
-- nullable but still UNIQUE.
ALTER TABLE account ALTER COLUMN sf_id DROP NOT NULL;
ALTER TABLE account DROP CONSTRAINT IF EXISTS account_sf_id_key;

ALTER TABLE project ALTER COLUMN sf_id DROP NOT NULL;
ALTER TABLE project DROP CONSTRAINT IF EXISTS project_sf_id_key;

ALTER TABLE account_contact DROP CONSTRAINT IF EXISTS account_contact_sf_id_key;
ALTER TABLE project_contact DROP CONSTRAINT IF EXISTS project_contact_sf_id_key;
ALTER TABLE "user" DROP CONSTRAINT IF EXISTS user_sf_id_key;
