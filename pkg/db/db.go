package db

import (
	"context"
	"fmt"
	"os"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// DriverSQLite is the SQLite database type.
	DriverSQLite = "sqlite"
)

type DB struct {
	*gorm.DB
}

// Option configures the database connection created by New.
type Option func(*config)

type config struct {
	driver string
	path   string
}

// defaultConfig returns the default connection settings: the SQLite
// driver with the path from GAMEDAY_DB (defaulting to "gameday.db").
func defaultConfig() config {
	path := os.Getenv("GAMEDAY_DB")
	if path == "" {
		path = "gameday.db"
	}
	return config{driver: DriverSQLite, path: path}
}

// WithDriver selects the database type. DriverSQLite is the only
// supported driver.
func WithDriver(driver string) Option {
	return func(c *config) {
		c.driver = driver
	}
}

// WithPath overrides the database file path.
func WithPath(path string) Option {
	return func(c *config) {
		c.path = path
	}
}

// WithEnvPath overrides the database file path from the named
// environment variable when it is set.
func WithEnvPath(name string) Option {
	return func(c *config) {
		if p := os.Getenv(name); p != "" {
			c.path = p
		}
	}
}

// New opens a database connection. Without options it connects to
// SQLite at the GAMEDAY_DB path (default "gameday.db").
func New(opts ...Option) (*DB, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	var d *gorm.DB
	var err error
	switch cfg.driver {
	case DriverSQLite:
		d, err = gorm.Open(sqlite.Open(cfg.path), &gorm.Config{
			PrepareStmt:            true,
			SkipDefaultTransaction: true,
		})
	default:
		return nil, fmt.Errorf("unknown database driver %q", cfg.driver)
	}
	if err != nil {
		return nil, err
	}
	return &DB{d}, nil
}

func (d *DB) Raw() *gorm.DB {
	return d.DB
}

func (d *DB) Migrate(tables ...interface{}) error {
	return d.AutoMigrate(tables)
}

// InsertOrUpdate creates or updates a record. Without an explicit
// clause it upserts on the primary key; callers with a business key
// may pass clause.OnConflict{Columns: ...} to upsert on a unique
// index instead.
func InsertOrUpdate[T any](ctx context.Context, db *gorm.DB, data *T, opts ...clause.Expression) error {
	if len(opts) > 0 {
		err := gorm.G[T](db, opts...).Create(ctx, data)
		return err
	}
	err := gorm.G[T](db, clause.OnConflict{
		UpdateAll: true,
	}).Create(ctx, data)
	return err
}
