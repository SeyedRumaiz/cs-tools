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

package service

import (
	"fmt"
	"strings"
	"time"
)

// formatOutageDuration renders an outage's length for the resolution email.
//
// *** THIS EXISTS BECAUSE THE FIRST LIVE SEND PRINTED
// "Outage Duration: 00:31:25.634362". *** That is a raw Postgres interval
// cast to text, microseconds and all. Nobody wants six decimal places on an
// outage, and no unit test caught it: they asserted against a hand-written
// string, so they only ever agreed with themselves. It took a real message
// in a real inbox.
//
// The repository now hands over whole seconds and the formatting happens
// here, where it is testable.
//
// *** THE FORMAT IS A STATED DIVERGENCE, like the timestamps. *** ServiceNow
// interpolates a glide_duration pill, whose display value depends on the
// instance's settings and the calling user's timezone. That is not
// reproducible from Go and not worth reproducing for a mail that goes to one
// group; this reads the same everywhere.
func formatOutageDuration(totalSeconds int64) string {
	// Zero renders empty rather than "0s". An outage with no duration has
	// not ended, and "0s" would read as one that ended instantly — a wrong
	// statement, where a blank is merely an absent one.
	if totalSeconds <= 0 {
		return ""
	}

	d := time.Duration(totalSeconds) * time.Second
	days := int64(d / (24 * time.Hour))
	hours := int64(d/time.Hour) % 24
	mins := int64(d/time.Minute) % 60
	secs := int64(d/time.Second) % 60

	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if mins > 0 {
		parts = append(parts, fmt.Sprintf("%dm", mins))
	}
	// Seconds only when nothing larger is present. On a two-day outage the
	// trailing seconds are noise; on a 45-second one they are the answer.
	if secs > 0 && days == 0 && hours == 0 {
		parts = append(parts, fmt.Sprintf("%ds", secs))
	}

	// Reachable when the duration is a whole number of hours or days: the
	// seconds branch is suppressed and every other part rounded away. An
	// exact 2h would otherwise return "2h", which is correct — this guards
	// the case where all four are zero yet totalSeconds was positive, which
	// cannot happen today but would silently return "" if the arithmetic
	// above ever changed.
	if len(parts) == 0 {
		return fmt.Sprintf("%ds", totalSeconds)
	}
	return strings.Join(parts, " ")
}
