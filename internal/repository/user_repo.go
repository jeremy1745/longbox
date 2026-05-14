package repository

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/jeremy/longbox/internal/model"
)

type UserRepo struct {
	read  *sql.DB
	write *sql.DB
}

func NewUserRepo(read, write *sql.DB) *UserRepo {
	return &UserRepo{read: read, write: write}
}

func (r *UserRepo) Create(u *model.User) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := r.write.Exec(`
		INSERT INTO users (username, password_hash, is_admin, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`,
		u.Username, u.PasswordHash, u.IsAdmin, now, now,
	)
	if err != nil {
		return fmt.Errorf("inserting user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting last insert id: %w", err)
	}
	u.ID = id
	u.CreatedAt, _ = time.Parse(time.RFC3339, now)
	u.UpdatedAt = u.CreatedAt
	return nil
}

func (r *UserRepo) GetByID(id int64) (*model.User, error) {
	row := r.read.QueryRow(`
		SELECT id, username, password_hash, is_admin, created_at, updated_at
		FROM users WHERE id = ?`, id)
	return scanUser(row)
}

func (r *UserRepo) GetByUsername(username string) (*model.User, error) {
	row := r.read.QueryRow(`
		SELECT id, username, password_hash, is_admin, created_at, updated_at
		FROM users WHERE username = ?`, username)
	return scanUser(row)
}

func (r *UserRepo) List() ([]model.User, error) {
	rows, err := r.read.Query(`
		SELECT id, username, password_hash, is_admin, created_at, updated_at
		FROM users ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing users: %w", err)
	}
	defer rows.Close()

	var users []model.User
	for rows.Next() {
		u, err := scanUserRow(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *u)
	}
	return users, nil
}

func (r *UserRepo) Count() (int, error) {
	var count int
	err := r.read.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting users: %w", err)
	}
	return count, nil
}

// CreateIfAllowed atomically checks for duplicate username, determines admin
// status based on user count, and inserts the user — all in one transaction.
// This prevents a race where two concurrent registrations both see count=0.
func (r *UserRepo) CreateIfAllowed(u *model.User) error {
	tx, err := r.write.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	// Check for duplicate username inside the transaction
	var existing int
	err = tx.QueryRow(`SELECT COUNT(*) FROM users WHERE username = ?`, u.Username).Scan(&existing)
	if err != nil {
		return fmt.Errorf("checking existing user: %w", err)
	}
	if existing > 0 {
		return fmt.Errorf("username already taken")
	}

	// Count users to determine admin status
	var count int
	err = tx.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count)
	if err != nil {
		return fmt.Errorf("counting users: %w", err)
	}
	u.IsAdmin = count == 0

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tx.Exec(`
		INSERT INTO users (username, password_hash, is_admin, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`,
		u.Username, u.PasswordHash, u.IsAdmin, now, now,
	)
	if err != nil {
		return fmt.Errorf("inserting user: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting last insert id: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	u.ID = id
	u.CreatedAt, _ = time.Parse(time.RFC3339, now)
	u.UpdatedAt = u.CreatedAt
	return nil
}

func (r *UserRepo) Delete(id int64) error {
	_, err := r.write.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

func (r *UserRepo) UpdatePassword(id int64, passwordHash string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.write.Exec(`
		UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
		passwordHash, now, id,
	)
	return err
}

// Session methods

func (r *UserRepo) CreateSession(s *model.Session) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.write.Exec(`
		INSERT INTO sessions (token, user_id, expires_at, created_at)
		VALUES (?, ?, ?, ?)`,
		s.Token, s.UserID, s.ExpiresAt.UTC().Format(time.RFC3339), now,
	)
	if err != nil {
		return fmt.Errorf("inserting session: %w", err)
	}
	s.CreatedAt, _ = time.Parse(time.RFC3339, now)
	return nil
}

func (r *UserRepo) GetSession(token string) (*model.Session, error) {
	row := r.read.QueryRow(`
		SELECT token, user_id, expires_at, created_at
		FROM sessions WHERE token = ?`, token)
	s := &model.Session{}
	var expiresAt, createdAt string
	err := row.Scan(&s.Token, &s.UserID, &expiresAt, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning session: %w", err)
	}
	s.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAt)
	s.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return s, nil
}

func (r *UserRepo) DeleteSession(token string) error {
	_, err := r.write.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}

func (r *UserRepo) DeleteUserSessions(userID int64) error {
	_, err := r.write.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

func (r *UserRepo) CleanExpiredSessions() error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.write.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now)
	return err
}

// Scan helpers

func scanUser(row *sql.Row) (*model.User, error) {
	u := &model.User{}
	var createdAt, updatedAt string
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.IsAdmin, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning user: %w", err)
	}
	u.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	u.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return u, nil
}

func scanUserRow(rows *sql.Rows) (*model.User, error) {
	u := &model.User{}
	var createdAt, updatedAt string
	err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.IsAdmin, &createdAt, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("scanning user row: %w", err)
	}
	u.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	u.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return u, nil
}
