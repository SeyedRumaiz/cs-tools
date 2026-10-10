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

package postgres

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestConnStringKeepsReservedCharacters(t *testing.T) {
	cfg := Config{Host: "db.example.com", Port: 5432, Database: "alert db", User: "svc@corp", Password: "p a+ss:/#?%", SSLMode: "require"}
	got, err := pgxpool.ParseConfig(connString(cfg))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	c := got.ConnConfig
	if c.User != cfg.User || c.Password != cfg.Password || c.Host != cfg.Host || c.Port != uint16(cfg.Port) || c.Database != cfg.Database {
		t.Fatalf("round trip mismatch: user=%q password=%q host=%q port=%d database=%q", c.User, c.Password, c.Host, c.Port, c.Database)
	}
	if got := c.RuntimeParams["sslmode"]; got != "" {
		t.Fatalf("sslmode leaked into runtime params: %q", got)
	}
}

// TestSizePool: unset derives poll.concurrency plus headroom, an explicit value at or above that is kept, and one leaving no headroom is refused.
func TestSizePool(t *testing.T) {
	cases := []struct {
		name    string
		max     int32
		workers int
		want    int32
		wantErr bool
	}{
		{name: "unset derives", max: 0, workers: 64, want: 80},
		{name: "explicit kept", max: 100, workers: 64, want: 100},
		{name: "explicit at minimum", max: 80, workers: 64, want: 80},
		{name: "explicit at workers", max: 64, workers: 64, wantErr: true},
		{name: "explicit too small", max: 79, workers: 64, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SizePool(Config{PoolMaxConns: tc.max}, tc.workers)
			if tc.wantErr {
				if err == nil {
					t.Fatal("SizePool should fail")
				}
				return
			}
			if err != nil {
				t.Fatalf("SizePool: %v", err)
			}
			if got.PoolMaxConns != tc.want {
				t.Errorf("PoolMaxConns = %d, want %d", got.PoolMaxConns, tc.want)
			}
		})
	}
}
