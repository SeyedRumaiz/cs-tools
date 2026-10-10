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

// Direct port of the webapp's utils/incidentFormOptions.ts, used by NewIncidentPage.tsx.
import type {
  IncidentCategory,
  IncidentContactType,
  IncidentImpact,
  IncidentPriority,
  IncidentSubcategory,
  IncidentUrgency,
} from "@src/types";

export const CATEGORY_OPTIONS: Array<{ value: IncidentCategory; label: string }> = [
  { value: "INQUIRY", label: "Inquiry / Help" },
  { value: "SERVICE_INTERRUPTION", label: "Service Interruption" },
  { value: "SECURITY", label: "Security" },
];

export const IMPACT_OPTIONS: Array<{ value: IncidentImpact; label: string }> = [
  { value: "HIGH", label: "High" },
  { value: "MEDIUM", label: "Medium" },
  { value: "LOW", label: "Low" },
];

export const URGENCY_OPTIONS: Array<{ value: IncidentUrgency; label: string }> = [
  { value: "HIGH", label: "High" },
  { value: "MEDIUM", label: "Medium" },
  { value: "LOW", label: "Low" },
];

/**
 * "Channel" — how the incident was reported. The values are ServiceNow's incident `contact_type`
 * choices, which ServiceNow's own incident form also labels "Channel". Only the label is
 * "Channel": the field is still `contactType` on the wire (IncidentCreatePayloadDto).
 */
export const CHANNEL_OPTIONS: Array<{ value: IncidentContactType; label: string }> = [
  { value: "SELF_SERVICE", label: "Self-service" },
  { value: "EMAIL", label: "Email" },
  { value: "WALK_IN", label: "Walk-in" },
  { value: "AZURE", label: "Azure" },
  { value: "EMAIL_INTERNAL", label: "Email Internal" },
  { value: "SITE_247", label: "Site 24/7" },
  { value: "DIRECT", label: "Direct" },
  { value: "PHONE", label: "Phone" },
  { value: "SENTINEL", label: "Sentinel" },
  { value: "VIRTUAL_AGENT", label: "Virtual Agent" },
  { value: "CHAT", label: "Chat" },
  { value: "EMAIL_EXTERNAL", label: "Email External" },
];

/**
 * Subcategory options, curated per category rather than one flat list — the backend's
 * subcategory enum has more values than appear here, but the rest don't have an obvious home in
 * Inquiry/Help, Service Interruption, or Security, so this curated picker doesn't surface them
 * (same scope as the webapp's own picker).
 */
export const SUBCATEGORY_OPTIONS_BY_CATEGORY: Record<
  IncidentCategory,
  Array<{ value: IncidentSubcategory; label: string }>
> = {
  INQUIRY: [
    { value: "CONFIG_CHANGE_REQUEST", label: "Config Change Request" },
    { value: "INFORMATION_REQUEST", label: "Information Request" },
  ],
  SERVICE_INTERRUPTION: [
    { value: "FULL_OUTAGE", label: "Full Outage" },
    { value: "PARTIAL_OUTAGE", label: "Partial Outage" },
    { value: "SLOWNESS", label: "Slowness" },
  ],
  SECURITY: [
    { value: "DOS_DDOS", label: "DOS/DDOS" },
    { value: "PRIVILEGE_ESCALATIONS", label: "Privilege Escalations" },
    { value: "THREAT_INTELLIGENCE", label: "Threat Intelligence" },
    { value: "SCANS_AND_PROBES", label: "Scans and Probes" },
    { value: "APPLICATION_SECURITY", label: "Application Security" },
    { value: "PRIVACY", label: "Privacy" },
    { value: "DATA_BREACH", label: "Data Breach" },
    { value: "SYSTEM_COMPROMISES", label: "System Compromises" },
    { value: "MALWARE", label: "Malware" },
    { value: "VULNERABILITY", label: "Vulnerability" },
    { value: "UNAUTHORIZED_ACCESS", label: "Unauthorized Access" },
    { value: "IDENTITY_PROTECTION", label: "Identity Protection" },
    { value: "PHISHING", label: "Phishing" },
    { value: "IMPROPER_CONFIGURATION", label: "Improper Configuration" },
  ],
};

const PRIORITY_MATRIX: Record<IncidentImpact, Record<IncidentUrgency, IncidentPriority>> = {
  HIGH: { HIGH: "CRITICAL", MEDIUM: "HIGH", LOW: "MODERATE" },
  MEDIUM: { HIGH: "HIGH", MEDIUM: "MODERATE", LOW: "LOW" },
  LOW: { HIGH: "MODERATE", MEDIUM: "LOW", LOW: "PLANNING" },
};

/**
 * The standard ITIL impact × urgency → priority matrix (ServiceNow's own out-of-the-box default),
 * used to show a live priority preview on the create form. This is purely a client-side preview —
 * IncidentCreatePayloadDto has no `priority` field; the real value is computed server-side by
 * ServiceNow from the impact/urgency actually submitted. Render the result with
 * incidentPriorityLabel/incidentPriorityColor (./incidentConfig.ts) rather than duplicating a
 * separate label/color map here.
 */
export function computeIncidentPriority(
  impact: IncidentImpact | "",
  urgency: IncidentUrgency | "",
): IncidentPriority | null {
  if (!impact || !urgency) return null;
  return PRIORITY_MATRIX[impact][urgency];
}
