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

package paging

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PhoneBook supplies the numbers the rota does not hold.
//
// TestCallTo, when set, is every recipient's number. It is how a flow built on
// assumed ranks is exercised without ringing the real people those ranks
// happen to land on - and on a local stack seeded from the real roster sheet,
// they are real people.
type PhoneBook struct {
	// ByEmail maps a lower-cased e-mail to an E.164 number.
	ByEmail map[string]string
	// TestCallTo overrides ByEmail entirely when non-empty.
	TestCallTo string
}

// ParsePhoneBook decodes INCIDENT_ESCALATION_PHONES, a JSON object of e-mail
// to number. Empty yields an empty book.
func ParsePhoneBook(raw, testCallTo string) (PhoneBook, error) {
	pb := PhoneBook{ByEmail: map[string]string{}, TestCallTo: strings.TrimSpace(testCallTo)}
	if strings.TrimSpace(raw) == "" {
		return pb, nil
	}
	var byEmail map[string]string
	if err := json.Unmarshal([]byte(raw), &byEmail); err != nil {
		return PhoneBook{}, fmt.Errorf("escalation: parse phone book: %w", err)
	}
	for email, number := range byEmail {
		pb.ByEmail[strings.ToLower(strings.TrimSpace(email))] = strings.TrimSpace(number)
	}
	return pb, nil
}

// IsEmpty reports whether this book can reach nobody, which would make every
// rung a NO_NUMBER.
func (pb PhoneBook) IsEmpty() bool { return pb.TestCallTo == "" && len(pb.ByEmail) == 0 }

// NumberFor returns the number to ring for an e-mail, or "" when there is none.
func (pb PhoneBook) NumberFor(email string) string {
	if pb.TestCallTo != "" {
		return pb.TestCallTo
	}
	return pb.ByEmail[strings.ToLower(strings.TrimSpace(email))]
}
