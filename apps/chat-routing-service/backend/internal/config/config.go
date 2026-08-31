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

// Package config loads this service's own PostgreSQL connection settings
// from the environment. Deliberately separate from entity-service's own
// internal/config (which this package's field names and DSN construction
// otherwise mirror for consistency) -- this service owns its own database,
// per the "standalone service" design covered in this feature's plan doc.
package config

import (
	"fmt"
	"net/url"
	"os"
)

// DBConfig holds the PostgreSQL connection settings for this service's own
// database (engineer presence + escalation queue -- see migrations/).
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

// DSN constructs a PostgreSQL connection string from c, mirroring
// entity-service's internal/config.Config.DSN.
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
