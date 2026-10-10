-- Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com). All Rights Reserved.
--
-- This software is the property of WSO2 LLC. and its suppliers, if any.
-- Dissemination of any information or reproduction of any material contained
-- herein in any form is strictly forbidden, unless permitted by WSO2 expressly.
-- You may not alter or remove any copyright or other notice from copies of this content.

-- Real account.phone values can carry more than one number concatenated
-- together (e.g. "07418040320+44 (0) 7418040320", 29 chars), exceeding
-- VARCHAR(20) and getting silently nulled instead of preserved.
ALTER TABLE account ALTER COLUMN phone TYPE VARCHAR(64);
