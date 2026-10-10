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

import { useState, type JSX } from "react";
import { useGetPagingChain, useGetPagingReadiness } from "../api/usePagingChain";
import type { PagingChainCode, PagingChainMember, PagingReadinessGap, RotaFamily } from "../types";
import CasePagingPanel from "./CasePagingPanel";
import PagingReadinessStrip, { type PhoneGapAction } from "./PagingReadinessStrip";
import { CALLING_LABEL } from "./PhoneChip";
import { usePagingPhoneControls } from "./usePagingPhoneControls";

/** The readiness gaps that are about one person's number. */
const PHONE_GAPS = new Set(["NO_PHONE", "PHONE_UNTESTED", "PHONE_TEST_FAILED"]);

/** How far ahead the readiness strip looks. */
export const PAGING_READINESS_DAYS = 7;

/** Where a chain's people come from on the rota: the family, and the rota
 *  within it where the chain is one rota's (SME draws on every SME rota). */
export interface PagingRotaTarget {
  family: RotaFamily;
  rotaCode?: string;
  teamKey?: string;
  zoneCode?: string;
  /** YYYY-MM-DD. */
  date?: string;
}

interface ChainOption {
  code: PagingChainCode | "PAAS";
  label: string;
  /** Where its people are set, for a chain that takes them from the rota. */
  rota?: { family: RotaFamily; rotaCode?: string };
  disabledReason?: string;
}

/** The chains, in the order the picker offers them: CRE, the three SRE
 *  sub-teams together, then SME. PaaS SRE has no rota yet, so it is shown --
 *  it is a chain people expect -- but cannot be picked. */
const CHAINS: readonly ChainOption[] = [
  { code: "CRE", label: "CRE" },
  { code: "SRE_SAAS", label: "SaaS SRE", rota: { family: "SRE", rotaCode: "SRE_SAAS" } },
  { code: "SRE_IAAS", label: "IaaS SRE", rota: { family: "SRE", rotaCode: "SRE_IAAS" } },
  { code: "PAAS", label: "PaaS SRE", disabledReason: "No PaaS rota yet" },
  { code: "SME", label: "SME", rota: { family: "SME" } },
];

interface Props {
  /** Switch the page to the week view on a rota (and team, zone, day). */
  onOpenRota: (target: PagingRotaTarget) => void;
  /** The chain the tab opens on: the reader's own (SME for an SME rota
   *  admin, their SRE sub-team's for SRE). CRE when absent. */
  initialChain?: PagingChainCode;
}

/**
 * The Case Paging tab: a chain picker, then the chain on screen. CRE's chain
 * is set here, in the editable panel; the SRE and SME chains page whoever the
 * rota has on, so for them the tab says where to set people and how ready the
 * chain is. Each chain opens with its readiness strip.
 */
export default function CasePagingTab({ onOpenRota, initialChain = "CRE" }: Props): JSX.Element {
  const chains = CHAINS;
  const offered = (code: PagingChainCode | undefined): code is PagingChainCode =>
    !!code && chains.some((c) => c.code === code && !c.disabledReason);
  // The reader's pick, else the chain they belong to, else the first one
  // offered. Derived rather than seeded into state, so a profile that settles
  // after the first frame still opens the right chain.
  const [chainChoice, setChainCode] = useState<PagingChainCode | undefined>(undefined);
  const firstOffered = chains.find((c) => !c.disabledReason)?.code as PagingChainCode | undefined;
  const chainCode: PagingChainCode = offered(chainChoice)
    ? chainChoice
    : offered(initialChain)
      ? initialChain
      : (firstOffered ?? initialChain);
  const readiness = useGetPagingReadiness(PAGING_READINESS_DAYS);
  // The people a phone gap can be acted on for: the CRE chain (the same read
  // as the panel's), and SRE's while an SRE chain is on screen. SME's people
  // are not served as a chain, so its phone gaps keep the profile hint.
  const sreOnScreen = chainCode === "SRE_SAAS" || chainCode === "SRE_IAAS";
  const creChain = useGetPagingChain("CRE");
  const sreChain = useGetPagingChain("SRE", sreOnScreen);
  const members: PagingChainMember[] = [
    ...(creChain.data?.members ?? []),
    ...(sreOnScreen ? (sreChain.data?.members ?? []) : []),
  ];
  const readAt = Math.max(creChain.dataUpdatedAt, sreOnScreen ? sreChain.dataUpdatedAt : 0);
  const { controls: phone, dialog } = usePagingPhoneControls(members, readAt);

  /** "Add number" or "Test call" on a phone gap, where the reader may edit
   *  that person's paging number. */
  const phoneActionFor = (gap: PagingReadinessGap): PhoneGapAction | null => {
    if (!PHONE_GAPS.has(gap.code) || !gap.userId) return null;
    const m = members.find((x) => x.userId === gap.userId && x.canEditPhone === true);
    if (!m) return null;
    if (gap.code !== "NO_PHONE" && m.pagingPhone) {
      const calling = phone.calling(m);
      return { label: calling ? CALLING_LABEL : "Test call", onClick: () => phone.test(m), pending: calling };
    }
    return { label: "Add number", onClick: () => phone.edit(m) };
  };

  const option = chains.find((c) => c.code === chainCode) ?? chains[0] ?? CHAINS[0];
  const chainReadiness = readiness.data?.chains.find((c) => c.chain === chainCode);
  const stripRead = {
    chain: chainReadiness,
    days: PAGING_READINESS_DAYS,
    isLoading: readiness.isLoading,
    isError: readiness.isError,
  };

  /** A rota gap opens the rota the gap is on: its team's, its zone's, or the
   *  chain's own. CRE's rota is the CRE family's. */
  const openGap = (gap: PagingReadinessGap) =>
    onOpenRota({
      family: option.rota?.family ?? "CRE",
      rotaCode: option.rota?.rotaCode,
      teamKey: gap.teamKey,
      zoneCode: gap.zoneCode,
      date: gap.date,
    });

  const picker = (
    <div className="seg teamseg cp-chains" role="tablist" aria-label="Paging chain">
      {chains.map((c) => {
        const disabled = Boolean(c.disabledReason);
        return (
          <button
            key={c.code}
            type="button"
            role="tab"
            aria-selected={chainCode === c.code}
            // aria-disabled rather than disabled: a disabled button shows no
            // tooltip and is skipped by the keyboard, and the reason it cannot
            // be picked is the point.
            aria-disabled={disabled || undefined}
            title={c.disabledReason}
            className={`${chainCode === c.code ? "on" : ""}${disabled ? " off" : ""}`.trim() || undefined}
            onClick={() => {
              if (!disabled) setChainCode(c.code as PagingChainCode);
            }}
          >
            {c.label}
          </button>
        );
      })}
    </div>
  );

  if (chainCode === "CRE") {
    return (
      <>
        <CasePagingPanel
          chainPicker={picker}
          readiness={stripRead}
          onOpenRota={openGap}
          phone={phone}
          phoneActionFor={phoneActionFor}
        />
        {dialog}
      </>
    );
  }

  const rota = option.rota;
  return (
    <>
      <div className="card-head">
        {picker}
        <h2>{option.label} paging chain</h2>
      </div>
      <PagingReadinessStrip
        key={chainCode}
        label={option.label}
        {...stripRead}
        onOpenRota={openGap}
        phoneActionFor={phoneActionFor}
      />
      <p className="cp-from">
        People come from the {option.label} rota; set them in the Team Schedule views.{" "}
        {rota ? (
          <button
            type="button"
            className="btn sm"
            onClick={() => onOpenRota({ family: rota.family, rotaCode: rota.rotaCode })}
          >
            Open the {option.label} rota
          </button>
        ) : null}
      </p>
      {dialog}
    </>
  );
}
