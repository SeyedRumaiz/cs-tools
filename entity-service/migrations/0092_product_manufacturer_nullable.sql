-- Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com). All Rights Reserved.
--
-- This software is the property of WSO2 LLC. and its suppliers, if any.
-- Dissemination of any information or reproduction of any material contained
-- herein in any form is strictly forbidden, unless permitted by WSO2 expressly.
-- You may not alter or remove any copyright or other notice from copies of this content.

-- cmdb_model's manufacturer reference field is legitimately unset on some
-- records; the sync's own value_map transform already nulls an empty raw
-- value rather than erroring, but NOT NULL here still rejected the row at
-- load time.
ALTER TABLE product ALTER COLUMN manufacturer DROP NOT NULL;
