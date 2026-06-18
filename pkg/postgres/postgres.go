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
	return db, nil
}

func RunMigrations(db *sql.DB, path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Warn().Str("path", path).Msg("Migrations directory not found, skipping migrations")
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
		content, err := os.ReadFile(filepath.Join(path, file))
		if err != nil {
			return err
		}
		
		log.Info().Str("file", file).Msg("Executing migration")
		if _, err := db.Exec(string(content)); err != nil {
			if strings.Contains(err.Error(), "already exists") {
				log.Info().Str("file", file).Msg("Migration already applied")
				continue
			}
			return err
		}
	}
	
	log.Info().Msg("Database migrations completed successfully")
	return nil
}
