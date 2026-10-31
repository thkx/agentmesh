package app

import (
	"database/sql"
	"log/slog"

	"github.com/thkx/agentmesh/internal/config"
)

type Container struct {
	Config config.Config
	Logger *slog.Logger
	DB     *sql.DB
}

func (c *Container) Close() error {
	if c == nil || c.DB == nil {
		return nil
	}
	return c.DB.Close()
}
