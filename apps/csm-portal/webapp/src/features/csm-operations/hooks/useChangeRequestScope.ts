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

import { useCallback, useMemo, useState } from "react";
import {
  useChangeRequestScopeLookups,
  type ChangeRequestScopeLookups,
  type ScopeDeployment,
  type ScopeOption,
} from "@features/csm-operations/api/useChangeRequestScopeLookups";
import type { BeCustomerContact } from "@api/backend/types";

/** What a form seeds the scope fields with (a restored draft, a clone, or the record being edited). */
export interface ChangeRequestScopeSeed {
  projectId?: string;
  projectLabel?: string;
  deployments?: ScopeOption[];
  deploymentProducts?: ScopeOption[];
}

export interface ChangeRequestScope {
  projectId: string;
  projectLabel: string;
  deploymentIds: string[];
  /** Derived from the chosen deployments; never picked by hand. */
  deploymentProductIds: string[];
  /** False while the derived deployment products are still being looked up. */
  productsReady: boolean;
  /** id -> display name for everything currently selected (for drafts / pickers). */
  deploymentLabels: Record<string, string>;
  deploymentProductLabels: Record<string, string>;
  /** Selectable deployments of the chosen project. */
  deploymentOptions: ScopeOption[];
  /**
   * The change request's "Customer Group": the chosen project's registered
   * contacts, derived by the backend and READ-ONLY (never picked, never sent).
   * Empty until a project is chosen and its lookup has settled.
   */
  customerContacts: BeCustomerContact[];
  /** True once `customerContacts` is known for the current project. */
  customerContactsReady: boolean;
  lookups: ChangeRequestScopeLookups;
  setProject: (id: string, label?: string) => void;
  setDeployments: (ids: string[]) => void;
}

function toLabelMap(options: ScopeOption[] | undefined): Record<string, string> {
  const out: Record<string, string> = {};
  for (const o of options ?? []) out[o.id] = o.label;
  return out;
}

function uniqueOptions(lists: Array<ScopeOption[] | undefined>): ScopeOption[] {
  const seen = new Map<string, ScopeOption>();
  for (const list of lists) {
    for (const o of list ?? []) if (!seen.has(o.id)) seen.set(o.id, o);
  }
  return [...seen.values()];
}

/**
 * State and cascade for the change-request form's scope fields, shared by the
 * create page and the edit dialog.
 *
 * - Picking (or changing) the Customer Project clears the deployments below it
 *   and re-derives the read-only Customer Group (the new project's registered
 *   contacts) — nothing about the group is held as state, so a group of the
 *   previous project can never survive a project change.
 * - Deployment products are derived from the chosen deployments (never held as
 *   independent state): the union of what those deployments carry. While a
 *   deployment's products are still loading, the seeded value stands in so an
 *   edit dialog / restored draft does not flash empty.
 */
export function useChangeRequestScope(seed: ChangeRequestScopeSeed = {}): ChangeRequestScope {
  // Frozen at mount: the seed is only the initial value, and the display names
  // the seeded ids fall back to until the lookups resolve them.
  const [initial] = useState(seed);
  const [projectId, setProjectId] = useState(initial.projectId ?? "");
  const [projectLabel, setProjectLabel] = useState(initial.projectLabel ?? "");
  const [deploymentIds, setDeploymentIds] = useState<string[]>(
    () => initial.deployments?.map((d) => d.id) ?? [],
  );

  const lookups = useChangeRequestScopeLookups(projectId || undefined, deploymentIds);
  const byId = useMemo(() => {
    const map = new Map<string, ScopeDeployment>();
    for (const d of lookups.deployments) map.set(d.id, d);
    return map;
  }, [lookups.deployments]);

  const chosen = useMemo(
    () => deploymentIds.map((id) => byId.get(id)).filter((d): d is ScopeDeployment => !!d),
    [deploymentIds, byId],
  );

  const deploymentOptions = lookups.deployments;

  const setProject = useCallback(
    (id: string, label?: string) => {
      if (id === projectId) return;
      setProjectId(id);
      setProjectLabel(id ? (label ?? "") : "");
      setDeploymentIds([]);
    },
    [projectId],
  );

  const setDeployments = useCallback((ids: string[]) => setDeploymentIds(ids), []);

  // Derived deployment products.
  const productsReady = chosen.length === deploymentIds.length && chosen.every((d) => d.products !== undefined);
  const seedDeploymentIds = initial.deployments?.map((d) => d.id) ?? [];
  const unchangedFromSeed =
    deploymentIds.length === seedDeploymentIds.length &&
    deploymentIds.every((id) => seedDeploymentIds.includes(id));
  const derivedProducts = useMemo<ScopeOption[]>(() => {
    if (deploymentIds.length === 0) return [];
    if (!productsReady && unchangedFromSeed) return initial.deploymentProducts ?? [];
    return uniqueOptions(chosen.map((d) => d.products));
  }, [deploymentIds.length, productsReady, unchangedFromSeed, initial.deploymentProducts, chosen]);

  const deploymentLabels = useMemo(() => {
    const labels = toLabelMap(initial.deployments);
    for (const id of deploymentIds) {
      const d = byId.get(id);
      if (d) labels[id] = d.label;
    }
    return labels;
  }, [initial.deployments, deploymentIds, byId]);
  const deploymentProductLabels = useMemo(() => toLabelMap(derivedProducts), [derivedProducts]);

  return {
    projectId,
    projectLabel,
    deploymentIds,
    deploymentProductIds: derivedProducts.map((p) => p.id),
    productsReady,
    deploymentLabels,
    deploymentProductLabels,
    deploymentOptions,
    customerContacts: lookups.customerContacts,
    customerContactsReady: lookups.contactsReady,
    lookups,
    setProject,
    setDeployments,
  };
}
