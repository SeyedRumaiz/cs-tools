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
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactRawBase64Images(t *testing.T) {
	t.Run("replaces an embedded base64 image payload, leaving the rest of the HTML intact", func(t *testing.T) {
		body := []byte(`{"comments":[{"content":"<p><span>before image</span><img src=\"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAXc\"></p>"}]}`)
		got := string(redactRawBase64Images(body))
		if strings.Contains(got, "iVBORw0KGgo") {
			t.Errorf("real base64 payload still present: %s", got)
		}
		if !strings.Contains(got, "before image") {
			t.Errorf("surrounding text was dropped: %s", got)
		}
		if !strings.Contains(got, redactedInlineImageSrc) {
			t.Errorf("redacted placeholder not present: %s", got)
		}
		// Redaction must produce valid JSON -- a broken response would fail
		// every caller regardless of role.
		if !json.Valid([]byte(got)) {
			t.Errorf("redacted body is not valid JSON: %s", got)
		}
	})

	t.Run("redacts every occurrence in a multi-comment response independently", func(t *testing.T) {
		body := []byte(`{"comments":[` +
			`{"content":"<img src=\"data:image/png;base64,AAAA\">"},` +
			`{"content":"<img src=\"data:image/jpeg;base64,BBBB\">"}` +
			`]}`)
		got := string(redactRawBase64Images(body))
		if strings.Contains(got, "AAAA") || strings.Contains(got, "BBBB") {
			t.Errorf("real base64 payloads still present: %s", got)
		}
		if strings.Count(got, redactedInlineImageSrc) != 2 {
			t.Errorf("expected both images redacted independently, got: %s", got)
		}
	})

	t.Run("leaves a response with no embedded image untouched", func(t *testing.T) {
		body := []byte(`{"comments":[{"content":"<p>plain text</p>"}]}`)
		got := redactRawBase64Images(body)
		if string(got) != string(body) {
			t.Errorf("body was modified when it had nothing to redact: %s", got)
		}
	})

	t.Run("a real .iix reference (not base64) is untouched -- it carries no bytes to redact", func(t *testing.T) {
		body := []byte(`{"content":"<img src=\"/inline/0123456789abcdef0123456789abcdef.iix\">"}`)
		got := redactRawBase64Images(body)
		if string(got) != string(body) {
			t.Errorf("a .iix reference should never be touched by this redaction: %s", got)
		}
	})
}

func TestShouldRedactInlineImages(t *testing.T) {
	g := NewAccessGuard(testAccessConfig())

	t.Run("nil guard fails closed", func(t *testing.T) {
		if !shouldRedactInlineImages(nil, []string{"test-admin"}) {
			t.Error("a nil access guard must redact, never pass through")
		}
	})

	t.Run("a role without PermDownloadAttachment is redacted", func(t *testing.T) {
		for _, role := range []string{"test-viewer", "test-escalator", "test-usage-metrics-viewer"} {
			if !shouldRedactInlineImages(g, []string{role}) {
				t.Errorf("%s: expected redaction, got none", role)
			}
		}
	})

	t.Run("a role with PermDownloadAttachment is not redacted", func(t *testing.T) {
		for _, role := range []string{"test-attachment-downloader", "test-cs-engineer", "test-admin"} {
			if shouldRedactInlineImages(g, []string{role}) {
				t.Errorf("%s: expected no redaction, got redaction", role)
			}
		}
	})
}
