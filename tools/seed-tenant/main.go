package main

import (
	"context"
	"fmt"
	"log"

	_ "github.com/lib/pq"

	"github.com/thingzio/devtrace/pkg/data/postgres"
	"github.com/thingzio/devtrace/pkg/tenant"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()

	store, err := postgres.NewFromEnv(ctx)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer store.Close()

	if merr := store.Migrate(ctx); merr != nil {
		return fmt.Errorf("migrate: %w", merr)
	}

	db := store.DB()

	tn, err := tenant.UpsertTenant(ctx, db, 12345, "test-user", "test@example.com", "", "Test User", "", "", "")
	if err != nil {
		return fmt.Errorf("upsert tenant: %w", err)
	}

	if terr := tenant.AcceptToS(ctx, db, tn.ID); terr != nil {
		return fmt.Errorf("accept tos: %w", terr)
	}

	token, err := tenant.CreateAPIToken(ctx, db, tn.ID, "local-dev")
	if err != nil {
		return fmt.Errorf("create token: %w", err)
	}

	fmt.Printf("Tenant ID:  %s\n", tn.ID)
	fmt.Printf("Username:   %s\n", tn.Username)
	fmt.Printf("Plan:       %s\n", tn.Plan)
	fmt.Printf("API Token:  %s\n\n", token)
	fmt.Println("Test with:")
	fmt.Printf("  curl -s -H 'Authorization: Bearer %s' http://localhost:8080/api/v1/score/octocat | jq .\n", token)

	return nil
}
