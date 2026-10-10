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
# Run the escalation ladder against the running local stack's Team Schedule and
# show who each rung reaches, for one priority or all of them.
#
# Made for the SRE ladder -- an incident assigned to an SRE team: L1 support at
# once, L2 five minutes later, L3 five minutes after that (L4 with -4), one
# person per rung, the same clock for every priority. A CRE team runs the CRE
# ladder instead, whose clock depends on the priority, which is what makes
# -p all worth running on one. A P0 on a CRE team climbs BOTH ladders; -l both
# runs the two one after the other so each can be read on its own.
#
# It drives the REAL engine (cmd/escalation-local) on a compressed clock. By
# default nothing leaves this machine: each chat card is printed here instead
# of being posted, and calls go to a local stub. -o chat posts the cards to your
# Google Chat space instead.
#
# WHY NOT THE SAME WAY AS trigger-escalation.sh
#
# That one publishes onto the compose topic and watches the compose container
# in real time, and that container resolves rungs from a stand-in JSON roster.
# This reads the Team Schedule, which entity-service only serves to a caller it
# can identify from x-jwt-assertion -- a header only the Choreo gateway adds --
# so this runs the repo's gateway shim in front of entity-service for the
# length of the run.
#
# Usage:
#   -t  team       the incident's assignment group; none for an incident
#                  assigned to no team                                  (default apollo)
#   -x  contact    how it was raised: AZURE, SITE_247, SENTINEL are
#                  monitoring sources, which climb the SRE ladder
#                  whatever the team; EMAIL, PHONE ... are people       (default none)
#   -p  priority   P0..P4, CRITICAL/HIGH/MODERATE/LOW, a comma list of
#                  those, or "all" for P0..P4                           (default HIGH)
#   -s  shift      when it was reported: LK, LK_MORNING, LK_EVENING,
#                  LK_WEEKEND, USA or USA_WEEKEND; decides who is on
#                  duty at each rung                                    (default LK)
#   -o  output     log: each rung printed here, nothing posted, real names
#                  chat: cards posted to your space, names masked       (default log)
#   -l  ladder     auto: the team's own ladder; cre, sre, or both --
#                  sre on a CRE team only runs at a P0                   (default auto)
#   -c  seconds    acknowledge this far into each run; 0 lets it climb
#                  every rung                                           (default 0)
#   -a  gesture    how -c acknowledges: assign (an engineer assigned),
#                  status (a move out of NEW) or comment (a public
#                  comment -- which must NOT stop an SRE ladder)        (default assign)
#   -k  kind       what starts the ladder: new (incident.created) or
#                  elevated (incident.priority_elevated to -p)          (default new)
#   -i             interactive: acknowledge whenever you choose while the
#                  ladder runs -- type a (assign), s (leave NEW) or c
#                  (public comment) and press Enter
#   -m  duration   how long one ladder minute lasts; 1m is real time    (default 1s)
#   -4             include the SRE ladder's unconfirmed L4 support rung
#
#   trigger-sre-escalation.sh                          # apollo, HIGH, logged
#   trigger-sre-escalation.sh -p all -m 200ms          # run them all, fast
#   trigger-sre-escalation.sh -t castor -p P0 -l both  # a CRE P0: both ladders
#   trigger-sre-escalation.sh -c 3 -o chat             # to the space, stopped after L1
#   trigger-sre-escalation.sh -m 1m -i                 # real time; acknowledge by typing
set -euo pipefail

TEAM=apollo
PRIORITIES=HIGH
SHIFT=LK
OUTPUT=log
LADDER=auto
CANCEL_AFTER=0
CANCEL_BY=assign
KIND=new
INTERACTIVE=false
CONTACT=
MINUTE=1s
L4=false

usage() { sed -n '/^# Usage:/,/^set -euo/p' "$0" | sed 's/^# \{0,1\}//;$d'; exit "${1:-0}"; }

while getopts ":t:p:s:o:l:c:a:k:x:m:4ih" opt; do
  case $opt in
    t) TEAM=$OPTARG ;;
    p) PRIORITIES=$OPTARG ;;
    s) SHIFT=$OPTARG ;;
    o) OUTPUT=$OPTARG ;;
    l) LADDER=$OPTARG ;;
    c) CANCEL_AFTER=$OPTARG ;;
    a) CANCEL_BY=$OPTARG ;;
    k) KIND=$OPTARG ;;
    i) INTERACTIVE=true ;;
    x) CONTACT=$OPTARG ;;
    m) MINUTE=$OPTARG ;;
    4) L4=true ;;
    h) usage 0 ;;
    *) usage 1 ;;
  esac
done
[[ "$PRIORITIES" == all ]] && PRIORITIES=P0,P1,P2,P3,P4
case "$OUTPUT" in log|chat) ;; *) echo "-o is log or chat" >&2; exit 1 ;; esac
case "$LADDER" in auto|cre|sre|both) ;; *) echo "-l is auto, cre, sre or both" >&2; exit 1 ;; esac
case "$CANCEL_BY" in assign|status|comment) ;; *) echo "-a is assign, status or comment" >&2; exit 1 ;; esac
case "$KIND" in new|elevated) ;; *) echo "-k is new or elevated" >&2; exit 1 ;; esac

cd "$(dirname "$0")/../.."
ROOT="$(pwd)"

ENTITY_URL="${ENTITY_URL:-http://localhost:8081}"
TOKEN_URL="${TOKEN_URL:-http://localhost:9100/oauth2/token}"
CLIENT_ID="${CLIENT_ID:-csm-notification-service-dev-client}"
CLIENT_SECRET="${CLIENT_SECRET:-dev-secret}"
REDIS_ADDR="${REDIS_ADDR:-localhost:6379}"
SHIM_PORT="${SHIM_PORT:-8899}"

# Only -o chat needs a space. The webhook is a credential, so it comes from the
# environment or the service's own git-ignored .env, never from here, and with
# `cut` rather than `source`, which strips the JSON's inner quotes.
if [[ "$OUTPUT" == chat ]]; then
  ENV_FILE="${ESCALATION_ENV_FILE:-integrations/csm-notification-service/.env}"
  if [[ -z "${GOOGLE_CHAT_SPACES:-}" && -f "$ENV_FILE" ]]; then
    GOOGLE_CHAT_SPACES="$(grep '^GOOGLE_CHAT_SPACES=' "$ENV_FILE" | cut -d= -f2- || true)"
  fi
  if [[ -z "${GOOGLE_CHAT_SPACES:-}" ]]; then
    echo "-o chat needs GOOGLE_CHAT_SPACES: export it, or point ESCALATION_ENV_FILE at the .env" >&2
    echo "that has it (a git worktree has no .env of its own). -o log needs neither." >&2
    exit 1
  fi
fi

echo "==> checking the local stack"
fail() { echo "    $1" >&2; exit 1; }
curl -sf -m 5 -o /dev/null "$ENTITY_URL/health" \
  || fail "entity-service is not answering at $ENTITY_URL (docker compose up -d entity-service)"
TOKEN="$(curl -sf -m 5 -u "$CLIENT_ID:$CLIENT_SECRET" -d grant_type=client_credentials "$TOKEN_URL" \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["access_token"])' 2>/dev/null)" \
  || fail "no token from $TOKEN_URL (docker compose up -d mock-oidc)"
(exec 3<>"/dev/tcp/${REDIS_ADDR%%:*}/${REDIS_ADDR##*:}") 2>/dev/null \
  || fail "Redis is not reachable at $REDIS_ADDR (docker compose up -d redis)"

if [[ "$TEAM" == none ]]; then
  FAMILY=NONE
else
FAMILY="$(curl -sf -m 5 -H "x-jwt-assertion: $TOKEN" "$ENTITY_URL/team-schedule/catalogue" \
  | python3 -c 'import sys,json
t=sys.argv[1].strip().lower()
for x in json.load(sys.stdin)["teams"]:
    if x["key"].lower()==t or x["name"].lower()==t: print(x["family"]); break' "$TEAM" 2>/dev/null)" \
  || fail "entity-service refused the rota read; is $CLIENT_ID in its AUTH_INTERNAL_CLIENT_IDS?"
fi
case "$FAMILY" in
  NONE) echo "    no assignment group: only a routing rule that takes team-less incidents (monitoring) starts a ladder" ;;
  SRE) echo "    $TEAM is an SRE team: the SRE ladder, the same clock for every priority" ;;
  "")  fail "$TEAM is not a team on the rota" ;;
  *)   echo "    $TEAM is a $FAMILY team: the CRE ladder, a clock set by the priority (and the SRE one too at P0)" ;;
esac
if [[ "$LADDER" == auto ]]; then
  if [[ "$FAMILY" == SRE || "$FAMILY" == NONE ]]; then LADDER=sre; else LADDER=cre; fi
fi
LADDERS=("$LADDER")
[[ "$LADDER" == both ]] && LADDERS=(cre sre)

echo "==> building the engine harness and the gateway shim"
BIN_DIR="${TMPDIR:-/tmp}"
( cd integrations/csm-notification-service && go build -o "$BIN_DIR/escalation-local" ./cmd/escalation-local )
( cd scripts/csm-compose/gateway-shim && go build -o "$BIN_DIR/escalation-gateway-shim" . )

if curl -s -m 2 -o /dev/null "http://localhost:$SHIM_PORT/health"; then
  fail "port $SHIM_PORT is already in use; set SHIM_PORT to a free one"
fi
PORT="$SHIM_PORT" UPSTREAM_URL="$ENTITY_URL" "$BIN_DIR/escalation-gateway-shim" >/dev/null 2>&1 &
SHIM_PID=$!
trap 'kill "$SHIM_PID" 2>/dev/null || true' EXIT
curl -s -m 30 --retry 15 --retry-delay 1 --retry-connrefused -o /dev/null "http://localhost:$SHIM_PORT/health" \
  || fail "the gateway shim did not come up"

# -max-calls is the harness's guard for live phone runs. Nothing here dials,
# and a chat rung posts once, not once per attempt, so a CRE P0 ladder (21
# attempts) must not be refused.
TEAM_ARG="$TEAM"; [[ "$TEAM" == none ]] && TEAM_ARG=""
ARGS=(-team "$TEAM_ARG" -shift "$SHIFT" -minute "$MINUTE" -max-calls 1000)
[[ -n "$CONTACT" ]] && ARGS+=(-contact-type "$CONTACT")
if [[ "$OUTPUT" == log ]]; then
  # The log channel runs the whole ladder and reaches nobody. Printed on this
  # terminal only, so the real people are shown: checking that each rung
  # reaches the right person is the point of a logged run.
  ARGS+=(-channel log -real-names)
else
  ARGS+=(-channel chat)
fi
[[ "$CANCEL_AFTER" != "0" ]] && ARGS+=(-cancel-after "${CANCEL_AFTER}s" -cancel-by "$CANCEL_BY")
ARGS+=(-kind "$KIND")
[[ "$INTERACTIVE" == true ]] && ARGS+=(-interactive)
[[ "$L4" == true ]] && ARGS+=(-sre-l4)

# Every variable the harness would otherwise take from a .env is set here, even
# to empty: it never overrides one that exists, so REDIS_URL="" is what keeps a
# .env's managed Redis out of a local run.
run() {
  env GOOGLE_CHAT_SPACES="${GOOGLE_CHAT_SPACES:-}" \
      REDIS_URL="" REDIS_ADDR="$REDIS_ADDR" \
      CUSTOMER_ENTITY_BASE_URL="http://localhost:$SHIM_PORT" \
      OAUTH2_TOKEN_URL="$TOKEN_URL" OAUTH2_CLIENT_ID="$CLIENT_ID" OAUTH2_CLIENT_SECRET="$CLIENT_SECRET" \
      INCIDENT_ESCALATION_RESOLVER=team-schedule \
      "$BIN_DIR/escalation-local" "$@"
}

cd "$ROOT/integrations/csm-notification-service"
IFS=, read -r -a LIST <<< "$PRIORITIES"
for P in "${LIST[@]}"; do
  for L in "${LADDERS[@]}"; do
    echo
    echo "############################################################################"
    echo "#  $TEAM · $P · $SHIFT · $L ladder · 1 ladder minute = $MINUTE · output: $OUTPUT"
    echo "############################################################################"
    # One run failing must not stop the rest of an -p all run.
    { run "${ARGS[@]}" -ladder "$L" -priority "$P" -incident-id "local-$(date +%s)-$P-$L" 2>&1 \
        || echo "    ($P on the $L ladder did not complete)"; } \
      | grep -vE '^[0-9/]{10} [0-9:]{8} (INFO|WARN) (notifications: no google chat space|escalation: (notifying|no incident-notes))' || true
  done
done
