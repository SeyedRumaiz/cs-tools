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

// Package config loads this service's PostgreSQL connection settings from
// the environment. This service usually shares a database with
// entity-service, kept separate via its own schema (see Schema).
package config

import (
	"fmt"
	"net/url"
	"os"
)

// Schema is the Postgres schema this service's tables live in. NewPool
// sets it as each connection's search_path via AfterConnect rather than a
// DSN parameter, since pgx's URI parser rejects a bare "search_path" query
// param. That lets queries use unqualified table names.
const Schema = "chat_routing"

// DBConfig holds the PostgreSQL connection settings this service uses.
type DBConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	Name     string
	SSLMode  string
}

// LoadDB reads PostgreSQL settings from the environment. Call after
// cmd/server/main.go's loadDotEnv, so .env values are already applied.
func LoadDB() DBConfig {
	return DBConfig{
		Host:     envOrDefault("DB_HOST", "localhost"),
		Port:     envOrDefault("DB_PORT", "5432"),
		User:     os.Getenv("DB_USER"),
		Password: os.Getenv("DB_PASSWORD"),
		Name:     os.Getenv("DB_NAME"),
		SSLMode:  envOrDefault("DB_SSLMODE", "disable"),
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Validate checks that the fields required to connect are present. Called
// once at startup so a missing credential is a fatal, immediately obvious
// error instead of a connection failure deep in the first request.
func (c DBConfig) Validate() error {
	if c.User == "" {
		return fmt.Errorf("DB_USER is required")
	}
	if c.Password == "" {
		return fmt.Errorf("DB_PASSWORD is required")
	}
	if c.Name == "" {
		return fmt.Errorf("DB_NAME is required")
	}
	return nil
}

// DSN constructs a PostgreSQL connection string from c. Doesn't set
// search_path -- see NewPool for how Schema gets applied instead.
func (c DBConfig) DSN() string {
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.User, c.Password),
		Host:   c.Host + ":" + c.Port,
		Path:   c.Name,
	}
	q := u.Query()
	q.Set("sslmode", c.SSLMode)
	u.RawQuery = q.Encode()
	return u.String()
}
