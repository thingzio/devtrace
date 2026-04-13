package main

import (
	"context"
	"fmt"
	"os"

	_ "github.com/lib/pq"

	"github.com/thingzio/devtrace/pkg/data/postgres"
	"github.com/thingzio/devtrace/pkg/tenant"
)

func main() {
	ctx := context.Background()

	store, err := postgres.NewFromEnv(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}

	db := store.DB()

	tn, err := tenant.UpsertTenant(ctx, db, 12345, "test-user", "test@example.com", "", "Test User", "", "", "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "upsert tenant: %v\n", err)
		os.Exit(1)
	}

	if err := tenant.AcceptToS(ctx, db, tn.ID); err != nil {
		fmt.Fprintf(os.Stderr, "accept tos: %v\n", err)
		os.Exit(1)
	}

	token, err := tenant.CreateAPIToken(ctx, db, tn.ID, "local-dev")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create token: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Tenant ID:  %s\n", tn.ID)
	fmt.Printf("Username:   %s\n", tn.Username)
	fmt.Printf("Plan:       %s\n", tn.Plan)
	fmt.Printf("API Token:  %s\n\n", token)
	fmt.Println("Test with:")
	fmt.Printf("  curl -s -H 'Authorization: Bearer %s' http://localhost:8080/api/v1/score/octocat | jq .\n", token)
}
