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

package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/apierror"
)

// GoogleChatSpace maps a single product to the Google Chat space that should
// receive its incident alerts.
type GoogleChatSpace struct {
	// Product identifies the product this space is dedicated to (e.g.
	// "api-manager", "identity-server"). Matched case-insensitively against
	// the product passed to SendIncidentAlert.
	Product string `json:"product"`
	// WebhookURL is that space's incoming webhook URL (Space settings > Apps
	// & integrations > Webhooks). It already carries its own key/token query
	// parameters, so no separate auth flow is needed.
	WebhookURL string `json:"webhookUrl"`
}

// GoogleChatConfig holds the configuration for the Google Chat notification
// channel: one space per product, since each WSO2 product has its own space.
type GoogleChatConfig struct {
	Spaces []GoogleChatSpace
	// MentionDomains lists the email domains (e.g. "wso2.com") whose users
	// are real Google Chat users and can be @mentioned. Google prints an
	// unrecognized mention as raw "<users/...>" text instead of rejecting it,
	// so an address outside these domains is never mentioned. Empty disables
	// mentions entirely.
	MentionDomains []string
}

// GoogleChatClient posts messages to a Google Chat space via an incoming
// webhook, routing each alert to the space configured for the case's
// product. Unlike the OAuth2-authenticated clients in this package, a
// webhook URL is the only credential required.
//
// NewGoogleChatClient never fails, so it is safe to construct with a
// zero-value GoogleChatConfig (e.g. when this channel is not yet configured
// for a given deployment) — a missing or unmatched product only surfaces as
// an error the first time SendIncidentAlert is called for it.
type GoogleChatClient struct {
	http                 *http.Client
	webhookURLsByProduct map[string]string
	mentionDomains       map[string]bool
}

// NewGoogleChatClient constructs a GoogleChatClient that routes alerts to the
// webhook configured for each product in cfg.Spaces.
func NewGoogleChatClient(cfg GoogleChatConfig) *GoogleChatClient {
	webhookURLsByProduct := make(map[string]string, len(cfg.Spaces))
	for _, space := range cfg.Spaces {
		product := normalizeProduct(space.Product)
		if product == "" || strings.TrimSpace(space.WebhookURL) == "" {
			continue
		}
		// A second space normalizing to the same product (e.g. "API-Manager"
		// and " api-manager ") is a configuration mistake — mark it
		// unconfigured rather than silently routing to whichever URL came
		// last.
		if _, exists := webhookURLsByProduct[product]; exists {
			webhookURLsByProduct[product] = ""
			continue
		}
		webhookURLsByProduct[product] = space.WebhookURL
	}
	mentionDomains := make(map[string]bool, len(cfg.MentionDomains))
	for _, d := range cfg.MentionDomains {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			mentionDomains[d] = true
		}
	}
	return &GoogleChatClient{
		http:                 &http.Client{Timeout: 10 * time.Second},
		webhookURLsByProduct: webhookURLsByProduct,
		mentionDomains:       mentionDomains,
	}
}

// canMention reports whether email is safe to place in mention markup and
// belongs to a configured Google Workspace domain.
func (c *GoogleChatClient) canMention(email string) bool {
	if !mentionEmailRe.MatchString(email) {
		return false
	}
	at := strings.LastIndex(email, "@")
	return c.mentionDomains[strings.ToLower(email[at+1:])]
}

// normalizeProduct makes product matching case- and whitespace-insensitive.
func normalizeProduct(product string) string {
	return strings.ToLower(strings.TrimSpace(product))
}

// redactURLError strips the request URL — which carries the webhook's secret
// key/token query parameters — out of a *url.Error before it's wrapped and
// potentially logged, keeping only the underlying (safe) failure reason.
func redactURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

// chatCardMessage is the wire shape Google Chat's webhook API expects for a
// single card message: https://developers.google.com/chat/api/guides/message-formats/cards
type chatCardMessage struct {
	// Text is the message's plain-text part, shown above the card. It is the
	// only place a Google Chat webhook message can @mention (ping) someone.
	Text    string            `json:"text,omitempty"`
	CardsV2 []chatCardWrapper `json:"cardsV2"`
}

type chatCardWrapper struct {
	CardID string   `json:"cardId"`
	Card   chatCard `json:"card"`
}

type chatCard struct {
	Header   chatCardHeader    `json:"header"`
	Sections []chatCardSection `json:"sections"`
}

type chatCardHeader struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
}

type chatCardSection struct {
	Header  string           `json:"header,omitempty"`
	Widgets []chatCardWidget `json:"widgets"`
}

// chatCardWidget is a union type: exactly one of TextParagraph or ButtonList
// is set per widget, matching Google Chat's widget schema.
type chatCardWidget struct {
	TextParagraph *chatTextParagraph `json:"textParagraph,omitempty"`
	DecoratedText *chatDecoratedText `json:"decoratedText,omitempty"`
	ButtonList    *chatButtonList    `json:"buttonList,omitempty"`
}

// chatDecoratedText renders a small label above a value: one row of a card.
type chatDecoratedText struct {
	TopLabel string `json:"topLabel,omitempty"`
	Text     string `json:"text"`
	WrapText bool   `json:"wrapText"`
}

type chatTextParagraph struct {
	Text string `json:"text"`
}

type chatButtonList struct {
	Buttons []chatButton `json:"buttons"`
}

type chatButton struct {
	Text    string      `json:"text"`
	OnClick chatOnClick `json:"onClick"`
}

type chatOnClick struct {
	OpenLink chatOpenLink `json:"openLink"`
}

type chatOpenLink struct {
	URL string `json:"url"`
}

// SendIncidentAlert posts a card message announcing a newly created
// incident/case, with a button linking back to the case in the CSM portal,
// to the Google Chat space configured for the given product.
func (c *GoogleChatClient) SendIncidentAlert(ctx context.Context, product, title, shortDescription, portalURL string) error {
	if title == "" {
		return fmt.Errorf("notifications: title is required")
	}
	webhookURL, ok := c.webhookURLsByProduct[normalizeProduct(product)]
	if !ok || webhookURL == "" {
		return fmt.Errorf("notifications: no google chat space configured for product %q", product)
	}

	msg := chatCardMessage{
		CardsV2: []chatCardWrapper{
			{
				CardID: "incident-alert",
				Card: chatCard{
					Header: chatCardHeader{Title: title},
					Sections: []chatCardSection{
						{
							Header: "Short Description",
							Widgets: []chatCardWidget{
								{TextParagraph: &chatTextParagraph{Text: shortDescription}},
							},
						},
						{
							Widgets: []chatCardWidget{
								{
									ButtonList: &chatButtonList{
										Buttons: []chatButton{
											{
												Text:    "Open in CSM Portal",
												OnClick: chatOnClick{OpenLink: chatOpenLink{URL: portalURL}},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	return c.postCard(ctx, webhookURL, msg, "")
}

// HasSpace reports whether a Google Chat space is configured for product.
func (c *GoogleChatClient) HasSpace(product string) bool {
	return c.webhookURLsByProduct[normalizeProduct(product)] != ""
}

// LiveChatDetail is one labeled row of a live-chat card. Value may use
// Google Chat's simple HTML (<b>, <i>, <br>), so callers must escape any
// user-supplied text.
type LiveChatDetail struct {
	Label string
	Value string
}

// LiveChatAlert describes a live-chat card: a header, labeled rows, an
// optional note, and the portal link behind the button. Title, at least one
// detail and PortalURL are required.
type LiveChatAlert struct {
	Title     string
	Subtitle  string
	Details   []LiveChatDetail
	Note      string
	PortalURL string
	// ThreadKey groups every card sharing it into one Google Chat thread, so
	// follow-ups about the same chat appear under the original card. Empty
	// posts a standalone message.
	ThreadKey string
	// MentionEmail @mentions that Google Chat user above the card so they are
	// notified, if its domain is one of the client's MentionDomains. If Google
	// rejects the mention outright, the card is re-sent without it.
	MentionEmail string
}

// mentionEmailRe is deliberately strict: the address is placed inside Google
// Chat's <users/...> markup, so it must not contain angle brackets or spaces.
var mentionEmailRe = regexp.MustCompile(`^[A-Za-z0-9._%+'-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`)

// SendLiveChatAlert posts a live-chat card with an "Open in CSM Portal" button
// to the space configured for product. The link only opens the portal;
// accepting still happens there, as the signed-in engineer.
func (c *GoogleChatClient) SendLiveChatAlert(ctx context.Context, product string, alert LiveChatAlert) error {
	if alert.Title == "" || len(alert.Details) == 0 || alert.PortalURL == "" {
		return fmt.Errorf("notifications: title, details and portalURL are required")
	}
	webhookURL, ok := c.webhookURLsByProduct[normalizeProduct(product)]
	if !ok || webhookURL == "" {
		return fmt.Errorf("notifications: no google chat space configured for product %q", product)
	}

	detailWidgets := make([]chatCardWidget, 0, len(alert.Details))
	for _, d := range alert.Details {
		if d.Value == "" {
			continue
		}
		detailWidgets = append(detailWidgets, chatCardWidget{DecoratedText: &chatDecoratedText{TopLabel: d.Label, Text: d.Value, WrapText: true}})
	}
	sections := []chatCardSection{{Widgets: detailWidgets}}
	if alert.Note != "" {
		sections = append(sections, chatCardSection{Widgets: []chatCardWidget{
			{TextParagraph: &chatTextParagraph{Text: alert.Note}},
		}})
	}
	sections = append(sections, chatCardSection{Widgets: []chatCardWidget{
		{
			ButtonList: &chatButtonList{
				Buttons: []chatButton{
					{
						Text:    "Open in CSM Portal",
						OnClick: chatOnClick{OpenLink: chatOpenLink{URL: alert.PortalURL}},
					},
				},
			},
		},
	}})

	msg := chatCardMessage{
		CardsV2: []chatCardWrapper{{
			CardID: "live-chat-alert",
			Card:   chatCard{Header: chatCardHeader{Title: alert.Title, Subtitle: alert.Subtitle}, Sections: sections},
		}},
	}
	if c.canMention(alert.MentionEmail) {
		msg.Text = "<users/" + alert.MentionEmail + "> a live chat was assigned to you. Please accept it in the CSM Portal."
	}

	err := c.postCard(ctx, webhookURL, msg, alert.ThreadKey)
	var apiErr *apierror.Error
	if err != nil && msg.Text != "" && errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest {
		// An unresolvable mention must not cost the team the alert itself.
		slog.Warn("notifications: google chat rejected the mention, re-sending the card without it")
		msg.Text = ""
		err = c.postCard(ctx, webhookURL, msg, alert.ThreadKey)
	}
	return err
}

func (c *GoogleChatClient) postCard(ctx context.Context, webhookURL string, msg chatCardMessage, threadKey string) error {
	if threadKey != "" {
		u, err := url.Parse(webhookURL)
		if err != nil {
			return fmt.Errorf("notifications: parse google chat webhook url: %w", redactURLError(err))
		}
		q := u.Query()
		q.Set("threadKey", threadKey)
		q.Set("messageReplyOption", "REPLY_MESSAGE_FALLBACK_TO_NEW_THREAD")
		u.RawQuery = q.Encode()
		webhookURL = u.String()
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("notifications: encode google chat message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notifications: build google chat request: %w", redactURLError(err))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("notifications: post google chat message: %w", redactURLError(err))
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("notifications: read google chat response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		const maxErrBody = 256
		excerpt := respBody
		if len(excerpt) > maxErrBody {
			excerpt = excerpt[:maxErrBody]
		}
		return &apierror.Error{StatusCode: resp.StatusCode, Body: string(excerpt)}
	}

	return nil
}
