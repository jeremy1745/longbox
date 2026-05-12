package database

import (
	"database/sql"
	"embed"
	"fmt"
	"log/slog"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// DB wraps read and write database connections for SQLite.
// Using a single-writer connection avoids SQLITE_BUSY errors.
type DB struct {
	Write *sql.DB
	Read  *sql.DB
}

func Open(dbPath string) (*DB, error) {
	// Write connection: single connection, WAL mode
	writeDSN := fmt.Sprintf("file:%s?_pragma=journal_mode(wal)&_pragma=foreign_keys(on)&_pragma=busy_timeout(5000)", dbPath)
	writeDB, err := sql.Open("sqlite", writeDSN)
	if err != nil {
		return nil, fmt.Errorf("opening write db: %w", err)
	}
	writeDB.SetMaxOpenConns(1)

	// Read connection: pool for concurrent reads.
	// journal_mode is intentionally NOT set here — it cannot be changed on a
	// read-only handle and SQLite silently ignores the pragma, masking the
	// fact that the actual journal mode is whatever the writer established
	// on the file. foreign_keys is a session pragma so it stays.
	readDSN := fmt.Sprintf("file:%s?mode=ro&_pragma=foreign_keys(on)&_pragma=busy_timeout(5000)", dbPath)
	readDB, err := sql.Open("sqlite", readDSN)
	if err != nil {
		writeDB.Close()
		return nil, fmt.Errorf("opening read db: %w", err)
	}
	readDB.SetMaxOpenConns(4)

	return &DB{Write: writeDB, Read: readDB}, nil
}

func (db *DB) Close() error {
	if err := db.Read.Close(); err != nil {
		return err
	}
	return db.Write.Close()
}

func (db *DB) Migrate() error {
	goose.SetBaseFS(migrations)
	goose.SetLogger(goose.NopLogger())

	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("setting dialect: %w", err)
	}

	if err := goose.Up(db.Write, "migrations"); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	slog.Info("database migrations complete")
	return nil
}
