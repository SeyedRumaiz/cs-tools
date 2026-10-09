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

package assistant

import (
	"context"
	"strings"
	"time"
)

// Mock is a stand-in Provider for local development when no model is
// reachable. It streams a fixed reply word by word.
type Mock struct {
	// Delay is the pause between words; zero sends them at once.
	Delay time.Duration
}

// Answer implements Provider.
func (m Mock) Answer(ctx context.Context, turn Turn, emit func(Event)) {
	emit(Event{Type: EventStatus, Text: "Looking into it"})
	reply := "This is a placeholder answer from the local mock assistant. You asked: \"" +
		turn.Message + "\". Choose Chat with an Engineer to talk to a person."
	for _, word := range strings.SplitAfter(reply, " ") {
		select {
		case <-ctx.Done():
			emit(Event{Type: EventError, Text: "The answer was interrupted."})
			return
		case <-time.After(m.Delay):
		}
		emit(Event{Type: EventToken, Text: word})
	}
	emit(Event{Type: EventDone, Text: reply})
}
