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
# The one place to start a CRE escalation ladder test from.
#
# Run it with no arguments and it asks, one numbered list at a time:
#   1. which severity          S0..S3 (P0..P3 are the same levels, old names)
#   2. when it was reported    which rotation -- or a time of your own
#   3. which ABT               one of the ABTs, or not yet assigned
#   4. where it is acknowledged LEVEL_0..LEVEL_4, or never
#   5. how rungs reach people  log only, Google Chat, calls, or both
# then shows what it is about to do and runs it on the real clock -- one ladder
# minute is one minute. Or pick "every scenario" for the full suite.
#
# Every run is logged, whatever the channel: to
#   scripts/csm-compose/.run/cre-escalation-ladder/<time>_<sev>_<shift>_<team>_ack-<level>_<channel>.log
# headed by the exact command that repeats it and ended by a report of the
# rule, the outcome and every call's due time against when it was placed.
# The folder is git-ignored: the logs hold real staff names from the local
# roster, so keep them out of tickets, PRs and chat.
#
# Every question has a flag; anything given as a flag is not asked:
#   cre-escalation-ladder.sh -s S0 --shift LK_MORNING -a vega --ack L1 -c log
#   cre-escalation-ladder.sh -s S1 --at 19:30 -a none --ack never -c chat
#   cre-escalation-ladder.sh -s S0 -a vega --ack L2 -c call --to +94770000000
#   cre-escalation-ladder.sh --suite -s S0 -j 8
#
#   -s, --severity S0..S3|P0..P3
#       --shift LK_MORNING|LK|LK_EVENING|LK_WEEKEND|USA|USA_WEEKEND
#   -t, --at HH:MM|YYYY-MM-DDTHH:MM   (IST)   [--weekend with HH:MM]
#   -a, --team TEAM|none
#       --ack L0..L4|never           [--ack-by both|status|comment]
#   -c, --channel log|chat|call|both [--to E.164 for real calls; else a stub]
#       --elevated                   start it as a priority elevation
#       --suite [-j N] [--only REGEX]  every scenario of one severity
#   -y, --yes                        do not ask before starting
#
# Needs the compose stack's postgres, mock-oidc and entity-service up, docker,
# go and python3.

set -euo pipefail

# Everything below is one { ... } group, so bash reads the whole file before
# running any of it. A script is otherwise read as it runs, and editing it
# under a run in progress -- a real-time ladder lasts up to two hours --
# makes that run resume at the wrong line of the new file, or silently stop.
{

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${here}/../.." && pwd)"
config_file="${here}/paging-alert.yaml"
log_dir="${here}/.run/cre-escalation-ladder"

die() { echo "error: $*" >&2; exit 1; }
upper() { printf '%s' "$1" | tr '[:lower:]' '[:upper:]'; }
lower() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }

SEVERITY="" SHIFT="" AT="" WEEKEND="" TEAM="" TEAM_SET="" ACK="" ACK_BY=both
CHANNEL="" TO="" STUB="" ELEVATED="" YES="" MODE="" JOBS="" ONLY=""

while [ $# -gt 0 ]; do
  case "$1" in
    -s|--severity) SEVERITY="$2"; shift 2 ;;
    --shift)       SHIFT="$2"; shift 2 ;;
    -t|--at)       AT="$2"; shift 2 ;;
    --weekend)     WEEKEND=1; shift ;;
    -a|--team|--abt) TEAM="$2"; TEAM_SET=1; shift 2 ;;
    --ack)         ACK="$2"; shift 2 ;;
    --ack-by)      ACK_BY="$2"; shift 2 ;;
    -c|--channel)  CHANNEL="$2"; shift 2 ;;
    --to)          TO="$2"; shift 2 ;;
    --stub)        STUB=1; shift ;;
    --elevated)    ELEVATED=1; shift ;;
    --suite)       MODE=suite; shift ;;
    -j|--jobs)     JOBS="$2"; shift 2 ;;
    --only)        ONLY="$2"; shift 2 ;;
    -y|--yes)      YES=1; shift ;;
    -h|--help)     sed -n '/^# The one place/,/^# go and python3/p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown option: $1 (see --help)" ;;
  esac
done

interactive=""
[ -t 0 ] && interactive=1

# choose TITLE DEFAULT OPTION... -- a numbered list; Enter takes DEFAULT (a
# number). Sets CHOICE to the chosen number. A bad answer asks again.
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

echo "CRE escalation ladder -- real-time test"

# -- 0. one scenario, or all of them ------------------------------------------

if [ -z "${MODE}" ]; then
  if [ -n "${SEVERITY}${SHIFT}${AT}${TEAM_SET}${ACK}${CHANNEL}" ] || [ -z "${interactive}" ]; then
    MODE=single
  else
    choose "What do you want to run?" 1 \
      "One scenario -- you pick severity, rotation, team, acknowledgement and channel" \
      "Every scenario for a severity (the suite: ~63 ladders side by side, logged only)"
    [ "${CHOICE}" = 2 ] && MODE=suite || MODE=single
  fi
fi

# -- 1. severity ----------------------------------------------------------------

if [ -z "${SEVERITY}" ]; then
  choose "1. Severity of the incident  (P0..P3 are the old names of S0..S3)" 1 \
    "S0 (P0) -- critical   ladder ends at ~16 min" \
    "S1 (P1) -- high       ladder ends at ~48 min" \
    "S2 (P2) -- medium     ladder ends at ~75 min" \
    "S3 (P3) -- low        ladder ends at ~115 min"
  SEVERITY="S$((CHOICE - 1))"
fi
SEVERITY="$(upper "${SEVERITY}")"
case "${SEVERITY}" in
  S0|P0) SEV=S0; OPEN="0 1 4 8 12"; END=16 ;;
  S1|P1) SEV=S1; OPEN="6 9 18 28 38"; END=48 ;;
  S2|P2) SEV=S2; OPEN="9 15 30 45 60"; END=75 ;;
  S3|P3) SEV=S3; OPEN="12 20 40 65 90"; END=115 ;;
  *) die "--severity must be S0..S3 (or P0..P3)" ;;
esac

if [ "${MODE}" = suite ]; then
  set -- -s "${SEV}"
  [ -n "${JOBS}" ] && set -- "$@" -j "${JOBS}"
  [ -n "${ONLY}" ] && set -- "$@" --only "${ONLY}"
  "${here}/cre-severity-suite.sh" "$@" --dry-run | sed -n '1,5p'
  if [ -z "${YES}" ]; then
    ask "Start the suite? [Y/n] "
    case "${ANSWER}" in n|N|no|NO) echo "stopped."; exit 1 ;; esac
  fi
  exec "${here}/cre-severity-suite.sh" "$@"
fi

# -- 2. when it was reported --------------------------------------------------

# The next time each rotation's run would be reported at, as escalation-local
# picks it (cmd/escalation-local/main.go: 07:00, 11:00, 19:00, 22:00 on the next
# weekday; 10:00, 22:00 on the next weekend day), so the menu can say which
# day's roster is about to be read.
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

if [ -z "${SHIFT}" ] && [ -z "${AT}" ]; then
  choose "2. When the incident was reported  (the engine derives the shift from it)" 2 \
    "LK_MORNING   weekday 06:00-09:00 IST   -> $(next_at LK_MORNING)" \
    "LK           weekday 09:00-18:00 IST   -> $(next_at LK)" \
    "LK_EVENING   weekday 18:00-21:00 IST   -> $(next_at LK_EVENING)" \
    "USA          weekday night 21:00-06:00 IST (Americas)   -> $(next_at USA)" \
    "LK_WEEKEND   Sat/Sun 06:00-21:00 IST   -> $(next_at LK_WEEKEND)" \
    "USA_WEEKEND  Sat/Sun night 21:00-06:00 IST (Americas)   -> $(next_at USA_WEEKEND)" \
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
[ -n "${SHIFT}" ] && SHIFT="$(upper "${SHIFT}")"

# -- 3. team ------------------------------------------------------------------

cre_abts="$(awk '
  /^cre:/ {in_cre=1; next}
  /^[a-z]+:/ {in_cre=0}
  in_cre && /^[[:space:]]*abts:/ { sub(/.*\[/, ""); sub(/\].*/, ""); gsub(/[[:space:]]/, ""); print; exit }
' "${config_file}")"
[ -n "${cre_abts}" ] || die "could not read the CRE abts list from ${config_file}"

if [ -z "${TEAM_SET}" ]; then
  set --
  for t in ${cre_abts//,/ }; do set -- "$@" "${t}   ABT"; done
  n_abts=$#
  set -- "$@" "unassigned   not yet assigned to an ABT"
  choose "3. ABT the incident is assigned to  (Americas covers IST nights for every ABT; it is never the assignee)" "$((n_abts + 1))" "$@"
  pick="$(eval "printf '%s' \"\${${CHOICE}}\"")"
  TEAM="${pick%% *}"
fi
TEAM="$(lower "${TEAM}")"
case "${TEAM}" in none|unassigned|"") TEAM="" ;; esac

# -- 4. acknowledgement -------------------------------------------------------

open_at() { echo "${OPEN}" | awk -v n="$1" '{print $(n+1)}'; }
if [ -z "${ACK}" ]; then
  choose "4. Where the incident is acknowledged  (move out of NEW + a public comment, the moment that rung is called)" 2 \
    "LEVEL_0  first responders     -- opens at +$(open_at 0) min" \
    "LEVEL_1  team lead            -- opens at +$(open_at 1) min" \
    "LEVEL_2  team leads           -- opens at +$(open_at 2) min" \
    "LEVEL_3  CRE head             -- opens at +$(open_at 3) min" \
    "LEVEL_4  CS head              -- opens at +$(open_at 4) min" \
    "never    nobody answers; it climbs every rung and ends at +${END} min"
  [ "${CHOICE}" = 6 ] && ACK=never || ACK="L$((CHOICE - 1))"
fi
ACK="$(upper "${ACK}")"
case "${ACK}" in
  L[0-4]|LEVEL_[0-4]) ACK="L${ACK##*[!0-9]}" ;;
  NEVER|NONE) ACK=never ;;
  *) die "--ack must be L0..L4 or never" ;;
esac

# -- 5. channel ---------------------------------------------------------------

if [ -z "${CHANNEL}" ]; then
  choose "5. How each rung reaches people  (every run is logged, whichever you pick)" 1 \
    "log only     nobody is contacted -- each rung is written to the log" \
    "chat         a card per rung in the real Google Chat room (CRE_CHAT_WEBHOOK_URL in .env)" \
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

# -- what is about to happen ---------------------------------------------------

if [ "${ACK}" = never ]; then
  minutes="${END}"; ends="not acknowledged -- climbs every rung"
else
  minutes=$(( $(open_at "${ACK#L}") + 1 )); ends="acknowledged once LEVEL_${ACK#L} is called"
fi
when="${SHIFT:-at ${AT} IST${WEEKEND:+ (weekend)}}"
[ -n "${SHIFT}" ] && when="${SHIFT} -- reported $(next_at "${SHIFT}")"
reach="nobody -- logged only"
case "${CHANNEL}" in
  chat) reach="the real Google Chat room" ;;
  call) reach="${TO:+REAL calls to ${TO}}"; reach="${reach:-a local call stub (nothing dialled)}" ;;
  both) reach="the real Google Chat room + ${TO:+REAL calls to ${TO}}"; [ -z "${TO}" ] && reach="${reach}a local call stub" ;;
esac

# The command that repeats this run, with no questions.
rerun="scripts/csm-compose/cre-escalation-ladder.sh -s ${SEV}"
[ -n "${SHIFT}" ] && rerun="${rerun} --shift ${SHIFT}"
[ -n "${AT}" ] && rerun="${rerun} --at ${AT}${WEEKEND:+ --weekend}"
rerun="${rerun} -a ${TEAM:-none} --ack ${ACK}"
[ "${ACK_BY}" != both ] && rerun="${rerun} --ack-by ${ACK_BY}"
rerun="${rerun} -c ${CHANNEL}${TO:+ --to ${TO}}${ELEVATED:+ --elevated}"

cat <<CARD

------------------------------------------------------------------------------
 severity      ${SEV}${ELEVATED:+ -- by a priority elevation}
 reported      ${when}
 team          ${TEAM:-unassigned}
 ends          ${ends}
 reaches       ${reach}
 takes         about ${minutes} min, real time
 same again    ${rerun}
------------------------------------------------------------------------------
CARD

if [ -z "${YES}" ]; then
  if [ "${CHANNEL}" != log ] && { [ "${CHANNEL}" != call ] || [ -n "${TO}" ]; }; then
    ask "This contacts real people or costs money. Type yes to start: "
    [ "${ANSWER}" = yes ] || { echo "stopped."; exit 1; }
  else
    ask "Start? [Y/n] "
    case "${ANSWER}" in n|N|no|NO) echo "stopped."; exit 1 ;; esac
  fi
fi

# -- run -----------------------------------------------------------------------

# Its own Redis port, reserved the same way the suite reserves its ports, so a
# second terminal -- a single scenario or a whole suite -- never shares a ladder
# store with this one. See cre-ladder-ports.sh.
. "${here}/cre-ladder-ports.sh"
port="$(reserve_port 16400 16999)" || die "no free Redis port in 16400-16999"
trap release_ports EXIT

stamp="$(date +%Y%m%d-%H%M%S)"
tag="${SHIFT:-at-$(printf '%s' "${AT}" | tr -d ':-' | tr 'T' '-')${WEEKEND:+-weekend}}"
log="${log_dir}/${stamp}_${SEV}_${tag}_${TEAM:-unassigned}_ack-${ACK}_${CHANNEL}.log"

args=(-s "${SEV}" -c "${CHANNEL}" -y)
[ -n "${SHIFT}" ] && args+=(--shift "${SHIFT}")
[ -n "${AT}" ] && args+=(--at "${AT}")
[ -n "${WEEKEND}" ] && args+=(--weekend)
[ -n "${TEAM}" ] && args+=(-a "${TEAM}")
[ "${ACK}" != never ] && args+=(--ack-at "LEVEL_${ACK#L}" --ack-by "${ACK_BY}")
[ -n "${TO}" ] && args+=(--live --to "${TO}")
[ -n "${ELEVATED}" ] && args+=(--elevated)

{
  echo "### CRE escalation ladder run -- $(date '+%Y-%m-%d %H:%M:%S %Z')"
  echo "### params severity=${SEV} team=${TEAM:-unassigned} ack=${ACK} ack-by=${ACK_BY} channel=${CHANNEL} when=${SHIFT:-${AT}}"
  echo "### same again: ${rerun}"
  echo "### commit $(git -C "${repo_root}" log -1 --format='%h %s')"
  echo "### rungs open at ${OPEN} min; a full ladder ends at ${END} min"
} > "${log}"

echo "logging to ${log#"${repo_root}/"}"
echo
# Ctrl-C reaches the whole process group. The runner turns it into a clean
# stop (its ladder and Redis go with it); this script and tee ignore it, so
# the rest of the output still reaches the log and the report still runs.
trap ':' INT
set +e
REDIS_PORT="${port}" "${here}/run-cre-ladder.sh" "${args[@]}" 2>&1 | tee -i -a "${log}"
status="${PIPESTATUS[0]}"
set -e
trap - INT

echo
python3 "${here}/cre-ladder-report.py" single "${log}" | tee -a "${log}"
echo
echo "Full log (share it for review; it holds real staff names -- keep it out of tickets, PRs and chat):"
echo "  ${log}"
exit "${status}"

exit
}
