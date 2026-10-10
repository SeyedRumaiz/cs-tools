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

// Integration test for incident.special_ops_alert against a real Postgres
// with migration 0207: an incident's assignment group change is recorded by
// the work_item trigger and published by the incident report drainer when
// the new group is a Special Ops team. Skipped unless INCIDENT_REPORT_TEST_DSN
// is set.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/events"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

func TestSpecialOpsAlertIntegration(t *testing.T) {
	pool := incidentReportTestPool(t)
	pub := &alertPublisher{}
	d := NewIncidentReportDrainer(
		repository.NewIncidentReportRepository(repository.NewScoped(pool)),
		WithSpecialOpsAlerts(NewIncidentReportService(), pub, soTeams(t)),
		time.Second, IncidentReportMaxAttempts)
	prSeed(t, pool, d)
	ctx := context.Background()
	drain := func() {
		t.Helper()
		if _, err := d.drainOnce(ctx); err != nil {
			t.Fatalf("drainOnce: %v", err)
		}
	}
	setGroup := func(group, by string) {
		t.Helper()
		irExec(t, pool, `UPDATE work_item SET assignment_group_id = $2, updated_by = $3 WHERE id = $1`, prIncidentID, group, by)
	}

	// To an ordinary group: recorded, acknowledged, nothing published.
	setGroup(groupWSO2SRETeam, "jane.doe@wso2.com")
	drain()
	if len(pub.sent) != 0 || irPendingCountFor(t, pool, prIncidentID) != 0 {
		t.Fatalf("ordinary group: sent %d, pending %d; want none", len(pub.sent), irPendingCountFor(t, pool, prIncidentID))
	}

	// To Choreo Special Ops: one alert, the row done.
	setGroup(groupChoreoSpecialOps, "jane.doe@wso2.com")
	drain()
	if len(pub.sent) != 1 || pub.sent[0].Type != events.TypeIncidentSpecialOpsAlert {
		t.Fatalf("sent = %+v, want one incident.special_ops_alert", pub.sent)
	}
	var got events.IncidentSpecialOpsAlertPayload
	if err := json.Unmarshal(pub.sent[0].Payload, &got); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if got.Number != "INC-PR-0001" || got.Subject != "Choreo gateway 502s" || got.State != "IN_PROGRESS" ||
		got.ServiceID != postResolutionServiceChoreo || got.ServiceName != "Choreo" ||
		got.TeamKey != "choreo-special-ops" || got.AssignmentGroupID != groupChoreoSpecialOps ||
		got.AssignmentGroupName != "Choreo Special Ops" || got.PreviousAssignmentGroupID != groupWSO2SRETeam ||
		got.PreviousAssignmentGroupName != "WSO2 SRE Team" || got.ChangedBy != "jane.doe@wso2.com" || got.ChangedOn == "" {
		t.Errorf("payload = %+v", got)
	}
	if n := irPendingCountFor(t, pool, prIncidentID); n != 0 {
		t.Errorf("pending = %d after the alert, want 0", n)
	}

	// The same group written again is no change: no row, no second alert.
	setGroup(groupChoreoSpecialOps, "someone.else@wso2.com")
	drain()
	if len(pub.sent) != 1 {
		t.Errorf("rewriting the same group sent %d alerts, want still 1", len(pub.sent))
	}

	// A failed publish leaves the row pending, with the attempt counted.
	pub.failWith = errors.New("event hub down")
	setGroup(groupWSO2SRETeam, "x")
	drain()
	setGroup(groupChoreoSpecialOps, "x")
	drain()
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT attempts FROM event_outbox WHERE entity_type = 'incident' AND entity_id = $1 AND published_on IS NULL`,
		prIncidentID).Scan(&attempts); err != nil {
		t.Fatalf("read the failed row: %v", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
}

// Two Special Ops teams sharing one group: the alert names the team the
// handoff's reason note names (read back from the comment table), so the
// right SME rota is paged.
func TestSpecialOpsAlertIntegration_SharedGroupFollowsTheHandoffNote(t *testing.T) {
	pool := incidentReportTestPool(t)
	cfg, err := ParseSpecialistHandoffConfig(`{"products":[
		{"name":"Choreo","serviceIds":["` + postResolutionServiceChoreo + `"],"teams":[
			{"key":"choreo-runtime-team","label":"Choreo Runtime Team","groupId":"` + groupChoreoSpecialOps + `","smeTeam":"choreo-runtime"},
			{"key":"choreo-cloud-team","label":"Choreo Cloud Team","groupId":"` + groupChoreoSpecialOps + `","smeTeam":"cloud-core"}]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	pub := &alertPublisher{}
	d := NewIncidentReportDrainer(
		repository.NewIncidentReportRepository(repository.NewScoped(pool)),
		WithSpecialOpsAlerts(NewIncidentReportService(), pub, cfg),
		time.Second, IncidentReportMaxAttempts)
	prSeed(t, pool, d)
	ctx := repository.WithSystemIdentity(context.Background())

	repo := repository.NewIncidentRepository(repository.NewScoped(pool))
	if _, err := repo.CreateIncidentComment(ctx, prIncidentID, domain.CommentTypeWorkNote,
		`{"reasonCode":"runbook-not-working","reasonDescription":"Runbook doesn't solve the incident","escalationTeam":"choreo-cloud-team"}`,
		"jane.doe@wso2.com"); err != nil {
		t.Fatalf("reason note: %v", err)
	}
	irExec(t, pool, `UPDATE work_item SET assignment_group_id = $2, updated_by = $3 WHERE id = $1`, prIncidentID, groupChoreoSpecialOps, "jane.doe@wso2.com")
	if _, err := d.drainOnce(context.Background()); err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	if len(pub.sent) != 1 {
		t.Fatalf("sent %d alerts, want 1", len(pub.sent))
	}
	var got events.IncidentSpecialOpsAlertPayload
	_ = json.Unmarshal(pub.sent[0].Payload, &got)
	if got.TeamKey != "choreo-cloud-team" || got.SMETeam != "cloud-core" {
		t.Errorf("team %q smeTeam %q, want the picked choreo-cloud-team / cloud-core", got.TeamKey, got.SMETeam)
	}
}

// irPendingCountFor counts id's unpublished incident outbox rows.
func irPendingCountFor(t *testing.T, pool *pgxpool.Pool, id string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM event_outbox WHERE entity_type = 'incident' AND entity_id = $1 AND published_on IS NULL`, id).Scan(&n); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	return n
}
