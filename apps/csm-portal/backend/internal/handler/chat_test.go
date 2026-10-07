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
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/sdk-go/routingclient"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/stream"
)

// ----- mock entityChatClient -----

type mockChatEntityClient struct {
	patchCaseFn func(ctx context.Context, caseID string, body []byte) ([]byte, error)
}

func (m *mockChatEntityClient) PatchCase(ctx context.Context, caseID string, body []byte) ([]byte, error) {
	if m.patchCaseFn != nil {
		return m.patchCaseFn(ctx, caseID, body)
	}
	return []byte(`{}`), nil
}

// ----- mock routingService -----

type mockRoutingService struct {
	findOpenChatFn  func(ctx context.Context, customerEmail, projectID string) (*routingclient.OpenChat, error)
	completedFn     func(ctx context.Context, userID, caseID string) (routingclient.CompletedResult, error)
	acceptFn        func(ctx context.Context, userID, caseID string) (routingclient.AcceptResult, error)
	escalateFn      func(ctx context.Context, ci routingclient.CaseInfo) (routingclient.EscalateResult, error)
	getCaseInfoFn   func(ctx context.Context, caseID string) (routingclient.CaseInfo, error)
	convertToCaseFn func(ctx context.Context, userID, caseID, entityCaseID string) (routingclient.ConvertToCaseResult, error)
}

func (m *mockRoutingService) Escalate(ctx context.Context, ci routingclient.CaseInfo) (routingclient.EscalateResult, error) {
	if m.escalateFn != nil {
		return m.escalateFn(ctx, ci)
	}
	return routingclient.EscalateResult{}, nil
}

func (m *mockRoutingService) SetPresence(ctx context.Context, userID string, status routingclient.Status) (routingclient.PresenceResult, error) {
	return routingclient.PresenceResult{}, nil
}

func (m *mockRoutingService) Completed(ctx context.Context, userID, caseID string) (routingclient.CompletedResult, error) {
	if m.completedFn != nil {
		return m.completedFn(ctx, userID, caseID)
	}
	return routingclient.CompletedResult{}, nil
}

func (m *mockRoutingService) Decline(ctx context.Context, userID, caseID string) (routingclient.DeclineResult, error) {
	return routingclient.DeclineResult{}, nil
}

func (m *mockRoutingService) Accept(ctx context.Context, userID, caseID string) (routingclient.AcceptResult, error) {
	if m.acceptFn != nil {
		return m.acceptFn(ctx, userID, caseID)
	}
	return routingclient.AcceptResult{}, nil
}

func (m *mockRoutingService) GetPresence(ctx context.Context, userID string) (routingclient.PresenceDetail, error) {
	return routingclient.PresenceDetail{}, nil
}

func (m *mockRoutingService) SweepTimeouts(ctx context.Context) (routingclient.SweepResult, error) {
	return routingclient.SweepResult{}, nil
}

func (m *mockRoutingService) SetMaxConcurrentChats(ctx context.Context, userID string, max int) (routingclient.SetCapacityResult, error) {
	return routingclient.SetCapacityResult{}, nil
}

func (m *mockRoutingService) CreateWorkItem(ctx context.Context, ci routingclient.CaseInfo) error {
	return nil
}

func (m *mockRoutingService) AddComment(ctx context.Context, caseID, authorEmail, content string) error {
	return nil
}

func (m *mockRoutingService) GetCaseInfo(ctx context.Context, caseID string) (routingclient.CaseInfo, error) {
	if m.getCaseInfoFn != nil {
		return m.getCaseInfoFn(ctx, caseID)
	}
	return routingclient.CaseInfo{CaseID: caseID}, nil
}

func (m *mockRoutingService) ConvertToCase(ctx context.Context, userID, caseID, entityCaseID string) (routingclient.ConvertToCaseResult, error) {
	if m.convertToCaseFn != nil {
		return m.convertToCaseFn(ctx, userID, caseID, entityCaseID)
	}
	return routingclient.ConvertToCaseResult{}, nil
}

func (m *mockRoutingService) FindOpenChat(ctx context.Context, customerEmail, projectID string) (*routingclient.OpenChat, error) {
	if m.findOpenChatFn != nil {
		return m.findOpenChatFn(ctx, customerEmail, projectID)
	}
	return nil, nil
}

func (m *mockRoutingService) EndByTenant(ctx context.Context, caseID, tenantSlug string) (routingclient.CompletedResult, error) {
	return routingclient.CompletedResult{}, nil
}

// ----- mock ChatEventPusher -----

type mockChatEventPusher struct {
	pushEventFn  func(ctx context.Context, payload []byte) error
	createCaseFn func(ctx context.Context, payload []byte, userIDToken string) ([]byte, error)
	pushedEvents [][]byte
}

func (m *mockChatEventPusher) PushEvent(ctx context.Context, payload []byte) error {
	m.pushedEvents = append(m.pushedEvents, payload)
	if m.pushEventFn != nil {
		return m.pushEventFn(ctx, payload)
	}
	return nil
}

func (m *mockChatEventPusher) CreateCase(ctx context.Context, payload []byte, userIDToken string) ([]byte, error) {
	if m.createCaseFn != nil {
		return m.createCaseFn(ctx, payload, userIDToken)
	}
	return []byte(`{"entityCaseId":"cs-1"}`), nil
}

// newTestChatHandler builds a ChatHandler wired to the given mocks, with a
// real BroadcastHub (cheap, no network) and a "customer-portal" notifier
// registered under defaultNotifySource -- every test overrides only what it
// needs via the mocks' *Fn fields.
func newTestChatHandler(routing *mockRoutingService, notifier *mockChatEventPusher) *ChatHandler {
	return NewChatHandler(
		&mockChatEntityClient{},
		stream.NewBroadcastHub(),
		map[string]ChatEventPusher{defaultNotifySource: notifier},
		routing,
	)
}

const testConvertCaseID = "11111111-1111-1111-1111-111111111111"

func TestHandleConvertToCase(t *testing.T) {
	t.Run("unauthenticated returns 401", func(t *testing.T) {
		h := newTestChatHandler(&mockRoutingService{}, &mockChatEventPusher{})
		r := httptest.NewRequest(http.MethodPost, "/chat/sessions/"+testConvertCaseID+"/convert-to-case", nil)
		r.SetPathValue("id", testConvertCaseID)
		w := httptest.NewRecorder()

		h.HandleConvertToCase(w, r)

		assertStatus(t, w, http.StatusUnauthorized)
		assertErrorMessage(t, w, ErrMsgUnauthorized)
	})

	t.Run("invalid case id returns 400", func(t *testing.T) {
		h := newTestChatHandler(&mockRoutingService{}, &mockChatEventPusher{})
		r := withUser(httptest.NewRequest(http.MethodPost, "/chat/sessions/not-a-uuid/convert-to-case", nil))
		r.SetPathValue("id", "not-a-uuid")
		w := httptest.NewRecorder()

		h.HandleConvertToCase(w, r)

		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgInvalidUUID)
	})

	t.Run("routing service lookup failure returns 502", func(t *testing.T) {
		routing := &mockRoutingService{
			getCaseInfoFn: func(ctx context.Context, caseID string) (routingclient.CaseInfo, error) {
				return routingclient.CaseInfo{}, errors.New("routing service unreachable")
			},
		}
		h := newTestChatHandler(routing, &mockChatEventPusher{})
		r := withUser(httptest.NewRequest(http.MethodPost, "/chat/sessions/"+testConvertCaseID+"/convert-to-case", nil))
		r.SetPathValue("id", testConvertCaseID)
		w := httptest.NewRecorder()

		h.HandleConvertToCase(w, r)

		assertStatus(t, w, http.StatusBadGateway)
		assertErrorMessage(t, w, "Failed to look up this chat's details. Please try again.")
	})

	// A chat raised through console-chat-bridge (Ask AI, live-chat-demo, any
	// tenant other than the customer portal) has no customer account or
	// project to attach a case to, and console-chat-bridge doesn't implement
	// case creation -- HandleConvertToCase must reject these up front rather
	// than calling out to an endpoint that will never exist.
	t.Run("non-customer-portal source is rejected without calling the notifier", func(t *testing.T) {
		for _, source := range []string{"asgardeo", "console-chat-bridge", "some-future-tenant"} {
			t.Run(source, func(t *testing.T) {
				routing := &mockRoutingService{
					getCaseInfoFn: func(ctx context.Context, caseID string) (routingclient.CaseInfo, error) {
						return routingclient.CaseInfo{CaseID: caseID, Source: source}, nil
					},
				}
				notifier := &mockChatEventPusher{
					createCaseFn: func(ctx context.Context, payload []byte, userIDToken string) ([]byte, error) {
						t.Fatal("CreateCase must not be called for a non-customer-portal source")
						return nil, nil
					},
				}
				h := newTestChatHandler(routing, notifier)
				r := withUser(httptest.NewRequest(http.MethodPost, "/chat/sessions/"+testConvertCaseID+"/convert-to-case", nil))
				r.SetPathValue("id", testConvertCaseID)
				w := httptest.NewRecorder()

				h.HandleConvertToCase(w, r)

				assertStatus(t, w, http.StatusConflict)
				assertErrorMessage(t, w, "This chat can't be converted into a case — it didn't originate from the customer portal.")
			})
		}
	})

	t.Run("empty and explicit customer-portal source both proceed", func(t *testing.T) {
		for _, source := range []string{"", defaultNotifySource} {
			t.Run("source="+source, func(t *testing.T) {
				routing := &mockRoutingService{
					getCaseInfoFn: func(ctx context.Context, caseID string) (routingclient.CaseInfo, error) {
						return routingclient.CaseInfo{CaseID: caseID, ConversationID: "conv-1", Source: source}, nil
					},
				}
				notifier := &mockChatEventPusher{}
				h := newTestChatHandler(routing, notifier)
				r := withUser(httptest.NewRequest(http.MethodPost, "/chat/sessions/"+testConvertCaseID+"/convert-to-case", nil))
				r.SetPathValue("id", testConvertCaseID)
				w := httptest.NewRecorder()

				h.HandleConvertToCase(w, r)

				assertStatus(t, w, http.StatusOK)
				got := decodeJSON[createCaseResponseBody](t, w)
				if got.EntityCaseID != "cs-1" {
					t.Errorf("entityCaseId = %q, want %q", got.EntityCaseID, "cs-1")
				}
				if len(notifier.pushedEvents) != 1 {
					t.Fatalf("pushedEvents = %d, want 1 (converted_to_case)", len(notifier.pushedEvents))
				}
			})
		}
	})

	t.Run("notifier create-case failure returns 502", func(t *testing.T) {
		routing := &mockRoutingService{}
		notifier := &mockChatEventPusher{
			createCaseFn: func(ctx context.Context, payload []byte, userIDToken string) ([]byte, error) {
				return nil, errors.New("backend-v2 unreachable")
			},
		}
		h := newTestChatHandler(routing, notifier)
		r := withUser(httptest.NewRequest(http.MethodPost, "/chat/sessions/"+testConvertCaseID+"/convert-to-case", nil))
		r.SetPathValue("id", testConvertCaseID)
		w := httptest.NewRecorder()

		h.HandleConvertToCase(w, r)

		assertStatus(t, w, http.StatusBadGateway)
		assertErrorMessage(t, w, "Failed to create a case for this chat. Please try again.")
	})

	t.Run("notifier create-case with no entityCaseId returns 502", func(t *testing.T) {
		routing := &mockRoutingService{}
		notifier := &mockChatEventPusher{
			createCaseFn: func(ctx context.Context, payload []byte, userIDToken string) ([]byte, error) {
				return []byte(`{}`), nil
			},
		}
		h := newTestChatHandler(routing, notifier)
		r := withUser(httptest.NewRequest(http.MethodPost, "/chat/sessions/"+testConvertCaseID+"/convert-to-case", nil))
		r.SetPathValue("id", testConvertCaseID)
		w := httptest.NewRecorder()

		h.HandleConvertToCase(w, r)

		assertStatus(t, w, http.StatusBadGateway)
		assertErrorMessage(t, w, "Failed to create a case for this chat. Please try again.")
	})

	// The case was already created upstream by the time ConvertToCase (the
	// routing-service call that ends the chat session) fails -- the error
	// must surface that case's id so the engineer doesn't lose track of it.
	t.Run("routing service convert failure after case creation surfaces the case id", func(t *testing.T) {
		routing := &mockRoutingService{
			convertToCaseFn: func(ctx context.Context, userID, caseID, entityCaseID string) (routingclient.ConvertToCaseResult, error) {
				return routingclient.ConvertToCaseResult{}, errors.New("session already ended")
			},
		}
		notifier := &mockChatEventPusher{
			createCaseFn: func(ctx context.Context, payload []byte, userIDToken string) ([]byte, error) {
				return []byte(`{"entityCaseId":"cs-42"}`), nil
			},
		}
		h := newTestChatHandler(routing, notifier)
		r := withUser(httptest.NewRequest(http.MethodPost, "/chat/sessions/"+testConvertCaseID+"/convert-to-case", nil))
		r.SetPathValue("id", testConvertCaseID)
		w := httptest.NewRecorder()

		h.HandleConvertToCase(w, r)

		assertStatus(t, w, http.StatusBadGateway)
		assertErrorMessage(t, w, "Case cs-42 was created, but ending the chat session failed. Please refresh and check the case.")
	})
}
