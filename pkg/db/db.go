package db

import (
	"context"
	"os"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

func InsertOrUpdate[T any](ctx context.Context, db *gorm.DB, data *T) error {
	err := gorm.G[T](db, clause.OnConflict{
		UpdateAll: true,
	}).Create(ctx, data)
	return err
}
