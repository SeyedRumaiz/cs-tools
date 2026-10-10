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

// TestDSN_EscapesEachPart: a space, "+", "@" or "/" in the credentials or database name must reach PostgreSQL unchanged.
func TestDSN_EscapesEachPart(t *testing.T) {
	cfg := Config{Host: "db.example.com", Port: 5432, Database: "alert db", User: "svc@corp",
		Password: "p a+ss/w@rd:%", SSLMode: "require"}
	pc, err := pgxpool.ParseConfig(dsn(cfg))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	c := pc.ConnConfig
	if c.User != cfg.User || c.Password != cfg.Password || c.Database != cfg.Database ||
		c.Host != cfg.Host || c.Port != uint16(cfg.Port) {
		t.Errorf("parsed user=%q password=%q database=%q host=%q port=%d", c.User, c.Password, c.Database, c.Host, c.Port)
	}
	if c.TLSConfig == nil {
		t.Error("sslmode=require should leave TLS on")
	}
}

// TestSizePool: unset PGPOOLMAXCONNS is derived from the writers plus reserve and spare, a too-small explicit value is refused, and the warm floor never exceeds the cap.
func TestSizePool(t *testing.T) {
	cases := []struct {
		name             string
		max              int32
		writers, minConn int
		wantMax, wantMin int32
		wantErr          bool
	}{
		{name: "unset derives", max: 0, writers: 8, minConn: 2, wantMax: 12, wantMin: 2},
		{name: "explicit kept", max: 12, writers: 8, minConn: 2, wantMax: 12, wantMin: 2},
		{name: "explicit exact", max: 10, writers: 8, minConn: 2, wantMax: 10, wantMin: 2},
		{name: "explicit too small", max: 9, writers: 8, minConn: 2, wantErr: true},
		{name: "min capped", max: 10, writers: 1, minConn: 20, wantMax: 10, wantMin: 10},
		{name: "min zero", max: 0, writers: 8, minConn: 0, wantMax: 12, wantMin: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SizePool(Config{PoolMaxConns: tc.max}, tc.writers, tc.minConn)
			if tc.wantErr {
				if err == nil {
					t.Fatal("SizePool should fail")
				}
				return
			}
			if err != nil {
				t.Fatalf("SizePool: %v", err)
			}
			if got.PoolMaxConns != tc.wantMax || got.PoolMinConns != tc.wantMin {
				t.Errorf("max=%d min=%d, want max=%d min=%d", got.PoolMaxConns, got.PoolMinConns, tc.wantMax, tc.wantMin)
			}
		})
	}
}
