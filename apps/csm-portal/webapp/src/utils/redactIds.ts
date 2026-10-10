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

const UUID_PATTERN = "[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}";
const UUID_RE = new RegExp(UUID_PATTERN, "gi");

/**
 * Replaces raw UUIDs in a backend validation message with a human phrase.
 * Backend 4xx messages name the offending record by id, which is what makes
 * them actionable in logs but meaningless to the person reading a toast.
 * `noun` is what the id stands for in context ("user"): a leading "<noun> <id>"
 * becomes "That <noun>" and any other bare id becomes "that <noun>", so
 * "user 94a1... is not a contact on this case's project" reads
 * "That user is not a contact on this case's project".
 */
export function replaceUuids(message: string, noun: string): string {
  const withNoun = new RegExp(`\\b${noun}\\s+${UUID_PATTERN}`, "gi");
  const out = message.replace(withNoun, `that ${noun}`).replace(UUID_RE, `that ${noun}`);
  // Capitalise only a sentence that now opens with the replacement phrase; any
  // other message keeps its original first character (e.g. a field name).
  return out.startsWith(`that ${noun}`) ? out.charAt(0).toUpperCase() + out.slice(1) : out;
}
