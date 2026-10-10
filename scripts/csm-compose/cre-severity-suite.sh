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
# Every scenario of the CRE escalation ladder for ONE severity -- usually
# started from cre-escalation-ladder.sh (menu option 2) rather than directly, on the real
# clock, with the evidence collected into one file for review.
#
# S0 and P0 are the same severity: P0 is the old name, S0 the new one, and both
# are accepted (likewise S1/P1 ... S3/P3).
#
# What it runs, per severity:
#   every rotation of the rules sheet -- weekday morning, weekday day, weekday
#   evening, weekend day, Americas weekday night, Americas weekend night --
#   on an ABT and, where the rule depends on it, on no ABT; each one
#     acknowledged at LEVEL_0, LEVEL_1, LEVEL_2, LEVEL_3, LEVEL_4, and never;
#   plus a status move alone, a public comment alone, and a priority elevation.
#
# Real time only: one ladder minute is one wall-clock minute. No --fast, no
# compressed clock. --shift reports each incident at that rotation's next
# occurrence, so every rotation runs whatever the time of day is now.
#
# Nobody is contacted: every run is on the log channel.
#
# Scenarios run side by side, each with its own Redis -- two engines on one
# Redis work each other's ladders and the timings become meaningless. -j sets
# how many at once.
#
# What it collects, under scripts/csm-compose/.run/cre-escalation-ladder/<time>_suite_<severity>/
# (git-ignored -- it holds real staff names from the local roster):
#   cre-escalation-ladder-suite.log
#                    everything below in one file: share this one
#   summary.tsv      one row per scenario: rule, shift, outcome, timing drift
#   calls.tsv        one row per placed call: rung, attempt, who, due, actual
#   ground-truth/    who SHOULD be called: the roster, and the on-duty list at
#                    each scenario's own reported instant
#   scenarios/       each scenario's full log
#
# Usage:
#   cre-severity-suite.sh                 # S0, every scenario, 8 at a time
#   cre-severity-suite.sh -s S1 -j 10     # another severity, 10 at a time
#   cre-severity-suite.sh --dry-run       # list the scenarios, run nothing
#   cre-severity-suite.sh --only lkday    # just the scenarios whose id matches
#
# Needs the compose stack's postgres, mock-oidc and entity-service up, docker,
# go and python3. Ctrl-C stops every running scenario and removes its Redis.

set -euo pipefail

# Everything below is one { ... } group, so bash reads the whole file before
# running any of it. A script is otherwise read as it runs, and editing it
# under a run in progress -- a real-time ladder lasts up to two hours --
# makes that run resume at the wrong line of the new file, or silently stop.
{

SEVERITY=S0
JOBS=8
ONLY=""
DRY_RUN=""

usage() { sed -n '/^# Usage:/,/^# Needs/p' "$0" | sed 's/^# \{0,1\}//'; }
while [ $# -gt 0 ]; do
  case "$1" in
    -s|--severity) SEVERITY="$2"; shift 2 ;;
    -j|--jobs)     JOBS="$2"; shift 2 ;;
    --only)        ONLY="$2"; shift 2 ;;
    --dry-run)     DRY_RUN=1; shift ;;
    -h|--help)     usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

SEVERITY="$(printf '%s' "$SEVERITY" | tr '[:lower:]' '[:upper:]')"
case "$SEVERITY" in
  S0|P0) SEV=S0; OPEN="0 1 4 8 12";   END=16 ;;
  S1|P1) SEV=S1; OPEN="6 9 18 28 38"; END=48 ;;
  S2|P2) SEV=S2; OPEN="9 15 30 45 60"; END=75 ;;
  S3|P3) SEV=S3; OPEN="12 20 40 65 90"; END=115 ;;
  *) echo "-s must be S0..S3 (or P0..P3)" >&2; exit 2 ;;
esac
case "$JOBS" in ''|*[!0-9]*) echo "-j must be a number" >&2; exit 2 ;; esac
[ "$JOBS" -ge 1 ] || { echo "-j must be at least 1" >&2; exit 2; }

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
runner="${repo_root}/scripts/csm-compose/run-cre-ladder.sh"

# The minute LEVEL_n first opens, and roughly when a run acknowledged there
# ends (one minute after that rung's first call, for the acknowledgement to land).
open_at() { echo "$OPEN" | awk -v n="$1" '{print $(n+1)}'; }
minutes_for() {
  case "$1" in
    never)  echo $((END + 1)) ;;
    L0|L1|L2|L3|L4) echo $(( $(open_at "${1#L}") + 1 )) ;;
  esac
}

# ---------------------------------------------------------------------------
# The scenarios. One line each: id | expected rule | shift | team | ack | ack-by | extra
# team empty = on no ABT. ack "never" = never acknowledged.
# ---------------------------------------------------------------------------
CASES="
morning-vega|R1a|LK_MORNING|vega
morning-noabt|R1a|LK_MORNING|
weekendday-vega|R1b|LK_WEEKEND|vega
weekendday-noabt|R1b|LK_WEEKEND|
lkday-vega|R2|LK|vega
lkday-noabt|R3|LK|
evening-vega|R4a|LK_EVENING|vega
evening-noabt|R4b|LK_EVENING|
americas-night-vega|R5|USA|vega
americas-weekendnight-vega|R6|USA_WEEKEND|vega
"
# R5 and R6 do not depend on the ABT -- their first three rungs are the Americas
# team's -- so a no-ABT variant would repeat the vega one exactly.

SCENARIOS=""
add() { SCENARIOS="${SCENARIOS}$1
"; }
for ack in never L4 L3 L2 L1 L0; do                    # longest first, to pack the pool
  echo "$CASES" | while IFS='|' read -r name rule shift team; do
    [ -n "$name" ] || continue
    echo "${name}-ack${ack}|${rule}|${shift}|${team}|${ack}|both|"
  done
done > "${TMPDIR:-/tmp}/cre-suite-scenarios.$$"
while IFS= read -r l; do add "$l"; done < "${TMPDIR:-/tmp}/cre-suite-scenarios.$$"
rm -f "${TMPDIR:-/tmp}/cre-suite-scenarios.$$"
add "lkday-vega-statusonly|R2|LK|vega|L1|status|"
add "lkday-vega-commentonly|R2|LK|vega|L1|comment|"
add "lkday-vega-elevated|R2|LK|vega|L1|both|elevated"

if [ -n "$ONLY" ]; then
  SCENARIOS="$(printf '%s' "$SCENARIOS" | grep -E "^[^|]*(${ONLY})" || true)
"
fi
count="$(printf '%s' "$SCENARIOS" | grep -c . || true)"
[ "$count" -gt 0 ] || { echo "no scenario matches --only '${ONLY}'" >&2; exit 2; }

# Expected minutes, all scenarios, and an estimate at -j.
total=0
while IFS='|' read -r id rule shift team ack by extra; do
  [ -n "$id" ] || continue
  case "$by" in status|comment) m=$((END + 1)) ;; *) m="$(minutes_for "$ack")" ;; esac
  total=$((total + m))
done <<EOF
$SCENARIOS
EOF

echo "CRE escalation ladder -- ${SEV} suite, real time"
echo "  scenarios:   ${count}"
echo "  rungs open:  LEVEL_0..LEVEL_4 at ${OPEN// / · } min; a full ladder ends at ${END} min"
echo "  in parallel: ${JOBS}  (about $(( (total + JOBS - 1) / JOBS + 5 )) min wall clock; ${total} scenario-minutes)"
echo

if [ -n "$DRY_RUN" ]; then
  printf '%-44s %-5s %-12s %-6s %-6s %s\n' "SCENARIO" "RULE" "SHIFT" "TEAM" "ACK" "NOTES"
  while IFS='|' read -r id rule shift team ack by extra; do
    [ -n "$id" ] || continue
    note=""; [ "$by" != both ] && note="${by} only"; [ -n "$extra" ] && note="$extra"
    printf '%-44s %-5s %-12s %-6s %-6s %s\n' "$id" "$rule" "$shift" "${team:-none}" "$ack" "$note"
  done <<EOF
$SCENARIOS
EOF
  exit 0
fi

# ---------------------------------------------------------------------------
# Preconditions -- fail now rather than an hour in.
# ---------------------------------------------------------------------------
for c in docker go python3 curl; do command -v "$c" >/dev/null || { echo "$c is required" >&2; exit 1; }; done
[ "$(curl -s -o /dev/null -m 3 -w '%{http_code}' http://localhost:8081/health || true)" = 200 ] \
  || { echo "entity-service is not answering on :8081 -- docker compose up -d postgres mock-oidc entity-service" >&2; exit 1; }
token() {
  curl -s -m 5 -X POST http://localhost:9100/oauth2/token -d grant_type=client_credentials \
    -d client_id=csm-notification-service-dev-client -d client_secret=dev-secret \
    | python3 -c 'import sys,json; print(json.load(sys.stdin).get("access_token",""))'
}
[ -n "$(token)" ] || { echo "mock-oidc is not issuing tokens on :9100 -- docker compose up -d mock-oidc" >&2; exit 1; }

stamp="$(date +%Y%m%d-%H%M%S)"
out="${repo_root}/scripts/csm-compose/.run/cre-escalation-ladder/${stamp}_suite_${SEV}"
mkdir -p "${out}/scenarios" "${out}/ground-truth"

# ---------------------------------------------------------------------------
# Ground truth: who SHOULD be called. The roster now, before anything runs.
# ---------------------------------------------------------------------------
psql_q() { docker compose -f "${repo_root}/docker-compose.yml" exec -T postgres psql -U postgres -d csm_platform -AF$'\t' -c "$1"; }
{
  echo "# teams"
  psql_q "select key, type from team order by type, key;"
  echo
  echo "# team members with a rank or a nomination (role, alert_tier)"
  psql_q "select t.key as team, t.type, tm.role, coalesce(tm.alert_tier,'') as alert_tier, u.name, u.email
          from team_member tm join team t on t.id=tm.team_id join \"user\" u on u.id=tm.user_id
          where tm.role <> 'engineer' or tm.alert_tier is not null
          order by t.type, t.key, tm.role, tm.alert_tier nulls last, u.email;"
} > "${out}/ground-truth/roster.tsv" 2>&1
git -C "$repo_root" log -1 --format='commit %h %s' > "${out}/ground-truth/commit.txt"
cp "${repo_root}/scripts/csm-compose/paging-alert.yaml" "${out}/ground-truth/paging-alert.yaml"

# ---------------------------------------------------------------------------
# Run the pool.
# ---------------------------------------------------------------------------
# Ports come from the shared reservation (cre-ladder-ports.sh), never from a
# fixed base: four suites started together once all used 16501-16563 and broke
# each other's ladders. Every port this suite holds is released when it ends.
. "${repo_root}/scripts/csm-compose/cre-ladder-ports.sh"
# One build of the harness for the whole suite, shared by every scenario (see
# ESCALATION_LOCAL_BIN in run-cre-ladder.sh). A build per scenario, sixteen at a
# time, got killed under the load and took every scenario down with it.
suite_bin_dir="$(mktemp -d)"
trap 'release_ports; rm -rf "${suite_bin_dir}"' EXIT
echo "building the harness once for the suite..."
( cd "${repo_root}/integrations/csm-notification-service" && go build -o "${suite_bin_dir}/escalation-local" ./cmd/escalation-local ) \
  || { echo "building the harness failed" >&2; exit 1; }
export ESCALATION_LOCAL_BIN="${suite_bin_dir}/escalation-local"

stop_all() {
  trap - INT TERM
  echo; echo "stopping every running scenario..."
  for p in $(jobs -p); do kill -TERM "$p" 2>/dev/null || true; done
  wait 2>/dev/null || true
  echo "stopped. Partial results are in ${out}"
  exit 130
}
trap stop_all INT TERM

i=0
started="$(date +%s)"
while IFS='|' read -r id rule shift team ack by extra; do
  [ -n "$id" ] || continue
  i=$((i + 1))
  while [ "$(jobs -rp | wc -l | tr -d ' ')" -ge "$JOBS" ]; do sleep 2; done
  port="$(reserve_port 16400 16999)" || { echo "no free Redis port in 16400-16999" >&2; exit 1; }
  log="${out}/scenarios/$(printf '%02d' "$i")-${id}.log"
  args=(-s "$SEV" --shift "$shift" --max-calls 200)
  [ -n "$team" ] && args+=(-a "$team")
  [ "$ack" != never ] && args+=(--ack-at "LEVEL_${ack#L}" --ack-by "$by")
  [ "$extra" = elevated ] && args+=(--elevated)
  {
    echo "### SCENARIO ${id}"
    echo "### params expected-rule=${rule} shift=${shift} team=${team:-none} ack=${ack} ack-by=${by} extra=${extra:-none}"
    echo "### command: REDIS_PORT=${port} scripts/csm-compose/run-cre-ladder.sh ${args[*]}"
    echo "### started $(date '+%Y-%m-%d %H:%M:%S %Z')"
  } > "$log"
  REDIS_PORT="$port" "$runner" "${args[@]}" >> "$log" 2>&1 &
  printf '  [%2d/%d] started %-44s (%s, port %s)\n' "$i" "$count" "$id" "$rule" "$port"
  sleep 3   # stagger, so builds and Redis starts do not all land at once
done <<EOF
$SCENARIOS
EOF

echo; echo "all ${count} started; waiting for the last to finish..."
wait || true
trap - INT TERM
echo "finished in $(( ($(date +%s) - started) / 60 )) min"

# ---------------------------------------------------------------------------
# Ground truth per scenario: the on-duty list at its own reported instant.
# ---------------------------------------------------------------------------
tok="$(token)"
for log in "${out}"/scenarios/*.log; do
  at_ist="$(grep -m1 -oE 'reported at +[A-Za-z]{3} [0-9-]+ [0-9:]+ IST' "$log" | sed -E 's/reported at +[A-Za-z]{3} //; s/ IST//' || true)"
  [ -n "$at_ist" ] || continue
  at_utc="$(python3 -c "import sys,datetime as d; t=d.datetime.strptime(sys.argv[1],'%Y-%m-%d %H:%M').replace(tzinfo=d.timezone(d.timedelta(hours=5,minutes=30))); print(t.astimezone(d.timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ'))" "$at_ist")"
  curl -s -m 10 -H "x-jwt-assertion: ${tok}" "http://localhost:8081/team-schedule/on-duty?at=${at_utc}" \
    > "${out}/ground-truth/onduty-$(basename "$log" .log).json" || true
done

# ---------------------------------------------------------------------------
# Summaries, then everything into one file.
# ---------------------------------------------------------------------------
python3 "${repo_root}/scripts/csm-compose/cre-ladder-report.py" suite "$out"

{
  echo "######## CRE ${SEV} SUITE -- $(cat "${out}/ground-truth/commit.txt") -- $(date '+%Y-%m-%d %H:%M %Z')"
  echo "######## rungs open at ${OPEN} min; full ladder ends at ${END} min; parallel ${JOBS}"
  echo; echo "######## SUMMARY (summary.tsv)"; cat "${out}/summary.tsv"
  echo; echo "######## CALLS (calls.tsv)"; cat "${out}/calls.tsv"
  echo; echo "######## GROUND TRUTH: ROSTER"; cat "${out}/ground-truth/roster.tsv"
  for j in "${out}"/ground-truth/onduty-*.json; do
    [ -f "$j" ] || continue
    echo; echo "######## GROUND TRUTH: ON DUTY for $(basename "$j" .json | sed 's/^onduty-//')"
    python3 -c 'import sys,json
d=json.load(open(sys.argv[1]))
rows=d if isinstance(d,list) else (d.get("assignments") or d.get("data") or d.get("items") or [])
for r in rows:
    e=r.get("engineer",{}); print("\t".join([r.get("teamKey",""), r.get("shiftCode",""), e.get("name",""), e.get("email","")]))' "$j" 2>/dev/null || cat "$j"
  done
  for log in "${out}"/scenarios/*.log; do echo; echo "######## $(basename "$log")"; cat "$log"; done
} > "${out}/cre-escalation-ladder-suite.log"

echo
echo "Share this file for review:"
echo "  ${out}/cre-escalation-ladder-suite.log"
echo "It contains real staff names from the local roster -- keep it out of tickets, PRs and chat."

exit
}
