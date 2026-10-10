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

package dto

import (
	"encoding/json"
	"testing"

	"github.com/wso2-open-operations/cs-tools/apps/customer-portal/backend-v2/internal/entity"
)

// entityAttachmentSearchPayload is a verbatim-shaped POST /attachments/search
// response from entity-service, including the fields backend-v2 does not mirror
// (createdByUser, hasMore) so the lenient-decode assumption is exercised too.
const entityAttachmentSearchPayload = `{
  "attachments": [
    {
      "id": "470cac1d-3bfa-8710-3e1e-088aa4e45a89",
      "referenceId": "2b51f79d-3be6-8790-9140-4c6aa5e45a07",
      "referenceType": "case",
      "name": "diagnostics.zip",
      "type": "application/zip",
      "sizeBytes": 20481,
      "description": null,
      "createdBy": {
        "id": "a1b2c3d4",
        "name": "Jane Doe",
        "userId": "u-99",
        "email": "jane@example.com"
      },
      "createdByUser": {"id": "a1b2c3d4", "email": "jane@example.com", "name": "Jane Doe"},
      "createdOn": "2026-08-18T06:45:59Z",
      "downloadUrl": "https://example.invalid/d/470cac1d",
      "previewUrl": null
    },
    {
      "id": "72de20d9-eb7e-8710-fcf5-f5dabad0cd22",
      "referenceId": "2b51f79d-3be6-8790-9140-4c6aa5e45a07",
      "referenceType": "case",
      "name": "thread-dump.txt",
      "type": "text/plain",
      "sizeBytes": 774,
      "description": "second upload",
      "createdBy": {"email": "ops@example.com"},
      "createdByUser": null,
      "createdOn": "2026-08-18T06:51:32Z",
      "downloadUrl": null,
      "previewUrl": null
    }
  ],
  "total": 2,
  "limit": 10,
  "offset": 0,
  "hasMore": false
}`

// TestAttachmentSearch_DecodesObjectCreatedBy is the regression guard for the
// 500 on GET /cases/{id}/attachments.
//
// entity-service emits createdBy as a domain.UserRef *object* on the search item
// (unlike AttachmentDetail.createdBy, which genuinely is a string). backend-v2
// declared it `string`, so json.Unmarshal failed with "cannot unmarshal object
// into Go value of type string", aborting the entire decode — entity-service
// logged 200 while the portal showed "Failed to retrieve case attachments."
func TestAttachmentSearch_DecodesObjectCreatedBy(t *testing.T) {
	var resp entity.SearchAttachmentsResponse
	if err := json.Unmarshal([]byte(entityAttachmentSearchPayload), &resp); err != nil {
		t.Fatalf("decoding entity-service's real payload failed: %v", err)
	}
	if len(resp.Attachments) != 2 {
		t.Fatalf("decoded %d attachments, want 2", len(resp.Attachments))
	}
	if got := resp.Attachments[0].CreatedBy.Name; got != "Jane Doe" {
		t.Errorf("CreatedBy.Name = %q, want %q", got, "Jane Doe")
	}
	if got := resp.Attachments[0].CreatedBy.Email; got != "jane@example.com" {
		t.Errorf("CreatedBy.Email = %q, want %q", got, "jane@example.com")
	}
}

// TestMapCaseAttachments_FlattensCreatedByToEmail checks the portal contract
// stays a plain string holding the uploader's email (the frontend's owner check
// compares it to the signed-in user's email), so the upstream object must not
// leak through.
func TestMapCaseAttachments_FlattensCreatedByToEmail(t *testing.T) {
	var resp entity.SearchAttachmentsResponse
	if err := json.Unmarshal([]byte(entityAttachmentSearchPayload), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mapped := MapCaseAttachments(resp)

	if len(mapped.Attachments) != 2 {
		t.Fatalf("mapped %d attachments, want 2", len(mapped.Attachments))
	}
	// Resolved uploader: the email wins even when a name is present.
	if got := mapped.Attachments[0].CreatedBy; got != "jane@example.com" {
		t.Errorf("createdBy = %q, want %q", got, "jane@example.com")
	}
	// Uploader with an email but no name upstream.
	if got := mapped.Attachments[1].CreatedBy; got != "ops@example.com" {
		t.Errorf("createdBy = %q, want %q", got, "ops@example.com")
	}

	// createdBy must serialise as a JSON string, never an object.
	b, err := json.Marshal(mapped.Attachments[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
		t.Fatalf("probe: %v", err)
	}
	var asString string
	if err := json.Unmarshal(probe["createdBy"], &asString); err != nil {
		t.Errorf("createdBy is not a JSON string (%s); the frontend renders it directly", probe["createdBy"])
	}
}

// TestMapCaseAttachments_EmitsTotalRecords keeps the pagination key the
// attachments hook depends on — it destructures totalRecords with no
// array-length fallback.
func TestMapCaseAttachments_EmitsTotalRecords(t *testing.T) {
	var resp entity.SearchAttachmentsResponse
	if err := json.Unmarshal([]byte(entityAttachmentSearchPayload), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	b, err := json.Marshal(MapCaseAttachments(resp))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var probe struct {
		TotalRecords *int `json:"totalRecords"`
		Total        *int `json:"total"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if probe.TotalRecords == nil || *probe.TotalRecords != 2 {
		t.Errorf("totalRecords = %v, want 2", probe.TotalRecords)
	}
	if probe.Total != nil {
		t.Errorf("legacy \"total\" key still emitted: %v", *probe.Total)
	}
}

// TestAttachmentCreatedByIdentity covers the email-first rule and its fallback
// to the name when the email is empty.
func TestAttachmentCreatedByIdentity(t *testing.T) {
	cases := []struct {
		name string
		in   entity.UserRef
		want string
	}{
		{"email and name", entity.UserRef{Name: "Jane Doe", Email: " Jane@Example.com "}, "Jane@Example.com"},
		{"email only", entity.UserRef{Email: "ops@example.com"}, "ops@example.com"},
		{"empty email falls back to name", entity.UserRef{Name: " Jane Doe ", Email: "  "}, "Jane Doe"},
		{"both empty", entity.UserRef{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := attachmentCreatedByIdentity(c.in); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
