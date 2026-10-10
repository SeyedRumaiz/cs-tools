/* ============================================================================
   49 — DUMP REAL AVAILABILITY CASES AS GOLDEN TEST DATA.
        Run in: System Definition > Scripts - Background  (/sys.scripts.do)
        READ-ONLY. No insert(), update(), deleteRecord(), setValue(), setWorkflow().

   ── WHY ─────────────────────────────────────────────────────────────────
   The Go port of the availability calculation is business critical: after
   cutover the Cloud Status Dashboard's uptime figures come from it and
   nothing else. Unit tests written from reading the ServiceNow source prove
   the port matches MY READING of that source. They cannot prove the reading
   was right — and it already was not once: the first version skipped
   AvailabilityOutageProcessor entirely, which meant overlapping outages were
   summed instead of merged and planned windows did not excuse the downtime
   inside them.

   This script produces the only evidence that actually settles it: REAL
   INPUTS AND REAL OUTPUTS FROM PRODUCTION, replayed through the Go code and
   compared number for number.

   ── WHY THE STORED ROWS ARE A VALID BASELINE, DESPITE BEING v1 ──────────
   The instance runs v1 (`com.snc.availability.v2` = false), and the port
   targets v2. That looked like it ruled out comparison. It does not, for
   the FIXED period types, because the two engines agree where it matters:

     * v1's AvailabilityCalculator merges overlapping same-type outages
       (_mergeAfter / _mergeBefore) — same as v2's processOutages.
     * v1 splits an outage around a planned one ("OUTAGE IS SPLIT BY
       EXISTING PLANNED OUTAGE") — same as v2's _checkPlannedOutageOverlap.
     * both accumulate only type=outage, both measure AST through the
       schedule, both use the same availability/MTBF/MTRS formulas.

   *** THE ONE KNOWN DIVERGENCE IS ROLLING WINDOW LENGTH. *** v1 subtracts
   (N-2) days under PRB1304264, so its last30days spans 29 where v2's spans
   30. So this dump takes DAILY, WEEKLY, MONTHLY and ANNUALLY rows only, and
   deliberately skips last7days/last30days/last90days/last12months. A
   mismatch on a fixed period is a real bug in the port; a mismatch on a
   rolling one would prove nothing.

   ── WHAT IT EMITS ───────────────────────────────────────────────────────
   One JSON object per line, each a complete self-contained case:
   the period, the commitment's target and schedule, ServiceNow's computed
   OUTPUTS, and the raw outages that fed them. Paste the lines into
   entity-service/internal/service/testdata/availability_golden.jsonl and
   the Go golden test replays each one.

   Durations are emitted as SECONDS. A GlideDuration is stored as a datetime
   offset from 1970-01-01, which is unreadable as a number and was already
   the cause of one bug in this pack.

   *** PRIORITISES THE 220 ROWS THAT RECORD DOWNTIME. *** 212,684 of the
   212,904 rows are a flat 100% and prove almost nothing — they would pass
   against a calculator that always returned 100. The interesting cases are
   the ones where the arithmetic did something, so those come first and the
   all-clear rows are sampled only as controls.

   Set LIMIT lower if the output is truncated.
   ============================================================================ */

var LIMIT = 60;          // downtime cases to emit
var CONTROL_LIMIT = 8;   // zero-downtime cases, as controls

/* v1 and v2 agree on these. The rolling types are deliberately excluded —
   see the header. */
var FIXED_TYPES = 'daily,weekly,monthly,annually';

var BUDGET = 120000, used = 0, truncated = false;
function out(s) {
  s = String(s);
  if (truncated) return;
  if (used + s.length > BUDGET) { gs.info('*** BUDGET HIT — lower LIMIT and re-run ***'); truncated = true; return; }
  used += s.length; gs.info(s);
}

/* A GlideDuration column is stored as a datetime offset from 1970-01-01, so
   its seconds-since-epoch IS its length in seconds.
 *
 * *** THE FIRST VERSION OF THIS RETURNED 0 FOR EVERY FIELD. *** It used
 * gr.getElement(field).getNumericValue(), which came back empty on this
 * instance for every duration column — including on a row reading 79.35%
 * availability, which is impossible. The whole dump shipped with
 * absDownSecs/astSecs/mtbfSecs all zero, and the Go golden test was briefly
 * written to SKIP any case whose ast did not match its period: with every
 * ast zero that skipped all 68 cases and reported a clean pass having
 * verified nothing.
 *
 * Reading the stored string and re-parsing it is slower and it works. */
function durSecs(gr, field) {
  try {
    var raw = gr.getValue(field);
    if (!raw) return 0;
    return Math.round(new GlideDateTime(raw).getNumericValue() / 1000);
  } catch (e) { return 0; }
}

/* Constructed rather than built with setValue(). It would be an in-memory
   date object either way, not a write — but this pack tells readers to grep
   for setValue() before running anything, and a hit that needs explaining
   is worse than one line of different code. */
function epoch(gdtString) {
  if (!gdtString) return null;
  return Math.round(new GlideDateTime(gdtString).getNumericValue() / 1000);
}

/* The outages that fed one row, by the SAME rule the calculator uses:
   the subject's own cmdb_ci OR a row in the affected-CI m2m, type outage or
   planned, both ends set, overlapping the period. Getting this query wrong
   would make the golden data wrong in the same direction as a buggy port
   and the comparison would pass while both were incorrect — so it mirrors
   AvailabilityCalculatorV2._getOutages deliberately. */
function outagesFor(subjectSysId, startStr, endStr) {
  var seen = {}, list = [];

  function add(gr) {
    var id = gr.getUniqueValue();
    if (seen[id]) return;
    seen[id] = true;
    list.push({
      b: epoch(gr.getValue('begin')),
      e: epoch(gr.getValue('end')),
      t: gr.getValue('type')
    });
  }

  var direct = new GlideRecord('cmdb_ci_outage');
  direct.addQuery('cmdb_ci', subjectSysId);
  direct.addQuery('type', 'outage').addOrCondition('type', 'planned');
  direct.addNotNullQuery('begin');
  direct.addNotNullQuery('end');
  direct.addQuery('begin', '<', endStr);
  direct.addQuery('end', '>', startStr);
  direct.query();
  while (direct.next()) add(direct);

  /* The m2m leg. Walked as a separate query rather than a join so the two
     legs are visibly the same filter — a join here is where an accidental
     difference between the two would hide. */
  var m = new GlideRecord('cmdb_outage_ci_mtom');
  m.addQuery('ci_item', subjectSysId);
  m.query();
  while (m.next()) {
    var o = new GlideRecord('cmdb_ci_outage');
    if (!o.get(m.getValue('outage'))) continue;
    var ty = o.getValue('type');
    if (ty != 'outage' && ty != 'planned') continue;
    if (!o.getValue('begin') || !o.getValue('end')) continue;
    if (!(o.getValue('begin') < endStr && o.getValue('end') > startStr)) continue;
    add(o);
  }
  return list;
}

function emit(gr, kind) {
  var subject = gr.getValue('service_offering') || gr.getValue('cmdb_ci');
  if (!subject) return false; // ~11,000 rows have no subject at all

  var startStr = gr.getValue('start');
  var endStr = gr.getValue('end');

  var commitmentId = gr.getValue('service_commitment');
  var target = null, scheduleId = null, tz = null;
  if (commitmentId) {
    var sc = new GlideRecord('service_commitment');
    if (sc.get(commitmentId)) {
      target = parseFloat(sc.getValue('availability'));
      scheduleId = sc.getValue('schedule');
      tz = sc.getValue('timezone');
    }
  }

  var c = {
    kind: kind,
    row: gr.getUniqueValue(),
    type: gr.getValue('type'),
    start: startStr,
    end: endStr,
    startEpoch: epoch(startStr),
    endEpoch: epoch(endStr),
    tz: tz,
    target: target,
    schedule: scheduleId,
    // ServiceNow's own answers — what the port must reproduce.
    want: {
      absDownSecs: durSecs(gr, 'absolute_downtime'),
      schedDownSecs: durSecs(gr, 'scheduled_downtime'),
      astSecs: durSecs(gr, 'ast'),
      allowedSecs: durSecs(gr, 'allowed_downtime'),
      mtbfSecs: durSecs(gr, 'mtbf'),
      mtrsSecs: durSecs(gr, 'mtrs'),
      absAvail: parseFloat(gr.getValue('absolute_availability')),
      schedAvail: parseFloat(gr.getValue('scheduled_availability')),
      absCount: parseInt(gr.getValue('absolute_count'), 10),
      schedCount: parseInt(gr.getValue('scheduled_count'), 10),
      met: gr.getValue('met_commitment') == '1' || gr.getValue('met_commitment') == 'true'
    },
    outages: outagesFor(subject, startStr, endStr)
  };
  out(JSON.stringify(c));
  return true;
}

out('# 49 — availability golden cases.  instance=' + gs.getProperty('instance_name') +
    '  run=' + new GlideDateTime().getDisplayValue());
out('# Fixed period types only (daily/weekly/monthly/annually): v1 and v2 agree');
out('# on these. Rolling types are excluded — v1 spans N-1 days under PRB1304264.');
out('# Durations are SECONDS. Paste the JSON lines (not these comments) into');
out('# entity-service/internal/service/testdata/availability_golden.jsonl');

/* THE CASES THAT MATTER: rows where the arithmetic actually did something.
   A calculator hard-coded to return 100 would pass every other row. */
var emitted = 0;
var d = new GlideRecord('service_availability');
d.addEncodedQuery('absolute_downtime>1970-01-01 00:00:00^typeIN' + FIXED_TYPES);
d.orderByDesc('start');
d.setLimit(LIMIT * 2);
d.query();
while (d.next() && emitted < LIMIT) {
  if (emit(d, 'downtime')) emitted++;
}
out('# downtime cases emitted: ' + emitted);

/* Controls. A handful of all-clear rows, so a port that somehow produced
   downtime from nothing is caught too. */
var controls = 0;
var z = new GlideRecord('service_availability');
z.addEncodedQuery('absolute_downtime=1970-01-01 00:00:00^typeIN' + FIXED_TYPES);
z.orderByDesc('start');
z.setLimit(CONTROL_LIMIT * 4);
z.query();
while (z.next() && controls < CONTROL_LIMIT) {
  if (emit(z, 'clear')) controls++;
}
out('# control (zero-downtime) cases emitted: ' + controls);
out('# TOTAL: ' + (emitted + controls));
