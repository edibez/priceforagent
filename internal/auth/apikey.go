package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Store handles API key storage
type Store struct {
	db *sql.DB
}

// APIKey represents an API key record
type APIKey struct {
	ID        int64     `json:"id"`
	Key       string    `json:"key"`
	AgentID   string    `json:"agent_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used,omitempty"`
	HitCount  int64     `json:"hit_count"`
}

// NewStore creates a new API key store
func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	// Create tables
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key TEXT UNIQUE NOT NULL,
			agent_id TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_used DATETIME,
			hit_count INTEGER DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_key ON api_keys(key);
	`)
	if err != nil {
		return nil, err
	}

	return &Store{db: db}, nil
}

// GenerateKey creates a new API key
func (s *Store) GenerateKey(agentID string) (*APIKey, error) {
	key := generateRandomKey()

	result, err := s.db.Exec(
		"INSERT INTO api_keys (key, agent_id) VALUES (?, ?)",
		key, agentID,
	)
	if err != nil {
		return nil, err
	}

	id, _ := result.LastInsertId()
	return &APIKey{
		ID:        id,
		Key:       key,
		AgentID:   agentID,
		CreatedAt: time.Now(),
	}, nil
}

// ValidateKey checks if an API key exists
func (s *Store) ValidateKey(key string) (*APIKey, error) {
	var apiKey APIKey
	err := s.db.QueryRow(
		"SELECT id, key, agent_id, created_at, hit_count FROM api_keys WHERE key = ?",
		key,
	).Scan(&apiKey.ID, &apiKey.Key, &apiKey.AgentID, &apiKey.CreatedAt, &apiKey.HitCount)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("invalid API key")
	}
	if err != nil {
		return nil, err
	}

	return &apiKey, nil
}

// IncrementUsage updates the hit count and last used time
func (s *Store) IncrementUsage(key string) error {
	_, err := s.db.Exec(
		"UPDATE api_keys SET hit_count = hit_count + 1, last_used = CURRENT_TIMESTAMP WHERE key = ?",
		key,
	)
	return err
}

// GetUsageStats returns usage statistics for a key
func (s *Store) GetUsageStats(key string) (*APIKey, error) {
	return s.ValidateKey(key)
}

// ListKeys returns all API keys (admin)
func (s *Store) ListKeys() ([]APIKey, error) {
	rows, err := s.db.Query(
		"SELECT id, key, agent_id, created_at, last_used, hit_count FROM api_keys ORDER BY created_at DESC",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []APIKey
	for rows.Next() {
		var k APIKey
		var lastUsed sql.NullTime
		err := rows.Scan(&k.ID, &k.Key, &k.AgentID, &k.CreatedAt, &lastUsed, &k.HitCount)
		if err != nil {
			continue
		}
		if lastUsed.Valid {
			k.LastUsed = lastUsed.Time
		}
		keys = append(keys, k)
	}

	return keys, nil
}

func generateRandomKey() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return "pfa_" + hex.EncodeToString(bytes)
}

// Close closes the database connection
func (s *Store) Close() error {
	return s.db.Close()
}
