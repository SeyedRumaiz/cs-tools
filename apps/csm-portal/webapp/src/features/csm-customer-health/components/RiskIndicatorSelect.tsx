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

// No backend call, static option list. Matches the plain displayEmpty style
// every sibling filter in this bar uses (Region/Product/ABT Team/Health
// Status) -- this used to be the one filter styled with a floating
// InputLabel + OutlinedInput instead, which rendered its closed-state text
// at a different position than every other filter next to it.
import {
  Select,
  MenuItem,
  Checkbox,
  ListItemText,
  FormControl,
  Divider,
  type SelectChangeEvent,
} from "@wso2/oxygen-ui";
import type { RiskFilterKey } from "../api/customerHealthTypes";

const riskOptions: { key: RiskFilterKey; label: string }[] = [
  { key: "hasNoGoLive", label: "Has Gone Live" },
  { key: "noSupportCases6mo", label: "Support Activity (Last 6 Mo)" },
  { key: "hasEolProduct", label: "Using EOL Products" },
  { key: "hasAbandonedMigrations", label: "Abandoned Migrations" },
  { key: "hasMigrationDelays", label: "Migration Delays" },
  { key: "hasRecentEscalations", label: "Escalations (Last 3 Mo)" },
];

interface RiskIndicatorSelectProps {
  selectedRisks: RiskFilterKey[];
  onRiskChange: (newRisks: RiskFilterKey[]) => void;
}

export default function RiskIndicatorSelect({ selectedRisks, onRiskChange }: RiskIndicatorSelectProps) {
  const handleChange = (event: SelectChangeEvent<typeof selectedRisks>) => {
    const {
      target: { value },
    } = event;
    const valueArray = typeof value === "string" ? value.split(",") : value;

    if (valueArray.includes("clear-all" as RiskFilterKey)) {
      onRiskChange([]);
      return;
    }
    onRiskChange(valueArray as RiskFilterKey[]);
  };

  return (
    <FormControl sx={{ m: 1, width: 220 }} size="small">
      <Select
        multiple
        value={selectedRisks}
        onChange={handleChange}
        displayEmpty
        renderValue={(selected) => {
          if (selected.length === 0) return "All Indicators";
          if (selected.length === riskOptions.length) return "All Indicators";
          return `${selected.length} Selected`;
        }}
      >
        {selectedRisks.length > 0 && (
          <MenuItem value={"clear-all" as RiskFilterKey} sx={{ fontWeight: "bold", justifyContent: "center" }}>
            <ListItemText primary="Clear All" sx={{ textAlign: "center" }} />
          </MenuItem>
        )}

        {selectedRisks.length > 0 && <Divider />}

        {riskOptions.map((option) => (
          <MenuItem key={option.key} value={option.key}>
            <Checkbox checked={selectedRisks.indexOf(option.key) > -1} />
            <ListItemText primary={option.label} />
          </MenuItem>
        ))}
      </Select>
    </FormControl>
  );
}
