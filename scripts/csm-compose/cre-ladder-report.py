#!/usr/bin/env python3
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
# Reads the log of a CRE escalation ladder run (run-cre-ladder.sh's output) and
# reports what happened against what should have: the rule, the outcome, and
# every call's due time against when it was actually placed.
#
# One parser for both callers, so a single run and the suite are judged alike:
#
#   cre-ladder-report.py single RUN.log
#       a readable report for one run, printed (the caller appends it to the log)
#   cre-ladder-report.py suite SUITE_DIR
#       summary.tsv and calls.tsv for every SUITE_DIR/scenarios/*.log
#
# A log may start with a "### params key=value ..." line naming what the run was
# asked to do (ack, ack-by, team, expected-rule); the report checks against it.

import glob
import os
import re
import sys


def secs(d):
    """'1m30s', '+8m0s', '450ms' -> seconds."""
    t = 0.0
    for v, u in re.findall(r"([\d.]+)(h|ms|m|s)", d.strip().lstrip("+")):
        t += float(v) * {"h": 3600, "m": 60, "s": 1, "ms": 0.001}[u]
    return t


def parse(txt):
    head = re.search(r"^### params (.*)$", txt, re.M)
    params = dict(re.findall(r"(\S+?)=(\S+)", head.group(1))) if head else {}
    plan = re.findall(r"^\s+\+(\S+)\s+(LEVEL_\d)\s+#(\d)\s+(.+?)\s*$",
                      txt.split("running (ctrl-c")[0], re.M)
    placed = re.findall(r"^\s+\[\s*(\S+)\]\s+(LEVEL_\d)\s+#(\d)\s+(.+?)\s+called .*?\(ladder \+(\S+)\)",
                        txt, re.M)
    levels = sorted({p[1] for p in plan})

    # Only this run's own incident counts. A log can hold another run's lines
    # when two runs shared a Redis, and reading the first rule= or "ladder
    # cancelled" in it once reported a broken run as routed and finished.
    inc = (re.search(r"^\s+incident\s+(local-\d+)", txt, re.M) or [None, ""])[1]
    own = "\n".join(l for l in txt.splitlines() if inc and "incidentId=" + inc in l)
    foreign = sorted(set(re.findall(r"incidentId=(local-\d+)", txt)) - {inc})

    # What should happen comes from the scenario, not from the plan: a run that
    # died before planning has no plan, and judging it by one turned "never
    # started" into "expected exhausted".
    ack, by = params.get("ack", "never"), params.get("ack-by", "both")
    if ack in ("never", "none") or by in ("status", "comment"):
        expected = "exhausted"          # never acknowledged, or only half of it
    elif plan and "LEVEL_" + ack.lstrip("L") not in levels:
        expected = "exhausted"          # that rung is not on this rule's ladder
    else:
        expected = "acknowledged"

    if not inc or not plan:
        outcome = "not-started"
    elif "escalation: ladder cancelled" in own:
        outcome = "acknowledged"
    elif "ladder exhausted without acknowledgement" in own:
        outcome = "exhausted"
    elif "interrupted" in txt:
        outcome = "interrupted"
    else:
        outcome = "ERROR"
    issues = sorted(set(re.findall(
        r"(RESOLVE_FAILED|NO_RECIPIENTS|NO_CHAT_SPACE|CALL_FAILED|tick error|^error:.*|^docker: Error.*)",
        txt, re.M)))
    if foreign:
        issues.append("SHARED REDIS: saw other runs' incidents " + ", ".join(foreign[:3]))

    calls = []
    for el, lvl, att, who, due in placed:
        calls.append({"level": lvl, "attempt": att, "who": who.strip(),
                      "due": secs(due), "seen": secs(el), "drift": secs(el) - secs(due)})
    return {
        "params": params,
        "rule": (re.search(r"rule=(R\w+)", own) or [None, ""])[1],
        "shift": (re.search(r"shift +([A-Z_]+) \(derived", txt) or [None, ""])[1],
        "reported": (re.search(r"reported at +(.+? IST)", txt) or [None, ""])[1],
        "plan": plan, "levels": levels, "calls": calls,
        "expected": expected, "outcome": outcome, "issues": issues,
        "cancelled": (re.search(r"cancelledCalls=(\d+)", txt) or [None, ""])[1],
        "reached": max((c["level"] for c in calls), default="-"),
        "max_drift": max((abs(c["drift"]) for c in calls), default=0.0),
    }


def mmss(s):
    s = int(round(s))
    return f"{'-' if s < 0 else ''}{abs(s) // 60}:{abs(s) % 60:02d}"


def single(path):
    r = parse(open(path, encoding="utf-8", errors="replace").read())
    p = r["params"]
    line = "=" * 78
    print(line)
    print("Run report -- what the ladder did, against what it should have")
    print(line)
    print(f"  rule            {r['rule'] or '?'}"
          + (f"   (expected {p['expected-rule']}: "
             f"{'ok' if r['rule'] == p['expected-rule'] else 'MISMATCH'})" if "expected-rule" in p else ""))
    print(f"  shift           {r['shift'] or '?'}   reported {r['reported'] or '?'}")
    print(f"  team            {p.get('team', '?')}")
    print(f"  ladder levels   {' '.join(r['levels']) or 'none'}  ({len(r['plan'])} calls planned)")
    print(f"  outcome         {r['outcome']}   (expected {r['expected']}: "
          f"{'ok' if r['outcome'] == r['expected'] else 'MISMATCH'})")
    print(f"  reached         {r['reached']}"
          + (f", {r['cancelled']} pending call(s) cancelled" if r["cancelled"] else ""))
    print(f"  timing drift    largest {r['max_drift']:.0f}s"
          "  (up to 5s is the harness's tick, not a fault)")
    if r["issues"]:
        print(f"  issues          {', '.join(r['issues'])}")
    print()
    print("  per level: people planned (attempts each)")
    for lvl in r["levels"]:
        rows = [x for x in r["plan"] if x[1] == lvl]
        people = sorted({x[3] for x in rows})
        attempts = max(int(x[2]) for x in rows)
        first = min(secs(x[0]) for x in rows)
        print(f"    {lvl}  opens +{mmss(first)}  {len(people)} people x {attempts}  -- {', '.join(people)}")
    print()
    print("  calls placed        due    placed   drift")
    for c in r["calls"]:
        print(f"    {c['level']} #{c['attempt']}  {c['who'][:28]:<28} +{mmss(c['due']):>6} "
              f"+{mmss(c['seen']):>6}  {c['drift']:+4.0f}s")
    print(line)


def suite(out):
    rows, calls = [], []
    for path in sorted(glob.glob(os.path.join(out, "scenarios", "*.log"))):
        txt = open(path, encoding="utf-8", errors="replace").read()
        sid = re.search(r"### SCENARIO (\S+)", txt).group(1)
        r = parse(txt)
        p = r["params"]
        for c in r["calls"]:
            calls.append([sid, c["level"], c["attempt"], c["who"], f"{c['due']:.0f}",
                          f"{c['seen']:.0f}", f"{c['drift']:+.0f}"])
        outcome = r["outcome"] + ("+issues" if r["issues"] else "")
        rows.append([sid, p.get("expected-rule", ""), r["rule"],
                     "ok" if r["rule"] == p.get("expected-rule") else "MISMATCH",
                     r["shift"], r["reported"], p.get("team", ""), p.get("ack", ""), p.get("ack-by", ""),
                     r["expected"], outcome, "ok" if outcome == r["expected"] else "MISMATCH",
                     r["reached"], " ".join(r["levels"]),
                     str(sum(1 for x in r["plan"] if x[1] == "LEVEL_0")), str(len(r["plan"])),
                     str(len(r["calls"])), r["cancelled"], "; ".join(r["issues"])[:200],
                     f"{r['max_drift']:.0f}"])

    with open(os.path.join(out, "summary.tsv"), "w") as f:
        f.write("scenario\texpected_rule\trule\trule_check\tshift\treported_at\tteam\tack\tack_by\t"
                "expected_outcome\toutcome\toutcome_check\treached_level\tladder_levels\t"
                "level0_people\tplanned_calls\tplaced_calls\tcancelled\tissues\tmax_drift_s\n")
        for r in rows:
            f.write("\t".join(r) + "\n")
    with open(os.path.join(out, "calls.tsv"), "w") as f:
        f.write("scenario\tlevel\tattempt\trecipient\tdue_s\tobserved_s\tdrift_s\n")
        for c in calls:
            f.write("\t".join(c) + "\n")

    bad = [r for r in rows if r[3] != "ok" or r[11] != "ok"]
    print(f"\n{len(rows)} scenarios: {sum(r[3] == 'ok' for r in rows)} routed as expected, "
          f"{sum(r[11] == 'ok' for r in rows)} ended as expected; "
          f"largest timing drift {max((float(r[-1]) for r in rows), default=0):.0f}s")
    if bad:
        print("needs a look:")
        for r in bad:
            print(f"  {r[0]}: rule {r[2] or '?'} (expected {r[1]}), outcome {r[10]} (expected {r[9]})"
                  + (f" -- {r[18][:110]}" if r[18] else ""))


if __name__ == "__main__":
    if len(sys.argv) != 3 or sys.argv[1] not in ("single", "suite"):
        sys.exit("usage: cre-ladder-report.py single RUN.log | suite SUITE_DIR")
    (single if sys.argv[1] == "single" else suite)(sys.argv[2])
