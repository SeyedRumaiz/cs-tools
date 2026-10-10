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
	"fmt"
	"log/slog"
)

// Sweeper is the subset of *Client this task depends on.
type Sweeper interface {
	Sweep(ctx context.Context) (SweepResult, error)
}

// RecalculateAvailability returns the task handler.
//
// *** A PARTIAL RUN IS REPORTED, NOT SWALLOWED. *** entity-service keeps
// going when one subject fails, so a sweep can return 200 having skipped
// offerings. Returning nil on that would make the task look healthy while a
// cloud's uptime silently froze at yesterday's number — the exact failure
// this whole port exists to prevent somebody discovering from a customer.
// So any failure count fails the task, which routes it to the alert
// recipients like any other.
func RecalculateAvailability(sweeper Sweeper) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		res, err := sweeper.Sweep(ctx)
		if err != nil {
			return fmt.Errorf("availability: sweep: %w", err)
		}

		slog.InfoContext(ctx, "availability sweep complete",
			"subjects", res.Subjects, "rows", res.Rows, "failed", res.Failed)

		if res.Failed > 0 {
			return fmt.Errorf("availability: %d of %d subjects failed to recalculate",
				res.Failed, res.Subjects)
		}
		// Zero subjects is not success. It is what a missing
		// service_offering_commitment mapping looks like, and it would
		// otherwise read as a clean run that wrote nothing.
		if res.Subjects == 0 {
			return fmt.Errorf("availability: no subjects found; " +
				"service_offering_commitment is probably unmapped or empty")
		}
		return nil
	}
}
