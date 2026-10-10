-- Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com). All Rights Reserved.
--
-- This software is the property of WSO2 LLC. and its suppliers, if any.
-- Dissemination of any information or reproduction of any material contained
-- herein in any form is strictly forbidden, unless permitted by WSO2 expressly.
-- You may not alter or remove any copyright or other notice from copies of this content.

-- Fixes USER_ERROR_RECOMMANDATION_BEST_PRACTICES -> USER_ERROR_RECOMMENDATION_BEST_PRACTICES
-- across all five cause enums (case, service_request, engagement,
-- security_report_analysis, announcement each have their own independent
-- cause enum, all sharing the same value list). RENAME VALUE, not
-- drop/recreate, so any already-migrated rows using the old label keep
-- working. pg_enum is checked first since RENAME VALUE has no IF EXISTS
-- form and errors if the old label is already gone (e.g. re-run after a
-- partial apply).

DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_enum e JOIN pg_type t ON t.oid = e.enumtypid
        WHERE t.typname = 'case_cause_enum' AND e.enumlabel = 'USER_ERROR_RECOMMANDATION_BEST_PRACTICES'
    ) THEN
        ALTER TYPE case_cause_enum RENAME VALUE 'USER_ERROR_RECOMMANDATION_BEST_PRACTICES' TO 'USER_ERROR_RECOMMENDATION_BEST_PRACTICES';
    END IF;
END $$;

DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_enum e JOIN pg_type t ON t.oid = e.enumtypid
        WHERE t.typname = 'service_request_cause_enum' AND e.enumlabel = 'USER_ERROR_RECOMMANDATION_BEST_PRACTICES'
    ) THEN
        ALTER TYPE service_request_cause_enum RENAME VALUE 'USER_ERROR_RECOMMANDATION_BEST_PRACTICES' TO 'USER_ERROR_RECOMMENDATION_BEST_PRACTICES';
    END IF;
END $$;

DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_enum e JOIN pg_type t ON t.oid = e.enumtypid
        WHERE t.typname = 'engagement_cause_enum' AND e.enumlabel = 'USER_ERROR_RECOMMANDATION_BEST_PRACTICES'
    ) THEN
        ALTER TYPE engagement_cause_enum RENAME VALUE 'USER_ERROR_RECOMMANDATION_BEST_PRACTICES' TO 'USER_ERROR_RECOMMENDATION_BEST_PRACTICES';
    END IF;
END $$;

DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_enum e JOIN pg_type t ON t.oid = e.enumtypid
        WHERE t.typname = 'security_report_analysis_cause_enum' AND e.enumlabel = 'USER_ERROR_RECOMMANDATION_BEST_PRACTICES'
    ) THEN
        ALTER TYPE security_report_analysis_cause_enum RENAME VALUE 'USER_ERROR_RECOMMANDATION_BEST_PRACTICES' TO 'USER_ERROR_RECOMMENDATION_BEST_PRACTICES';
    END IF;
END $$;

DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_enum e JOIN pg_type t ON t.oid = e.enumtypid
        WHERE t.typname = 'announcement_cause_enum' AND e.enumlabel = 'USER_ERROR_RECOMMANDATION_BEST_PRACTICES'
    ) THEN
        ALTER TYPE announcement_cause_enum RENAME VALUE 'USER_ERROR_RECOMMANDATION_BEST_PRACTICES' TO 'USER_ERROR_RECOMMENDATION_BEST_PRACTICES';
    END IF;
END $$;
