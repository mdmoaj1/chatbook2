package postgres

import (
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "github.com/lib/pq"
	"github.com/rs/zerolog/log"
)

func Connect(url string) (*sql.DB, error) {
	db, err := sql.Open("postgres", url)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	// Connection pool tuned for small server (2GB RAM, 2 cores)
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	return db, nil
}

// RunMigrations executes SQL migration files in order.
// - Strips goose markers so plain db.Exec works correctly.
// - Tracks applied migrations in schema_migrations table (idempotent).
func RunMigrations(db *sql.DB, path string) error {
	// Ensure migrations tracking table exists
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		filename   TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`)
	if err != nil {
		return err
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Warn().Str("path", path).Msg("Migrations directory not found, skipping")
			return nil
		}
		return err
	}

	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".sql" {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)

	for _, file := range files {
		// Check if already applied
		var count int
		db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE filename = $1`, file).Scan(&count)
		if count > 0 {
			log.Info().Str("file", file).Msg("Migration already applied, skipping")
			continue
		}

		content, err := os.ReadFile(filepath.Join(path, file))
		if err != nil {
			return err
		}

		sql := extractUpSection(string(content))
		if strings.TrimSpace(sql) == "" {
			log.Warn().Str("file", file).Msg("No UP section found in migration, skipping")
			continue
		}

		log.Info().Str("file", file).Msg("Executing migration")
		if _, err := db.Exec(sql); err != nil {
			return err
		}

		// Mark as applied
		if _, err := db.Exec(`INSERT INTO schema_migrations (filename) VALUES ($1)`, file); err != nil {
			return err
		}
		log.Info().Str("file", file).Msg("Migration applied successfully")
	}

	log.Info().Msg("Database migrations completed successfully")
	return nil
}

// extractUpSection strips goose markers and returns only the SQL between
// "-- +goose Up" and "-- +goose Down" (or end of file if no Down section).
// Also strips "+goose StatementBegin" / "+goose StatementEnd" markers.
func extractUpSection(content string) string {
	lines := strings.Split(content, "\n")

	inUp := false
	hasGoose := false
	var result []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Goose control markers — skip them all
		if strings.HasPrefix(trimmed, "-- +goose") {
			hasGoose = true
			if trimmed == "-- +goose Up" {
				inUp = true
			} else if trimmed == "-- +goose Down" {
				inUp = false
			}
			// Skip the marker line itself
			continue
		}

		if inUp {
			result = append(result, line)
		}
	}

	// If no goose markers found, execute the whole file as-is
	if !hasGoose {
		return content
	}

	return strings.Join(result, "\n")
}
