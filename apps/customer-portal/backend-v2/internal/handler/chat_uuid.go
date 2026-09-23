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

package handler

import (
	"crypto/rand"
	"fmt"
)

// newLiveChatCaseID mints a fresh RFC 4122 version 4 (random) UUID, used by
// HandleEscalate to generate a brand-new caseId/liveChatId for every new
// live-engineer-chat escalation -- see that method's own doc comment on why
// this is no longer just req.ConversationID.
//
// Implemented directly against crypto/rand rather than pulling in
// github.com/google/uuid: no module in this repo already depends on it (see
// the project's identity-split design notes), and generating 16 random
// bytes and setting two nibbles per RFC 4122 §4.4 is a handful of lines --
// not worth a new dependency for. uuidRe (response.go) already validates
// the result reads back as a UUID everywhere this backend expects one
// (e.g. HandleCreateCase's req.ConversationID check), and this generator
// produces a value matching that same 8-4-4-4-12 lowercase-hex shape.
//
// crypto/rand.Read on a source that cannot supply randomness is a
// programming/environment error this backend cannot recover from (every
// other use of crypto/rand in Go's standard toolchain treats it the same
// way) -- panicking here surfaces that immediately and loudly rather than
// silently minting a low-entropy or all-zero "UUID" that could collide.
func newLiveChatCaseID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("newLiveChatCaseID: crypto/rand unavailable: %v", err))
	}

	// RFC 4122 §4.4: set the version (4) and variant (10xxxxxx) bits.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
