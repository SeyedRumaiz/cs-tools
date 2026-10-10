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
	"encoding/base64"
	"io"
	"log/slog"
	"testing"
)

func TestVerifySecret(t *testing.T) {
	salt, err := GenerateSalt()
	if err != nil {
		t.Fatalf("GenerateSalt: %v", err)
	}
	const secret = "correct-horse-battery-staple"
	saltB64 := base64.StdEncoding.EncodeToString(salt)
	hash, err := HashSecret(secret, salt, Iterations)
	if err != nil {
		t.Fatalf("HashSecret: %v", err)
	}
	hashB64 := base64.StdEncoding.EncodeToString(hash)

	if !VerifySecret(secret, saltB64, hashB64, Iterations) {
		t.Error("the correct secret must verify")
	}
	if VerifySecret("wrong-secret", saltB64, hashB64, Iterations) {
		t.Error("a wrong secret must not verify")
	}
	if VerifySecret(secret[:5], saltB64, hashB64, Iterations) {
		t.Error("a prefix of the secret must not verify")
	}
	// A different iteration count derives a different key, so it must not verify.
	if VerifySecret(secret, saltB64, hashB64, Iterations+1) {
		t.Error("a mismatched iteration count must not verify")
	}
	for name, bad := range map[string][2]string{
		"malformed salt": {"not-base64!!", hashB64},
		"malformed hash": {saltB64, "not-base64!!"},
	} {
		if VerifySecret(secret, bad[0], bad[1], Iterations) {
			t.Errorf("%s must not verify", name)
		}
	}
}

func TestGenerateSalt_IsRandomAndCorrectLength(t *testing.T) {
	a, err := GenerateSalt()
	if err != nil {
		t.Fatalf("GenerateSalt: %v", err)
	}
	b, err := GenerateSalt()
	if err != nil {
		t.Fatalf("GenerateSalt: %v", err)
	}
	if len(a) != SaltLen {
		t.Errorf("salt length = %d, want %d", len(a), SaltLen)
	}
	if string(a) == string(b) {
		t.Error("two salts must not be identical")
	}
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
