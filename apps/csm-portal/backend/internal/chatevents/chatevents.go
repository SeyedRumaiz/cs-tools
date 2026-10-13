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

// Package chatevents publishes live-chat events to Azure Event Hub for
// csm-notification-service to act on. It writes to its own topic
// (CHAT_EVENT_HUB_TOPIC, e.g. "chat-events"), never to case-events:
// entity-service is the only publisher of case-events, which is why this
// backend's earlier case-event producer was removed.
//
// This file is the source of truth for the wire shape; csm-notification-
// service's internal/events/chat.go mirrors it by hand.
package chatevents

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	kafka "github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/plain"
)

// TypeAssignedWhileAway: a live chat was assigned to an engineer whose
// csm-portal tab has been closed for longer than the grace period, so the
// in-portal alert cannot reach them. csm-notification-service emails that
// engineer. EntityID is the chat's case ID.
const TypeAssignedWhileAway = "chat.assigned_while_away"

// Envelope is the Kafka record value, the same shape every event
// csm-notification-service consumes uses.
type Envelope struct {
	Type     string          `json:"type"`
	EntityID string          `json:"entityId"`
	Payload  json.RawMessage `json:"payload"`
}

// AssignedWhileAwayPayload is TypeAssignedWhileAway's payload. It carries
// no message text: the email only tells the engineer a chat is waiting.
type AssignedWhileAwayPayload struct {
	CaseID        string `json:"caseId"`
	EngineerEmail string `json:"engineerEmail"`
	// CustomerName is the customer's name, else their email.
	CustomerName string `json:"customerName,omitempty"`
	// Product is the tenant the chat came from, e.g. "devant".
	Product string `json:"product,omitempty"`
	Subject string `json:"subject,omitempty"`
	// AssignedOn is when the chat was assigned, RFC3339.
	AssignedOn string `json:"assignedOn"`
	// ChatLink is csm-portal's Chat page, where the engineer accepts it.
	// Built here because only this backend knows the portal's address.
	ChatLink string `json:"chatLink"`
}

// Config is the Event Hub connection. Broker and ConnectionString are the
// same values entity-service and csm-notification-service use.
type Config struct {
	// Broker is "<namespace>.servicebus.windows.net:9093".
	Broker string
	// ConnectionString is the namespace's Shared Access Policy connection
	// string, used as the SASL/PLAIN password.
	ConnectionString string
	Topic            string
	// CAFile, when set, is a PEM file of the only CA trusted for the
	// broker's certificate, e.g. the local Kafka's dev CA. Empty uses the
	// system roots, which is right for Event Hub.
	CAFile string
}

// Publisher writes envelopes to the configured topic.
type Publisher struct {
	writer *kafka.Writer
}

// NewPublisher returns a publisher for cfg. Nothing connects until the
// first Publish.
func NewPublisher(cfg Config) (*Publisher, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("chatevents: read CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("chatevents: no certificate in CA file %s", cfg.CAFile)
		}
		tlsConfig.RootCAs = pool
	}
	return &Publisher{writer: &kafka.Writer{
		Addr:  kafka.TCP(cfg.Broker),
		Topic: cfg.Topic,
		// One key per chat keeps its events on one partition, in order.
		Balancer:     &kafka.Hash{},
		RequiredAcks: kafka.RequireAll,
		Transport: &kafka.Transport{
			TLS: tlsConfig,
			// Event Hub's Kafka endpoint expects this literal username.
			SASL: plain.Mechanism{Username: "$ConnectionString", Password: cfg.ConnectionString},
		},
		Logger:      kafka.LoggerFunc(func(msg string, args ...any) { slog.Debug(fmt.Sprintf(msg, args...)) }),
		ErrorLogger: kafka.LoggerFunc(func(msg string, args ...any) { slog.Error(fmt.Sprintf(msg, args...)) }),
		// No compression: Event Hub's Kafka endpoint rejects compressed
		// batches (see csm-notification-service's internal/eventbus).
	}}, nil
}

// Publish writes one event keyed by entityID.
func (p *Publisher) Publish(ctx context.Context, eventType, entityID string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("chatevents: encode %s payload: %w", eventType, err)
	}
	value, err := json.Marshal(Envelope{Type: eventType, EntityID: entityID, Payload: raw})
	if err != nil {
		return fmt.Errorf("chatevents: encode %s envelope: %w", eventType, err)
	}
	if err := p.writer.WriteMessages(ctx, kafka.Message{Key: []byte(entityID), Value: value}); err != nil {
		return fmt.Errorf("chatevents: publish %s: %w", eventType, err)
	}
	return nil
}

// Close flushes and closes the writer.
func (p *Publisher) Close() error {
	return p.writer.Close()
}
