package db

import (
	"database/sql"
	"fmt"
	"log"
	"time"
	_ "github.com/lib/pq" 
)

func Connect(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure the connection pool settings for optimal concurrent access
	db.SetMaxOpenConns(25)                 // Max open connections to the database
	db.SetMaxIdleConns(25)                 // Max idle connections in the pool
	db.SetConnMaxLifetime(5 * time.Minute) // Maximum amount of time a connection may be reused

	// test ping
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	log.Println("[DB] Successfully connected to PostgreSQL")
	return db, nil
}