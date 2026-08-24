// // Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
// //
// // WSO2 LLC. licenses this file to you under the Apache License,
// // Version 2.0 (the "License"); you may not use this file except
// // in compliance with the License. You may obtain a copy of the License at
// //
// // http://www.apache.org/licenses/LICENSE-2.0
// //
// // Unless required by applicable law or agreed to in writing,
// // software distributed under the License is distributed on an
// // "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// // KIND, either express or implied.  See the License for the
// // specific language governing permissions and limitations
// // under the License.

package routing

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/entity"
)

var EntityClient *entity.Client

func init() {
	envPaths := []string{".env", "../.env", "../../.env", "../../../.env"}
	for _, path := range envPaths {
		if err := godotenv.Load(path); err == nil {
			slog.Info("Loaded .env file successfully", "path", path)
			break
		}
	}

	entityBaseURL := os.Getenv("ENTITY_BASE_URL")
	if entityBaseURL == "" {
		entityBaseURL = os.Getenv("ENTITY_SERVICE_BASE_URL")
	}

	if entityBaseURL != "" {
		EntityClient = entity.NewClient(entity.Config{
			BaseURL: entityBaseURL,
		})
		slog.Info("Successfully initialized EntityClient (PostgreSQL)", "baseURL", entityBaseURL)
	} else {
		slog.Warn("ENTITY_BASE_URL not set; EntityClient remains nil")
	}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func HandleEngineerWS(w http.ResponseWriter, r *http.Request) {
	engineerID := r.URL.Query().Get("engineerId")

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	Hub.RegisterEngineer(conn, engineerID)

	defer func() {
		remaining := Hub.UnregisterEngineer(conn, engineerID)
		conn.Close()

		if engineerID != "" && remaining == 0 {
			HandleEngineerDisconnect(engineerID)
		}
	}()

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
}

func HandleEngineerDisconnect(engineerID string) {
	slog.Info("Engineer WebSocket disconnected. Updating DB presence...", "engineerId", engineerID)

	if EntityClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = EntityClient.UpdateEngineerHeartbeat(ctx, engineerID, "OFFLINE")
	}

	Hub.BroadcastToAllEngineers(map[string]interface{}{
		"type":           "SESSION_CLOSED",
		"engineerId":     engineerID,
		"engineerStatus": "OFFLINE",
		"timestamp":      time.Now().Format(time.RFC3339),
	})
}

func HandleSetEngineerStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Inside HandleSetEngineerStatus in handlers.go
	if r.Method == http.MethodGet {
		engineerID := r.URL.Query().Get("engineerId")
		if engineerID == "" {
			engineerID = "ENG-001"
		}

		// Default to AVAILABLE so alerts aren't blocked during page load
		status := "AVAILABLE"

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"engineerId":     engineerID,
			"engineerStatus": status,
		})
		return
	}

	var req struct {
		EngineerID string `json:"engineerId"`
		Status     string `json:"status"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if EntityClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = EntityClient.UpdateEngineerHeartbeat(ctx, req.EngineerID, req.Status)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":         "success",
		"engineerStatus": req.Status,
	})
}

func HandleCustomerBroadcast(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, x-user-id-token")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	var body struct {
		CustomerName      string `json:"customerName"`
		UserEmail         string `json:"userEmail"`
		Message           string `json:"message"`
		ProjectId         string `json:"projectId"`
		DeploymentId      string `json:"deploymentId"`
		DeployedProductId string `json:"deployedProductId"`
		ConversationId    string `json:"conversationId"`
		AccountId         string `json:"accountId"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	conversationId := body.ConversationId
	if conversationId == "" || conversationId == "NEW_SESSION" || !strings.Contains(conversationId, "-") {
		conversationId = uuid.NewString()
	}

	customerName := body.CustomerName
	if customerName == "" && body.UserEmail != "" {
		customerName = body.UserEmail
	}
	if customerName == "" {
		customerName = "Customer User"
	}

	escMessage := body.Message
	if escMessage == "" {
		escMessage = "Customer requested live engineer assistance."
	}

	// 1. Trigger Async ServiceNow Case Creation in Background
	if EntityClient != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			// Use fallback UUIDs if frontend did not supply deployment IDs
			depID := body.DeploymentId
			if depID == "" {
				depID = "00000000-0000-0000-0000-000000000001"
			}
			depProdID := body.DeployedProductId
			if depProdID == "" {
				depProdID = "00000000-0000-0000-0000-000000000002"
			}

			casePayload := map[string]interface{}{
				"type":              "case",
				"projectId":         body.ProjectId,
				"deploymentId":      depID,
				"deployedProductId": depProdID,
				"subject":           fmt.Sprintf("Live Support Chat - %s", customerName),
				"description":       escMessage,
				"conversationId":    conversationId,
			}

			bodyBytes, err := json.Marshal(casePayload)
			if err != nil {
				slog.Error("Failed to marshal case payload", "err", err)
				return
			}

			_, err = EntityClient.CreateCase(ctx, bodyBytes)
			if err != nil {
				slog.Error("Failed to create ServiceNow case", "err", err)
			}
		}()
	}

	// 2. Alert Connected Support Engineers via WebSocket Hub
	Hub.BroadcastToAllEngineers(map[string]interface{}{
		"type":           "CUSTOMER_ALERT",
		"conversationId": conversationId,
		"customerName":   customerName,
		"message":        escMessage,
		"projectId":      body.ProjectId,
		"timestamp":      time.Now().Format(time.RFC3339),
	})

	// 3. Return HTTP Response to Frontend Chat Widget
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":         "QUEUED",
		"conversationId": conversationId,
	})
}

func HandleAcceptSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	var body struct {
		ConversationId string `json:"conversationId"`
		EngineerId     string `json:"engineerId"`
		EngineerName   string `json:"engineerName"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid Body", http.StatusBadRequest)
		return
	}

	engName := body.EngineerName
	if engName == "" {
		engName = "CSM Support Engineer"
	}

	// Atomically claim next chat in PostgreSQL using FOR UPDATE SKIP LOCKED
	if EntityClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = EntityClient.DispatchNextChat(ctx, body.EngineerId)
	}

	Hub.BroadcastToAllEngineers(map[string]interface{}{
		"type":           "SESSION_ACCEPTED",
		"conversationId": body.ConversationId,
		"engineerId":     body.EngineerId,
		"engineerName":   engName,
		"timestamp":      time.Now().Format(time.RFC3339),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ACCEPTED"})
}

func HandleSendMessage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	var body struct {
		ConversationId string `json:"conversationId"`
		Sender         string `json:"sender"`
		SenderName     string `json:"senderName"`
		Message        string `json:"message"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	timestamp := time.Now().Format(time.RFC3339)

	// Append message directly into work_item_comment table
	if EntityClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = EntityClient.AppendChatMessage(ctx, body.ConversationId, body.Sender, body.SenderName, body.Message)
	}

	Hub.BroadcastToAllEngineers(map[string]interface{}{
		"type":           "CHAT_MESSAGE",
		"conversationId": body.ConversationId,
		"sender":         body.Sender,
		"senderName":     body.SenderName,
		"message":        body.Message,
		"timestamp":      timestamp,
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "SENT"})
}

func HandleCompleteSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	var body struct {
		ConversationId string `json:"conversationId"`
		EngineerId     string `json:"engineerId"`
		EngineerStatus string `json:"engineerStatus"` // Preserves requested status (e.g. "OFFLINE")
		CaseID         string `json:"caseId"`
		TranscriptText string `json:"transcriptText"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	// 1. Determine final status: Default to AVAILABLE if empty or BUSY, otherwise respect requested status (e.g., OFFLINE)
	finalStatus := body.EngineerStatus
	if finalStatus == "" || finalStatus == "BUSY" {
		finalStatus = "AVAILABLE"
	}

	// 2. Update engineer presence state
	if EntityClient != nil && body.EngineerId != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = EntityClient.UpdateEngineerHeartbeat(ctx, body.EngineerId, finalStatus)
	}

	// 3. Attach transcript work note if provided
	if EntityClient != nil && body.CaseID != "" && body.TranscriptText != "" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			commentPayload := map[string]interface{}{
				"type":    "work_notes",
				"content": body.TranscriptText,
			}

			commentBytes, err := json.Marshal(commentPayload)
			if err != nil {
				slog.Error("Failed to marshal comment payload", "err", err)
				return
			}

			_, err = EntityClient.CreateCaseComment(ctx, body.CaseID, commentBytes)
			if err != nil {
				slog.Error("Failed to attach transcript work notes", "err", err)
			}
		}()
	}

	// 4. Broadcast SESSION_CLOSED with the accurate final status and conversation ID
	Hub.BroadcastToAllEngineers(map[string]interface{}{
		"type":           "SESSION_CLOSED",
		"conversationId": body.ConversationId,
		"engineerId":     body.EngineerId,
		"engineerStatus": finalStatus,
		"timestamp":      time.Now().Format(time.RFC3339),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":         "CLOSED",
		"engineerStatus": finalStatus,
	})
}
