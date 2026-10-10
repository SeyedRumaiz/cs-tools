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

import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import UserProfileModal from "@components/header/UserProfileModal";
import { getRoleLabel } from "@features/settings/utils/settings";
import type { UserDetails } from "@features/settings/types/users";

let mockUserDetails: Partial<UserDetails> | undefined = {
  firstName: "Ada",
  lastName: "Lovelace",
  email: "ada@test.dev",
  phoneNumber: "",
  roles: ["customer_admin"],
};

vi.mock("@features/settings/api/useGetUserDetails", () => ({
  default: () => ({
    data: mockUserDetails,
    isLoading: false,
  }),
}));

vi.mock("@api/useGetMetadata", () => ({
  default: () => ({ data: { timeZones: [] }, isLoading: false }),
}));

vi.mock("@features/settings/api/usePatchUserMe", () => ({
  usePatchUserMe: () => ({ mutate: vi.fn(), isPending: false }),
}));

vi.mock("@context/error-banner/ErrorBannerContext", () => ({
  useErrorBanner: () => ({ showError: vi.fn() }),
}));

vi.mock("@context/success-banner/SuccessBannerContext", () => ({
  useSuccessBanner: () => ({ showSuccess: vi.fn() }),
}));

describe("getRoleLabel", () => {
  it("returns 'Not Available' when roles is undefined or empty", () => {
    expect(getRoleLabel(undefined)).toBe("Not Available");
    expect(getRoleLabel([])).toBe("Not Available");
  });

  it("returns 'Admin' for dev environment role 'customer_admin'", () => {
    expect(getRoleLabel(["customer_admin"])).toBe("Admin");
  });

  it("returns 'Admin' for 'admin'", () => {
    expect(getRoleLabel(["admin"])).toBe("Admin");
  });

  it("returns 'Admin' for staging ServiceNow role 'sn_customerservice.customer_admin'", () => {
    expect(getRoleLabel(["sn_customerservice.customer_admin"])).toBe("Admin");
  });

  it("returns 'Admin' for staging ServiceNow role 'sn_customerservice.admin'", () => {
    expect(getRoleLabel(["sn_customerservice.admin"])).toBe("Admin");
  });

  it("returns 'Admin' when multiple roles include customer_admin and customer", () => {
    expect(getRoleLabel(["customer", "customer_admin"])).toBe("Admin");
    expect(
      getRoleLabel([
        "sn_customerservice.customer",
        "sn_customerservice.customer_admin",
      ]),
    ).toBe("Admin");
  });

  it("returns 'Partner Admin' for partner admin roles", () => {
    expect(getRoleLabel(["partner_admin"])).toBe("Partner Admin");
    expect(getRoleLabel(["sn_customerservice.partner_admin"])).toBe("Partner Admin");
  });

  it("returns 'Lead' for lead role", () => {
    expect(getRoleLabel(["lead"])).toBe("Lead");
  });

  it("returns 'Security User' for security roles", () => {
    expect(getRoleLabel(["security_user"])).toBe("Security User");
    expect(getRoleLabel(["security"])).toBe("Security User");
  });

  it("returns 'Partner' for partner roles", () => {
    expect(getRoleLabel(["partner"])).toBe("Partner");
    expect(getRoleLabel(["partner_user"])).toBe("Partner");
    expect(getRoleLabel(["sn_customerservice.partner"])).toBe("Partner");
  });

  it("returns 'Internal User' for internal and agent roles", () => {
    expect(getRoleLabel(["agent"])).toBe("Internal User");
    expect(getRoleLabel(["internal"])).toBe("Internal User");
    expect(getRoleLabel(["wso2_agent"])).toBe("Internal User");
    expect(getRoleLabel(["snc_internal"])).toBe("Internal User");
  });

  it("returns 'System User' only for explicit system integration roles", () => {
    expect(getRoleLabel(["system_user"])).toBe("System User");
    expect(getRoleLabel(["integration_user"])).toBe("System User");
  });

  it("returns 'Portal User' for standard customer roles", () => {
    expect(getRoleLabel(["customer"])).toBe("Portal User");
    expect(getRoleLabel(["customer_user"])).toBe("Portal User");
    expect(getRoleLabel(["portal_user"])).toBe("Portal User");
    expect(getRoleLabel(["sn_customerservice.customer"])).toBe("Portal User");
    expect(getRoleLabel(["snc_external"])).toBe("Portal User");
  });

  it("returns 'Portal User' for unknown custom roles instead of 'System User'", () => {
    expect(getRoleLabel(["viewer"])).toBe("Portal User");
  });
});

describe("UserProfileModal", () => {
  it("renders user name and role when open", () => {
    mockUserDetails = {
      firstName: "Ada",
      lastName: "Lovelace",
      email: "ada@test.dev",
      phoneNumber: "",
      roles: ["customer_admin"],
    };
    render(<UserProfileModal open onClose={() => {}} />);
    expect(screen.getByText("Ada Lovelace")).toBeInTheDocument();
    expect(screen.getByText("Admin")).toBeInTheDocument();
  });

  it("renders 'Admin' role when user has ServiceNow customer_admin role", () => {
    mockUserDetails = {
      firstName: "Ada",
      lastName: "Lovelace",
      email: "ada@test.dev",
      phoneNumber: "",
      roles: ["sn_customerservice.customer_admin"],
    };
    render(<UserProfileModal open onClose={() => {}} />);
    expect(screen.getByText("Admin")).toBeInTheDocument();
  });
});
