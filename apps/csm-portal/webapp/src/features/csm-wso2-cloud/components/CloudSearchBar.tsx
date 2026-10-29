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

import { Box, Button, TextField } from "@wso2/oxygen-ui";
import { Search } from "@wso2/oxygen-ui-icons-react";
import { useState, type FormEvent, type JSX } from "react";

/**
 * A search box that searches on Enter or the button, not on every keystroke:
 * the organisations page opens an exact match directly, which must only happen
 * when the user asks for it.
 */
export default function CloudSearchBar({
  initialValue,
  label,
  placeholder,
  onSearch,
}: {
  initialValue: string;
  label: string;
  placeholder: string;
  onSearch: (query: string) => void;
}): JSX.Element {
  const [value, setValue] = useState(initialValue);

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();
    onSearch(value.trim());
  };

  return (
    <Box component="form" onSubmit={handleSubmit} sx={{ display: "flex", gap: 1, maxWidth: 560 }}>
      <TextField
        size="small"
        fullWidth
        label={label}
        placeholder={placeholder}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        slotProps={{ htmlInput: { "aria-label": label } }}
      />
      <Button type="submit" variant="contained" startIcon={<Search size={16} />}>
        Search
      </Button>
    </Box>
  );
}
