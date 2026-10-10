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
# The one place to start an SRE escalation ladder test from.
#
# The SRE ladder: L1 support at once, L2 five minutes later, L3 five minutes
# after that (L4 at +15 with --l4, unconfirmed), one person per rung, the same
# clock for every priority. Who each rung reaches comes from the Team Schedule
# for the time the incident was reported, so the rotation -- weekday or
# weekend, Sri Lanka hours or the Americas night -- decides the people.
# It stops when an engineer is assigned or the incident leaves NEW; a public
# comment does NOT stop it (internal/paging/engine.go Handle).
#
# Run it with no arguments and it asks, one numbered list at a time:
#   1. which severity          S0..S3 (P0..P3 are the same levels, old names)
#   2. when it was reported    a weekday or weekend rotation, or a time of your own
#   3. which SRE team          apollo, artemis, or an unassigned monitoring alert
#   4. where it is acknowledged L1, L2, L3 (L4), or never
#   5. how rungs reach people  log only, Google Chat, calls, or both
# then shows what it is about to do and runs it on the real clock -- one ladder
# minute is one minute. Or pick "every scenario" for the suite: every weekday
# and weekend rotation x every acknowledgement level, side by side.
#
# Every run is logged, whatever the channel, to
#   scripts/csm-compose/.run/sre-escalation-ladder/<time>_<sev>_<shift>_<team>_ack-<level>_<channel>.log
# headed by the exact command that repeats it and ended by a report: each
# rung's due time against when it was placed, and PASS/FAIL against what the
# rules say should happen. The folder is git-ignored: the logs hold real staff
# names from the local roster, so keep them out of tickets, PRs and chat.
#
# Every question has a flag; anything given as a flag is not asked:
#   sre-escalation-ladder.sh -s S0 --shift LK_MORNING -a apollo --ack L2 -c log
#   sre-escalation-ladder.sh -s S1 --shift USA_WEEKEND -a artemis --ack never -c chat
#   sre-escalation-ladder.sh -s S1 --at 23:30 --weekend -a none --ack L1 -c log
#   sre-escalation-ladder.sh -s S0 -a apollo --ack L3 -c call --to +94770000000
#   sre-escalation-ladder.sh --suite -a apollo -j 8
#
#   -s, --severity S0..S3|P0..P3
#       --shift LK_MORNING|LK|LK_EVENING|USA|LK_WEEKEND|USA_WEEKEND
#   -t, --at HH:MM|YYYY-MM-DDTHH:MM   (IST)   [--weekend with HH:MM]
#   -a, --team apollo|artemis|none     none = an unassigned monitoring alert
#       --ack L1..L3|L4|never          [--ack-by assign|status|comment]
#       --l4                           add the unconfirmed L4 support rung
#   -c, --channel log|chat|call|both   [--to E.164 for real calls; else a stub]
#       --elevated                     start it as a priority elevation
#   -m, --minute DURATION              one ladder minute (default 1m, real time)
#       --suite [-j N] [--only REGEX]  every rotation x every ack level, logged only
#   -y, --yes                          do not ask before starting
#
# Needs the compose stack's postgres, mock-oidc and entity-service up, docker,
# go and python3. Chat posts to the space in SRE_CHAT_WEBHOOK_URL and real
# calls need TWILIO_*: from the environment, scripts/csm-compose/.env, or
# ESCALATION_ENV_FILE (default: the service's .env). All three are git-ignored;
# a webhook URL is a credential, so it never goes in this script.

set -euo pipefail

# One { ... } group, so bash reads the whole file before running any of it: a
# real-time suite runs for a while, and editing the script under a running one
# would otherwise make it resume at the wrong line of the new file.
{

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${here}/../.." && pwd)"
service_dir="${repo_root}/integrations/csm-notification-service"
run_dir="${here}/.run/sre-escalation-ladder"
bin_dir="${run_dir}/bin"
ports_dir="${run_dir}/.ports"

ENTITY_URL="${ENTITY_URL:-http://localhost:8081}"
TOKEN_URL="${TOKEN_URL:-http://localhost:9100/oauth2/token}"
CLIENT_ID="${CLIENT_ID:-csm-notification-service-dev-client}"
CLIENT_SECRET="${CLIENT_SECRET:-dev-secret}"
ENV_FILE="${ESCALATION_ENV_FILE:-${service_dir}/.env}"

ALL_SHIFTS="LK_MORNING LK LK_EVENING USA LK_WEEKEND USA_WEEKEND"

die() { echo "error: $*" >&2; exit 1; }
upper() { printf '%s' "$1" | tr '[:lower:]' '[:upper:]'; }
lower() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }

SEVERITY="" SHIFT="" AT="" WEEKEND="" TEAM="" TEAM_SET="" ACK="" ACK_BY=assign
CHANNEL="" TO="" STUB="" ELEVATED="" L4="" MINUTE=1m YES="" MODE="" JOBS=6 ONLY=""
JOB=""   # internal: one suite job, run by the suite itself

while [ $# -gt 0 ]; do
  case "$1" in
    -s|--severity) SEVERITY="$2"; shift 2 ;;
    --shift)       SHIFT="$2"; shift 2 ;;
    -t|--at)       AT="$2"; shift 2 ;;
    --weekend)     WEEKEND=1; shift ;;
    -a|--team)     TEAM="$2"; TEAM_SET=1; shift 2 ;;
    --ack)         ACK="$2"; shift 2 ;;
    --ack-by)      ACK_BY="$2"; shift 2 ;;
    --l4)          L4=1; shift ;;
    -c|--channel)  CHANNEL="$2"; shift 2 ;;
    --to)          TO="$2"; shift 2 ;;
    --stub)        STUB=1; shift ;;
    --elevated)    ELEVATED=1; shift ;;
    -m|--minute)   MINUTE="$2"; shift 2 ;;
    --suite)       MODE=suite; shift ;;
    -j|--jobs)     JOBS="$2"; shift 2 ;;
    --only)        ONLY="$2"; shift 2 ;;
    -y|--yes)      YES=1; shift ;;
    --job)         JOB="$2"; MODE=single; YES=1; shift 2 ;;
    -h|--help)     sed -n '/^# The one place/,/^# the environment or/p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown option: $1 (see --help)" ;;
  esac
done

interactive=""
[ -t 0 ] && [ -z "${JOB}" ] && interactive=1

# choose TITLE DEFAULT OPTION... -- a numbered list; Enter takes DEFAULT.
# Sets CHOICE to the chosen number. A bad answer asks again.
choose() {
  local title="$1" def="$2" i=1 opt answer
  shift 2
  [ -n "${interactive}" ] || die "${title}: not given as a flag, and there is no terminal to ask on"
  echo
  echo "${title}"
  for opt in "$@"; do
    printf '  %2d) %s\n' "$i" "$opt"
    i=$((i + 1))
  done
  while :; do
    printf 'Choose 1-%d [%s]: ' "$#" "$def"
    read -r answer
    answer="${answer:-$def}"
    case "$answer" in
      ''|*[!0-9]*) ;;
      *) if [ "$answer" -ge 1 ] && [ "$answer" -le "$#" ]; then CHOICE="$answer"; return; fi ;;
    esac
    echo "  please enter a number from 1 to $#"
  done
}

ask() { # ask PROMPT -> ANSWER
  [ -n "${interactive}" ] || die "$1: not given as a flag, and there is no terminal to ask on"
  printf '%s' "$1"; read -r ANSWER
}

# The next time each rotation's run is reported at, as escalation-local picks
# it (cmd/escalation-local/main.go triggerTime: 07:00, 11:00, 19:00, 22:00 on
# the next weekday; 10:00, 22:00 on the next weekend day), so the menu can say
# which day's roster is about to be read.
next_at() {
  python3 - "$1" <<'PY'
import sys, datetime as d
ist = d.timezone(d.timedelta(hours=5, minutes=30))
hour, weekend = {"LK_MORNING": (7, False), "LK": (11, False), "LK_EVENING": (19, False),
                 "USA": (22, False), "LK_WEEKEND": (10, True), "USA_WEEKEND": (22, True)}[sys.argv[1]]
now = d.datetime.now(ist)
for day in range(8):
    t = (now + d.timedelta(days=day)).replace(hour=hour, minute=0, second=0, microsecond=0)
    if (t.weekday() >= 5) == weekend and t > now:
        print(t.strftime("%a %d %b %H:%M IST")); break
PY
}

# -- ports: one Redis per ladder ----------------------------------------------
#
# Two engines on one Redis work each other's ladders, so every run -- and every
# job of a suite -- gets its own. A reservation is a directory made with mkdir
# (atomic) holding the owner's pid, taken over only when that owner is gone.

mkdir -p "${ports_dir}"
reserve_port() { # reserve_port FIRST LAST -> prints the port
  local p d owner
  for p in $(seq "$1" "$2"); do
    d="${ports_dir}/${p}"
    if ! mkdir "${d}" 2>/dev/null; then
      owner="$(cat "${d}/pid" 2>/dev/null || true)"
      if [ -n "${owner}" ] && kill -0 "${owner}" 2>/dev/null; then continue; fi
      [ -z "${owner}" ] && [ -z "$(find "${d}" -maxdepth 0 -mmin +2 2>/dev/null)" ] && continue
      rm -rf "${d}"; mkdir "${d}" 2>/dev/null || continue
    fi
    echo $$ > "${d}/pid"
    (exec 3<>"/dev/tcp/127.0.0.1/${p}") 2>/dev/null && continue   # held by something else
    echo "${p}"; return 0
  done
  return 1
}
release_ports() {
  local d
  for d in "${ports_dir}"/*; do
    [ -d "${d}" ] && [ "$(cat "${d}/pid" 2>/dev/null)" = "$$" ] && rm -rf "${d}"
  done
  return 0
}

# -- checks, binaries and the gateway shim ------------------------------------
#
# entity-service only serves the Team Schedule to a caller it can identify from
# x-jwt-assertion -- a header the Choreo gateway adds -- so the repo's gateway
# shim stands in front of it for the length of the run. A suite starts one shim
# and every job shares it (it is stateless); a single run starts its own.

prepare() {
  command -v docker >/dev/null || die "docker is required"
  command -v go >/dev/null || die "go is required"
  curl -sf -m 5 -o /dev/null "${ENTITY_URL}/health" \
    || die "entity-service is not answering at ${ENTITY_URL}. Start it: docker compose up -d postgres mock-oidc entity-service"
  local token
  token="$(curl -sf -m 5 -u "${CLIENT_ID}:${CLIENT_SECRET}" -d grant_type=client_credentials "${TOKEN_URL}" \
    | python3 -c 'import sys,json;print(json.load(sys.stdin)["access_token"])' 2>/dev/null)" \
    || die "mock-oidc at ${TOKEN_URL} did not issue a token. Start it: docker compose up -d mock-oidc"
  if [ -n "${TEAM}" ]; then
    local family
    family="$(curl -sf -m 5 -H "x-jwt-assertion: ${token}" "${ENTITY_URL}/team-schedule/catalogue" \
      | python3 -c 'import sys,json
t=sys.argv[1].lower()
for x in json.load(sys.stdin)["teams"]:
    if x["key"].lower()==t or x["name"].lower()==t: print(x["family"]); break' "${TEAM}" 2>/dev/null)" \
      || die "entity-service refused the rota read; is ${CLIENT_ID} one of its internal clients?"
    [ "${family}" = SRE ] || die "${TEAM} is ${family:-not a team on the rota}, not an SRE team"
  fi

  echo "==> building the engine harness and the gateway shim"
  mkdir -p "${bin_dir}"
  ( cd "${service_dir}" && go build -o "${bin_dir}/escalation-local" ./cmd/escalation-local )
  ( cd "${here}/gateway-shim" && go build -o "${bin_dir}/gateway-shim" . )

  SHIM_PORT="$(reserve_port 17380 17399)" || die "no free gateway shim port in 17380-17399"
  PORT="${SHIM_PORT}" UPSTREAM_URL="${ENTITY_URL}" "${bin_dir}/gateway-shim" >/dev/null 2>&1 &
  SHIM_PID=$!
  curl -s -m 30 --retry 15 --retry-delay 1 --retry-connrefused -o /dev/null "http://localhost:${SHIM_PORT}/health" \
    || die "the gateway shim did not come up on ${SHIM_PORT}"
  export SHIM_PORT
}

# A credential is read with cut, not source, which would strip a JSON value's
# inner quotes; and only what this run needs.
env_value() {
  local f v
  for f in "${here}/.env" "${ENV_FILE}"; do
    [ -f "${f}" ] || continue
    v="$(grep "^$1=" "${f}" | tail -1 | cut -d= -f2- || true)"
    [ -n "${v}" ] && { printf '%s' "${v}"; return 0; }
  done
  return 0
}

# -- one ladder ---------------------------------------------------------------

# run_ladder LOG -- one ladder on its own Redis, its output appended to LOG.
run_ladder() {
  local log="$1" port name status=0
  port="$(reserve_port 17400 17999)" || { echo "no free Redis port in 17400-17999" >> "${log}"; return 1; }
  name="sre-ladder-run-redis-${port}"
  docker rm -f "${name}" >/dev/null 2>&1 || true
  docker run -d --name "${name}" -p "127.0.0.1:${port}:6379" redis:7-alpine >/dev/null
  local i
  for i in $(seq 1 30); do (exec 3<>"/dev/tcp/127.0.0.1/${port}") 2>/dev/null && break; sleep 1; done

  local args=(-ladder sre -priority "${SEV}" -minute "${MINUTE}" -max-calls 1000
              -redis "127.0.0.1:${port}" -incident-id "sre-ladder-$(date +%s)-$$-${RANDOM}")
  args+=(-team "${TEAM}")
  [ -z "${TEAM}" ] && args+=(-contact-type AZURE)   # unassigned: a monitoring alert
  if [ -n "${AT}" ]; then args+=(-at "${AT}"); [ -n "${WEEKEND}" ] && args+=(-weekend); else args+=(-shift "${SHIFT}"); fi
  [ "${ACK}" != never ] && args+=(-cancel-at "LEVEL_$(( ${ACK#L} - 1 ))" -cancel-by "${ACK_BY}")
  [ -n "${ELEVATED}" ] && args+=(-kind elevated)
  [ -n "${L4}" ] && args+=(-sre-l4)
  case "${CHANNEL}" in
    log)  args+=(-channel log) ;;
    chat) args+=(-channel chat -chat-webhook-env SRE_CHAT_WEBHOOK_URL) ;;
    call) args+=(-channel call) ;;
    both) args+=(-channel both -chat-webhook-env SRE_CHAT_WEBHOOK_URL) ;;
  esac
  # Always the real people the Team Schedule resolves, on every channel: these runs test the
  # live rota, so a card or call must name whoever is really on duty. Without -real-names the
  # harness shows each person as "L1 support #1" (cmd/escalation-local maskedResolver).
  args+=(-real-names)
  [ -n "${TO}" ] && args+=(-live -to "${TO}")

  # Every variable the harness would otherwise take from a .env is set, even
  # to empty: it never overrides one that exists, so REDIS_URL="" keeps a
  # .env's managed Redis out of a local run.
  ( cd "${service_dir}" && env \
      GOOGLE_CHAT_SPACES="" SRE_CHAT_WEBHOOK_URL="${SRE_CHAT_WEBHOOK_URL:-}" \
      TWILIO_ACCOUNT_SID="${TWILIO_ACCOUNT_SID:-}" TWILIO_AUTH_TOKEN="${TWILIO_AUTH_TOKEN:-}" \
      TWILIO_FROM_NUMBER="${TWILIO_FROM_NUMBER:-}" \
      REDIS_URL="" REDIS_ADDR="127.0.0.1:${port}" \
      CUSTOMER_ENTITY_BASE_URL="http://localhost:${SHIM_PORT}" \
      OAUTH2_TOKEN_URL="${TOKEN_URL}" OAUTH2_CLIENT_ID="${CLIENT_ID}" OAUTH2_CLIENT_SECRET="${CLIENT_SECRET}" \
      INCIDENT_ESCALATION_RESOLVER=team-schedule \
      "${bin_dir}/escalation-local" "${args[@]}" ) >> "${log}" 2>&1 || status=$?

  docker rm -f "${name}" >/dev/null 2>&1 || true
  rm -rf "${ports_dir:?}/${port}"
  return "${status}"
}

# report LOG -- each rung's due time against when it was placed, and the
# outcome against the SRE rules: an assignment or a move out of NEW stops the
# ladder; a public comment does not.
report() {
  python3 - "$1" "${ACK}" "${ACK_BY}" "${MINUTE}" <<'PY'
import re, sys
log, ack, ack_by, minute = sys.argv[1:5]

def secs(s):
    s = s.strip().lstrip("+")
    total, n = 0.0, re.findall(r"([\d.]+)(h|ms|m|s|µs|us)", s)
    for v, u in n:
        total += float(v) * {"h": 3600, "m": 60, "s": 1, "ms": .001, "µs": 1e-6, "us": 1e-6}[u]
    return total

scale = secs(minute) / 60.0          # real seconds per ladder second
text = open(log, encoding="utf-8", errors="replace").read()
rung = {"LEVEL_0": "L1", "LEVEL_1": "L2", "LEVEL_2": "L3", "LEVEL_3": "L4"}

planned = re.findall(r"^\s+\+(\S+)\s+(LEVEL_\d)\s+#(\d+)\s+(.+?)\s*$", text, re.M)
called = re.findall(r"^\s+\[\s*(\S+)\]\s+(LEVEL_\d)\s+#(\d+)\s+(.+?)\s+called\s+(\S+)\s+\(ladder \+(\S+)\)", text, re.M)
unresolved = re.findall(r"^ {14,}(LEVEL_\d)[ ]+(.+)$", text, re.M)
stopped = "STOPPED — the engine cancelled" in text
not_stopped = "NOT STOPPED" in text
reported = re.search(r"reported at\s+(.+)", text)
shift = re.search(r"shift\s+(\S+)", text)

print("-" * 78)
print("Report")
print("-" * 78)
if reported: print(f"  reported   {reported.group(1).strip()}   shift {shift.group(1) if shift else '?'}")
print(f"  planned    {len(planned)} rung(s); called {len(called)}")
for lvl, why in unresolved:
    print(f"  UNRESOLVED {rung.get(lvl, lvl)}: {why.strip()}")
print()
print(f"  {'rung':<5}{'who':<26}{'due':>9}{'placed':>10}{'drift':>9}")
worst = 0.0
for elapsed, lvl, _, who, _, due in called:
    real_due = secs(due) * scale
    drift = secs(elapsed) - real_due
    worst = max(worst, abs(drift))
    print(f"  {rung.get(lvl, lvl):<5}{who[:25]:<26}{real_due:>8.0f}s{secs(elapsed):>9.0f}s{drift:>+8.1f}s")

levels = sorted({lvl for _, lvl, _, _ in planned})
got = sorted({lvl for _, lvl, _, _, _, _ in called})
if ack == "never" or ack_by == "comment":
    want, want_stop = levels, False
else:
    stop_at = f"LEVEL_{int(ack[1:]) - 1}"
    want, want_stop = [l for l in levels if l <= stop_at], True
ok = got == want and (stopped if want_stop else not stopped)
gesture = {"assign": "an engineer assigned", "status": "the incident left NEW"}.get(ack_by, ack_by)
expect = ("stops once " + ack + " is called (" + gesture + ")") if want_stop else \
         ("climbs every rung" + ("" if ack == "never" else f" -- a {ack_by} is not an acknowledgement"))
print()
print(f"  expected   {expect}: {', '.join(rung.get(l, l) for l in want) or 'no rungs'}")
print(f"  got        {', '.join(rung.get(l, l) for l in got) or 'no rungs'}"
      + ("; stopped" if stopped else "; not stopped" if not_stopped else ""))
print(f"  timing     worst drift {worst:.1f}s")
print(f"  RESULT     {'PASS' if ok else 'FAIL'}")
PY
}

# -- the suite ----------------------------------------------------------------

[ -z "${JOB}" ] && echo "SRE escalation ladder -- real-time test"

if [ -z "${MODE}" ]; then
  if [ -n "${SEVERITY}${SHIFT}${AT}${TEAM_SET}${ACK}${CHANNEL}" ] || [ -z "${interactive}" ]; then
    MODE=single
  else
    choose "What do you want to run?" 1 \
      "One scenario -- you pick severity, rotation, team, acknowledgement and channel" \
      "Every scenario -- each weekday and weekend rotation x L1/L2/L3/never, side by side, logged only"
    [ "${CHOICE}" = 2 ] && MODE=suite || MODE=single
  fi
fi

# -- 1. severity --------------------------------------------------------------

if [ -z "${SEVERITY}" ]; then
  choose "1. Severity of the incident  (the SRE clock is the same for all; S0 on a CRE team also runs this ladder)" 2 \
    "S0 (P0) -- critical" "S1 (P1) -- high" "S2 (P2) -- medium" "S3 (P3) -- low"
  SEVERITY="S$((CHOICE - 1))"
fi
SEVERITY="$(upper "${SEVERITY}")"
case "${SEVERITY}" in
  S0|P0) SEV=P0 ;; S1|P1) SEV=P1 ;; S2|P2) SEV=P2 ;; S3|P3) SEV=P3 ;;
  *) die "--severity must be S0..S3 (or P0..P3)" ;;
esac
SEV_LABEL="S${SEV#P}"

# -- 3 first for the suite: the team ------------------------------------------

pick_team() {
  if [ -z "${TEAM_SET}" ]; then
    choose "3. SRE team the incident is assigned to" 1 \
      "apollo     SRE ABT" \
      "artemis    SRE ABT" \
      "unassigned a monitoring alert (AZURE) with no assignment group yet"
    case "${CHOICE}" in 1) TEAM=apollo ;; 2) TEAM=artemis ;; 3) TEAM="" ;; esac
    TEAM_SET=1
  fi
  TEAM="$(lower "${TEAM}")"
  case "${TEAM}" in none|unassigned) TEAM="" ;; esac
}

if [ "${MODE}" = suite ]; then
  pick_team
  ack_list="L1 L2 L3 never"; [ -n "${L4}" ] && ack_list="L1 L2 L3 L4 never"
  scenarios=()
  for s in ${ALL_SHIFTS}; do for a in ${ack_list}; do
    id="${s}_ack-${a}"
    if [ -n "${ONLY}" ] && ! printf '%s' "${id}" | grep -Eq "${ONLY}"; then continue; fi
    scenarios+=("${id}")
  done; done
  [ "${#scenarios[@]}" -gt 0 ] || die "--only ${ONLY} matches no scenario"
  ladder_min=11; [ -n "${L4}" ] && ladder_min=16
  cat <<CARD

------------------------------------------------------------------------------
 suite         ${#scenarios[@]} ladders: ${ALL_SHIFTS// /, } x ${ack_list// /, }
 severity      ${SEV_LABEL}      team ${TEAM:-unassigned (monitoring)}
 reaches       nobody -- logged only
 runs          ${JOBS} at a time, 1 ladder minute = ${MINUTE}
               (~${ladder_min} ladder minutes each; about $(( (${#scenarios[@]} + JOBS - 1) / JOBS * ladder_min )) min in all at real time)
------------------------------------------------------------------------------
CARD
  if [ -z "${YES}" ]; then
    ask "Start the suite? [Y/n] "
    case "${ANSWER}" in n|N|no|NO) echo "stopped."; exit 1 ;; esac
  fi
  trap 'kill "${SHIM_PID:-}" 2>/dev/null || true; release_ports' EXIT
  prepare
  stamp="$(date +%Y%m%d-%H%M%S)"
  suite_dir="${run_dir}/suite-${stamp}_${SEV_LABEL}_${TEAM:-unassigned}"
  mkdir -p "${suite_dir}"
  echo "==> logging every ladder under ${suite_dir#"${repo_root}/"}"
  for id in "${scenarios[@]}"; do
    while [ "$(jobs -rp | wc -l | tr -d ' ')" -ge "${JOBS}" ]; do sleep 1; done
    s="${id%%_ack-*}"; a="${id##*_ack-}"
    echo "    start ${id}"
    SHIM_PORT="${SHIM_PORT}" "$0" --job "${suite_dir}/${id}.log" -s "${SEV}" --shift "${s}" \
      -a "${TEAM:-none}" --ack "${a}" --ack-by "${ACK_BY}" -c log -m "${MINUTE}" \
      ${L4:+--l4} ${ELEVATED:+--elevated} >/dev/null 2>&1 &
  done
  wait
  echo
  printf '%-30s %-6s %s\n' "scenario" "result" "called"
  pass=0 fail=0
  for id in "${scenarios[@]}"; do
    f="${suite_dir}/${id}.log"
    r="$(grep -o 'RESULT     [A-Z]*' "${f}" 2>/dev/null | awk '{print $2}')"; r="${r:-ERROR}"
    g="$(grep -o '^  got        .*' "${f}" 2>/dev/null | sed 's/^  got        //')"
    printf '%-30s %-6s %s\n' "${id}" "${r}" "${g}"
    [ "${r}" = PASS ] && pass=$((pass + 1)) || fail=$((fail + 1))
  done
  echo
  echo "${pass} passed, ${fail} failed. Logs (real staff names -- keep them out of tickets, PRs and chat):"
  echo "  ${suite_dir}"
  [ "${fail}" -eq 0 ]
  exit
fi

# -- 2. when it was reported --------------------------------------------------

if [ -z "${SHIFT}" ] && [ -z "${AT}" ]; then
  choose "2. When the incident was reported  (decides whose rota each rung reads)" 2 \
    "LK_MORNING   weekday        06:00-09:00 IST  Sri Lanka  -> $(next_at LK_MORNING)" \
    "LK           weekday        09:00-18:00 IST  Sri Lanka  -> $(next_at LK)" \
    "LK_EVENING   weekday        18:00-21:00 IST  Sri Lanka  -> $(next_at LK_EVENING)" \
    "USA          weekday night  21:00-06:00 IST  Americas   -> $(next_at USA)" \
    "LK_WEEKEND   Sat/Sun        06:00-21:00 IST  Sri Lanka  -> $(next_at LK_WEEKEND)" \
    "USA_WEEKEND  Sat/Sun night  21:00-06:00 IST  Americas   -> $(next_at USA_WEEKEND)" \
    "A time of my own (IST)"
  case "${CHOICE}" in
    1) SHIFT=LK_MORNING ;; 2) SHIFT=LK ;; 3) SHIFT=LK_EVENING ;; 4) SHIFT=USA ;;
    5) SHIFT=LK_WEEKEND ;; 6) SHIFT=USA_WEEKEND ;;
    7) ask "   time in IST -- HH:MM (next weekday) or YYYY-MM-DDTHH:MM: "; AT="${ANSWER}"
       case "${AT}" in
         [0-2][0-9]:[0-5][0-9])
           ask "   on the next weekend day instead of a weekday? [y/N] "
           case "${ANSWER}" in y|Y|yes|YES) WEEKEND=1 ;; esac ;;
       esac ;;
  esac
fi
if [ -n "${SHIFT}" ]; then
  SHIFT="$(upper "${SHIFT}")"
  case " ${ALL_SHIFTS} " in *" ${SHIFT} "*) ;; *) die "--shift must be one of ${ALL_SHIFTS}" ;; esac
fi

# -- 3. team ------------------------------------------------------------------

pick_team

# -- 4. acknowledgement -------------------------------------------------------

if [ -z "${ACK}" ]; then
  set -- "L1 support   -- called at +0 min" "L2 support   -- called at +5 min" "L3 support   -- called at +10 min"
  [ -n "${L4}" ] && set -- "$@" "L4 support   -- called at +15 min (unconfirmed rung)"
  set -- "$@" "never        nobody takes it; it climbs every rung"
  choose "4. Where an engineer is assigned  (the moment that rung is called)" 1 "$@"
  if [ "${CHOICE}" -eq "$#" ]; then ACK=never; else ACK="L${CHOICE}"; fi
fi
ACK="$(upper "${ACK}")"
case "${ACK}" in
  L[1-3]) ;;
  L4) [ -n "${L4}" ] || die "--ack L4 needs --l4 (the L4 rung is off by default)" ;;
  NEVER|NONE) ACK=never ;;
  *) die "--ack must be L1..L3 (L4 with --l4) or never" ;;
esac
case "${ACK_BY}" in assign|status|comment) ;; *) die "--ack-by must be assign, status or comment" ;; esac

# -- 5. channel ---------------------------------------------------------------

if [ -z "${CHANNEL}" ]; then
  choose "5. How each rung reaches people  (every run is logged, whichever you pick)" 1 \
    "log only     nobody is contacted -- each rung is written to the log" \
    "chat         a card per rung in the SRE Google Chat space (SRE_CHAT_WEBHOOK_URL)" \
    "call         a call per rung -- a local stub, or your own phone" \
    "both         chat and call"
  case "${CHOICE}" in 1) CHANNEL=log ;; 2) CHANNEL=chat ;; 3) CHANNEL=call ;; 4) CHANNEL=both ;; esac
fi
CHANNEL="$(lower "${CHANNEL}")"
case "${CHANNEL}" in log|chat|call|both) ;; *) die "--channel must be log, chat, call or both" ;; esac

if { [ "${CHANNEL}" = call ] || [ "${CHANNEL}" = both ]; } && [ -z "${TO}" ] && [ -z "${STUB}" ] && [ -n "${interactive}" ]; then
  choose "   Calls go to" 1 \
    "a local stub -- nothing is dialled, no cost" \
    "my own phone through Twilio -- EVERY rung rings one number, and it costs money"
  if [ "${CHOICE}" = 2 ]; then
    while :; do
      ask "   your number, E.164 (e.g. +94771234567): "
      case "${ANSWER}" in +[1-9][0-9][0-9][0-9][0-9][0-9][0-9]*) TO="${ANSWER}"; break ;; esac
      echo "   that is not an E.164 number"
    done
  fi
fi
[ -n "${TO}" ] && case "${CHANNEL}" in call|both) ;; *) die "--to only applies to --channel call or both" ;; esac

if [ "${CHANNEL}" = chat ] || [ "${CHANNEL}" = both ]; then
  SRE_CHAT_WEBHOOK_URL="${SRE_CHAT_WEBHOOK_URL:-$(env_value SRE_CHAT_WEBHOOK_URL)}"
  case "${SRE_CHAT_WEBHOOK_URL}" in
    https://chat.googleapis.com/*) ;;
    "") die "chat needs SRE_CHAT_WEBHOOK_URL: put it in scripts/csm-compose/.env (git-ignored), or export it" ;;
    *) die "SRE_CHAT_WEBHOOK_URL is not a Google Chat webhook (https://chat.googleapis.com/...)" ;;
  esac
  export SRE_CHAT_WEBHOOK_URL
fi
if [ -n "${TO}" ]; then
  for k in TWILIO_ACCOUNT_SID TWILIO_AUTH_TOKEN TWILIO_FROM_NUMBER; do
    [ -n "${!k:-}" ] || export "${k}=$(env_value "${k}")"
    [ -n "${!k}" ] || die "real calls need ${k}: export it, or point ESCALATION_ENV_FILE at a .env that has it"
  done
fi

# -- what is about to happen --------------------------------------------------

if [ "${ACK}" = never ]; then
  ends="nobody takes it -- climbs every rung"; minutes=11; [ -n "${L4}" ] && minutes=16
elif [ "${ACK_BY}" != comment ]; then
  ends="$( [ "${ACK_BY}" = assign ] && echo "an engineer is assigned" || echo "the incident leaves NEW" ) once ${ACK} is called -- it should stop there"
  minutes=$(( (${ACK#L} - 1) * 5 + 1 ))
else
  ends="a public comment once ${ACK} is called -- which must NOT stop it"; minutes=11; [ -n "${L4}" ] && minutes=16
fi
when="at ${AT} IST${WEEKEND:+ (next weekend day)}"
[ -n "${SHIFT}" ] && when="${SHIFT} -- reported $(next_at "${SHIFT}")"
reach="nobody -- logged only"
case "${CHANNEL}" in
  chat) reach="the SRE Google Chat space, real names from the Team Schedule" ;;
  call) reach="${TO:+REAL calls to ${TO}}"; reach="${reach:-a local call stub (nothing dialled)}" ;;
  both) reach="the SRE Google Chat space (real names) + ${TO:+REAL calls to ${TO}}"; [ -z "${TO}" ] && reach="${reach}a local call stub" ;;
esac

rerun="scripts/csm-compose/sre-escalation-ladder.sh -s ${SEV_LABEL}"
[ -n "${SHIFT}" ] && rerun="${rerun} --shift ${SHIFT}"
[ -n "${AT}" ] && rerun="${rerun} --at ${AT}${WEEKEND:+ --weekend}"
rerun="${rerun} -a ${TEAM:-none} --ack ${ACK}"
[ "${ACK_BY}" != assign ] && rerun="${rerun} --ack-by ${ACK_BY}"
rerun="${rerun} -c ${CHANNEL}${TO:+ --to ${TO}}${L4:+ --l4}${ELEVATED:+ --elevated}"
[ "${MINUTE}" != 1m ] && rerun="${rerun} -m ${MINUTE}"

if [ -z "${JOB}" ]; then
  cat <<CARD

------------------------------------------------------------------------------
 severity      ${SEV_LABEL}${ELEVATED:+ -- by a priority elevation}
 reported      ${when}
 team          ${TEAM:-unassigned -- a monitoring (AZURE) alert}
 ends          ${ends}
 reaches       ${reach}
 takes         about ${minutes} ladder min (1 ladder minute = ${MINUTE})
 same again    ${rerun}
------------------------------------------------------------------------------
CARD
fi

if [ -z "${YES}" ]; then
  if [ "${CHANNEL}" != log ] && { [ "${CHANNEL}" != call ] || [ -n "${TO}" ]; }; then
    ask "This contacts real people or costs money. Type yes to start: "
    [ "${ANSWER}" = yes ] || { echo "stopped."; exit 1; }
  else
    ask "Start? [Y/n] "
    case "${ANSWER}" in n|N|no|NO) echo "stopped."; exit 1 ;; esac
  fi
fi

# -- run ----------------------------------------------------------------------

if [ -n "${JOB}" ]; then
  log="${JOB}"   # a suite job: the suite owns the shim and the binaries
  : "${SHIM_PORT:?a suite job needs SHIM_PORT from its suite}"
  trap release_ports EXIT
else
  trap 'kill "${SHIM_PID:-}" 2>/dev/null || true; release_ports' EXIT
  prepare
  stamp="$(date +%Y%m%d-%H%M%S)"
  tag="${SHIFT:-at-$(printf '%s' "${AT}" | tr -d ':-' | tr 'T' '-')${WEEKEND:+-weekend}}"
  mkdir -p "${run_dir}"
  log="${run_dir}/${stamp}_${SEV_LABEL}_${tag}_${TEAM:-unassigned}_ack-${ACK}_${CHANNEL}.log"
fi

{
  echo "### SRE escalation ladder run -- $(date '+%Y-%m-%d %H:%M:%S %Z')"
  echo "### params severity=${SEV_LABEL} team=${TEAM:-unassigned} ack=${ACK} ack-by=${ACK_BY} channel=${CHANNEL} when=${SHIFT:-${AT}${WEEKEND:+ weekend}} minute=${MINUTE}${L4:+ l4}"
  echo "### same again: ${rerun}"
  echo "### commit $(git -C "${repo_root}" log -1 --format='%h %s')"
  echo "### rungs: L1 +0, L2 +5, L3 +10${L4:+, L4 +15} min"
} > "${log}"

status=0
if [ -n "${JOB}" ]; then
  run_ladder "${log}" || status=$?
  report "${log}" >> "${log}"
  exit "${status}"
fi

echo "logging to ${log#"${repo_root}/"}"
echo
# Follow the log as it grows so this terminal shows the ladder live. Ctrl-C
# stops the harness (and its Redis goes with the EXIT trap); the report still runs.
tail -n +1 -f "${log}" & tail_pid=$!
trap ':' INT
run_ladder "${log}" || status=$?
trap - INT
sleep 1; kill "${tail_pid}" 2>/dev/null || true
echo
report "${log}" | tee -a "${log}"
echo
echo "Full log (real staff names -- keep it out of tickets, PRs and chat):"
echo "  ${log}"
exit "${status}"

exit
}
