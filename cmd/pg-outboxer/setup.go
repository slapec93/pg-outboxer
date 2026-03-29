package main

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/lib/pq"
	"github.com/spf13/cobra"

	"github.com/slapec93/pg-outboxer/internal/config"
)

//go:embed all:migrations
var migrationsFS embed.FS

var (
	setupCDC   bool
	forceSetup bool
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Setup database tables and indexes",
	Long: `Creates the required database tables and indexes for pg-outboxer.

This command will:
- Create the outbox table with indexes
- Create the outbox_dead_letter table with indexes
- Optionally create CDC publication and replication slot (--cdc flag)

The setup is idempotent and safe to run multiple times.`,
	RunE: runSetup,
}

func init() {
	setupCmd.Flags().BoolVar(&setupCDC, "cdc", false,
		"Also create CDC publication and replication slot")
	setupCmd.Flags().BoolVar(&forceSetup, "force", false,
		"Force recreation of CDC slot if it exists")
	rootCmd.AddCommand(setupCmd)
}

func runSetup(cmd *cobra.Command, args []string) error {
	slog.Info("starting pg-outboxer setup", "config", cfgFile)

	// Load config
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Validate config
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// Connect to database
	db, err := sql.Open("postgres", cfg.Source.DSN)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("database not reachable: %w", err)
	}

	slog.Info("connected to database")

	// Run migrations
	if err := runMigrations(ctx, db, cfg); err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}

	// Setup CDC if requested
	if setupCDC {
		if cfg.Source.Type != "cdc" {
			slog.Warn("--cdc flag provided but source.type is not 'cdc' in config")
		}
		if err := setupCDCMode(ctx, db, cfg); err != nil {
			return fmt.Errorf("CDC setup failed: %w", err)
		}
	}

	fmt.Println()
	fmt.Println("✅ Setup complete!")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Your application writes to the 'outbox' table in the same transaction as business data")
	fmt.Println("  2. Start pg-outboxer: pg-outboxer run --config=config.yaml")
	fmt.Println("  3. Monitor metrics: curl http://localhost:9090/metrics")

	if setupCDC {
		fmt.Println()
		fmt.Println("CDC is enabled. Make sure your postgresql.conf has:")
		fmt.Println("  wal_level = logical")
		fmt.Println("  max_replication_slots >= 1")
	}

	return nil
}

func runMigrations(ctx context.Context, db *sql.DB, cfg *config.Config) error {
	// Read all migration files
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("failed to read migrations: %w", err)
	}

	// Sort by filename to ensure order
	var migrationFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			migrationFiles = append(migrationFiles, entry.Name())
		}
	}
	sort.Strings(migrationFiles)

	fmt.Println("Running migrations...")
	fmt.Println()

	// Execute each migration
	for _, filename := range migrationFiles {
		fmt.Printf("  → %s ... ", filename)

		content, err := migrationsFS.ReadFile(filepath.Join("migrations", filename))
		if err != nil {
			return fmt.Errorf("failed to read migration %s: %w", filename, err)
		}

		// Execute migration
		if _, err := db.ExecContext(ctx, string(content)); err != nil {
			fmt.Println("❌")
			return fmt.Errorf("failed to execute migration %s: %w", filename, err)
		}

		fmt.Println("✅")
	}

	// Verify tables were created
	var outboxExists, deadLetterExists bool

	err = db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT FROM information_schema.tables
			WHERE table_schema = 'public'
			AND table_name = 'outbox'
		)
	`).Scan(&outboxExists)
	if err != nil {
		return fmt.Errorf("failed to verify outbox table: %w", err)
	}

	err = db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT FROM information_schema.tables
			WHERE table_schema = 'public'
			AND table_name = 'outbox_dead_letter'
		)
	`).Scan(&deadLetterExists)
	if err != nil {
		return fmt.Errorf("failed to verify dead letter table: %w", err)
	}

	fmt.Println()
	if outboxExists {
		fmt.Println("✅ outbox table created")
	} else {
		return fmt.Errorf("outbox table not found after migration")
	}

	if deadLetterExists {
		fmt.Println("✅ outbox_dead_letter table created")
	} else {
		return fmt.Errorf("outbox_dead_letter table not found after migration")
	}

	return nil
}

func setupCDCMode(ctx context.Context, db *sql.DB, cfg *config.Config) error {
	fmt.Println()
	fmt.Println("Setting up CDC (logical replication)...")
	fmt.Println()

	slotName := cfg.Source.SlotName
	pubName := cfg.Source.Publication
	tableName := cfg.Source.Table

	// Check if publication exists
	var pubExists bool
	err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_publication WHERE pubname = $1
		)
	`, pubName).Scan(&pubExists)
	if err != nil {
		return fmt.Errorf("failed to check publication: %w", err)
	}

	if pubExists {
		fmt.Printf("  ℹ️  Publication '%s' already exists\n", pubName)
	} else {
		fmt.Printf("  → Creating publication '%s' ... ", pubName)
		_, err = db.ExecContext(ctx, fmt.Sprintf(
			"CREATE PUBLICATION %s FOR TABLE %s",
			pubName, tableName,
		))
		if err != nil {
			fmt.Println("❌")
			return fmt.Errorf("failed to create publication: %w", err)
		}
		fmt.Println("✅")
	}

	// Check if replication slot exists
	var slotExists bool
	err = db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_replication_slots WHERE slot_name = $1
		)
	`, slotName).Scan(&slotExists)
	if err != nil {
		return fmt.Errorf("failed to check replication slot: %w", err)
	}

	if slotExists {
		if forceSetup {
			fmt.Printf("  → Dropping existing slot '%s' ... ", slotName)
			_, err = db.ExecContext(ctx, fmt.Sprintf(
				"SELECT pg_drop_replication_slot('%s')",
				slotName,
			))
			if err != nil {
				fmt.Println("❌")
				return fmt.Errorf("failed to drop replication slot: %w", err)
			}
			fmt.Println("✅")
			slotExists = false
		} else {
			fmt.Printf("  ℹ️  Replication slot '%s' already exists (use --force to recreate)\n", slotName)
		}
	}

	if !slotExists {
		fmt.Printf("  → Creating replication slot '%s' ... ", slotName)
		_, err = db.ExecContext(ctx, fmt.Sprintf(
			"SELECT pg_create_logical_replication_slot('%s', 'pgoutput')",
			slotName,
		))
		if err != nil {
			fmt.Println("❌")
			return fmt.Errorf("failed to create replication slot: %w", err)
		}
		fmt.Println("✅")
	}

	fmt.Println()
	fmt.Println("✅ CDC setup complete")

	return nil
}
