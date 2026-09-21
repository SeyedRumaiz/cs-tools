// Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

import {
  Box,
  MenuItem,
  Select,
  Typography,
  type SelectChangeEvent,
} from "@wso2/oxygen-ui";
import type { JSX } from "react";
import {
  useGetEngineerStatus,
  useSetEngineerStatus,
  useSetMaxConcurrentChats,
  type EngineerStatus,
} from "@features/csm-chat/api/useEngineerStatus";

// Options offered in the capacity dropdown below. chat-routing-service's own
// CHECK constraint allows 1-10 (see cs_engineer_status.max_concurrent_chats).
const CAPACITY_OPTIONS = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10];

// Display info for every status. All three are directly selectable:
// chat_status is a plain manual toggle, independent of how many cases the
// engineer is actually holding, so there is no derived PENDING/BUSY the
// engineer can't pick directly -- BUSY is a real do-not-disturb an engineer
// sets themselves, same as Available/Offline.
const STATUS_DISPLAY: Record<EngineerStatus, { label: string; color: string }> = {
  AVAILABLE: { label: "Available", color: "#22C55E" },
  BUSY: { label: "Busy", color: "#EAB308" },
  OFFLINE: { label: "Offline", color: "#EF4444" },
};

const SELECTABLE_STATUSES: { value: EngineerStatus; label: string; color: string }[] = [
  { value: "AVAILABLE", ...STATUS_DISPLAY.AVAILABLE },
  { value: "BUSY", ...STATUS_DISPLAY.BUSY },
  { value: "OFFLINE", ...STATUS_DISPLAY.OFFLINE },
];

/**
 * Header dropdown for an engineer's live-chat-routing presence — Available/
 * Busy/Offline (see csm-portal/backend's internal/handler/chat.go
 * HandleSetPresence/HandleGetPresence and the standalone chat-routing-
 * service they proxy to). Modeled on ThemeSelect's plain-Select pattern
 * rather than UserProfile's UserMenu, since this picks a status value, not
 * a person.
 *
 * Defaults to Offline — the routing service's own default for an engineer
 * it has never seen a presence update from — while the initial fetch is in
 * flight, so the dropdown never flashes an incorrect Available state.
 *
 * Also shows this engineer's current concurrent-chat load next to the
 * status dropdown (e.g. "2/3"), and a second small dropdown to change their
 * own max_concurrent_chats (see useSetMaxConcurrentChats).
 */
export default function EngineerStatusMenu(): JSX.Element {
  const { data: presence } = useGetEngineerStatus();
  const setStatus = useSetEngineerStatus();
  const setMaxConcurrentChats = useSetMaxConcurrentChats();

  const current: EngineerStatus = presence?.chatStatus ?? "OFFLINE";
  const currentOption = STATUS_DISPLAY[current];
  const activeChats = presence?.activeChats ?? 0;
  const maxConcurrentChats = presence?.maxConcurrentChats ?? 1;
  const showLoad = maxConcurrentChats > 1 || activeChats > 0;
  // The dropdown must always include the engineer's actual current value,
  // even if it's outside CAPACITY_OPTIONS's 1-10 range (set via a manual DB
  // update, or a future admin tool) -- otherwise MUI's Select would render
  // blank instead of showing what's really set.
  const capacityOptions = CAPACITY_OPTIONS.includes(maxConcurrentChats)
    ? CAPACITY_OPTIONS
    : [...CAPACITY_OPTIONS, maxConcurrentChats].sort((a, b) => a - b);

  const handleChange = (e: SelectChangeEvent<string>): void => {
    const next = e.target.value as EngineerStatus;
    if (next === current) return;
    setStatus.mutate(next);
  };

  const handleCapacityChange = (e: SelectChangeEvent<string>): void => {
    const next = Number(e.target.value);
    if (next === maxConcurrentChats) return;
    setMaxConcurrentChats.mutate(next);
  };

  return (
    <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
      <Select
        value={current}
        onChange={handleChange}
        size="small"
        variant="standard"
        disableUnderline
        aria-label="Set your engineer status"
        disabled={setStatus.isPending}
        // Without this, MUI renders the matching MenuItem's children (its
        // own colored dot + label) as the closed-state display *in addition
        // to* the startAdornment dot below -- two dots for one status.
        // renderValue takes over the closed-state display entirely, so only
        // the startAdornment's dot shows once collapsed; each MenuItem's own
        // dot still renders normally in the open dropdown list.
        renderValue={() => currentOption.label}
        startAdornment={
          <Box
            component="span"
            sx={{
              width: 8,
              height: 8,
              borderRadius: "50%",
              bgcolor: currentOption.color,
              mr: 1,
              flexShrink: 0,
            }}
          />
        }
        sx={{
          minWidth: 116,
          fontSize: "0.8125rem",
          color: "text.secondary",
          "& .MuiSelect-select": {
            display: "flex",
            alignItems: "center",
            py: 0.5,
          },
        }}
      >
        {SELECTABLE_STATUSES.map((o) => (
          <MenuItem key={o.value} value={o.value} sx={{ fontSize: "0.8125rem" }}>
            <Box
              component="span"
              sx={{
                width: 8,
                height: 8,
                borderRadius: "50%",
                bgcolor: o.color,
                display: "inline-block",
                mr: 1,
              }}
            />
            {o.label}
          </MenuItem>
        ))}
      </Select>
      {showLoad && (
        <Typography variant="caption" color="text.secondary" sx={{ fontSize: "0.75rem" }}>
          {activeChats}/
        </Typography>
      )}
      <Select
        value={String(maxConcurrentChats)}
        onChange={handleCapacityChange}
        size="small"
        variant="standard"
        disableUnderline
        aria-label="Set your max concurrent chats"
        disabled={setMaxConcurrentChats.isPending}
        sx={{
          minWidth: 36,
          fontSize: "0.75rem",
          color: "text.secondary",
          "& .MuiSelect-select": { py: 0.25, pr: "20px !important" },
        }}
      >
        {capacityOptions.map((n) => (
          <MenuItem key={n} value={String(n)} sx={{ fontSize: "0.8125rem" }}>
            {n}
          </MenuItem>
        ))}
      </Select>
    </Box>
  );
}
