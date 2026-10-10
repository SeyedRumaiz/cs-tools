-- Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com). All Rights Reserved.
--
-- This software is the property of WSO2 LLC. and its suppliers, if any.
-- Dissemination of any information or reproduction of any material contained
-- herein in any form is strictly forbidden, unless permitted by WSO2 expressly.
-- You may not alter or remove any copyright or other notice from copies of this content.

-- u_wso2_product.u_product_code values like "wso2-customer-service-portal"
-- (29 chars) exceeded VARCHAR(20), rejecting the whole row at load
-- (SQLSTATE 22001) instead of truncating.
ALTER TABLE product ALTER COLUMN code TYPE VARCHAR(64);
