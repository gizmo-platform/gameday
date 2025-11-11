package db

import (
	"os"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type DB struct {
	*gorm.DB
}

func New() (*DB, error) {
	dbPath := os.Getenv("GAMEDAY_DB")
	if dbPath == "" {
		dbPath = "gameday.db"
	}
	d, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		PrepareStmt:            true,
		SkipDefaultTransaction: true,
	})
	if err != nil {
		return nil, err
	}
	return &DB{d}, nil
}

func (d *DB) Migrate(tables ...interface{}) error {
	return d.AutoMigrate(tables)
}
