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

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom/vitest";

// QueryErrorState imports BackendApiError from the real API client module,
// which reads window.config at load; mock it with a real class so `instanceof`
// still works (same technique as CreateChangeRequestPage.test.tsx).
vi.mock("@api/backend/client", () => ({
  BackendApiError: class BackendApiError extends Error {
    status: number;
    constructor(status: number, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

import QueryErrorState from "@components/QueryErrorState";

describe("QueryErrorState — retry", () => {
  it("renders no retry button when onRetry is not provided", () => {
    render(<QueryErrorState message="Could not load." />);
    expect(screen.getByText("Could not load.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /try again/i })).not.toBeInTheDocument();
  });

  it("renders a 'Try again' button that calls onRetry when provided", () => {
    const onRetry = vi.fn();
    render(<QueryErrorState message="Could not load." onRetry={onRetry} />);

    fireEvent.click(screen.getByRole("button", { name: /try again/i }));

    expect(onRetry).toHaveBeenCalledTimes(1);
  });
});
