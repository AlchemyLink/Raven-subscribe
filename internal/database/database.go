package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"xray-subscription/internal/models"
	_ "modernc.org/sqlite" // register SQLite driver
)

type DB struct {
	conn *sql.DB
}

type BalancerRuntimeConfig struct {
	Strategy     string `json:"strategy"`
	ProbeURL     string `json:"probe_url"`
	ProbeInterval string `json:"probe_interval"`
}

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
	return nil
}

// ─── Users ───────────────────────────────────────────────────────────────────

func (db *DB) CreateUser(username, token string) (*models.User, error) {
	now := time.Now().UTC()
	res, err := db.conn.Exec(
		`INSERT INTO users (username, token, enabled, client_routes, created_at, updated_at)
		 VALUES (?, ?, 1, '[]', ?, ?)`,
		username, token, now, now,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &models.User{
		ID: id, Username: username, Token: token, Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (db *DB) GetUserByToken(token string) (*models.User, error) {
	return db.scanUser(db.conn.QueryRow(
		`SELECT id, username, token, enabled, client_routes, created_at, updated_at FROM users WHERE token = ?`, token,
	))
}

func (db *DB) GetUserByUsername(username string) (*models.User, error) {
	return db.scanUser(db.conn.QueryRow(
		`SELECT id, username, token, enabled, client_routes, created_at, updated_at FROM users WHERE username = ?`, username,
	))
}

func (db *DB) GetUserByID(id int64) (*models.User, error) {
	return db.scanUser(db.conn.QueryRow(
		`SELECT id, username, token, enabled, client_routes, created_at, updated_at FROM users WHERE id = ?`, id,
	))
}

func (db *DB) ListUsers() ([]models.User, error) {
	rows, err := db.conn.Query(
		`SELECT id, username, token, enabled, client_routes, created_at, updated_at FROM users ORDER BY created_at`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (db *DB) UpdateUserToken(userID int64, token string) error {
	_, err := db.conn.Exec(
		`UPDATE users SET token = ?, updated_at = ? WHERE id = ?`,
		token, time.Now().UTC(), userID,
	)
	return err
}

func (db *DB) SetUserEnabled(userID int64, enabled bool) error {
	_, err := db.conn.Exec(
		`UPDATE users SET enabled = ?, updated_at = ? WHERE id = ?`,
		boolInt(enabled), time.Now().UTC(), userID,
	)
	return err
}

func (db *DB) DeleteUser(id int64) error {
	_, err := db.conn.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

func (db *DB) scanUser(row interface {
	Scan(...any) error
}) (*models.User, error) {
	var u models.User
	var enabled int
	err := row.Scan(&u.ID, &u.Username, &u.Token, &enabled, &u.ClientRoutes, &u.CreatedAt, &u.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.Enabled = enabled == 1
	return &u, nil
}

func (db *DB) UpdateUserClientRoutes(userID int64, routesJSON string) error {
	_, err := db.conn.Exec(
		`UPDATE users SET client_routes = ?, updated_at = ? WHERE id = ?`,
		routesJSON, time.Now().UTC(), userID,
	)
	return err
}

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

func (db *DB) UpdateGlobalClientRoutes(routesJSON string) error {
	_, err := db.conn.Exec(
		`INSERT INTO app_settings (key, value)
		 VALUES ('global_client_routes', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		routesJSON,
	)
	return err
}

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

func (db *DB) DeleteBalancerRuntimeConfig() error {
	_, err := db.conn.Exec(`DELETE FROM app_settings WHERE key = 'balancer_runtime_config'`)
	return err
}

// ─── Inbounds ────────────────────────────────────────────────────────────────

func (db *DB) UpsertInbound(tag, protocol string, port int, configFile, rawConfig string) (int64, error) {
	now := time.Now().UTC()
	res, err := db.conn.Exec(
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
	_ = res
	return id, nil
}

func (db *DB) ListInbounds() ([]models.Inbound, error) {
	rows, err := db.conn.Query(
		`SELECT id, tag, protocol, port, config_file, raw_config, updated_at FROM inbounds ORDER BY tag`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (db *DB) DeleteInboundsByFile(configFile string) error {
	_, err := db.conn.Exec(`DELETE FROM inbounds WHERE config_file = ?`, configFile)
	return err
}

func (db *DB) GetInboundTagsNotInFile(configFile string, presentTags []string) ([]string, error) {
	if len(presentTags) == 0 {
		rows, err := db.conn.Query(`SELECT tag FROM inbounds WHERE config_file = ?`, configFile)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
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
	defer rows.Close()
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

func (db *DB) DeleteInboundByTag(tag string) error {
	_, err := db.conn.Exec(`DELETE FROM inbounds WHERE tag = ?`, tag)
	return err
}

// ─── UserClients ──────────────────────────────────────────────────────────────

func (db *DB) UpsertUserClient(userID, inboundID int64, clientConfig string) error {
	_, err := db.conn.Exec(
		`INSERT INTO user_clients (user_id, inbound_id, client_config, enabled)
		 VALUES (?, ?, ?, 1)
		 ON CONFLICT(user_id, inbound_id) DO UPDATE SET client_config=excluded.client_config`,
		userID, inboundID, clientConfig,
	)
	return err
}

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
	defer rows.Close()

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

func (db *DB) SetUserClientEnabled(userID, inboundID int64, enabled bool) error {
	_, err := db.conn.Exec(
		`UPDATE user_clients SET enabled = ? WHERE user_id = ? AND inbound_id = ?`,
		boolInt(enabled), userID, inboundID,
	)
	return err
}

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
	defer rows.Close()

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

func (db *DB) DeleteUserClientsByInbound(inboundID int64) error {
	_, err := db.conn.Exec(`DELETE FROM user_clients WHERE inbound_id = ?`, inboundID)
	return err
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
