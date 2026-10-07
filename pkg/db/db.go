package db

import (
	"context"
	"fmt"
	"os"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// DriverSQLite is the SQLite database type.
	DriverSQLite = "sqlite"

	// DriverPostgres is the PostgreSQL database type.
	DriverPostgres = "postgres"
)

type DB struct {
	*gorm.DB
}

// Option configures the database connection created by New.
type Option func(*config)

type config struct {
	driver string
	path   string
	dsn    string
}

// defaultConfig returns the default connection settings. The driver is
// selected by GAMEDAY_DB_DRIVER (default "sqlite"); for SQLite the path
// comes from GAMEDAY_DB (defaulting to "gameday.db"), and for Postgres
// the DSN comes from GAMEDAY_DB_DSN.
func defaultConfig() config {
	driver := os.Getenv("GAMEDAY_DB_DRIVER")
	if driver == "" {
		driver = DriverSQLite
	}
	cfg := config{driver: driver}
	switch driver {
	case DriverPostgres:
		cfg.dsn = os.Getenv("GAMEDAY_DB_DSN")
	default:
		path := os.Getenv("GAMEDAY_DB")
		if path == "" {
			path = "gameday.db"
		}
		cfg.path = path
	}
	return cfg
}

// WithDriver selects the database type. DriverSQLite and
// DriverPostgres are the supported drivers.
func WithDriver(driver string) Option {
	return func(c *config) {
		c.driver = driver
	}
}

// WithPath overrides the database file path (SQLite only).
func WithPath(path string) Option {
	return func(c *config) {
		c.path = path
	}
}

// WithEnvPath overrides the database file path from the named
// environment variable when it is set (SQLite only).
func WithEnvPath(name string) Option {
	return func(c *config) {
		if p := os.Getenv(name); p != "" {
			c.path = p
		}
	}
}

// WithDSN overrides the PostgreSQL DSN.
func WithDSN(dsn string) Option {
	return func(c *config) {
		c.dsn = dsn
	}
}

// WithEnvDSN overrides the PostgreSQL DSN from the named environment
// variable when it is set.
func WithEnvDSN(name string) Option {
	return func(c *config) {
		if d := os.Getenv(name); d != "" {
			c.dsn = d
		}
	}
}

// New opens a database connection. Without options it connects to
// SQLite at the GAMEDAY_DB path (default "gameday.db"); setting
// GAMEDAY_DB_DRIVER to "postgres" connects to PostgreSQL using the
// DSN from GAMEDAY_DB_DSN.
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
	case DriverPostgres:
		if cfg.dsn == "" {
			return nil, fmt.Errorf("postgres driver requires a DSN; set GAMEDAY_DB_DSN")
		}
		d, err = gorm.Open(postgres.Open(cfg.dsn), &gorm.Config{
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
