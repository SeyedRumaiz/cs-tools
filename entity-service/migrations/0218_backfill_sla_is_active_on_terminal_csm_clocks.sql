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

-- Backfill: SLAEngineRepository.CompleteClock/ReviseClocks previously never
-- cleared sla.is_active on a clock's genuine completion (stage ACHIEVED) or
-- cancellation (stage CANCELLED) -- is_active was only ever set TRUE, at
-- RegisterClock's own INSERT, and never touched again. Every source='CSM'
-- row that reached one of those terminal stages before that fix shipped is
-- still stuck at is_active = TRUE, so GET /sla-status (and
-- csm-notification-service's own Reconcile and pre-alert verification pass,
-- which both filter on is_active) still report it as "currently active"
-- long after it has genuinely, cleanly resolved. BREACHED is deliberately
-- left untouched here, same as the application-side fix: a clock whose
-- wall-clock duration ran out without yet being satisfied is not finished
-- (see sla_engine_repo.go's own slaEngineOpenStageFilter). COMPLETED is
-- included for symmetry with that same terminal-stage definition, even
-- though this engine has never itself written that label.
--
-- One-time and idempotent: a second run touches zero rows once the first
-- has already cleared them.
UPDATE sla
SET is_active = FALSE
WHERE source = 'CSM'
  AND is_active
  AND stage::TEXT IN ('ACHIEVED', 'CANCELLED', 'COMPLETED');
