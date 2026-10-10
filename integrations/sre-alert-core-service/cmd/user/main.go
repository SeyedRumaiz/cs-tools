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

// Command user manages integration_users rows (e.g. webhook-integration-user): create/rotate, list, enable, disable. Uses the same PG* env vars as the server.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"alert-core-service/internal/auth"
	"alert-core-service/internal/postgres"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	pool, err := connect()
	if err != nil {
		log.Fatalf("user: %v", err)
	}
	defer pool.Close()
	repo := auth.NewUserRepo(pool)

	switch os.Args[1] {
	case "create":
		runCreate(repo, os.Args[2:])
	case "list":
		runList(repo, os.Args[2:])
	case "enable":
		runSetEnabled(repo, os.Args[2:], true)
	case "disable":
		runSetEnabled(repo, os.Args[2:], false)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: user <create|list|enable|disable> [flags]")
	fmt.Fprintln(os.Stderr, "  create  -username <name> [-secret <value>] [-created-by <who>]")
	fmt.Fprintln(os.Stderr, "                                                 create a user, or rotate its secret if it already exists")
	fmt.Fprintln(os.Stderr, "  list    [-username <name>]                    list all users, or show one user's full detail")
	fmt.Fprintln(os.Stderr, "  enable  -username <name>                      re-enable a user")
	fmt.Fprintln(os.Stderr, "  disable -username <name>                      disable a user")
}

// connect reads PG* env vars (same ones the server uses) and opens a pool.
func connect() (*pgxpool.Pool, error) {
	cfg, err := postgres.ConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("read postgres config: %w", err)
	}
	pool, err := postgres.Connect(cfg, 10*time.Second, 10*time.Second, false)
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	return pool, nil
}

func runCreate(repo *auth.UserRepo, args []string) {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	username := fs.String("username", "", "internal user to create, e.g. webhook-integration-user (required)")
	secret := fs.String("secret", "", "secret to set; if omitted, a random secret is generated and printed once")
	createdBy := fs.String("created-by", "", "operator provisioning this user; defaults to $USER, falls back to \"unknown\"")
	fs.Parse(args)

	if *username == "" {
		log.Fatal("user create: -username is required")
	}

	ctx := context.Background()
	existing, err := repo.Get(ctx, *username)
	isNew := errors.Is(err, auth.ErrUserNotFound)
	if err != nil && !isNew {
		log.Fatalf("user create: %v", err)
	}

	plainSecret := *secret
	generated := false
	if plainSecret == "" {
		s, err := generateSecret()
		if err != nil {
			log.Fatalf("user create: generate secret: %v", err)
		}
		plainSecret = s
		generated = true
	}

	salt, err := auth.GenerateSalt()
	if err != nil {
		log.Fatalf("user create: generate salt: %v", err)
	}
	hash, err := auth.HashSecret(plainSecret, salt, auth.Iterations)
	if err != nil {
		log.Fatalf("user create: hash secret: %v", err)
	}

	now := time.Now().UTC()
	u := auth.User{
		Username:        *username,
		SecretHash:      base64.StdEncoding.EncodeToString(hash),
		Salt:            base64.StdEncoding.EncodeToString(salt),
		Iterations:      auth.Iterations,
		Enabled:         true,
		CreatedAt:       now,
		CreatedBy:       resolveCreatedBy(*createdBy),
		UpdatedAt:       now,
		SecretRotatedAt: now,
	}
	if !isNew {
		u.CreatedAt = existing.CreatedAt
		if *createdBy == "" {
			u.CreatedBy = existing.CreatedBy
		}
		u.Enabled = existing.Enabled
	}
	// u.ID is left unset for both branches: Upsert's INSERT omits the id column, so a new row
	// gets one from integration_users.id's gen_random_uuid() default, and an existing row's
	// ON CONFLICT clause never touches id.

	if err := repo.Upsert(ctx, u); err != nil {
		log.Fatalf("user create: upsert user: %v", err)
	}

	verb := "created"
	if !isNew {
		verb = "rotated"
	}
	fmt.Printf("%s internal user %q\n", verb, *username)
	if generated {
		fmt.Printf("secret (shown once, store securely): %s\n", plainSecret)
	}
}

// resolveCreatedBy prefers an explicit -created-by flag, then $USER, then "unknown".
func resolveCreatedBy(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "unknown"
}

func runList(repo *auth.UserRepo, args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	username := fs.String("username", "", "show full detail for one user instead of the summary table")
	fs.Parse(args)

	ctx := context.Background()
	if *username != "" {
		u, err := repo.Get(ctx, *username)
		if err != nil {
			log.Fatalf("user list: %v", err)
		}
		printUserDetail(u)
		return
	}

	users, err := repo.List(ctx)
	if err != nil {
		log.Fatalf("user list: %v", err)
	}
	if len(users) == 0 {
		fmt.Println("no internal users found")
		return
	}
	fmt.Printf("%-30s %-8s %s\n", "USERNAME", "ENABLED", "CREATED_BY")
	for _, u := range users {
		fmt.Printf("%-30s %-8t %s\n", u.Username, u.Enabled, u.CreatedBy)
	}
}

// printUserDetail prints every field of one user, one per line; never prints secret_hash/salt.
func printUserDetail(u auth.User) {
	fmt.Printf("username:           %s\n", u.Username)
	fmt.Printf("id:                 %s\n", u.ID)
	fmt.Printf("enabled:            %t\n", u.Enabled)
	fmt.Printf("iterations:         %d\n", u.Iterations)
	fmt.Printf("created_at:         %s\n", formatTime(u.CreatedAt))
	fmt.Printf("created_by:         %s\n", u.CreatedBy)
	fmt.Printf("updated_at:         %s\n", formatTime(u.UpdatedAt))
	fmt.Printf("secret_rotated_at:  %s\n", formatTime(u.SecretRotatedAt))
	fmt.Printf("last_used_at:       %s\n", formatTime(u.LastUsedAt))
}

// formatTime renders a timestamp as RFC3339, or "-" for an unset (zero) one.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format(time.RFC3339)
}

func runSetEnabled(repo *auth.UserRepo, args []string, enabled bool) {
	name := "enable"
	if !enabled {
		name = "disable"
	}
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	username := fs.String("username", "", "internal user to "+name+" (required)")
	fs.Parse(args)

	if *username == "" {
		log.Fatalf("user %s: -username is required", name)
	}

	if _, err := repo.Get(context.Background(), *username); err != nil {
		log.Fatalf("user %s: %v", name, err)
	}
	if err := repo.SetEnabled(context.Background(), *username, enabled); err != nil {
		log.Fatalf("user %s: %v", name, err)
	}
	fmt.Printf("%sd internal user %q\n", name, *username)
}

// generateSecret returns a random, URL-safe base64-encoded 32-byte secret.
func generateSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
