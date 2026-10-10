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

package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	csm "github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/middleware"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/plg/entityclient"
)

const correlationHeader = "X-CSM-Correlation-ID"

// headerCapture holds a header value written on the test server's goroutine and
// read on the test's own.
//
// The HTTP round trip does order those two in practice — net/http hands the
// response to the caller over a channel, and httptest tracks connections under a
// mutex — but that ordering is an implementation detail of the transport, not
// something the Go memory model promises. This backend's `make test` runs
// `go test -race` and `make build` depends on it, so an intermittent report here
// would block builds for a reason nobody would find quickly. A mutex is cheaper
// than relying on an edge we do not control.
type headerCapture struct {
	mu    sync.Mutex
	value string
}

func (c *headerCapture) set(v string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value = v
}

func (c *headerCapture) get() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

// callThrough runs one request through the real csm CorrelationID middleware and
// the PLG forwarder, then — from inside the handler, as a PLG handler would —
// makes an actual entity-service call through the real client. It returns the
// correlation header the stubbed entity service received.
//
// Asserting on the HEADER THAT ARRIVES, rather than on a context value, is what
// makes this a regression test. The bug was that two packages used different
// context keys: the id was present in the request context the whole time, just
// not where the entity client looked for it. Only the wire shows that.
//
// csm.CorrelationID is the production middleware, not a stand-in, for the same
// reason — setting the key by hand would pass against the broken code.
func callThrough(t *testing.T, requestHeader string, wrap bool) (receivedByEntity, generatedByCsm string) {
	t.Helper()

	var capture headerCapture
	entity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.set(r.Header.Get(correlationHeader))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"products":[]}`))
	}))
	t.Cleanup(entity.Close)

	client := entityclient.New(entityclient.Config{BaseURL: entity.URL})

	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		generatedByCsm = csm.CorrelationIDFromContext(r.Context())
		if _, err := client.ListProducts(r.Context()); err != nil {
			t.Errorf("entity call failed: %v", err)
		}
	})

	var handler http.Handler = inner
	if wrap {
		handler = ForwardCorrelationID(inner)
	}
	handler = csm.CorrelationID(handler)

	req := httptest.NewRequest(http.MethodGet, "/plg/products", nil)
	if requestHeader != "" {
		req.Header.Set(correlationHeader, requestHeader)
	}
	handler.ServeHTTP(httptest.NewRecorder(), req)
	return capture.get(), generatedByCsm
}

// TestForwardCorrelationID_ReachesEntityService is the regression test: the
// caller's id must arrive at entity-service unchanged.
func TestForwardCorrelationID_ReachesEntityService(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"
	received, _ := callThrough(t, id, true)
	if received != id {
		t.Errorf("entity service received correlation id %q, want %q", received, id)
	}
}

// TestForwardCorrelationID_ForwardsAGeneratedID covers the ordinary case: a
// browser sends no header, csm's middleware mints one, and that value must
// travel onward — otherwise entity-service mints a second, unrelated id and the
// two services cannot be joined.
func TestForwardCorrelationID_ForwardsAGeneratedID(t *testing.T) {
	received, generated := callThrough(t, "", true)
	if generated == "" {
		t.Fatal("csm middleware generated no id; this test would prove nothing")
	}
	if received != generated {
		t.Errorf("entity service received %q, want the generated %q", received, generated)
	}
}

// TestWithoutTheForwarder_NothingArrives pins the bug this fixes.
//
// Running the identical chain WITHOUT ForwardCorrelationID must leave the header
// absent. If this ever starts passing, something else is forwarding the id and
// the middleware is redundant — worth knowing either way.
func TestWithoutTheForwarder_NothingArrives(t *testing.T) {
	received, generated := callThrough(t, "aaaa-bbbb", false)
	if generated == "" {
		t.Fatal("csm middleware put no id in context; the fixture is wrong")
	}
	if received != "" {
		t.Errorf("without the forwarder the header should be absent, got %q", received)
	}
}

// TestForwardCorrelationID_DoesNotFabricateAnID pins the deliberate
// non-behaviour.
//
// With no id in context the request must pass through untouched. csm-portal's
// own middleware has already generated one by the time PLG's chain runs, so an
// empty value here means the chain was assembled wrongly — and inventing an id
// would produce a plausible-looking trace that joins to nothing, hiding the
// misconfiguration instead of surfacing it.
func TestForwardCorrelationID_DoesNotFabricateAnID(t *testing.T) {
	var capture headerCapture
	entity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.set(r.Header.Get(correlationHeader))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"products":[]}`))
	}))
	defer entity.Close()

	client := entityclient.New(entityclient.Config{BaseURL: entity.URL})
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if _, err := client.ListProducts(r.Context()); err != nil {
			t.Errorf("entity call failed: %v", err)
		}
	})

	// The forwarder alone, with NO csm.CorrelationID in front of it.
	req := httptest.NewRequest(http.MethodGet, "/plg/products", nil)
	ForwardCorrelationID(inner).ServeHTTP(httptest.NewRecorder(), req)

	if got := capture.get(); got != "" {
		t.Errorf("no id was available, so no header should have been sent; got %q", got)
	}
}
