#!/usr/bin/env bash
# Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
#
# WSO2 LLC. licenses this file to you under the Apache License,
# Version 2.0 (the "License"); you may not use this file except
# in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied.  See the License for the
# specific language governing permissions and limitations
# under the License.
#
# The SRE escalation ladder, end to end, on the real clock.
#
# Unlike trigger-sre-escalation.sh, which drives the engine in-process on a
# compressed clock, this goes through the running system: the event is put on
# the local Kafka topic by entity-service's own publisher, consumed by the
# running csm-notification-service container (both engines, their own consumer
# groups, Redis, the ticker), resolved against the Team Schedule, and stopped
# by a real incident.assigned / incident.acknowledged / incident.comment_added
# on the same topic. One ladder minute is one real minute: L2 comes 5 minutes
# after L1. Nothing is dialled: paging-alert.yaml runs both ladders on the log
# channel.
#
# Only incident creation is skipped -- POST /incidents is 503 on the local
# Postgres data source -- so the publisher stands in for entity-service.
#
# Usage:
#   sre-e2e.sh up                      build, start, roster L1/L2/L3 for now
#   sre-e2e.sh run [-t team] [-p priority] [-x contactType]
#                                      THE ONE TO USE: create an incident, show
#                                      every rung live as it is called, and
#                                      acknowledge by pressing a / s / c
#   sre-e2e.sh create [-t team] [-p priority] [-x contactType]
#                                      publish incident.created (default apollo, HIGH)
#   sre-e2e.sh ack assign|status|comment [incident]
#                                      publish a stop gesture (default: the last incident)
#   sre-e2e.sh watch [incident]        follow the ladder in the service's log
#   sre-e2e.sh status [incident]       the ladder's state in Redis, both ladders
#   sre-e2e.sh oncall                  who holds each SRE tier right now
#   sre-e2e.sh sme [-s smeTeam] [-k specialOpsTeam] [incident]
#                                      roster L1-L3 on the live SME window of
#                                      asgardeo and choreo-runtime, then publish
#                                      the Special Ops alert "Escalate to
#                                      specialist team" raises (default: smeTeam
#                                      choreo-runtime, on the last incident) and
#                                      follow the SME ladder (L1 now, L2 and L3
#                                      after sme.timing.interval each)
#   sre-e2e.sh smeoncall               who is on duty on each SME rota right now
#   sre-e2e.sh down                    remove the test rota, restore the base stack
set -euo pipefail

cd "$(dirname "$0")/../.."
COMPOSE=(docker compose -f docker-compose.yml -f scripts/csm-compose/sre-e2e.compose.yml)
STATE="${TMPDIR:-/tmp}/sre-e2e-last-incident"
PSQL=(docker compose exec -T postgres psql -U postgres -d csm_platform -v ON_ERROR_STOP=1)

last_incident() { [[ -s "$STATE" ]] && cat "$STATE" || { echo "no incident yet: run '$0 create' first, or pass one" >&2; exit 1; }; }

build_publisher() {
  local arch
  arch="$(docker compose exec -T entity-service uname -m </dev/null | tr -d '\r' | sed 's/aarch64/arm64/;s/x86_64/amd64/')"
  ( cd entity-service && GOOS=linux GOARCH="$arch" go build -o "${TMPDIR:-/tmp}/publish-incident" ./internal/tools/publishincident/main.go )
  docker compose cp "${TMPDIR:-/tmp}/publish-incident" entity-service:/tmp/publish-incident >/dev/null
}

# Every docker compose exec reads from /dev/null: left on the terminal it
# would swallow the keys 'run' is waiting for.
publish() { docker compose exec -T entity-service /tmp/publish-incident -broker kafka:9094 -topic case-events "$@" </dev/null; }
# The Special Ops alert goes to the operations topic, as entity-service sends it.
publish_sre() { docker compose exec -T entity-service /tmp/publish-incident -broker kafka:9094 -topic sre-events "$@" </dev/null; }

smeoncall() {
  "${PSQL[@]}" </dev/null -c "
    SELECT a.team_key AS team, s.code AS window, coalesce(a.tier::text, 'L1 (no tier)') AS tier, u.name AS engineer,
           to_char(a.ends_at AT TIME ZONE 'Asia/Kolkata', 'HH24:MI') AS until_ist, coalesce(a.note,'') AS note
      FROM team_schedule_assignment a
      JOIN team_schedule_shift s ON s.id = a.shift_id
      JOIN \"user\" u ON u.id = a.user_id
     WHERE s.family = 'SME' AND tstzrange(a.starts_at, a.ends_at, '[)') @> now()
     ORDER BY 1, 3"
}

# Roster a free engineer on each of L1, L2 and L3 of the SME team's Day or
# Night window live right now, for the tiers the seed leaves empty. Different
# people per team and tier, so the page shows which rota and rung answered.
# Tagged QA-SRE-E2E; 'down' removes it.
roster_sme() {
  "${PSQL[@]}" -v team="$1" <<'SQL' >/dev/null
-- psql substitutes :'team' here, never inside the DO block's dollar quotes.
SELECT set_config('qa_sme.team', :'team', false);
DO $$
DECLARE
  k text := current_setting('qa_sme.team'); w record; who uuid; tid uuid; ttype text; t text;
BEGIN
  SELECT id, type INTO tid, ttype FROM team WHERE key = k;
  IF tid IS NULL THEN RAISE NOTICE 'no team %', k; RETURN; END IF;
  SELECT s.id AS shift_id, s.zone_id, d.day,
         (d.day + make_interval(mins => s.start_minute)) AT TIME ZONE 'Asia/Kolkata' AS starts_at,
         (d.day + make_interval(mins => s.end_minute))   AT TIME ZONE 'Asia/Kolkata' AS ends_at
    INTO w
    FROM team_schedule_shift s
    JOIN team_schedule_zone z ON z.id = s.zone_id
    JOIN team_schedule_rota r ON r.id = z.rota_id,
         LATERAL (VALUES ((now() AT TIME ZONE 'Asia/Kolkata')::date),
                         ((now() AT TIME ZONE 'Asia/Kolkata')::date - 1)) d(day)
   WHERE s.family = 'SME' AND r.team_type = ttype
     AND now() >= (d.day + make_interval(mins => s.start_minute)) AT TIME ZONE 'Asia/Kolkata'
     AND now() <  (d.day + make_interval(mins => s.end_minute))   AT TIME ZONE 'Asia/Kolkata'
   LIMIT 1;
  IF w IS NULL THEN RAISE NOTICE 'no live SME window for %', k; RETURN; END IF;
  -- A test row whose person is away that day is not on duty: replace it.
  DELETE FROM team_schedule_assignment a
   WHERE a.note = 'QA-SRE-E2E' AND a.team_key = k AND a.shift_id = w.shift_id AND a.rota_date = w.day
     AND EXISTS (SELECT 1 FROM team_schedule_absence b WHERE b.user_id = a.user_id
                  AND b.starts_on <= w.day AND (b.ends_on IS NULL OR b.ends_on >= w.day));
  FOREACH t IN ARRAY ARRAY['L1','L2','L3'] LOOP
    -- An untiered row on the window counts as L1, as the ladder reads it.
    IF EXISTS (SELECT 1 FROM team_schedule_assignment a
                WHERE a.team_key = k AND a.shift_id = w.shift_id AND a.rota_date = w.day
                  AND coalesce(a.tier::text, 'L1') = t) THEN
      CONTINUE;
    END IF;
    SELECT m.user_id INTO who FROM team_member m JOIN team tm ON tm.id = m.team_id
     WHERE tm.type = 'sre-abt' AND m.role = 'engineer'
       AND NOT EXISTS (SELECT 1 FROM team_schedule_absence b WHERE b.user_id = m.user_id
                        AND b.starts_on <= w.day AND (b.ends_on IS NULL OR b.ends_on >= w.day))
       AND NOT EXISTS (SELECT 1 FROM team_schedule_assignment r WHERE r.user_id = m.user_id
                        AND tstzrange(r.starts_at, r.ends_at, '[)') && tstzrange(w.starts_at, w.ends_at, '[)'))
     ORDER BY md5(m.user_id::text || k || t) LIMIT 1;
    IF who IS NULL THEN RAISE NOTICE 'no free engineer for % %', k, t; CONTINUE; END IF;
    INSERT INTO team_schedule_assignment
           (user_id, team_id, team_key, shift_id, zone_id, tier, rota_date, starts_at, ends_at,
            is_on_call, source, note, is_rotation, created_by, updated_by)
    VALUES (who, tid, k, w.shift_id, w.zone_id, t::team_schedule_tier_enum, w.day, w.starts_at, w.ends_at,
            TRUE, 'MANUAL', 'QA-SRE-E2E', TRUE, 'qa-sre-e2e', 'qa-sre-e2e');
  END LOOP;
END $$;
SQL
}

oncall() {
  "${PSQL[@]}" </dev/null -c "
    SELECT a.team_key AS team, coalesce(a.tier::text, s.tier::text) AS tier, s.code AS window, u.name AS engineer,
           to_char(a.ends_at AT TIME ZONE 'Asia/Kolkata', 'HH24:MI') AS until_ist, coalesce(a.note,'') AS note
      FROM team_schedule_assignment a
      JOIN team_schedule_shift s ON s.id = a.shift_id
      JOIN \"user\" u ON u.id = a.user_id
     WHERE s.family = 'SRE' AND s.is_escalation
       AND tstzrange(a.starts_at, a.ends_at, '[)') @> now()
       AND coalesce(a.tier::text, s.tier::text) IS NOT NULL
     ORDER BY 2, 1"
}

# Roster Apollo on every SRE tier of the escalation window live right now, for
# the tiers the seed leaves empty, with an engineer who is free (no absence that
# day, nothing else overlapping). Tagged QA-SRE-E2E; 'down' removes it.
roster_now() {
  "${PSQL[@]}" <<'SQL' >/dev/null
DO $$
DECLARE
  t text; w record; who uuid; apollo uuid;
BEGIN
  SELECT id INTO apollo FROM team WHERE key = 'apollo';
  FOREACH t IN ARRAY ARRAY['L1','L2','L3'] LOOP
    IF EXISTS (SELECT 1 FROM team_schedule_assignment a JOIN team_schedule_shift s ON s.id = a.shift_id
                WHERE a.team_key = 'apollo' AND s.family = 'SRE' AND s.is_escalation
                  AND coalesce(a.tier::text, s.tier::text) = t
                  AND tstzrange(a.starts_at, a.ends_at, '[)') @> now()) THEN
      CONTINUE;
    END IF;
    -- The live escalation window that can hold this tier: the L1 window for L1
    -- when there is one, otherwise the zone's open-tier window.
    SELECT s.id AS shift_id, s.zone_id, d.day,
           (d.day + make_interval(mins => s.start_minute)) AT TIME ZONE 'Asia/Kolkata' AS starts_at,
           (d.day + make_interval(mins => s.end_minute))   AT TIME ZONE 'Asia/Kolkata' AS ends_at
      INTO w
      FROM team_schedule_shift s,
           LATERAL (VALUES ((now() AT TIME ZONE 'Asia/Kolkata')::date),
                           ((now() AT TIME ZONE 'Asia/Kolkata')::date - 1)) d(day)
     WHERE s.family = 'SRE' AND s.is_escalation
       AND (s.tier IS NULL OR s.tier::text = t)
       AND (s.day_scope::text = 'ANY'
            OR (s.day_scope::text = 'WEEKDAY') = (extract(isodow FROM d.day) < 6))
       AND now() >= (d.day + make_interval(mins => s.start_minute)) AT TIME ZONE 'Asia/Kolkata'
       AND now() <  (d.day + make_interval(mins => s.end_minute))   AT TIME ZONE 'Asia/Kolkata'
     ORDER BY (s.tier IS NULL) -- a fixed-tier window first
     LIMIT 1;
    IF w IS NULL THEN
      RAISE NOTICE 'no live SRE window can hold %', t;
      CONTINUE;
    END IF;
    SELECT m.user_id INTO who FROM team_member m
     WHERE m.team_id = apollo AND m.role = 'engineer'
       AND NOT EXISTS (SELECT 1 FROM team_schedule_absence b WHERE b.user_id = m.user_id
                        AND b.starts_on <= w.day AND (b.ends_on IS NULL OR b.ends_on >= w.day))
       AND NOT EXISTS (SELECT 1 FROM team_schedule_assignment r WHERE r.user_id = m.user_id
                        AND tstzrange(r.starts_at, r.ends_at, '[)') && tstzrange(w.starts_at, w.ends_at, '[)'))
     ORDER BY m.user_id LIMIT 1;
    IF who IS NULL THEN
      RAISE NOTICE 'no free Apollo engineer for %', t;
      CONTINUE;
    END IF;
    INSERT INTO team_schedule_assignment
           (user_id, team_id, team_key, shift_id, zone_id, tier, rota_date, starts_at, ends_at,
            is_on_call, source, note, is_rotation, created_by, updated_by)
    VALUES (who, apollo, 'apollo', w.shift_id, w.zone_id,
            CASE WHEN (SELECT tier FROM team_schedule_shift WHERE id = w.shift_id) IS NULL THEN t::team_schedule_tier_enum END,
            w.day, w.starts_at, w.ends_at, TRUE, 'MANUAL', 'QA-SRE-E2E', TRUE, 'qa-sre-e2e', 'qa-sre-e2e');
  END LOOP;
END $$;
SQL
}

check_engine() {
  local resolver logs
  resolver="$(docker inspect csm-platform-csm-notification-service-1 \
    --format '{{range .Config.Env}}{{println .}}{{end}}' 2>/dev/null | sed -n 's/^INCIDENT_ESCALATION_RESOLVER=//p')"
  logs="$(docker compose logs csm-notification-service 2>/dev/null)"
  if [[ "$resolver" != team-schedule ]] || ! grep -q 'ladder=escalation-sre' <<<"$logs" \
     || grep -q 'invalid escalation configuration' <<<"$logs"; then
    echo "==> the notification service running now is not this SRE test setup" >&2
    grep -E 'invalid escalation configuration|escalation is disabled|is not the' <<<"$logs" | tail -2 | sed 's/^/    /' >&2
    echo "    (another worktree's 'docker compose up' replaces it: the compose project is shared)" >&2
    echo "    run:  $0 up   -- then run this again" >&2
    exit 1
  fi
}

# One line per thing the ladder does, in words, stamped with the wall clock and
# the minute of the ladder.
narrate() {
  local start=$1 line now el
  while IFS= read -r line; do
    now=$(date +%s); el=$(( (now - start) / 60 ))
    case "$line" in
      *"routing put the incident on this ladder"*)
        printf '  %s  +%2dm  ROUTED    %s ladder, by routing rule "%s"\n' "$(date +%H:%M:%S)" "$el" \
          "$(sed -E 's/.* ladder=([a-z]+) .*/\1/' <<<"$line" | tr a-z A-Z)" "$(sed -E 's/.* rule=([a-z0-9-]+) .*/\1/' <<<"$line")" ;;
      *"ladder scheduled"*)
        printf '  %s  +%2dm  PLANNED   %s ladder: %s, %s call(s)\n' "$(date +%H:%M:%S)" "$el" \
          "$(sed -E 's/.* ladder=([A-Z]+) .*/\1/' <<<"$line")" "$(sed -E 's/.* levels="([^"]+)".*/\1/' <<<"$line")" "$(sed -E 's/.* calls=([0-9]+).*/\1/' <<<"$line")" ;;
      *"would notify"*)
        printf '  %s  +%2dm  CALLING   %-11s -> %s   (window %s)\n' "$(date +%H:%M:%S)" "$el" \
          "$(sed -E 's/.* role="([^"]+)".*/\1/' <<<"$line")" "$(sed -E 's/.* name="([^"]+)".*/\1/' <<<"$line")" "$(sed -E 's/.* shift=([A-Z0-9_]+).*/\1/' <<<"$line")" ;;
      *"level cannot be called"*)
        printf '  %s  +%2dm  SKIPPED   %s: nobody on that tier (%s)\n' "$(date +%H:%M:%S)" "$el" \
          "$(sed -E 's/.* level=([A-Z_0-9]+) .*/\1/' <<<"$line")" "$(sed -E 's/.* reason=([A-Z_]+).*/\1/' <<<"$line")" ;;
      *"ladder cancelled"*)
        printf '  %s  +%2dm  STOPPED   by "%s" -- %s rung call(s) made, %s cancelled\n' "$(date +%H:%M:%S)" "$el" \
          "$(sed -E 's/.* reason="?([^"=]+)"? rule=.*/\1/' <<<"$line")" "$(sed -E 's/.* placedCalls=([0-9]+).*/\1/' <<<"$line")" "$(sed -E 's/.* cancelledCalls=([0-9]+).*/\1/' <<<"$line")" ;;
      *"half acknowledged"*)
        printf '  %s  +%2dm  NOT YET   CRE ladder saw one gesture; it stops only on both (status AND comment)\n' "$(date +%H:%M:%S)" "$el" ;;
      *"exhausted without acknowledgement"*)
        printf '  %s  +%2dm  FINISHED  every rung called, nobody acknowledged\n' "$(date +%H:%M:%S)" "$el" ;;
      *"SME paged"*)
        printf '  %s  +%2dm  SME PAGE  %s, on duty for SME team %s   (window %s)\n' "$(date +%H:%M:%S)" "$el" \
          "$(sed -E 's/.* recipient="?([^"=]+)"? shift=.*/\1/' <<<"$line")" "$(sed -E 's/.* smeTeam=([a-z0-9_-]+).*/\1/' <<<"$line")" "$(sed -E 's/.* shift=([A-Z0-9_]+).*/\1/' <<<"$line")" ;;
      *"no SME page"*|*"ignored, SME page"*|*"ignored, an engineer"*)
        printf '  %s  +%2dm  NO SME    %s\n' "$(date +%H:%M:%S)" "$el" "$(sed -E 's/.*msg="escalation: ([^"]+)".*/\1/' <<<"$line")" ;;
      *"SME rung cannot be called"*)
        printf '  %s  +%2dm  SKIPPED   SME %s: nobody on that tier (%s)\n' "$(date +%H:%M:%S)" "$el" \
          "$(sed -E 's/.* level=([A-Z_0-9]+) .*/\1/' <<<"$line")" "$(sed -E 's/.* reason=([A-Z_]+).*/\1/' <<<"$line")" ;;
      *"SME pages closed"*)
        printf '  %s  +%2dm  ANSWERED  engineer assigned; SME page closed\n' "$(date +%H:%M:%S)" "$el" ;;
      *"configuration does not escalate"*)
        printf '  %s  +%2dm  NOT STARTED  CRE ladder: %s\n' "$(date +%H:%M:%S)" "$el" "$(sed -E 's/.* reason="([^"]+)".*/\1/' <<<"$line")" ;;
    esac
  done
}

running() {  # how many of the incident's ladders are still in Redis
  local n=0 ns
  for ns in "incident:escalation:state:" "incident:escalation:sre:state:"; do
    [[ "$(docker compose exec -T redis redis-cli EXISTS "$ns$1" </dev/null | tr -d '\r')" == 1 ]] && n=$((n + 1))
  done
  echo "$n"
}

cmd="${1:-}"; shift || true
case "$cmd" in
  run)
    TEAM=apollo PRIORITY=HIGH CONTACT=""
    while getopts ":t:p:x:" opt; do
      case $opt in t) TEAM=$OPTARG ;; p) PRIORITY=$OPTARG ;; x) CONTACT=$OPTARG ;; *) echo "run [-t team] [-p priority] [-x contactType]" >&2; exit 2 ;; esac
    done
    [[ "$TEAM" == none ]] && TEAM=""
    # The compose project is shared by every worktree: a 'docker compose up'
    # from another branch replaces this container with that branch's build and
    # config. Check that what is running now is this test setup, with the SRE
    # ladder enabled, before publishing anything into it.
    check_engine
    echo "==> on call right now"
    oncall
    build_publisher
    ARGS=(-priority "$PRIORITY" -team "$TEAM" -title "SRE end-to-end test incident")
    [[ -n "$CONTACT" ]] && ARGS+=(-contact-type "$CONTACT")
    OUT="$(publish "${ARGS[@]}")"
    INC="$(printf '%s' "$OUT" | sed -n 's/^published incident.created for \([^ ]*\) .*/\1/p')"
    [[ -n "$INC" ]] || { echo "$OUT" >&2; echo "could not publish the incident" >&2; exit 1; }
    echo "$INC" > "$STATE"
    START=$(date +%s)
    cat <<BANNER

  Incident $INC  (team ${TEAM:-none}, priority $PRIORITY${CONTACT:+, raised by $CONTACT}) created at $(date +%H:%M:%S)
  The ladder runs on the real clock: L1 now, L2 at +5 min, L3 at +10 min.

  Acknowledge at any moment by typing a letter and pressing Enter:
     a  assign an engineer      (stops the SRE ladder)
     s  move it out of NEW      (stops the SRE ladder; one of the CRE ladder's two gestures)
     c  post a public comment   (does NOT stop the SRE ladder; the CRE ladder's other gesture)
     q  stop watching           (the ladder keeps running in the service)

BANNER
    docker compose logs -f --since 30s csm-notification-service 2>/dev/null \
      | grep --line-buffered "$INC" | narrate "$START" &
    # Everything in that pipeline is a child of this shell; stop all of it on
    # the way out, or 'docker compose logs -f' outlives the run.
    trap 'pkill -P $$ 2>/dev/null; true' EXIT
    seen=0
    while true; do
      if read -r -t 5 key; then
        case "$key" in
          a|assign)  publish -event assigned     -incident-id "$INC" >/dev/null && echo "  $(date +%H:%M:%S)         SENT      an engineer assigned" ;;
          s|status)  publish -event acknowledged -incident-id "$INC" >/dev/null && echo "  $(date +%H:%M:%S)         SENT      moved out of NEW" ;;
          c|comment) publish -event comment      -incident-id "$INC" >/dev/null && echo "  $(date +%H:%M:%S)         SENT      a public comment" ;;
          q|quit)    echo "  stopped watching; the ladder carries on in the service ('$0 status $INC')"; exit 0 ;;
          "") ;;
          *) echo "  (a = assign, s = leave NEW, c = public comment, q = stop watching)" ;;
        esac
      fi
      n=$(running "$INC")
      if [[ $n -gt 0 ]]; then seen=1; fi
      if [[ $seen == 1 && $n == 0 ]]; then
        sleep 2
        echo; echo "==> every ladder for $INC has finished or been stopped"
        exit 0
      fi
      if [[ $seen == 0 && $(( $(date +%s) - START )) -gt 40 ]]; then
        echo; echo "==> no ladder started for $INC within 40 s -- that is the expected result when no routing rule takes it (e.g. -t none -x EMAIL)"
        exit 0
      fi
    done
    ;;
  up)
    echo "==> building entity-service, the notification service and the gateway shim from this branch"
    "${COMPOSE[@]}" build entity-service csm-notification-service entity-gateway-shim >/dev/null
    echo "==> starting them with the SRE end-to-end override"
    "${COMPOSE[@]}" up -d entity-service entity-gateway-shim csm-notification-service mock-oidc redis kafka >/dev/null
    for _ in $(seq 60); do
      docker compose logs csm-notification-service --since 2m 2>/dev/null | grep -q 'ladder=escalation-sre' && break
      sleep 2
    done
    docker compose logs csm-notification-service --since 3m 2>/dev/null \
      | grep -E 'Team Schedule|incident call escalation is enabled' | sed 's/^/    /' | tail -3
    echo "==> rostering Apollo L1/L2/L3 on the SRE window live now (QA-SRE-E2E)"
    roster_now
    oncall
    echo "==> ready. Next: $0 create"
    ;;
  create)
    TEAM=apollo PRIORITY=HIGH CONTACT=""
    while getopts ":t:p:x:" opt; do
      case $opt in t) TEAM=$OPTARG ;; p) PRIORITY=$OPTARG ;; x) CONTACT=$OPTARG ;; *) echo "create [-t team] [-p priority] [-x contactType]" >&2; exit 2 ;; esac
    done
    [[ "$TEAM" == none ]] && TEAM=""
    build_publisher
    ARGS=(-priority "$PRIORITY" -team "$TEAM" -title "SRE end-to-end test incident")
    [[ -n "$CONTACT" ]] && ARGS+=(-contact-type "$CONTACT")
    OUT="$(publish "${ARGS[@]}")"
    echo "$OUT"
    INC="$(printf '%s' "$OUT" | sed -n 's/^published incident.created for \([^ ]*\) .*/\1/p')"
    echo "$INC" > "$STATE"
    echo "==> incident $INC published at $(TZ=Asia/Kolkata date '+%H:%M:%S IST'). Next: $0 watch"
    ;;
  ack)
    GESTURE="${1:?ack assign|status|comment [incident]}"; INC="${2:-$(last_incident)}"
    case "$GESTURE" in assign) EV=assigned ;; status) EV=acknowledged ;; comment) EV=comment ;; *) echo "ack assign|status|comment" >&2; exit 2 ;; esac
    build_publisher
    publish -event "$EV" -incident-id "$INC"
    echo "==> sent at $(TZ=Asia/Kolkata date '+%H:%M:%S IST'). Watch for 'ladder cancelled' (assign, status) or nothing (comment)."
    ;;
  watch)
    INC="${1:-local-inc-}"
    echo "==> following ${1:-every test incident} (ctrl-c to stop)"
    docker compose logs -f --since 30m csm-notification-service 2>/dev/null \
      | grep --line-buffered "$INC" \
      | grep --line-buffered -E 'routing put|ladder scheduled|would notify|ALERT TRIGGERED|ladder cancelled|exhausted|half acknowledged|level cannot be called|scheduled no|configuration does not|SME' \
      | sed -E -u 's/^csm-notification-service-1 *\| *//'
    ;;
  status)
    INC="${1:-$(last_incident)}"
    for ns in "incident:escalation:state:" "incident:escalation:sre:state:"; do
      printf '%-36s ' "$ns"
      docker compose exec -T redis redis-cli EXISTS "$ns$INC" | tr -d '\r' | sed 's/1/running/;s/0/no ladder (never started, finished or stopped)/'
    done
    ;;
  oncall) oncall ;;
  smeoncall) smeoncall ;;
  sme)
    SME=choreo-runtime SO=choreo-runtime-team
    while getopts ":s:k:" opt; do
      case $opt in s) SME=$OPTARG ;; k) SO=$OPTARG ;; *) echo "sme [-s smeTeam] [-k specialOpsTeam] [incident]" >&2; exit 2 ;; esac
    done
    shift $((OPTIND - 1))
    INC="${1:-$(last_incident)}"
    echo "==> rostering L1-L3 on the live SME window of asgardeo and choreo-runtime (QA-SRE-E2E)"
    roster_sme asgardeo
    roster_sme choreo-runtime
    smeoncall
    build_publisher
    START=$(date +%s)
    publish_sre -event special-ops -incident-id "$INC" -sme-team "$SME" -so-team "$SO" -previous-group Apollo >/dev/null
    echo "==> $INC escalated to Special Ops team $SO (SME team ${SME:-from sme.teams}) at $(TZ=Asia/Kolkata date '+%H:%M:%S IST')"
    echo "==> following the SME ladder for ${SME_WATCH_SECONDS:-660}s (SME_WATCH_SECONDS; ctrl-c stops watching only)"
    docker compose logs -f --since 5s csm-notification-service 2>/dev/null \
      | grep --line-buffered "$INC" | narrate "$START" &
    trap 'pkill -P $$ 2>/dev/null; true' EXIT
    sleep "${SME_WATCH_SECONDS:-660}"
    ;;
  down)
    "${PSQL[@]}" -c "DELETE FROM team_schedule_assignment WHERE note = 'QA-SRE-E2E'" >/dev/null && echo "==> test rota removed"
    docker compose up -d csm-notification-service >/dev/null && echo "==> notification service back on the base stack"
    "${COMPOSE[@]}" stop entity-gateway-shim >/dev/null 2>&1 || true
    ;;
  *) sed -n '/^# Usage:/,/^set -euo/p' "$0" | sed 's/^# \{0,1\}//;$d'; exit 2 ;;
esac
