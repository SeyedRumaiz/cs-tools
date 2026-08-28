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
  type SelectChangeEvent,
} from "@wso2/oxygen-ui";
import type { JSX } from "react";
import {
  useGetEngineerStatus,
  useSetEngineerStatus,
  type EngineerStatus,
} from "@features/csm-chat/api/useEngineerStatus";

const STATUS_OPTIONS: {
  value: EngineerStatus;
  label: string;
  color: string;
}[] = [
  { value: "AVAILABLE", label: "Available", color: "#22C55E" },
  { value: "BUSY", label: "Busy", color: "#F59E0B" },
  { value: "OFFLINE", label: "Offline", color: "#9CA3AF" },
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
 */
export default function EngineerStatusMenu(): JSX.Element {
  const { data: status } = useGetEngineerStatus();
  const setStatus = useSetEngineerStatus();

  const current: EngineerStatus = status ?? "OFFLINE";
  const currentOption =
    STATUS_OPTIONS.find((o) => o.value === current) ?? STATUS_OPTIONS[2];

  const handleChange = (e: SelectChangeEvent<string>): void => {
    const next = e.target.value as EngineerStatus;
    if (next === current) return;
    setStatus.mutate(next);
  };

  return (
    <Box sx={{ display: "flex", alignItems: "center" }}>
      <Select
        value={current}
        onChange={handleChange}
        size="small"
        variant="standard"
        disableUnderline
        aria-label="Set your engineer status"
        disabled={setStatus.isPending}
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
        {STATUS_OPTIONS.map((o) => (
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
    </Box>
  );
}
