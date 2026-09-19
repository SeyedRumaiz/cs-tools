#!/usr/bin/env bash
# Manual multi-engineer routing test — covers the things the automated
# suite and your earlier single-engineer curl test deliberately DON'T cover:
#   1. Tie-break on availability time (two engineers tied on chats today ->
#      whoever's been AVAILABLE longer wins)
#   2. Least-busy ranking (an engineer who accepted a chat today looks
#      busier than one who hasn't)
#   3. Decline -> reassignment to another engineer
#
# NOTE: as of the 2026-09-07 DB schema review, there is no more per-customer
# "sticky" routing (see migrations/000013_drop_customer_engineer_assignments)
# and engineers are identified by userId, not email — this script no longer
# tests stickiness and no longer sends an email/engineerId field anywhere.
#
# Uses fully fake engineer/customer ids and fake case ids -- doesn't touch
# any real engineer or real case. Safe to run against your normal dev
# database.
# Run from anywhere once chat-routing-service is up on :9096.

set -euo pipefail

BASE="http://localhost:9096"
TOK="${ROUTING_SERVICE_TOKEN:?Set ROUTING_SERVICE_TOKEN first (e.g. cd apps/chat-routing-service/backend && set -a && source .env && set +a)}"
TS=$(date +%s)

ENG_A="test-eng-a-${TS}"
ENG_B="test-eng-b-${TS}"
CUST_X="test-customer-x-${TS}@test.local"
CUST_Y="test-customer-y-${TS}@test.local"

hdr=(-H "X-Routing-Service-Token: $TOK" -H "Content-Type: application/json")

req() { curl -sS "${hdr[@]}" "$@"; echo; }

section() { echo; echo "=================================================="; echo "$1"; echo "=================================================="; }

section "-1. Safety check: empty queue, and no OTHER engineer currently AVAILABLE"
# Plain grep/sed only -- deliberately avoids piping a script into an
# interpreter (python3/perl/etc.), since that pattern gets killed outright
# by endpoint-security software on some managed machines (SIGKILL, no
# useful error) regardless of what the script actually does.
STATE_CHECK=$(curl -sS "${hdr[@]}" "$BASE/route/debug/state")
echo "$STATE_CHECK"

QUEUE_FIELD=$(echo "$STATE_CHECK" | grep -o '"queue":\[[^]]*\]')
AVAILABLE_FIELD=$(echo "$STATE_CHECK" | grep -o '"available":\[[^]]*\]')

PROBLEM=0
if [ "$QUEUE_FIELD" != '"queue":[]' ]; then
  echo "!!! - the queue is NOT empty ($QUEUE_FIELD) -- a fake engineer going AVAILABLE"
  echo "!!!   could auto-steal a real waiting customer's case."
  PROBLEM=1
fi
if [ "$AVAILABLE_FIELD" != '"available":[]' ]; then
  echo "!!! - at least one engineer is ALREADY available right now: $AVAILABLE_FIELD"
  echo "!!!   they could get handed one of this script's fake test cases and see a"
  echo "!!!   fake alert pop up in their UI."
  PROBLEM=1
fi

if [ "$PROBLEM" -eq 1 ]; then
  echo
  echo "!!! STOP -- not safe to run this script right now. Take the engineer(s)"
  echo "!!! above OFFLINE (or clear the queue) first, then re-run."
  exit 1
fi
echo "OK: queue is empty and no engineer is currently AVAILABLE. Safe to proceed."

section "0. Both engineers OFFLINE -> AVAILABLE (A first, then B)"
req -XPOST "$BASE/route/presence" -d "{\"userId\":\"$ENG_A\",\"status\":\"AVAILABLE\"}"
sleep 1   # ensure A's available_since is strictly earlier than B's, for a clean tie-break
req -XPOST "$BASE/route/presence" -d "{\"userId\":\"$ENG_B\",\"status\":\"AVAILABLE\"}"

section "1. Customer X's escalation -- expect it to land on A (tied on 0 chats today, A available longer)"
req -XPOST "$BASE/route/escalate" -d "{\"caseId\":\"case1-${TS}\",\"conversationId\":\"conv1-${TS}\",\"customerEmail\":\"$CUST_X\",\"customerName\":\"Test X\",\"subject\":\"test\",\"message\":\"hi\"}"
echo "^ CHECK: engineerId above should be $ENG_A."

echo "--- accept + complete case1 as A (so A now has 1 chat accepted today, and rejoins the pool) ---"
req -XPOST "$BASE/route/accept" -d "{\"userId\":\"$ENG_A\",\"caseId\":\"case1-${TS}\"}"
req -XPOST "$BASE/route/completed" -d "{\"userId\":\"$ENG_A\"}"

section "2. Customer Y escalates -- expect LEAST-BUSY: goes to B (0 chats vs A's 1), even though A rejoined the pool most recently"
req -XPOST "$BASE/route/escalate" -d "{\"caseId\":\"case2-${TS}\",\"conversationId\":\"conv2-${TS}\",\"customerEmail\":\"$CUST_Y\",\"customerName\":\"Test Y\",\"subject\":\"test\",\"message\":\"hello\"}"
echo "^ CHECK: engineerId above should be $ENG_B."

echo "--- accept + complete case2 as B (A and B are now tied on 1 chat accepted today each) ---"
req -XPOST "$BASE/route/accept" -d "{\"userId\":\"$ENG_B\",\"caseId\":\"case2-${TS}\"}"
req -XPOST "$BASE/route/completed" -d "{\"userId\":\"$ENG_B\"}"

section "3. Customer X escalates AGAIN -- tied 1-1 on chats today, so tie-break by available_since: A rejoined the pool (step 1) before B did (step 2) -> expect A"
req -XPOST "$BASE/route/escalate" -d "{\"caseId\":\"case3-${TS}\",\"conversationId\":\"conv3-${TS}\",\"customerEmail\":\"$CUST_X\",\"customerName\":\"Test X\",\"subject\":\"test again\",\"message\":\"hi again\"}"
echo "^ CHECK: engineerId above should be $ENG_A."

section "4. DECLINE test -- A declines case3, only B is available -> expect reassignment to B"
req -XPOST "$BASE/route/decline" -d "{\"userId\":\"$ENG_A\",\"caseId\":\"case3-${TS}\"}"
echo "^ CHECK: reassignedTo above should be $ENG_B."

echo "--- clean up: accept+complete the reassigned case, then take both engineers OFFLINE ---"
req -XPOST "$BASE/route/accept" -d "{\"userId\":\"$ENG_B\",\"caseId\":\"case3-${TS}\"}"
req -XPOST "$BASE/route/completed" -d "{\"userId\":\"$ENG_B\"}"
req -XPOST "$BASE/route/presence" -d "{\"userId\":\"$ENG_A\",\"status\":\"OFFLINE\"}"
req -XPOST "$BASE/route/presence" -d "{\"userId\":\"$ENG_B\",\"status\":\"OFFLINE\"}"

section "5. Final state -- both fake engineers should show OFFLINE, queue empty"
req "$BASE/route/debug/state"

echo
echo "Done. Fake identities used (all end in -${TS}), safe to ignore/leave -- they're"
echo "OFFLINE now and will never be picked by real routing again."
