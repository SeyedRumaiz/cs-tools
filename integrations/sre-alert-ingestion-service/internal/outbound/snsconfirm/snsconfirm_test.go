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
package snsconfirm

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeAWS plays SNS: it serves the signing certificate and the SubscribeURL, and signs messages.
type fakeAWS struct {
	srv      *httptest.Server
	key      *rsa.PrivateKey
	confirms atomic.Int32
	status   int
}

func newFakeAWS(t *testing.T, status int) *fakeAWS {
	return newFakeAWSCert(t, status, "sns.amazonaws.com", time.Now().Add(time.Hour))
}

// newFakeAWSCert serves a leaf-only .pem, as real SNS does, issued to cn and valid until notAfter.
func newFakeAWSCert(t *testing.T, status int, cn string, notAfter time.Time) *fakeAWS {
	t.Helper()
	caKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	ca, _ := x509.ParseCertificate(caDER)
	leafKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	leafTmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-2 * time.Hour), NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature}
	leafDER, _ := x509.CreateCertificate(rand.Reader, leafTmpl, ca, &leafKey.PublicKey, caKey)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})

	f := &fakeAWS{key: leafKey, status: status}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".pem") {
			_, _ = w.Write(pemBytes)
			return
		}
		f.confirms.Add(1)
		w.WriteHeader(f.status)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAWS) subscribeURL() string {
	return f.srv.URL + "/?Action=ConfirmSubscription&Token=abc"
}

// signed builds a SubscriptionConfirmation signed by the fake AWS key.
func (f *fakeAWS) signed(t *testing.T, version, subscribeURL string) []byte {
	t.Helper()
	return f.signedAs(t, "SubscriptionConfirmation", version, subscribeURL)
}

// signedAs builds an SNS message of type typ signed by the fake AWS key.
func (f *fakeAWS) signedAs(t *testing.T, typ, version, subscribeURL string) []byte {
	t.Helper()
	m := message{Type: typ, MessageID: "m-1", Token: "abc",
		TopicArn: "arn:aws:sns:us-east-1:000000000000:example", Message: "You have chosen to subscribe",
		SubscribeURL: subscribeURL, Timestamp: "2026-09-30T06:00:00.000Z",
		SignatureVersion: version, SigningCertURL: f.srv.URL + "/SimpleNotificationService-test.pem"}
	hash := map[string]crypto.Hash{"1": crypto.SHA1, "2": crypto.SHA256}[version]
	h := hash.New()
	h.Write([]byte(m.stringToSign()))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, hash, h.Sum(nil))
	if err != nil {
		t.Fatal(err)
	}
	m.Signature = base64.StdEncoding.EncodeToString(sig)
	raw, _ := json.Marshal(m)
	return raw
}

// handlerFor returns a Handler that trusts the fake AWS host and CA.
func handlerFor(f *fakeAWS) (*Handler, *bytes.Buffer) {
	var logs bytes.Buffer
	var mu sync.Mutex
	h := New(slog.New(slog.NewTextHandler(&lockedWriter{w: &logs, mu: &mu}, nil)), time.Second)
	h.allowURL = func(u *url.URL) bool { return "http://"+u.Host == f.srv.URL }
	return h, &logs
}

type lockedWriter struct {
	w  *bytes.Buffer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func TestNotConfirmation_LeftForTransform(t *testing.T) {
	f := newFakeAWS(t, 200)
	h, _ := handlerFor(f)
	for _, raw := range []string{`{"Type":"Notification","Message":"{}"}`, `not json`, `{}`} {
		if h.HandleIfConfirmation([]byte(raw)) {
			t.Errorf("%s: handled as a confirmation", raw)
		}
	}
}

func TestSignedConfirmation_Confirmed(t *testing.T) {
	for _, version := range []string{"1", "2"} {
		f := newFakeAWS(t, 200)
		h, _ := handlerFor(f)
		if !h.HandleIfConfirmation(f.signed(t, version, f.subscribeURL())) {
			t.Fatal("confirmation not handled")
		}
		if f.confirms.Load() != 1 {
			t.Errorf("v%s: SubscribeURL fetched %d times, want 1", version, f.confirms.Load())
		}
	}
}

func TestUnsignedOrForged_NotFetched(t *testing.T) {
	f := newFakeAWS(t, 200)
	valid := f.signed(t, "2", f.subscribeURL())
	var tampered map[string]any
	_ = json.Unmarshal(valid, &tampered)
	tampered["SubscribeURL"] = f.srv.URL + "/?Action=ConfirmSubscription&Token=attacker"
	forged, _ := json.Marshal(tampered)

	cases := map[string][]byte{
		"unsigned": []byte(`{"Type":"SubscriptionConfirmation","SubscribeURL":"` + f.subscribeURL() + `"}`),
		"forged":   forged,
	}
	for name, raw := range cases {
		h, logs := handlerFor(f)
		if !h.HandleIfConfirmation(raw) {
			t.Fatalf("%s: want handled (answer 200, store nothing)", name)
		}
		if f.confirms.Load() != 0 {
			t.Errorf("%s: fetched %d times, want 0", name, f.confirms.Load())
		}
		if !strings.Contains(logs.String(), "failed signature check") {
			t.Errorf("%s: want a signature warning, got:\n%s", name, logs.String())
		}
	}
}

func TestCertificateNotForSNS_Rejected(t *testing.T) {
	f := newFakeAWSCert(t, 200, "attacker.example", time.Now().Add(time.Hour))
	h, logs := handlerFor(f)
	h.HandleIfConfirmation(f.signed(t, "2", f.subscribeURL()))
	if f.confirms.Load() != 0 {
		t.Error("a certificate not issued to SNS must not lead to a fetch")
	}
	if !strings.Contains(logs.String(), "not SNS") {
		t.Errorf("want the wrong-name warning, got:\n%s", logs.String())
	}
}

func TestExpiredCertificate_Rejected(t *testing.T) {
	f := newFakeAWSCert(t, 200, "sns.amazonaws.com", time.Now().Add(-time.Hour))
	h, logs := handlerFor(f)
	h.HandleIfConfirmation(f.signed(t, "2", f.subscribeURL()))
	if f.confirms.Load() != 0 {
		t.Error("an expired certificate must not lead to a fetch")
	}
	if !strings.Contains(logs.String(), "not valid at") {
		t.Errorf("want the expiry warning, got:\n%s", logs.String())
	}
}

func TestSignedUnsubscribe_HandledNotFetched(t *testing.T) {
	f := newFakeAWS(t, 200)
	h, logs := handlerFor(f)
	if !h.HandleIfConfirmation(f.signedAs(t, "UnsubscribeConfirmation", "2", f.subscribeURL())) {
		t.Fatal("unsubscribe confirmation not handled; it would be stored as an alert")
	}
	if f.confirms.Load() != 0 {
		t.Errorf("SubscribeURL fetched %d times, want 0: fetching it would subscribe again", f.confirms.Load())
	}
	if !strings.Contains(logs.String(), "subscription removed") {
		t.Errorf("want the removed-subscription warning, got:\n%s", logs.String())
	}
}

func TestForgedUnsubscribe_Ignored(t *testing.T) {
	f := newFakeAWS(t, 200)
	h, logs := handlerFor(f)
	if !h.HandleIfConfirmation([]byte(`{"Type":"UnsubscribeConfirmation","SubscribeURL":"` + f.subscribeURL() + `"}`)) {
		t.Fatal("want handled (answer 200, store nothing)")
	}
	if f.confirms.Load() != 0 || !strings.Contains(logs.String(), "failed signature check") {
		t.Errorf("fetches = %d, logs:\n%s", f.confirms.Load(), logs.String())
	}
}

func TestCertificateFromNonSNSHost_Rejected(t *testing.T) {
	f := newFakeAWS(t, 200)
	h, _ := handlerFor(f)
	h.allowURL = isSNSURL // the fake AWS host is plain http on localhost, so not SNS
	h.HandleIfConfirmation(f.signed(t, "2", f.subscribeURL()))
	if f.confirms.Load() != 0 {
		t.Error("a certificate from a non-SNS host must be rejected")
	}
}

func TestConfirmFails_LogsCritical(t *testing.T) {
	f := newFakeAWS(t, 500)
	h, logs := handlerFor(f)
	h.HandleIfConfirmation(f.signed(t, "2", f.subscribeURL()))
	if !strings.Contains(logs.String(), "CRITICAL") {
		t.Errorf("want a CRITICAL log, got:\n%s", logs.String())
	}
}

func TestSignedButNonSNSSubscribeURL_NotFetched(t *testing.T) {
	f := newFakeAWS(t, 200)
	h, logs := handlerFor(f)
	h.HandleIfConfirmation(f.signed(t, "2", "https://attacker.example/phish"))
	if f.confirms.Load() != 0 {
		t.Errorf("fetched %d times, want 0", f.confirms.Load())
	}
	if !strings.Contains(logs.String(), "not fetched") {
		t.Errorf("want the not-SNS log, got:\n%s", logs.String())
	}
}

func TestIsSNSURL(t *testing.T) {
	cases := map[string]bool{
		"https://sns.us-east-1.amazonaws.com/?Action=ConfirmSubscription":     true,
		"https://sns.cn-north-1.amazonaws.com.cn/?Action=ConfirmSubscription": true,
		"http://sns.us-east-1.amazonaws.com/":                                 false,
		"https://sns.us-east-1.amazonaws.com.evil.com/":                       false,
		"https://evil.com/sns.us-east-1.amazonaws.com":                        false,
		"https://169.254.169.254/latest/meta-data":                            false,
	}
	for raw, want := range cases {
		u, _ := url.Parse(raw)
		if got := isSNSURL(u); got != want {
			t.Errorf("isSNSURL(%s) = %v, want %v", raw, got, want)
		}
	}
}
