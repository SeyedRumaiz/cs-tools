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

package auth

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRequireWakeToken: only the exact shared token passes, and an unset token rejects everything.
func TestRequireWakeToken(t *testing.T) {
	const token = "4f9c0e7d2b1a8e6f5c3d9b0a7e1f2c4d6b8a0e9f3c5d7b1a2e4f6c8d0b9a7e5f"
	call := func(configured, header string) int {
		h := RequireWakeToken(configured, slog.New(slog.NewTextHandler(io.Discard, nil)))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		}))
		r := httptest.NewRequest(http.MethodPost, "/alertz", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	cases := []struct {
		name, configured, header string
		want                     int
	}{
		{"correct token", token, "Bearer " + token, http.StatusAccepted},
		{"scheme is case-insensitive", token, "bearer " + token, http.StatusAccepted},
		{"wrong token", token, "Bearer " + token[:len(token)-1] + "0", http.StatusUnauthorized},
		{"prefix of the token", token, "Bearer " + token[:32], http.StatusUnauthorized},
		{"no header", token, "", http.StatusUnauthorized},
		{"basic auth", token, "Basic dXNlcjpzZWNyZXQ=", http.StatusUnauthorized},
		{"empty bearer", token, "Bearer ", http.StatusUnauthorized},
		{"token unset rejects everything", "", "Bearer ", http.StatusUnauthorized},
		{"token unset rejects any bearer", "", "Bearer anything", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		if got := call(tc.configured, tc.header); got != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, got, tc.want)
		}
	}
}
