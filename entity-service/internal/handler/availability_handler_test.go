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
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/service"
)

type ctxCapturingSweeper struct {
	errAtStart error
	deadline   time.Time
	hasDL      bool
}

func (s *ctxCapturingSweeper) Sweep(ctx context.Context, _ time.Time) (service.AvailabilitySweepResult, error) {
	s.errAtStart = ctx.Err()
	s.deadline, s.hasDL = ctx.Deadline()
	return service.AvailabilitySweepResult{Subjects: 1, Rows: 12}, nil
}

// A sweep must not inherit the request's cancellation or its 30-second
// deadline: cutting a run off part-way leaves some offerings recomputed and
// the rest stale. It runs on its own five-minute limit instead.
func TestAvailabilitySweep_DetachedFromRequestDeadline(t *testing.T) {
	sw := &ctxCapturingSweeper{}
	h := NewAvailabilityHandler(sw)

	reqCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	cancel() // the caller has already gone away
	rec := httptest.NewRecorder()
	h.Sweep(rec, httptest.NewRequest(http.MethodPost, "/internal/availability/sweep", nil).WithContext(reqCtx))

	if sw.errAtStart != nil {
		t.Fatalf("sweep started with a cancelled context (%v); it must be detached from the request", sw.errAtStart)
	}
	if !sw.hasDL {
		t.Fatal("sweep context has no deadline; it must keep a limit of its own")
	}
	if left := time.Until(sw.deadline); left < 4*time.Minute || left > availabilitySweepTimeout {
		t.Errorf("sweep deadline is %s away, want about %s", left.Round(time.Second), availabilitySweepTimeout)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}
