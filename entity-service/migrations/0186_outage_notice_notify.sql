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

-- Wakes entity-service's outage notice drainer the moment an outage changes,
-- so the two outage emails go out in about a second instead of on the
-- drainer's fallback poll.
--
-- pg_notify is transactional: listeners hear it only when the writing
-- transaction commits, so the drainer never reads a change that may still roll
-- back. STATEMENT-level, with an empty payload, on purpose: the drainer
-- re-evaluates every outage anyway, so it only needs to know that something
-- changed -- and a bulk csm-sync upsert of hundreds of outages sends one
-- notification, not hundreds.
--
-- Not a guarantee of delivery: a notification sent while the drainer is not
-- listening (restarting, connection lost) is gone. The drainer's fallback poll
-- covers that, so this only ever makes the email faster, never more correct.
-- Writes with session_replication_role = replica skip the trigger for the
-- same reason and are caught the same way.

CREATE OR REPLACE FUNCTION trg_outage_notice_notify() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('outage_notice', '');
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS outage_notice_notify ON outage;
CREATE TRIGGER outage_notice_notify
    AFTER INSERT OR UPDATE ON outage
    FOR EACH STATEMENT EXECUTE FUNCTION trg_outage_notice_notify();
