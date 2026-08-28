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

// Mirrors csm-portal/backend's internal/handler/chat.go chatEvent — the
// single JSON envelope published on GET /chat/alerts/stream (event name
// "chat_alert") and, separately, pushed to customer-portal/backend-v2's
// /internal/chat-events. Not every field applies to every Type; see below.
export type ChatAlertType =
  | "customer_escalation"
  | "session_accepted"
  | "customer_message"
  | "session_closed";

export type ChatAlertEvent = {
  type: ChatAlertType;
  /** Present on every type — the case this escalation created. */
  caseId?: string;
  /** Present on every type — the Novera conversation this escalation was raised from. */
  conversationId?: string;
  /** Set on customer_escalation only. */
  projectId?: string;
  /** Set on customer_escalation only — always "Live engineer requested via Novera chat" today. */
  subject?: string;
  /** Set on customer_escalation only. */
  customerEmail?: string;
  /** Set on customer_escalation only — display label, not an identity. */
  customerName?: string;
  /** Set on session_accepted/session_closed — the engineer who accepted/ended the session. */
  engineerEmail?: string;
  /** Set on customer_escalation (opening text) and customer_message. */
  message?: string;
  timestamp: string;
};
