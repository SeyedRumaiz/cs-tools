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
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

const (
	testDeploySysid = "0123456789abcdef0123456789abcdef"
	testDeployProj  = "11111111-2222-3333-4444-555555555555"
	testProductID   = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	testCreatedBy   = "jane.doe@example.com"
	// Far in the past so a test can tell it from the current time.
	testReplyCreatedOn = "2020-01-01 00:00:00"
)

func testCreateDeploymentReq() domain.CreateDeploymentRequest {
	typ := domain.DeploymentTypeDevelopment
	return domain.CreateDeploymentRequest{ProjectID: testDeployProj, Name: "Dev One", Type: &typ, Description: "d"}
}

func testCreateDeployedProductReq() domain.CreateDeployedProductRequest {
	return domain.CreateDeployedProductRequest{
		ProjectID:    testDeployProj,
		DeploymentID: testDeployProj,
		ProductID:    testProductID,
		VersionID:    testProductID,
	}
}

// createHandler serves POST path with body and counts every request that
// reaches the server (any path), so tests can assert no extra call is made.
func createHandler(path, body string, calls *int32) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		if r.URL.Path != path {
			http.Error(w, `{"message":"unexpected path"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	return mux
}

func createReply(key, number string) string {
	d := map[string]string{"id": testDeploySysid, "createdOn": testReplyCreatedOn, "createdBy": testCreatedBy}
	if number != "" {
		d["number"] = number
	}
	b, _ := json.Marshal(map[string]any{"message": "ok", key: d})
	return string(b)
}

func assertCreatedOnIsNow(t *testing.T, got, before, after time.Time) {
	t.Helper()
	if got.Before(before) || got.After(after) || got.Location() != time.UTC {
		t.Fatalf("createdOn = %v (%v), want UTC within [%v, %v]", got, got.Location(), before, after)
	}
}

func TestCreateDeploymentSNFirstDetails_NumberInReply_UsedWithoutExtraCall(t *testing.T) {
	var calls int32
	svc := &snDeploymentService{client: newTestSNClient(t, createHandler("/deployments", createReply("deployment", "DEP0001"), &calls))}
	id, number, by, _, err := svc.createDeploymentSNFirstDetails(t.Context(), testCreateDeploymentReq())
	if err != nil {
		t.Fatal(err)
	}
	if number != "DEP0001" || id != sysidToUUID(testDeploySysid) || by != testCreatedBy {
		t.Fatalf("got id=%s number=%s by=%s", id, number, by)
	}
	if calls != 1 {
		t.Fatalf("expected exactly the create call, got %d requests", calls)
	}
}

func TestCreateDeploymentSNFirstDetails_NoNumber_DownstreamError(t *testing.T) {
	var calls int32
	svc := &snDeploymentService{client: newTestSNClient(t, createHandler("/deployments", createReply("deployment", ""), &calls))}
	_, _, _, _, err := svc.createDeploymentSNFirstDetails(t.Context(), testCreateDeploymentReq())
	var de *apierror.DownstreamError
	if !errors.As(err, &de) {
		t.Fatalf("want DownstreamError, got %T: %v", err, err)
	}
	var ve *apierror.ValidationError
	if errors.As(err, &ve) {
		t.Fatalf("a created-upstream reply without a number must not be a ValidationError: %v", err)
	}
	if strings.Contains(de.Msg, testDeploySysid) {
		t.Fatalf("caller-safe message leaks the upstream id: %q", de.Msg)
	}
	if calls != 1 {
		t.Fatalf("expected no lookup after a missing number, got %d requests", calls)
	}
}

func TestCreateDeploymentSNFirstDetails_CreatedOnIsNowNotReply(t *testing.T) {
	var calls int32
	svc := &snDeploymentService{client: newTestSNClient(t, createHandler("/deployments", createReply("deployment", "DEP0001"), &calls))}
	before := time.Now().UTC()
	_, _, _, createdOn, err := svc.createDeploymentSNFirstDetails(t.Context(), testCreateDeploymentReq())
	after := time.Now().UTC()
	if err != nil {
		t.Fatal(err)
	}
	assertCreatedOnIsNow(t, createdOn, before, after)
}

func TestCreateDeployedProductSNFirstDetails_NumberInReply_Used(t *testing.T) {
	var calls int32
	svc := &snDeployedProductService{client: newTestSNClient(t, createHandler("/deployed-products", createReply("deployedProduct", "IBITM0001"), &calls))}
	id, number, by, _, err := svc.createDeployedProductSNFirstDetails(t.Context(), testCreateDeployedProductReq())
	if err != nil {
		t.Fatal(err)
	}
	if number != "IBITM0001" || id != sysidToUUID(testDeploySysid) || by != testCreatedBy {
		t.Fatalf("got id=%s number=%s by=%s", id, number, by)
	}
	if calls != 1 {
		t.Fatalf("expected exactly the create call, got %d requests", calls)
	}
}

func TestCreateDeployedProductSNFirstDetails_NoNumber_DownstreamError(t *testing.T) {
	var calls int32
	svc := &snDeployedProductService{client: newTestSNClient(t, createHandler("/deployed-products", createReply("deployedProduct", ""), &calls))}
	_, _, _, _, err := svc.createDeployedProductSNFirstDetails(t.Context(), testCreateDeployedProductReq())
	var de *apierror.DownstreamError
	if !errors.As(err, &de) {
		t.Fatalf("want DownstreamError, got %T: %v", err, err)
	}
	var ve *apierror.ValidationError
	if errors.As(err, &ve) {
		t.Fatalf("a created-upstream reply without a number must not be a ValidationError: %v", err)
	}
	if strings.Contains(de.Msg, testDeploySysid) {
		t.Fatalf("caller-safe message leaks the upstream id: %q", de.Msg)
	}
}

func TestCreateDeployedProductSNFirstDetails_CreatedOnIsNowNotReply(t *testing.T) {
	var calls int32
	svc := &snDeployedProductService{client: newTestSNClient(t, createHandler("/deployed-products", createReply("deployedProduct", "IBITM0001"), &calls))}
	before := time.Now().UTC()
	_, _, _, createdOn, err := svc.createDeployedProductSNFirstDetails(t.Context(), testCreateDeployedProductReq())
	after := time.Now().UTC()
	if err != nil {
		t.Fatal(err)
	}
	assertCreatedOnIsNow(t, createdOn, before, after)
}
