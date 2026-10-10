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

package availability

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type stubSweeper struct {
	res  SweepResult
	err  error
	runs int
}

func (s *stubSweeper) Sweep(context.Context) (SweepResult, error) {
	s.runs++
	return s.res, s.err
}

func TestRecalculateAvailability_HealthyRun(t *testing.T) {
	s := &stubSweeper{res: SweepResult{Subjects: 146, Rows: 1168}}
	if err := RecalculateAvailability(s)(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.runs != 1 {
		t.Errorf("sweeps = %d, want 1", s.runs)
	}
}

// *** THE TEST THAT MATTERS. *** entity-service returns 200 having skipped
// subjects, so a task that only checked the error would report success while
// a cloud's uptime froze at yesterday's figure — invisible until a customer
// asks why the status page has not moved.
func TestRecalculateAvailability_PartialFailureFailsTheTask(t *testing.T) {
	s := &stubSweeper{res: SweepResult{Subjects: 146, Rows: 1100, Failed: 6}}
	err := RecalculateAvailability(s)(context.Background())
	if err == nil {
		t.Fatal("a sweep that skipped 6 subjects reported success")
	}
	if !strings.Contains(err.Error(), "6 of 146") {
		t.Errorf("error does not say how many failed: %v", err)
	}
}

// Zero subjects is what an unmapped service_offering_commitment looks like,
// and it is indistinguishable from a clean run unless it is called out.
func TestRecalculateAvailability_NoSubjectsIsAFailure(t *testing.T) {
	s := &stubSweeper{res: SweepResult{}}
	err := RecalculateAvailability(s)(context.Background())
	if err == nil {
		t.Fatal("a sweep that found no subjects at all reported success")
	}
	if !strings.Contains(err.Error(), "service_offering_commitment") {
		t.Errorf("error does not name the likely cause: %v", err)
	}
}

func TestRecalculateAvailability_TransportErrorPropagates(t *testing.T) {
	want := errors.New("boom")
	s := &stubSweeper{err: want}
	if err := RecalculateAvailability(s)(context.Background()); !errors.Is(err, want) {
		t.Fatalf("error = %v, want it to wrap %v", err, want)
	}
}
