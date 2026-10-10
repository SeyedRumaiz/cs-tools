// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
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

package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"sre-alert-ingestion-service/internal/model"
	"sre-alert-ingestion-service/internal/sources"
	"sre-alert-ingestion-service/internal/transport/auth"
)

type fakeSubmitter struct {
	mu     sync.Mutex
	next   int
	calls  [][]model.Alert
	source string
	err    error
}

func (f *fakeSubmitter) Submit(_ context.Context, source, _ string, alerts []model.Alert) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, alerts)
	f.source = source
	if f.err != nil {
		return nil, f.err
	}
	ids := make([]string, len(alerts))
	for i := range alerts {
		f.next++
		ids[i] = fmt.Sprintf("ALT%09d", f.next)
	}
	return ids, nil
}

type recordingRejects struct {
	mu   sync.Mutex
	list []Rejection
}

func (r *recordingRejects) Rejected(rej Rejection) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list = append(r.list, rej)
}

func newIngestServer(t *testing.T, sub Submitter, rejects RejectNotifier) *Server {
	t.Helper()
	for _, v := range []string{"AWS", "AZURE", "DATADOG", "ELASTICSEARCH", "GCP", "ICINGA", "OPENOBSERVE", "OPENSEARCH", "PROMETHEUS", "SITE24X7"} {
		t.Setenv(v+"_ALERT_CONFIG", "")
	}
	reg, err := sources.New()
	if err != nil {
		t.Fatal(err)
	}
	return New(Options{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Auth:         auth.None{},
		Pipeline:     NewIngestor(reg, sub, time.Second),
		Rejects:      rejects,
		Sources:      reg.Names(),
		MaxBodyBytes: 4096,
	})
}

const datadogBody = `{"event_name":"prod-web high memory usage","transition":"Triggered","alert_id":"148502937","tags":"env:production,severity:1"}`

const prometheusBatch = `{"receiver":"sre","status":"firing","alerts":[
 {"status":"firing","labels":{"alertname":"A","severity":"critical"},"fingerprint":"f1"},
 {"status":"firing","labels":{"alertname":"B","severity":"warning"},"fingerprint":"f2"},
 {"status":"firing","labels":{"alertname":"C","severity":"critical"},"fingerprint":"f3"}]}`

func TestIngest_201(t *testing.T) {
	sub := &fakeSubmitter{}
	rec := do(t, newIngestServer(t, sub, nil), "POST", SourceRoutePrefix+"datadog", datadogBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if got, want := strings.TrimSpace(rec.Body.String()), `{"status":"OK","alt_id":"ALT000000001"}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
	if len(sub.calls) != 1 || sub.source != "datadog" || sub.calls[0][0].UniqueIdentifier != "148502937" {
		t.Errorf("submitted %+v for %s", sub.calls, sub.source)
	}
}

func TestIngest_PrometheusBatchSubmittedTogether(t *testing.T) {
	sub := &fakeSubmitter{}
	rec := do(t, newIngestServer(t, sub, nil), "POST", SourceRoutePrefix+"prometheus", prometheusBatch)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	m := decode(t, rec)
	if m["status"] != "OK" {
		t.Errorf("body = %v", m)
	}
	if len(sub.calls) != 1 || len(sub.calls[0]) != 3 {
		t.Errorf("want one submission of 3 alerts, got %d submissions", len(sub.calls))
	}
}

func TestIngest_ServiceNowForwardStoresCanonicalAlerts(t *testing.T) {
	sub := &fakeSubmitter{}
	body := `[{"source":"AWS","severity":"Critical","unique_identifier":"a1"},{"source":"Azure","severity":"OK","unique_identifier":"z1"}]`
	rec := do(t, newIngestServer(t, sub, nil), "POST", SourceRoutePrefix+"servicenow", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if m := decode(t, rec); m["status"] != "OK" {
		t.Errorf("body = %v", m)
	}
	if sub.source != "servicenow" || len(sub.calls) != 1 || sub.calls[0][0].Source != "AWS" || sub.calls[0][1].UniqueIdentifier != "z1" {
		t.Errorf("submitted %+v for %s", sub.calls, sub.source)
	}
}

func TestIngest_PrometheusBatch503WhenAnyFails(t *testing.T) {
	sub := &fakeSubmitter{err: errors.New("alert could not be stored")}
	rec := do(t, newIngestServer(t, sub, nil), "POST", SourceRoutePrefix+"prometheus", prometheusBatch)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "60" {
		t.Fatalf("status = %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestIngest_400NeverSubmitsAndReportsRejection(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"not json":         "hello",
		"structurally bad": `{"transition":"Triggered"}`, // datadog needs an event or monitor id/name
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			sub := &fakeSubmitter{}
			rejects := &recordingRejects{}
			s := newIngestServer(t, sub, rejects)
			req := httptest.NewRequest("POST", SourceRoutePrefix+"datadog", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(RequestIDHeader, "req-400")
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if m := decode(t, rec); m["status"] != "rejected" || !strings.Contains(m["error"].(string), "datadog") {
				t.Errorf("body = %v, want the transform's error", m)
			}
			if len(sub.calls) != 0 {
				t.Error("a rejected payload must never be submitted (it would claim an id)")
			}
			if len(rejects.list) != 1 {
				t.Fatalf("rejections = %d, want 1", len(rejects.list))
			}
			r := rejects.list[0]
			if r.Source != "datadog" || r.Status != 400 || r.RequestID != "req-400" || r.ContentType != "application/json" ||
				r.Route != SourceRoutePrefix+"datadog" || string(r.Body) != body || r.BodySize != int64(len(body)) || r.SourceTotal != 1 {
				t.Errorf("rejection = %+v", r)
			}
		})
	}
}

func TestIngest_400KeepsOnlyThePreview(t *testing.T) {
	rejects := &recordingRejects{}
	body := "not json " + strings.Repeat("x", 3000)
	rec := do(t, newIngestServer(t, &fakeSubmitter{}, rejects), "POST", SourceRoutePrefix+"datadog", body)
	if rec.Code != http.StatusBadRequest || len(rejects.list) != 1 {
		t.Fatalf("status = %d, rejections = %d", rec.Code, len(rejects.list))
	}
	r := rejects.list[0]
	if len(r.Body) != logPreviewChars || string(r.Body) != body[:logPreviewChars] || r.BodySize != int64(len(body)) {
		t.Errorf("rejection body = %d bytes, size = %d; want the %d-char preview and the full size %d",
			len(r.Body), r.BodySize, logPreviewChars, len(body))
	}
}

func TestIngest_413ReportsRejectionWithoutSubmitting(t *testing.T) {
	sub := &fakeSubmitter{}
	rejects := &recordingRejects{}
	rec := do(t, newIngestServer(t, sub, rejects), "POST", SourceRoutePrefix+"aws", strings.Repeat("x", 5000))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	if len(sub.calls) != 0 || len(rejects.list) != 1 || rejects.list[0].Status != 413 || rejects.list[0].BodySize != 5000 {
		t.Errorf("calls = %d, rejections = %+v", len(sub.calls), rejects.list)
	}
}

func TestIngest_404And405(t *testing.T) {
	s := newIngestServer(t, &fakeSubmitter{}, nil)
	if rec := do(t, s, "POST", SourceRoutePrefix+"splunk", "{}"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown source = %d, want 404", rec.Code)
	}
	if rec := do(t, s, "PUT", SourceRoutePrefix+"aws", "{}"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT = %d, want 405", rec.Code)
	}
}

func TestIngest_503QueueFull(t *testing.T) {
	sub := &fakeSubmitter{err: errors.New("alert queue full")}
	rec := do(t, newIngestServer(t, sub, nil), "POST", SourceRoutePrefix+"datadog", datadogBody)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if m := decode(t, rec); m["status"] != "unavailable" || m["error"] != "alert queue full" {
		t.Errorf("body = %v", m)
	}
}

func TestIngest_RejectionCountIsPerSource(t *testing.T) {
	rejects := &recordingRejects{}
	s := newIngestServer(t, &fakeSubmitter{}, rejects)
	do(t, s, "POST", SourceRoutePrefix+"datadog", "x")
	do(t, s, "POST", SourceRoutePrefix+"datadog", "x")
	do(t, s, "POST", SourceRoutePrefix+"opensearch", "x")
	got := []int64{rejects.list[0].SourceTotal, rejects.list[1].SourceTotal, rejects.list[2].SourceTotal}
	if got[0] != 1 || got[1] != 2 || got[2] != 1 {
		t.Errorf("per-source totals = %v, want [1 2 1]", got)
	}
}

type fakeConfirmer struct{ calls int }

func (f *fakeConfirmer) HandleIfConfirmation(raw []byte) bool {
	if !strings.Contains(string(raw), `"SubscriptionConfirmation"`) {
		return false
	}
	f.calls++
	return true
}

func TestIngest_SNSConfirmationAnswers200WithoutStoring(t *testing.T) {
	sub := &fakeSubmitter{}
	confirmer := &fakeConfirmer{}
	for _, v := range []string{"AWS", "AZURE", "DATADOG", "ELASTICSEARCH", "GCP", "ICINGA", "OPENOBSERVE", "OPENSEARCH", "PROMETHEUS", "SITE24X7"} {
		t.Setenv(v+"_ALERT_CONFIG", "")
	}
	reg, err := sources.New()
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Auth: auth.None{},
		Pipeline: NewIngestor(reg, sub, time.Second).WithSNSConfirmer(confirmer),
		Sources:  reg.Names(), MaxBodyBytes: 4096,
	})

	rec := do(t, s, "POST", SourceRoutePrefix+"aws", `{"Type":"SubscriptionConfirmation","SubscribeURL":"https://sns.us-east-1.amazonaws.com/x"}`)
	if rec.Code != http.StatusOK || len(sub.calls) != 0 {
		t.Fatalf("status = %d, submits = %d; want 200 and nothing stored", rec.Code, len(sub.calls))
	}
	if confirmer.calls != 1 {
		t.Errorf("confirmer calls = %d, want 1", confirmer.calls)
	}

	rec = do(t, s, "POST", SourceRoutePrefix+"aws", `{"Type":"Notification","Message":"{\"AlarmName\":\"a\",\"AlarmArn\":\"arn\",\"NewStateValue\":\"ALARM\"}"}`)
	if rec.Code != http.StatusCreated || len(sub.calls) != 1 {
		t.Errorf("notification: status = %d, submits = %d; want 201 and stored", rec.Code, len(sub.calls))
	}
}
