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

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/caseevents"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/chatnotify"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/dashboard"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/entity"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/eventbus"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/eventpublisher"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/handler"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/middleware"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/routingclient"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/scim"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/stream"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/updates"
)

func main() {
	loadDotEnv(".env")
	middleware.ConfigureLogger()

	// The dashboard registry is config-driven and loaded once at startup; a
	// missing or malformed DASHBOARDS_CONFIG logs an error (see
	// ParseDashboardsConfig) and leaves it empty rather than failing
	// startup, since no other endpoint depends on it.
	dashboard.Dashboards = dashboard.ParseDashboardsConfig(os.Getenv("DASHBOARDS_CONFIG"))

	// All upstream service clients (entity, updates, SCIM, and future notification
	// channels) authenticate as the same OAuth2 client-credentials app; only the
	// base URL and scopes differ per service.
	oauth2ClientID := mustEnv("OAUTH2_CLIENT_ID")
	oauth2ClientSecret := mustEnv("OAUTH2_CLIENT_SECRET")
	oauth2TokenURL := mustEnv("OAUTH2_TOKEN_URL")

	customerEntityCfg := entity.CustomerEntityConfig{
		BaseURL:      mustEnv("CUSTOMER_ENTITY_BASE_URL"),
		TokenURL:     oauth2TokenURL,
		ClientID:     oauth2ClientID,
		ClientSecret: oauth2ClientSecret,
		// Scopes is optional; set CUSTOMER_ENTITY_SCOPES as a comma-separated list if required.
		Scopes: splitComma(os.Getenv("CUSTOMER_ENTITY_SCOPES")),
	}

	customerEntityClient := entity.NewCustomerEntityClient(customerEntityCfg)

	// Event Hub — shared by the producer (this backend's own writes, via
	// eventPublisher below), the case-events consumer (internal/caseevents),
	// and the case-activity SSE broadcast hub those events feed
	// (internal/stream.BroadcastHub, consumed by the dedicated :9092
	// listener started further down). Optional: left unset, this backend
	// neither publishes, consumes, nor serves live case-activity updates —
	// callers fall back to their existing manual-refresh/staleTime polling.
	var caseEventsConsumer *eventbus.Consumer
	var eventPublisher *eventpublisher.Publisher
	var activityHub *stream.BroadcastHub
	if broker := os.Getenv("EVENT_HUB_BROKER"); broker != "" {
		eventBusCfg := eventbus.Config{
			Broker:           broker,
			ConnectionString: mustEnv("EVENT_HUB_CONNECTION_STRING"),
			Topic:            mustEnv("EVENT_HUB_TOPIC"),
		}
		// Every replica gets its own consumer group (a suffix on top of the
		// configured/default base name) rather than sharing one, so each
		// replica sees 100% of events instead of Kafka load-balancing them
		// across replicas — a shared group would mean an SSE client
		// connected to replica B never learns about an event a shared
		// group happened to hand to replica A. See internal/stream.
		// BroadcastHub: it only knows about subscribers on its own
		// process, so every replica must independently observe every event
		// to be able to broadcast it to whichever subscribers it happens to
		// be holding the connection for. Every ordinary rolling deploy (not
		// just a crash-restart) still means a brand new consumer group —
		// even newReplicaID's hostname-based suffix changes with every new
		// pod — so LatestOffset (not the package default) is required
		// here: EarliestOffset would replay the entire retained topic
		// history into a burst of stale case_updated notifications on
		// every deploy. This does mean an unbounded number of dead,
		// never-explicitly-deleted consumer groups accumulate on the
		// broker over the service's lifetime — an accepted tradeoff, since
		// per-replica uniqueness is required for the fan-out property
		// above and Event Hub's Kafka surface has no API this backend can
		// call to delete a consumer group it's done with.
		consumerGroupBase := envOrDefault("EVENT_HUB_CONSUMER_GROUP", "csm-portal-backend")
		consumerGroup := fmt.Sprintf("%s-replica-%s", consumerGroupBase, newReplicaID())
		caseEventsConsumer = eventbus.NewConsumer(eventBusCfg, consumerGroup, eventbus.LatestOffset)
		eventPublisher = eventpublisher.New(eventbus.NewProducer(eventBusCfg), customerEntityClient)
		activityHub = stream.NewBroadcastHub()
	}

	caseHandler := handler.NewCaseHandler(customerEntityClient, eventPublisher, activityHub)

	// Live-engineer-chat escalation feature (see internal/handler/chat.go).
	// engineerHub is unconditional — unlike activityHub above, this feature
	// has no Kafka-backed fallback to degrade to, so it is always
	// constructed regardless of EVENT_HUB_BROKER.
	engineerHub := stream.NewBroadcastHub()
	// internalChatToken authenticates the two directions of
	// service-to-service traffic this feature needs with customer-portal/
	// backend-v2 (see internal/middleware.InternalToken and
	// internal/chatnotify's doc comments). Both backends must be configured
	// with the same value.
	internalChatToken := os.Getenv("INTERNAL_CHAT_TOKEN")
	chatNotifyClient := chatnotify.NewClient(chatnotify.Config{
		// customer-portal/backend-v2's WS_PORT listener — the same
		// unauthenticated-by-user-JWT listener GET /ws already runs on
		// (see that backend's cmd/server/main.go), since neither route can
		// carry an x-jwt-assertion header.
		BaseURL:       envOrDefault("CUSTOMER_PORTAL_INTERNAL_BASE_URL", "http://localhost:8082"),
		InternalToken: internalChatToken,
	})
	// routingClient calls the standalone chat-routing-service (see
	// internal/routingclient and apps/chat-routing-service/backend) that
	// decides which available engineer, if any, an escalation is routed
	// to. Like internalChatToken above, ROUTING_SERVICE_TOKEN is read with
	// plain os.Getenv rather than mustEnv: an empty token always fails
	// that service's own InternalToken check rather than silently
	// disabling auth, and HandleEscalate already degrades to broadcasting
	// if this client's calls error, so a missing token fails safe.
	routingClient := routingclient.NewClient(routingclient.Config{
		BaseURL:       envOrDefault("ROUTING_SERVICE_BASE_URL", "http://localhost:9096"),
		InternalToken: os.Getenv("ROUTING_SERVICE_TOKEN"),
	})
	chatHandler := handler.NewChatHandler(customerEntityClient, engineerHub, chatNotifyClient, routingClient)

	dashboardHandler := handler.NewDashboardHandler(customerEntityClient)
	accountHandler := handler.NewAccountHandler(customerEntityClient)
	projectHandler := handler.NewProjectHandler(customerEntityClient)
	productHandler := handler.NewProductHandler(customerEntityClient)
	deploymentHandler := handler.NewDeploymentHandler(customerEntityClient)
	changeRequestHandler := handler.NewChangeRequestHandler(customerEntityClient)
	itServiceHandler := handler.NewITServiceHandler(customerEntityClient)
	serviceOfferingHandler := handler.NewServiceOfferingHandler(customerEntityClient)
	groupHandler := handler.NewGroupHandler(customerEntityClient)
	referenceHandler := handler.NewReferenceHandler(customerEntityClient)
	configurationItemHandler := handler.NewConfigurationItemHandler(customerEntityClient)
	catalogHandler := handler.NewCatalogHandler(customerEntityClient)
	timeCardHandler := handler.NewTimeCardHandler(customerEntityClient)
	productVulnerabilityHandler := handler.NewProductVulnerabilityHandler(customerEntityClient)
	conversationHandler := handler.NewConversationHandler(customerEntityClient)
	taskSlaHandler := handler.NewTaskSlaHandler(customerEntityClient)
	taskHandler := handler.NewTaskHandler(customerEntityClient)
	incidentHandler := handler.NewIncidentHandler(customerEntityClient)
	problemHandler := handler.NewProblemHandler(customerEntityClient)

	updatesCfg := updates.Config{
		BaseURL:      mustEnv("UPDATES_BASE_URL"),
		TokenURL:     oauth2TokenURL,
		ClientID:     oauth2ClientID,
		ClientSecret: oauth2ClientSecret,
		Scopes:       splitComma(os.Getenv("UPDATES_SCOPES")),
	}
	updatesClient := updates.NewClient(updatesCfg)
	updatesHandler := handler.NewUpdatesHandler(updatesClient)

	scimCfg := scim.Config{
		BaseURL:      mustEnv("SCIM_BASE_URL"),
		TokenURL:     oauth2TokenURL,
		ClientID:     oauth2ClientID,
		ClientSecret: oauth2ClientSecret,
		Scopes:       splitComma(os.Getenv("SCIM_SCOPES")),
	}
	scimClient := scim.NewClient(scimCfg)
	usersHandler := handler.NewUsersHandler(scimClient, customerEntityClient)

	authCfg := middleware.Config{
		JWKSEndpoint:          mustEnv("AUTH_JWKS_ENDPOINT"),
		Issuer:                mustEnv("AUTH_ISSUER"),
		Audiences:             splitComma(mustEnv("AUTH_AUDIENCE")),
		ClockSkew:             5 * time.Second,
		TokenValidatorEnabled: os.Getenv("AUTH_TOKEN_VALIDATOR_ENABLED") != "false",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /cases", caseHandler.CreateCase)
	mux.HandleFunc("GET /cases/{id}", caseHandler.GetCase)
	mux.HandleFunc("PATCH /cases/{id}", caseHandler.PatchCase)
	mux.HandleFunc("POST /cases/{id}/comments", caseHandler.CreateCaseComment)
	mux.HandleFunc("POST /cases/{id}/comments/search", caseHandler.SearchCaseComments)
	mux.HandleFunc("POST /cases/{id}/activities/search", caseHandler.SearchCaseActivities)
	mux.HandleFunc("POST /attachments", caseHandler.CreateCaseAttachment)
	mux.HandleFunc("POST /attachments/search", caseHandler.SearchCaseAttachments)
	mux.HandleFunc("GET /attachments/{id}/content", caseHandler.GetCaseAttachmentContent)
	mux.HandleFunc("DELETE /attachments/{id}", caseHandler.DeleteCaseAttachment)
	mux.HandleFunc("POST /cases/{id}/call-requests", caseHandler.CreateCallRequest)
	mux.HandleFunc("POST /cases/{id}/call-requests/search", caseHandler.SearchCallRequests)
	mux.HandleFunc("PATCH /cases/{caseId}/call-requests/{callRequestId}", caseHandler.PatchCallRequest)
	mux.HandleFunc("POST /cases/{id}/github-issues", caseHandler.CreateCaseGithubIssue)
	mux.HandleFunc("POST /cases/{id}/tags", caseHandler.AddCaseTag)
	mux.HandleFunc("DELETE /cases/{id}/tags/{tagId}", caseHandler.RemoveCaseTag)
	mux.HandleFunc("GET /tags/search", caseHandler.SearchTags)
	mux.HandleFunc("POST /cases/search", caseHandler.SearchCases)
	mux.HandleFunc("GET /dashboards", dashboardHandler.GetDashboards)
	mux.HandleFunc("GET /dashboards/{dashboardId}", dashboardHandler.GetDashboardDetail)
	mux.HandleFunc("GET /updates/product-update-levels", updatesHandler.GetProductUpdateLevels)
	mux.HandleFunc("POST /updates/levels/search", updatesHandler.SearchUpdatesBetweenUpdateLevels)
	mux.HandleFunc("GET /users/me", usersHandler.GetMe)
	mux.HandleFunc("PATCH /users/me", usersHandler.PatchMe)
	mux.HandleFunc("POST /users/search", usersHandler.SearchUsers)
	mux.HandleFunc("GET /users/{id}", usersHandler.GetUser)
	mux.HandleFunc("POST /roles/search", referenceHandler.SearchRoles)
	mux.HandleFunc("POST /teams/search", referenceHandler.SearchTeams)
	mux.HandleFunc("GET /accounts/{id}", accountHandler.GetAccount)
	mux.HandleFunc("POST /accounts/search", accountHandler.SearchAccounts)
	mux.HandleFunc("POST /accounts/{id}/contacts/search", accountHandler.SearchAccountContacts)
	mux.HandleFunc("GET /projects/{id}", projectHandler.GetProject)
	mux.HandleFunc("POST /projects/search", projectHandler.SearchProjects)
	mux.HandleFunc("POST /projects/{id}/contacts/search", projectHandler.SearchProjectContacts)
	mux.HandleFunc("GET /projects/{id}/contacts/{contactId}", projectHandler.GetProjectContact)
	mux.HandleFunc("PATCH /projects/{id}", projectHandler.UpdateProject)
	mux.HandleFunc("POST /products/search", productHandler.SearchProducts)
	mux.HandleFunc("POST /products/{id}/versions/search", productHandler.SearchProductVersions)
	mux.HandleFunc("POST /deployments", deploymentHandler.PostDeployment)
	mux.HandleFunc("POST /deployments/search", deploymentHandler.SearchDeployments)
	mux.HandleFunc("PATCH /deployments/{id}", deploymentHandler.PatchDeployment)
	mux.HandleFunc("POST /deployments/{id}/products", deploymentHandler.PostDeployedProduct)
	mux.HandleFunc("POST /deployments/{id}/products/search", deploymentHandler.SearchDeployedProducts)
	mux.HandleFunc("PATCH /deployments/{deploymentId}/products/{productId}", deploymentHandler.PatchDeployedProduct)
	mux.HandleFunc("POST /change-requests", changeRequestHandler.CreateChangeRequest)
	mux.HandleFunc("GET /change-requests/{id}", changeRequestHandler.GetChangeRequest)
	mux.HandleFunc("GET /change-requests/{id}/approvals", changeRequestHandler.GetChangeRequestApprovals)
	mux.HandleFunc("POST /change-requests/{id}/approvals/decision", changeRequestHandler.DecideChangeRequestApproval)
	mux.HandleFunc("PATCH /change-requests/{id}", changeRequestHandler.PatchChangeRequest)
	mux.HandleFunc("POST /change-requests/search", changeRequestHandler.SearchChangeRequests)
	mux.HandleFunc("POST /services/search", itServiceHandler.SearchITServices)
	mux.HandleFunc("POST /service-offerings/search", serviceOfferingHandler.SearchServiceOfferings)
	mux.HandleFunc("POST /groups/search", groupHandler.SearchGroups)
	mux.HandleFunc("POST /configuration-items/search", configurationItemHandler.SearchConfigurationItems)
	mux.HandleFunc("POST /time-cards/search", timeCardHandler.SearchTimeCards)
	mux.HandleFunc("POST /time-cards", timeCardHandler.CreateTimeCard)
	mux.HandleFunc("PATCH /time-cards/{id}", timeCardHandler.UpdateTimeCard)
	mux.HandleFunc("POST /catalogs/search", catalogHandler.SearchCatalogs)
	mux.HandleFunc("GET /catalogs/{catalogId}/items/{catalogItemId}/variables", catalogHandler.GetCatalogItemVariables)
	mux.HandleFunc("POST /products/vulnerabilities/search", productVulnerabilityHandler.SearchProductVulnerabilities)
	mux.HandleFunc("GET /products/vulnerabilities/{id}", productVulnerabilityHandler.GetProductVulnerability)
	mux.HandleFunc("GET /conversations/{id}/messages", conversationHandler.GetConversationMessages)
	mux.HandleFunc("POST /conversations/search", conversationHandler.SearchConversations)
	mux.HandleFunc("POST /slas/search", taskSlaHandler.SearchTaskSlas)
	mux.HandleFunc("GET /slas/{id}", taskSlaHandler.GetTaskSla)
	mux.HandleFunc("POST /cases/{caseId}/tasks/search", taskHandler.SearchCaseTasks)
	mux.HandleFunc("POST /tasks/search", taskHandler.SearchTasks)
	mux.HandleFunc("GET /tasks/{id}", taskHandler.GetTask)
	mux.HandleFunc("POST /cases/{caseId}/tasks", taskHandler.CreateCaseTask)
	mux.HandleFunc("PATCH /tasks/{id}", taskHandler.UpdateTask)
	mux.HandleFunc("POST /incidents/search", incidentHandler.SearchIncidents)
	mux.HandleFunc("POST /incidents", incidentHandler.CreateIncident)
	mux.HandleFunc("GET /incidents/{id}", incidentHandler.GetIncident)
	mux.HandleFunc("PATCH /incidents/{id}", incidentHandler.PatchIncident)
	mux.HandleFunc("POST /incidents/{id}/comments", incidentHandler.CreateIncidentComment)
	mux.HandleFunc("POST /incidents/{id}/comments/search", incidentHandler.SearchIncidentComments)
	mux.HandleFunc("POST /change-requests/{id}/comments", changeRequestHandler.CreateChangeRequestComment)
	mux.HandleFunc("POST /change-requests/{id}/comments/search", changeRequestHandler.SearchChangeRequestComments)
	mux.HandleFunc("POST /problems", problemHandler.CreateProblem)
	mux.HandleFunc("GET /problems/{id}", problemHandler.GetProblem)
	mux.HandleFunc("POST /problems/search", problemHandler.SearchProblems)

	// Live-engineer-chat escalation (see internal/handler/chat.go). No
	// "/api/v1" prefix, matching every other route on this mux (/cases,
	// /dashboards, /users/me, ...) — none of them use one.
	mux.HandleFunc("POST /chat/sessions/{id}/accept", chatHandler.HandleAcceptSession)
	mux.HandleFunc("POST /chat/sessions/{id}/messages", chatHandler.HandleEngineerMessage)
	mux.HandleFunc("POST /chat/sessions/{id}/complete", chatHandler.HandleCompleteSession)
	mux.HandleFunc("POST /chat/sessions/{id}/decline", chatHandler.HandleDeclineSession)
	mux.HandleFunc("POST /engineers/me/status", chatHandler.HandleSetPresence)
	mux.HandleFunc("GET /engineers/me/status", chatHandler.HandleGetPresence)

	// Built once and reused on both listeners below: Auth() does a real JWKS
	// fetch (when TokenValidatorEnabled), so calling it a second time would
	// duplicate that startup network round-trip and double the chance of a
	// transient JWKS hiccup aborting startup, for no benefit — both
	// listeners validate the exact same tokens the exact same way.
	authMiddleware := middleware.Auth(authCfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := ":" + mustPort("PORT", "8080")

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		slog.Error("failed to bind", "addr", addr, "err", err)
		os.Exit(1)
	}

	srv := &http.Server{
		// SecurityHeaders must stay outermost so its headers are present on
		// every response, including a CORS preflight — CORS runs next,
		// still ahead of Auth (see middleware.CORS's doc comment): a
		// preflight OPTIONS carries no x-jwt-assertion for Auth to accept.
		// In a real deployment Choreo's gateway supplies CORS itself, so
		// this is a no-op there; it matters when the gateway isn't in the
		// path (local development, where the browser calls this listener
		// directly). CORS_ALLOWED_ORIGINS is a comma-separated allow-list;
		// unset allows any origin (see middleware.CORS on why that's safe
		// here).
		Handler: middleware.SecurityHeaders(
			middleware.CORS(splitComma(os.Getenv("CORS_ALLOWED_ORIGINS")))(
				middleware.CorrelationID(
					authMiddleware(
						middleware.Logger(mux),
					),
				),
			),
		),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// The case-activity SSE stream (GET /cases/{id}/activities/stream) needs
	// a connection that can stay open indefinitely, which the timeouts above
	// would kill — WriteTimeout/IdleTimeout apply for the life of the
	// connection, not per-request. It gets its own listener/http.Server on a
	// separate port with those timeouts disabled, mirroring how this
	// backend's WebSocket work solved the identical problem. Only started
	// when Event Hub is configured (activityHub != nil) — without it there
	// is nothing for the stream to ever broadcast, so standing up a second
	// server would just be a permanently-503 endpoint.
	var streamSrv *http.Server
	var streamLn net.Listener
	if activityHub != nil {
		streamMux := http.NewServeMux()
		streamMux.HandleFunc("GET /cases/{id}/activities/stream", caseHandler.StreamCaseActivities)

		streamAddr := ":" + mustPort("STREAM_PORT", "9092")
		streamLn, err = (&net.ListenConfig{}).Listen(ctx, "tcp", streamAddr)
		if err != nil {
			slog.Error("failed to bind", "addr", streamAddr, "err", err)
			os.Exit(1)
		}

		streamSrv = &http.Server{
			// SecurityHeaders must stay outermost so its headers are present
			// on every response, including a CORS preflight — CORS runs
			// next, still ahead of Auth: a browser preflight carries no
			// x-jwt-assertion header, so Auth must never see it first. See
			// middleware.CORS's doc comment.
			// STREAM_CORS_ALLOWED_ORIGINS is a comma-separated allow-list;
			// unset allows any origin (local-dev-friendly default — see
			// middleware.CORS on why that's safe here).
			Handler: middleware.SecurityHeaders(
				middleware.CORS(splitComma(os.Getenv("STREAM_CORS_ALLOWED_ORIGINS")))(
					middleware.CorrelationID(
						authMiddleware(
							middleware.Logger(streamMux),
						),
					),
				),
			),
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      0,
			IdleTimeout:       0,
		}
	}

	// Live-engineer-chat alert stream (GET /chat/alerts/stream — no "/api/v1"
	// prefix, see the route registrations above) needs the same "no
	// WriteTimeout/IdleTimeout" treatment as the case-activity stream above,
	// but — unlike that one — is always started: it has no Event-Hub-gated
	// fallback (see engineerHub's construction above).
	chatStreamMux := http.NewServeMux()
	chatStreamMux.HandleFunc("GET /chat/alerts/stream", chatHandler.StreamEngineerAlerts)

	chatStreamAddr := ":" + mustPort("CHAT_STREAM_PORT", "9094")
	chatStreamLn, err := (&net.ListenConfig{}).Listen(ctx, "tcp", chatStreamAddr)
	if err != nil {
		slog.Error("failed to bind", "addr", chatStreamAddr, "err", err)
		os.Exit(1)
	}
	chatStreamSrv := &http.Server{
		Handler: middleware.SecurityHeaders(
			middleware.CORS(splitComma(os.Getenv("STREAM_CORS_ALLOWED_ORIGINS")))(
				middleware.CorrelationID(
					authMiddleware(
						middleware.Logger(chatStreamMux),
					),
				),
			),
		),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       0,
	}

	// Internal listener for service-to-service calls from customer-portal/
	// backend-v2 (POST /internal/chat/escalate, POST /internal/chat/
	// customer-message) — see internal/middleware.InternalToken's doc
	// comment for why these cannot go through authMiddleware/mux above:
	// backend-v2 has no engineer JWT to present. Ordinary short-lived-request
	// timeouts are fine here (unlike the two SSE listeners), since neither
	// route holds a connection open.
	internalChatMux := http.NewServeMux()
	internalChatMux.HandleFunc("POST /internal/chat/escalate", chatHandler.HandleEscalate)
	internalChatMux.HandleFunc("POST /internal/chat/customer-message", chatHandler.HandleCustomerMessage)

	internalChatAddr := ":" + mustPort("INTERNAL_CHAT_PORT", "9095")
	internalChatLn, err := (&net.ListenConfig{}).Listen(ctx, "tcp", internalChatAddr)
	if err != nil {
		slog.Error("failed to bind", "addr", internalChatAddr, "err", err)
		os.Exit(1)
	}
	internalChatSrv := &http.Server{
		Handler: middleware.SecurityHeaders(
			middleware.CorrelationID(
				middleware.InternalToken(internalChatToken)(
					middleware.Logger(internalChatMux),
				),
			),
		),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("server exited", "err", err)
			os.Exit(1)
		}
	}()
	slog.Info("CSM Portal Backend started", "addr", addr)

	if streamSrv != nil {
		go func() {
			if err := streamSrv.Serve(streamLn); err != nil && err != http.ErrServerClosed {
				slog.Error("stream server exited", "err", err)
				os.Exit(1)
			}
		}()
		slog.Info("case-activity stream server started", "addr", streamLn.Addr().String())
	}

	go func() {
		if err := chatStreamSrv.Serve(chatStreamLn); err != nil && err != http.ErrServerClosed {
			slog.Error("chat alert stream server exited", "err", err)
			os.Exit(1)
		}
	}()
	slog.Info("engineer chat alert stream server started", "addr", chatStreamLn.Addr().String())

	go func() {
		if err := internalChatSrv.Serve(internalChatLn); err != nil && err != http.ErrServerClosed {
			slog.Error("internal chat server exited", "err", err)
			os.Exit(1)
		}
	}()
	slog.Info("internal chat server started", "addr", internalChatLn.Addr().String())

	if caseEventsConsumer != nil {
		go caseEventsConsumer.Run(ctx, caseevents.NewHandler(activityHub).Handle)
		slog.Info("case-events consumer started")
	}

	<-ctx.Done()
	stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Both listeners get the shutdown goroutine's own use of shutdownCtx,
	// running concurrently rather than one after the other — Shutdown
	// doesn't cancel an already-running handler's own request context, it
	// only waits (up to shutdownCtx) for handlers to return on their own,
	// so a long-lived SSE connection on the stream listener can otherwise
	// consume the *entire* shared budget before the main API's own
	// Shutdown even starts, turning what should be a graceful drain into
	// an abrupt os.Exit(1) below.
	var wg sync.WaitGroup
	if streamSrv != nil {
		wg.Go(func() {
			if err := streamSrv.Shutdown(shutdownCtx); err != nil {
				slog.Error("stream server graceful shutdown failed", "err", err)
			}
		})
	}
	wg.Go(func() {
		if err := chatStreamSrv.Shutdown(shutdownCtx); err != nil {
			slog.Error("chat alert stream server graceful shutdown failed", "err", err)
		}
	})
	wg.Go(func() {
		if err := internalChatSrv.Shutdown(shutdownCtx); err != nil {
			slog.Error("internal chat server graceful shutdown failed", "err", err)
		}
	})
	var srvErr error
	wg.Go(func() {
		srvErr = srv.Shutdown(shutdownCtx)
	})
	wg.Wait()
	if srvErr != nil {
		slog.Error("graceful shutdown failed", "err", srvErr)
		os.Exit(1)
	}

	// Both listeners have now drained, so no new publishAsync goroutine can
	// start — safe to wait for the ones already in flight before closing
	// the producer/consumer they depend on. A state-changing request can
	// return 200 with its publishAsync goroutine still running; closing the
	// producer out from under it would otherwise silently drop the event.
	caseHandler.WaitPendingPublishes(shutdownCtx)

	if caseEventsConsumer != nil {
		caseEventsConsumer.Close()
	}
	if eventPublisher != nil {
		eventPublisher.Close()
	}

	slog.Info("CSM Portal Backend stopped")
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		slog.Error("required environment variable is not set", "key", key)
		os.Exit(1)
	}
	return v
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// newReplicaID returns a stable identifier for this process's Kafka
// consumer group suffix (see the EVENT_HUB_BROKER block above), preferring
// the container/pod hostname — in Kubernetes/Choreo this is the pod name by
// default, requiring no extra deployment config — over a fresh random ID.
// A random ID on every plain process restart would make Event Hub treat it
// as a brand new, never-before-seen consumer group every time: NewConsumer
// starts new groups at kafka.FirstOffset, so the restart would replay every
// retained event and re-broadcast stale case_updated notifications to
// whichever SSE clients happen to be connected. The hostname is only stable
// within the same pod's lifetime, so an actual redeploy (new pod, new
// hostname) still causes a one-time replay — an accepted, much rarer
// tradeoff than replaying on every restart.
//
// Falls back to a random UUID v4 (deliberately duplicating
// middleware.newCorrelationID's same crypto/rand-based approach rather than
// exporting/sharing it — the two ids serve unrelated purposes and don't
// need to be coupled by a shared helper) only if the hostname is
// unavailable, which is rare outside unusual sandboxed environments.
func newReplicaID() string {
	if host, err := os.Hostname(); err == nil && host != "" {
		return host
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		slog.Error("failed to generate replica id", "err", err)
		os.Exit(1)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant bits
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// mustPort returns the value of the given environment variable (or def if
// unset) as a bare port number, e.g. "8080" — not an address like ":8080" or
// "localhost:8080". Exits the process if the value isn't a valid TCP port.
func mustPort(key, def string) string {
	v := envOrDefault(key, def)
	port, err := strconv.Atoi(v)
	if err != nil || port < 1 || port > 65535 {
		slog.Error("environment variable must be a plain port number (e.g. \"8080\"), not an address", "key", key, "value", v)
		os.Exit(1)
	}
	return v
}

// loadDotEnv reads a .env file and sets any unset environment variables from it.
// Silently ignored if the file does not exist; logs a warning for any other error.
func loadDotEnv(path string) {
	f, err := os.Open(path) // #nosec G304 -- path is always the hardcoded literal ".env" at the only call site
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("loadDotEnv: failed to open .env file", "err", err)
		}
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		// Strip surrounding quotes from value.
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		if os.Getenv(k) == "" {
			_ = os.Setenv(k, v)
		}
	}
	if err := scanner.Err(); err != nil {
		slog.Warn("loadDotEnv: error reading .env file", "err", err)
	}
}

func splitComma(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			result = append(result, t)
		}
	}
	return result
}
