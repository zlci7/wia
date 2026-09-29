package storage

import ()

import "database/sql"

type WorldStore struct {
	path string
	db   *sql.DB
}
