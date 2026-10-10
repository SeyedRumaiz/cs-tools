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
	"context"
	"testing"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

// pgNotesRepo records the comments DATA_SOURCE=postgres writes for an incident update. (Creating an
// incident on Postgres is covered by incident_create_portal_test.go.)
type pgNotesRepo struct {
	stubIncidentRepo
	comments []domain.CommentType
}

func newPGNotesRepo() *pgNotesRepo {
	r := &pgNotesRepo{}
	r.createIncidentComment = func(_ context.Context, _ string, kind domain.CommentType, _, _ string) (domain.CaseComment, error) {
		r.comments = append(r.comments, kind)
		return domain.CaseComment{}, nil
	}
	r.getIncidentByID = func(_ context.Context, id string) (domain.IncidentView, error) { return newTestIncidentView(id), nil }
	return r
}

// sre-alert-core-service pushes each later alert as a work note (PATCH /incidents/{id}); with no
// ServiceNow behind it, the note is written and nothing is mirrored.
func TestIncidentService_PostgresUpdateWritesWorkNotes(t *testing.T) {
	repo := newPGNotesRepo()
	svc := NewIncidentServiceWithPublisher(repo, nil, &mockEventPublisher{})
	note := "Duplicate alert ALT000000002"
	resp, err := svc.UpdateIncident(context.Background(), domain.UpdateIncidentRequest{ID: testDeploymentUUID, WorkNotes: &note})
	if err != nil {
		t.Fatalf("UpdateIncident: %v", err)
	}
	if resp.Incident.ID == nil || len(repo.comments) != 1 || repo.comments[0] != domain.CommentTypeWorkNote {
		t.Errorf("comments = %v, want one work note", repo.comments)
	}
}
