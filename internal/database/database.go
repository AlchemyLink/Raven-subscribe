// Package database provides SQLite-backed persistence for users, inbounds, and client credentials.
package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alchemylink/raven-subscribe/internal/models"
	_ "modernc.org/sqlite" // register SQLite driver
)

// DB wraps a SQLite connection and exposes typed data-access methods.
type DB struct {
	conn *sql.DB
}

// BalancerRuntimeConfig holds runtime-overridable balancer parameters stored in the DB.
type BalancerRuntimeConfig struct {
	Strategy     string `json:"strategy"`
	ProbeURL     string `json:"probe_url"`
	ProbeInterval string `json:"probe_interval"`
}

// New opens (or creates) the SQLite database at path and runs schema migrations.
func New(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	conn.SetMaxOpenConns(1) // SQLite is single-writer

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		if closeErr := conn.Close(); closeErr != nil {
			return nil, fmt.Errorf("migrate: %w; close db: %v", err, closeErr)
		}
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

// Close releases the underlying database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) migrate() error {
	schema := `
	PRAGMA journal_mode=WAL;
	PRAGMA foreign_keys=ON;

	CREATE TABLE IF NOT EXISTS users (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		username   TEXT    UNIQUE NOT NULL,
		token      TEXT    UNIQUE NOT NULL,
		enabled    INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS inbounds (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		tag         TEXT    UNIQUE NOT NULL,
		protocol    TEXT    NOT NULL,
		port        INTEGER NOT NULL,
		config_file TEXT    NOT NULL,
		raw_config  TEXT    NOT NULL,
		updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS user_clients (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		inbound_id    INTEGER NOT NULL REFERENCES inbounds(id) ON DELETE CASCADE,
		client_config TEXT    NOT NULL,
		enabled       INTEGER NOT NULL DEFAULT 1,
		UNIQUE(user_id, inbound_id)
	);

	CREATE TABLE IF NOT EXISTS app_settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_users_token ON users(token);
	CREATE INDEX IF NOT EXISTS idx_user_clients_user ON user_clients(user_id);

	CREATE TABLE IF NOT EXISTS emergency_profiles (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		name         TEXT    NOT NULL UNIQUE,
		description  TEXT    NOT NULL DEFAULT '',
		inbound_tags TEXT    NOT NULL DEFAULT '[]',
		created_at   DATETIME NOT NULL,
		updated_at   DATETIME NOT NULL
	);
	`
	_, err := db.conn.Exec(schema)
	if err != nil {
		return err
	}

	// Backward-compatible migration for older DBs.
	if _, err := db.conn.Exec(`ALTER TABLE users ADD COLUMN client_routes TEXT NOT NULL DEFAULT '[]'`); err != nil {
		// Ignore duplicate column error if already migrated.
		if !strings.Contains(err.Error(), "duplicate column name: client_routes") {
			return err
		}
	}
	if _, err := db.conn.Exec(`ALTER TABLE users ADD COLUMN email TEXT NOT NULL DEFAULT ''`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name: email") {
			return err
		}
	}
	if _, err := db.conn.Exec(`UPDATE users SET email = username WHERE trim(COALESCE(email, '')) = ''`); err != nil {
		return err
	}
	if _, err := db.conn.Exec(`ALTER TABLE users ADD COLUMN fallback_token TEXT DEFAULT NULL`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name: fallback_token") {
			return err
		}
	}
	if _, err := db.conn.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_fallback_token ON users(fallback_token)`); err != nil {
		return err
	}
	if _, err := db.conn.Exec(`ALTER TABLE users ADD COLUMN fallback_accessed_at DATETIME DEFAULT NULL`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name: fallback_accessed_at") {
			return err
		}
	}
	return nil
}

// ─── Users ───────────────────────────────────────────────────────────────────

// CreateUser inserts a new user. If email is empty, it is set to username (Xray client email / monitoring).
// fallbackToken should be a pre-generated secure token; pass empty string to leave NULL.
func (db *DB) CreateUser(username, email, token, fallbackToken string) (*models.User, error) {
	username = strings.TrimSpace(username)
	email = strings.TrimSpace(email)
	if email == "" {
		email = username
	}
	fallbackToken = strings.TrimSpace(fallbackToken)
	now := time.Now().UTC()
	var fallbackArg interface{}
	if fallbackToken != "" {
		fallbackArg = fallbackToken
	}
	res, err := db.conn.Exec(
		`INSERT INTO users (username, email, token, fallback_token, enabled, client_routes, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 1, '[]', ?, ?)`,
		username, email, token, fallbackArg, now, now,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &models.User{
		ID: id, Username: username, Email: email, Token: token, FallbackToken: fallbackToken, Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

// GetUserByToken returns the user matching the given subscription token, or nil if not found.
func (db *DB) GetUserByToken(token string) (*models.User, error) {
	return db.scanUser(db.conn.QueryRow(
		`SELECT id, username, email, token, fallback_token, fallback_accessed_at, enabled, client_routes, created_at, updated_at FROM users WHERE token = ?`, token,
	))
}

// GetUserByFallbackToken returns the user matching the given fallback token, or nil if not found.
func (db *DB) GetUserByFallbackToken(token string) (*models.User, error) {
	if token == "" {
		return nil, nil
	}
	return db.scanUser(db.conn.QueryRow(
		`SELECT id, username, email, token, fallback_token, fallback_accessed_at, enabled, client_routes, created_at, updated_at FROM users WHERE fallback_token = ?`, token,
	))
}

// GetUserByUsername returns the user with the given username, or nil if not found.
func (db *DB) GetUserByUsername(username string) (*models.User, error) {
	return db.scanUser(db.conn.QueryRow(
		`SELECT id, username, email, token, fallback_token, fallback_accessed_at, enabled, client_routes, created_at, updated_at FROM users WHERE username = ?`, username,
	))
}

// GetUserByID returns the user with the given ID, or nil if not found.
func (db *DB) GetUserByID(id int64) (*models.User, error) {
	return db.scanUser(db.conn.QueryRow(
		`SELECT id, username, email, token, fallback_token, fallback_accessed_at, enabled, client_routes, created_at, updated_at FROM users WHERE id = ?`, id,
	))
}

// ListUsers returns all users ordered by creation time.
func (db *DB) ListUsers() ([]models.User, error) {
	return db.ListUsersPaginated(0, 0)
}

// ListUsersPaginated returns users with pagination. limit/offset 0 = no limit.
func (db *DB) ListUsersPaginated(limit, offset int) ([]models.User, error) {
	query := `SELECT id, username, email, token, fallback_token, fallback_accessed_at, enabled, client_routes, created_at, updated_at FROM users ORDER BY created_at`
	var args []interface{}
	if limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		if offset < 0 {
			offset = 0
		}
		args = append(args, limit, offset)
	} else if offset > 0 {
		query += ` LIMIT -1 OFFSET ?`
		args = append(args, offset)
	}
	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var users []models.User
	for rows.Next() {
		u, err := db.scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *u)
	}
	return users, rows.Err()
}

// CountUsers returns the total number of users.
func (db *DB) CountUsers() (int, error) {
	var n int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// UpdateUserToken replaces the subscription token for the specified user.
func (db *DB) UpdateUserToken(userID int64, token string) error {
	_, err := db.conn.Exec(
		`UPDATE users SET token = ?, updated_at = ? WHERE id = ?`,
		token, time.Now().UTC(), userID,
	)
	return err
}

// UpdateFallbackToken replaces the fallback token for the specified user.
func (db *DB) UpdateFallbackToken(userID int64, fallbackToken string) error {
	_, err := db.conn.Exec(
		`UPDATE users SET fallback_token = ?, updated_at = ? WHERE id = ?`,
		fallbackToken, time.Now().UTC(), userID,
	)
	return err
}

// SetFallbackAccessedAt records the time the fallback subscription was accessed.
func (db *DB) SetFallbackAccessedAt(userID int64, t time.Time) error {
	_, err := db.conn.Exec(
		`UPDATE users SET fallback_accessed_at = ? WHERE id = ?`,
		t.UTC(), userID,
	)
	return err
}

// GetFallbackEnabled returns true if the fallback subscription endpoint is globally enabled.
// Defaults to true when no setting is stored.
func (db *DB) GetFallbackEnabled() (bool, error) {
	var val string
	err := db.conn.QueryRow(`SELECT value FROM app_settings WHERE key = 'fallback_enabled'`).Scan(&val)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return val != "false", nil
}

// SetFallbackEnabled sets the global fallback subscription enabled/disabled flag.
func (db *DB) SetFallbackEnabled(enabled bool) error {
	val := "true"
	if !enabled {
		val = "false"
	}
	_, err := db.conn.Exec(
		`INSERT INTO app_settings (key, value) VALUES ('fallback_enabled', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		val,
	)
	return err
}

// SetUserEnabled enables or disables the user account.
func (db *DB) SetUserEnabled(userID int64, enabled bool) error {
	_, err := db.conn.Exec(
		`UPDATE users SET enabled = ?, updated_at = ? WHERE id = ?`,
		boolInt(enabled), time.Now().UTC(), userID,
	)
	return err
}

// DeleteUser removes the user with the given ID from the database.
func (db *DB) DeleteUser(id int64) error {
	_, err := db.conn.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

func (db *DB) scanUser(row interface {
	Scan(...any) error
}) (*models.User, error) {
	var u models.User
	var enabled int
	var fallbackToken sql.NullString
	var fallbackAccessedAt sql.NullTime
	err := row.Scan(&u.ID, &u.Username, &u.Email, &u.Token, &fallbackToken, &fallbackAccessedAt, &enabled, &u.ClientRoutes, &u.CreatedAt, &u.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.Enabled = enabled == 1
	if fallbackToken.Valid {
		u.FallbackToken = fallbackToken.String
	}
	if fallbackAccessedAt.Valid {
		t := fallbackAccessedAt.Time
		u.FallbackAccessedAt = &t
	}
	if strings.TrimSpace(u.Email) == "" {
		u.Email = u.Username
	}
	return &u, nil
}

// UpdateUserClientRoutes persists the JSON-encoded routing rules for a user.
func (db *DB) UpdateUserClientRoutes(userID int64, routesJSON string) error {
	_, err := db.conn.Exec(
		`UPDATE users SET client_routes = ?, updated_at = ? WHERE id = ?`,
		routesJSON, time.Now().UTC(), userID,
	)
	return err
}

// GetGlobalClientRoutes returns the global routing rules JSON, or "[]" if unset.
func (db *DB) GetGlobalClientRoutes() (string, error) {
	var routes string
	err := db.conn.QueryRow(`SELECT value FROM app_settings WHERE key = 'global_client_routes'`).Scan(&routes)
	if err == sql.ErrNoRows {
		return "[]", nil
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(routes) == "" {
		return "[]", nil
	}
	return routes, nil
}

// UpdateGlobalClientRoutes upserts the global routing rules JSON.
func (db *DB) UpdateGlobalClientRoutes(routesJSON string) error {
	_, err := db.conn.Exec(
		`INSERT INTO app_settings (key, value)
		 VALUES ('global_client_routes', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		routesJSON,
	)
	return err
}

// GetBalancerRuntimeConfig returns the persisted balancer config, or nil if not set.
func (db *DB) GetBalancerRuntimeConfig() (*BalancerRuntimeConfig, error) {
	var raw string
	err := db.conn.QueryRow(`SELECT value FROM app_settings WHERE key = 'balancer_runtime_config'`).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg BalancerRuntimeConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// SetBalancerRuntimeConfig upserts the balancer runtime configuration.
func (db *DB) SetBalancerRuntimeConfig(cfg BalancerRuntimeConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = db.conn.Exec(
		`INSERT INTO app_settings (key, value)
		 VALUES ('balancer_runtime_config', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		string(raw),
	)
	return err
}

// DeleteBalancerRuntimeConfig removes the persisted balancer config, reverting to defaults.
func (db *DB) DeleteBalancerRuntimeConfig() error {
	_, err := db.conn.Exec(`DELETE FROM app_settings WHERE key = 'balancer_runtime_config'`)
	return err
}

// ─── Inbounds ────────────────────────────────────────────────────────────────

// UpsertInbound inserts or updates an inbound record identified by tag, returning its DB ID.
func (db *DB) UpsertInbound(tag, protocol string, port int, configFile, rawConfig string) (int64, error) {
	now := time.Now().UTC()
	_, err := db.conn.Exec(
		`INSERT INTO inbounds (tag, protocol, port, config_file, raw_config, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(tag) DO UPDATE SET
		   protocol=excluded.protocol, port=excluded.port,
		   config_file=excluded.config_file, raw_config=excluded.raw_config,
		   updated_at=excluded.updated_at`,
		tag, protocol, port, configFile, rawConfig, now,
	)
	if err != nil {
		return 0, err
	}

	// Get the ID (works for both insert and update)
	var id int64
	err = db.conn.QueryRow(`SELECT id FROM inbounds WHERE tag = ?`, tag).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// ListInbounds returns all inbounds ordered by tag.
func (db *DB) ListInbounds() ([]models.Inbound, error) {
	return db.ListInboundsPaginated(0, 0)
}

// ListInboundsPaginated returns inbounds with pagination. limit/offset 0 = no limit.
func (db *DB) ListInboundsPaginated(limit, offset int) ([]models.Inbound, error) {
	query := `SELECT id, tag, protocol, port, config_file, raw_config, updated_at FROM inbounds ORDER BY tag`
	var args []interface{}
	if limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		if offset < 0 {
			offset = 0
		}
		args = append(args, limit, offset)
	} else if offset > 0 {
		query += ` LIMIT -1 OFFSET ?`
		args = append(args, offset)
	}
	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var inbounds []models.Inbound
	for rows.Next() {
		var ib models.Inbound
		if err := rows.Scan(&ib.ID, &ib.Tag, &ib.Protocol, &ib.Port, &ib.ConfigFile, &ib.RawConfig, &ib.UpdatedAt); err != nil {
			return nil, err
		}
		inbounds = append(inbounds, ib)
	}
	return inbounds, rows.Err()
}

// CountInbounds returns the total number of inbounds.
func (db *DB) CountInbounds() (int, error) {
	var n int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM inbounds`).Scan(&n)
	return n, err
}

// DeleteInboundsByFile removes all inbound records originating from the given config file.
func (db *DB) DeleteInboundsByFile(configFile string) error {
	_, err := db.conn.Exec(`DELETE FROM inbounds WHERE config_file = ?`, configFile)
	return err
}

// GetInboundTagsNotInFile returns inbound tags from configFile that are absent from presentTags.
func (db *DB) GetInboundTagsNotInFile(configFile string, presentTags []string) ([]string, error) {
	if len(presentTags) == 0 {
		rows, err := db.conn.Query(`SELECT tag FROM inbounds WHERE config_file = ?`, configFile)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		var tags []string
		for rows.Next() {
			var t string
			if err := rows.Scan(&t); err != nil {
				return nil, err
			}
			tags = append(tags, t)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return tags, nil
	}
	// Build NOT IN clause
	query := `SELECT tag FROM inbounds WHERE config_file = ? AND tag NOT IN (`
	args := []interface{}{configFile}
	for i, tag := range presentTags {
		if i > 0 {
			query += ","
		}
		query += "?"
		args = append(args, tag)
	}
	query += ")"
	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var tags []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tags, nil
}

// DeleteInboundByTag removes the inbound record with the given tag.
func (db *DB) DeleteInboundByTag(tag string) error {
	_, err := db.conn.Exec(`DELETE FROM inbounds WHERE tag = ?`, tag)
	return err
}

// ─── UserClients ──────────────────────────────────────────────────────────────

// UpsertUserClient inserts or updates the client credential for a user/inbound pair.
func (db *DB) UpsertUserClient(userID, inboundID int64, clientConfig string) error {
	_, err := db.conn.Exec(
		`INSERT INTO user_clients (user_id, inbound_id, client_config, enabled)
		 VALUES (?, ?, ?, 1)
		 ON CONFLICT(user_id, inbound_id) DO UPDATE SET client_config=excluded.client_config`,
		userID, inboundID, clientConfig,
	)
	return err
}

// GetUserClients returns all enabled client records for the given user, joined with inbound data.
func (db *DB) GetUserClients(userID int64) ([]models.UserClientFull, error) {
	rows, err := db.conn.Query(`
		SELECT
			uc.id, uc.user_id, uc.inbound_id, uc.client_config, uc.enabled,
			ib.tag, ib.protocol, ib.port, ib.raw_config
		FROM user_clients uc
		JOIN inbounds ib ON ib.id = uc.inbound_id
		WHERE uc.user_id = ? AND uc.enabled = 1
		ORDER BY ib.tag
	`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []models.UserClientFull
	for rows.Next() {
		var f models.UserClientFull
		var enabled int
		err := rows.Scan(
			&f.ID, &f.UserID, &f.InboundID, &f.ClientConfig, &enabled,
			&f.InboundTag, &f.InboundProtocol, &f.InboundPort, &f.InboundRaw,
		)
		if err != nil {
			return nil, err
		}
		f.Enabled = enabled == 1
		result = append(result, f)
	}
	return result, rows.Err()
}

// GetUserClientByUserAndInbound returns the inbound tag and client config for a user/inbound pair.
// Returns ("", "", nil) if not found. Does not filter by enabled.
func (db *DB) GetUserClientByUserAndInbound(userID, inboundID int64) (tag, clientConfig string, err error) {
	err = db.conn.QueryRow(
		`SELECT ib.tag, uc.client_config FROM user_clients uc
		 JOIN inbounds ib ON ib.id = uc.inbound_id
		 WHERE uc.user_id = ? AND uc.inbound_id = ?`,
		userID, inboundID,
	).Scan(&tag, &clientConfig)
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	return tag, clientConfig, err
}

// SetUserClientEnabled enables or disables a specific user/inbound client entry.
func (db *DB) SetUserClientEnabled(userID, inboundID int64, enabled bool) error {
	_, err := db.conn.Exec(
		`UPDATE user_clients SET enabled = ? WHERE user_id = ? AND inbound_id = ?`,
		boolInt(enabled), userID, inboundID,
	)
	return err
}

// ListUserClients returns all user client records joined with inbound data, ordered by user and tag.
func (db *DB) ListUserClients() ([]models.UserClientFull, error) {
	rows, err := db.conn.Query(`
		SELECT
			uc.id, uc.user_id, uc.inbound_id, uc.client_config, uc.enabled,
			ib.tag, ib.protocol, ib.port, ib.raw_config
		FROM user_clients uc
		JOIN inbounds ib ON ib.id = uc.inbound_id
		ORDER BY uc.user_id, ib.tag
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []models.UserClientFull
	for rows.Next() {
		var f models.UserClientFull
		var enabled int
		err := rows.Scan(
			&f.ID, &f.UserID, &f.InboundID, &f.ClientConfig, &enabled,
			&f.InboundTag, &f.InboundProtocol, &f.InboundPort, &f.InboundRaw,
		)
		if err != nil {
			return nil, err
		}
		f.Enabled = enabled == 1
		result = append(result, f)
	}
	return result, rows.Err()
}

// DeleteUserClientsByInbound removes all user client entries for the given inbound ID.
func (db *DB) DeleteUserClientsByInbound(inboundID int64) error {
	_, err := db.conn.Exec(`DELETE FROM user_clients WHERE inbound_id = ?`, inboundID)
	return err
}

// UserClientForInbound holds user identity and client config for a user enrolled in an inbound.
type UserClientForInbound struct {
	Username     string
	Email        string
	ClientConfig string
	Protocol     string
}

// XrayClientEmail is the identity passed to Xray (client email field).
func (r UserClientForInbound) XrayClientEmail() string {
	if strings.TrimSpace(r.Email) != "" {
		return strings.TrimSpace(r.Email)
	}
	return strings.TrimSpace(r.Username)
}

// ListUserClientsByInboundTag returns all users with their stored config for the given inbound tag.
// Used for restoring users to Xray API and syncing DB to config files.
// Only returns entries where both the user account and the user_client are enabled.
func (db *DB) ListUserClientsByInboundTag(tag string) ([]UserClientForInbound, error) {
	rows, err := db.conn.Query(`
		SELECT u.username, u.email, uc.client_config, ib.protocol
		FROM user_clients uc
		JOIN users u ON u.id = uc.user_id
		JOIN inbounds ib ON ib.id = uc.inbound_id
		WHERE ib.tag = ? AND uc.enabled = 1 AND u.enabled = 1
		ORDER BY u.username
	`, tag)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []UserClientForInbound
	for rows.Next() {
		var r UserClientForInbound
		if err := rows.Scan(&r.Username, &r.Email, &r.ClientConfig, &r.Protocol); err != nil {
			return nil, err
		}
		if strings.TrimSpace(r.Email) == "" {
			r.Email = r.Username
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// ─── Emergency profiles ───────────────────────────────────────────────────────

// CreateEmergencyProfile inserts a new emergency profile.
func (db *DB) CreateEmergencyProfile(name, description string, inboundTags []string) (*models.EmergencyProfile, error) {
	tagsJSON, err := json.Marshal(inboundTags)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	res, err := db.conn.Exec(
		`INSERT INTO emergency_profiles (name, description, inbound_tags, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		name, description, string(tagsJSON), now, now,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &models.EmergencyProfile{
		ID: id, Name: name, Description: description, InboundTags: inboundTags,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

// GetEmergencyProfile returns the profile with the given ID, or nil if not found.
func (db *DB) GetEmergencyProfile(id int64) (*models.EmergencyProfile, error) {
	var p models.EmergencyProfile
	var tagsJSON string
	err := db.conn.QueryRow(
		`SELECT id, name, description, inbound_tags, created_at, updated_at FROM emergency_profiles WHERE id = ?`, id,
	).Scan(&p.ID, &p.Name, &p.Description, &tagsJSON, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(tagsJSON), &p.InboundTags); err != nil {
		return nil, fmt.Errorf("parse inbound_tags for profile %d: %w", id, err)
	}
	return &p, nil
}

// ListEmergencyProfiles returns all emergency profiles ordered by name.
func (db *DB) ListEmergencyProfiles() ([]models.EmergencyProfile, error) {
	rows, err := db.conn.Query(
		`SELECT id, name, description, inbound_tags, created_at, updated_at FROM emergency_profiles ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var profiles []models.EmergencyProfile
	for rows.Next() {
		var p models.EmergencyProfile
		var tagsJSON string
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &tagsJSON, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(tagsJSON), &p.InboundTags); err != nil {
			return nil, fmt.Errorf("parse inbound_tags for profile %d: %w", p.ID, err)
		}
		profiles = append(profiles, p)
	}
	return profiles, rows.Err()
}

// UpdateEmergencyProfile updates the profile fields. Returns nil if not found.
func (db *DB) UpdateEmergencyProfile(id int64, name, description string, inboundTags []string) (*models.EmergencyProfile, error) {
	tagsJSON, err := json.Marshal(inboundTags)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	res, err := db.conn.Exec(
		`UPDATE emergency_profiles SET name = ?, description = ?, inbound_tags = ?, updated_at = ? WHERE id = ?`,
		name, description, string(tagsJSON), now, id,
	)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}
	return &models.EmergencyProfile{
		ID: id, Name: name, Description: description, InboundTags: inboundTags, UpdatedAt: now,
	}, nil
}

// DeleteEmergencyProfile removes the profile with the given ID.
func (db *DB) DeleteEmergencyProfile(id int64) error {
	_, err := db.conn.Exec(`DELETE FROM emergency_profiles WHERE id = ?`, id)
	return err
}

// ─── Emergency status ──────────────────────────────────────────────────────��──

// GetEmergencyStatus returns the current emergency mode state including the active profile.
func (db *DB) GetEmergencyStatus() (*models.EmergencyStatus, error) {
	status := &models.EmergencyStatus{Active: false}

	var activeStr string
	err := db.conn.QueryRow(`SELECT value FROM app_settings WHERE key = 'emergency_active'`).Scan(&activeStr)
	if err == sql.ErrNoRows {
		return status, nil
	}
	if err != nil {
		return nil, err
	}
	if activeStr != "true" {
		return status, nil
	}
	status.Active = true

	var profileIDStr string
	if err := db.conn.QueryRow(`SELECT value FROM app_settings WHERE key = 'emergency_profile_id'`).Scan(&profileIDStr); err == nil && profileIDStr != "" {
		var profileID int64
		if _, err := fmt.Sscanf(profileIDStr, "%d", &profileID); err == nil {
			status.ProfileID = &profileID
			if p, err := db.GetEmergencyProfile(profileID); err == nil && p != nil {
				status.Profile = p
			}
		}
	}

	var activatedAtStr string
	if err := db.conn.QueryRow(`SELECT value FROM app_settings WHERE key = 'emergency_activated_at'`).Scan(&activatedAtStr); err == nil {
		if t, err := time.Parse(time.RFC3339, activatedAtStr); err == nil {
			status.ActivatedAt = &t
		}
	}

	return status, nil
}

// ActivateEmergency sets the emergency mode to active with the given profile.
func (db *DB) ActivateEmergency(profileID int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	upsert := func(key, value string) error {
		_, err := db.conn.Exec(
			`INSERT INTO app_settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			key, value,
		)
		return err
	}
	if err := upsert("emergency_active", "true"); err != nil {
		return err
	}
	if err := upsert("emergency_profile_id", fmt.Sprintf("%d", profileID)); err != nil {
		return err
	}
	return upsert("emergency_activated_at", now)
}

// DeactivateEmergency clears the emergency mode.
func (db *DB) DeactivateEmergency() error {
	_, err := db.conn.Exec(`DELETE FROM app_settings WHERE key IN ('emergency_active', 'emergency_profile_id', 'emergency_activated_at')`)
	return err
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
