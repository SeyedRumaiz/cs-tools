/**
 * Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

import {
  Alert,
  Autocomplete,
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  MenuItem,
  TextField,
  Typography,
} from "@wso2/oxygen-ui";
import { useMemo, useState, type JSX } from "react";
import { useGenerateRota, type GenerateRotaResult } from "../api/useGenerateRota";
import GeneratedRotaPreview from "./GeneratedRotaPreview";
import { monthKey, monthLabel } from "../utils/rotaMonth";

/** How many of the generator's notes are listed before the rest are counted. */
const SHOWN_WARNINGS = 8;

/** Where a lead's last TZ3 choice is kept, so next month starts from it. */
const CREW_STORAGE_KEY = "csm.teamSchedule.generate.tz3Crew";

/** One team of the generated rota, with the people who can be put on its TZ3. */
export interface GenerateRotaTeam {
  key: string;
  name: string;
  members: readonly { userId: string; name: string; email: string }[];
}

function loadCrew(): Record<string, string[]> {
  try {
    const raw = window.localStorage.getItem(CREW_STORAGE_KEY);
    const parsed: unknown = raw ? JSON.parse(raw) : {};
    if (!parsed || typeof parsed !== "object") return {};
    const out: Record<string, string[]> = {};
    for (const [team, ids] of Object.entries(parsed)) {
      if (Array.isArray(ids)) out[team] = ids.filter((id): id is string => typeof id === "string");
    }
    return out;
  } catch {
    return {};
  }
}

function saveCrew(crew: Record<string, string[]>): void {
  try {
    window.localStorage.setItem(CREW_STORAGE_KEY, JSON.stringify(crew));
  } catch {
    // Private window or storage blocked: the choice just isn't remembered.
  }
}

export interface GenerateRotaDialogProps {
  open: boolean;
  onClose: () => void;
  /** Today, for the month choices. Injected so a test can fix it. */
  today?: Date;
  /** The rota's teams, for choosing who works each team's TZ3. */
  teams?: readonly GenerateRotaTeam[];
}

/**
 * The SaaS rota's "Generate month": pick a month, preview what the generator
 * would write from the availability marked on the roster, then write it.
 * Turns a lead set by hand are never replaced, and generating a month again
 * replaces only the generated turns from today on.
 */
export default function GenerateRotaDialog({
  open,
  onClose,
  today = new Date(),
  teams = [],
}: GenerateRotaDialogProps): JSX.Element {
  const months = useMemo(() => [0, 1, 2].map((n) => monthKey(today, n)), [today]);
  const [month, setMonth] = useState(months[1]);
  const [preview, setPreview] = useState<GenerateRotaResult | null>(null);
  const [written, setWritten] = useState<GenerateRotaResult | null>(null);
  const generate = useGenerateRota();
  const [crew, setCrew] = useState<Record<string, string[]>>(loadCrew);
  const [showRota, setShowRota] = useState(false);
  /** A real write is in flight. The request cannot be called back, so the
   *  dialog stays open until it lands: closing now would only hide a month
   *  that is still being written. A preview writes nothing and may be left. */
  const writing = generate.isPending && generate.variables?.dryRun !== true;

  /** The chosen TZ3 people still on their team, by team; teams with nobody
   *  chosen are left out, so they keep the usual nights. */
  const nightCrew = useMemo(() => {
    const out: Record<string, string[]> = {};
    for (const t of teams) {
      const ids = (crew[t.key] ?? []).filter((id) => t.members.some((m) => m.userId === id));
      if (ids.length > 0) out[t.key] = ids;
    }
    return Object.keys(out).length > 0 ? out : undefined;
  }, [teams, crew]);

  const reset = (): void => {
    setPreview(null);
    setWritten(null);
    setShowRota(false);
    generate.reset();
  };
  const close = (): void => {
    if (writing) return;
    reset();
    onClose();
  };

  const runPreview = (): void => {
    setWritten(null);
    generate.mutate({ month, dryRun: true, nightCrew }, { onSuccess: (res) => setPreview(res) });
  };
  const runGenerate = (): void => {
    if (!preview) return;
    generate.mutate(
      { month, regenerate: preview.alreadyGenerated, nightCrew },
      { onSuccess: (res) => setWritten(res) },
    );
  };

  const shown = written ?? preview;
  const warnings = shown?.warnings ?? [];

  return (
    <Dialog
      open={open}
      onClose={close}
      disableEscapeKeyDown={writing}
      maxWidth="md"
      fullWidth
      aria-labelledby="generate-rota-title"
    >
      <DialogTitle id="generate-rota-title">Generate SaaS rota</DialogTitle>
      <DialogContent>
        <Typography variant="body2" sx={{ mb: 2 }}>
          Works out the month for Apollo and Artemis, per team: L1 and L2 for TZ1, TZ2 and TZ3 every day
          (weekend on-call and escalation included), everyone&rsquo;s regular hours (SUP) on weekdays,
          and lieu leave after a weekend on-call. Every
          engineer is on the rota, nights included, unless marked away on the roster (leave, RnD, ENG or
          any other tag); TZ3 regular hours, if marked, decide who is on nights. One person works one
          zone a day: each team&rsquo;s lead is L3 on TZ1, and a teammate on TZ2 and TZ3. Turns set by hand
          are kept.
        </Typography>

        <TextField
          select
          label="Month"
          value={month}
          onChange={(e) => {
            setMonth(e.target.value);
            reset();
          }}
          size="small"
          fullWidth
          disabled={generate.isPending || written !== null}
        >
          {months.map((m) => (
            <MenuItem key={m} value={m}>
              {monthLabel(m)}
            </MenuItem>
          ))}
        </TextField>

        {teams.length > 0 ? (
          <Box sx={{ mt: 2 }}>
            <Typography variant="subtitle2">TZ3 people (optional)</Typography>
            <Typography variant="caption" color="text.secondary" component="p" sx={{ mt: 0.25, mb: 1.5 }}>
              Choose who works a team&rsquo;s TZ3 this month, for example three people: TZ3 L1, L2 and L3 go
              round them night by night, and they work no other zone. Leave a team empty to use the TZ3
              hours marked on the roster.
            </Typography>
            {teams.map((t) => {
              const chosen = (crew[t.key] ?? [])
                .map((id) => t.members.find((m) => m.userId === id))
                .filter((m): m is GenerateRotaTeam["members"][number] => m !== undefined);
              return (
                <Autocomplete
                  key={t.key}
                  multiple
                  size="small"
                  options={[...t.members]}
                  value={chosen}
                  getOptionLabel={(m) => m.name || m.email}
                  isOptionEqualToValue={(a, b) => a.userId === b.userId}
                  onChange={(_, people) => {
                    const next = { ...crew, [t.key]: people.map((m) => m.userId) };
                    setCrew(next);
                    saveCrew(next);
                    reset();
                  }}
                  disabled={generate.isPending || written !== null}
                  renderInput={(params) => (
                    <TextField
                      {...params}
                      label={`${t.name} TZ3`}
                      placeholder={chosen.length === 0 ? "As marked on the roster" : undefined}
                      helperText={
                        chosen.length > 0 && chosen.length < 3
                          ? "With fewer than three, the rest of TZ3 comes from the team."
                          : undefined
                      }
                    />
                  )}
                  sx={{ mb: 1.5 }}
                />
              );
            })}
          </Box>
        ) : null}

        {generate.isError ? (
          <Alert severity="error" sx={{ mt: 2 }}>
            {generate.error.message || "The rota could not be generated."}
          </Alert>
        ) : null}

        {shown ? (
          <Box sx={{ mt: 2 }} role="status">
            {written ? (
              <Alert severity="success">
                {monthLabel(written.month)} written from {written.from}: {written.summary.written} turns and SUP
                {written.summary.replaced > 0 ? `, ${written.summary.replaced} earlier generated turns replaced` : ""}
                {written.summary.lieuWritten > 0 ? `, ${written.summary.lieuWritten} lieu leave` : ""}.
              </Alert>
            ) : (
              <>
                <Typography variant="body2">
                  <b>{shown.summary.planned - shown.summary.keptByHand}</b> turns and SUP from {shown.from}
                  {shown.summary.keptByHand > 0
                    ? `, ${shown.summary.keptByHand} left as set by hand`
                    : ""}
                  {shown.summary.lieuPlanned > 0 ? `, ${shown.summary.lieuPlanned} lieu leave` : ""}.
                </Typography>
                {shown.alreadyGenerated ? (
                  <Alert severity="info" sx={{ mt: 1 }}>
                    This month has already been generated. Generating again replaces the generated turns
                    from {shown.from} on; turns set by hand are kept.
                  </Alert>
                ) : null}
                <Button size="small" sx={{ mt: 1, px: 0 }} onClick={() => setShowRota((v) => !v)} aria-expanded={showRota}>
                  {showRota ? "Hide who works when" : "Show who works when"}
                </Button>
                {showRota ? (
                  <GeneratedRotaPreview
                    result={shown}
                    teamNames={Object.fromEntries(teams.map((t) => [t.key, t.name]))}
                  />
                ) : null}
              </>
            )}
            {warnings.length > 0 ? (
              <Alert severity="warning" sx={{ mt: 1 }}>
                <Typography variant="body2" sx={{ fontWeight: 600 }}>
                  {warnings.length} slot{warnings.length === 1 ? "" : "s"} to look at
                </Typography>
                <Box component="ul" sx={{ m: 0, pl: 2 }}>
                  {warnings.slice(0, SHOWN_WARNINGS).map((w, i) => (
                    <li key={i}>
                      {w.date ? `${w.date}: ` : ""}
                      {w.message}
                    </li>
                  ))}
                </Box>
                {warnings.length > SHOWN_WARNINGS ? (
                  <Typography variant="caption">and {warnings.length - SHOWN_WARNINGS} more</Typography>
                ) : null}
              </Alert>
            ) : null}
          </Box>
        ) : null}
      </DialogContent>
      <DialogActions>
        {writing ? (
          <Typography variant="body2" color="text.secondary" role="status" sx={{ mr: "auto", pl: 1 }}>
            Writing the month&hellip;
          </Typography>
        ) : null}
        <Button onClick={close} disabled={writing}>
          {written ? "Done" : "Cancel"}
        </Button>
        {written ? null : preview ? (
          <Button variant="contained" onClick={runGenerate} disabled={generate.isPending}>
            {preview.alreadyGenerated ? "Regenerate" : "Generate"}
          </Button>
        ) : (
          <Button variant="contained" onClick={runPreview} disabled={generate.isPending}>
            Preview
          </Button>
        )}
      </DialogActions>
    </Dialog>
  );
}
