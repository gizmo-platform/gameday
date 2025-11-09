package cmd

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	_ "github.com/the-maldridge/authware/backend/htpasswd"

	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/modules/game"
	"github.com/gizmo-platform/gameday/modules/team"
)

var (
	onsiteServeCmd = &cobra.Command{
		Use:   "serve",
		Short: "serve - Start an on-site webserver",
		Long:  onsiteServeCmdLongDocs,
		Run:   onsiteServeCmdRun,
	}

	onsiteServeCmdLongDocs = `serve

Serve starts a webserver that will provide all of the on-site services that are handled by gameday.`
)

func init() {
	onsiteCmd.AddCommand(onsiteServeCmd)
}

func onsiteServeCmdRun(c *cobra.Command, args []string) {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	d, err := db.New()
	if err != nil {
		slog.Error("Error initializing database", "error", err)
		os.Exit(2)
	}

	w, err := web.NewServer()
	if err != nil {
		slog.Error("Error initializing webserver", "error", err)
		os.Exit(2)
	}

	modMap := make(map[string]modules.Web)
	modMap["team"] = team.New(d, w)
	modMap["game"] = game.New(d, w)

	for mod, handle := range modMap {
		slog.Info("Mounting module", "module", mod)
		w.Mount(path.Join("/ui/mod", mod), handle.Router())
		w.AddNavElement(handle.NavList(path.Join("/ui/mod", mod))...)
		w.AddTemplateLoader(handle.TemplateLoader())

		if err := handle.Migrate(); err != nil {
			slog.Error("Error migrating", "module", mod, "error", err)
			quit <- syscall.SIGINT
		}
	}

	go func() {
		if err := w.Serve(":8080"); err != nil && err != http.ErrServerClosed {
			slog.Error("Error binding webserver", "error", err)
			quit <- syscall.SIGINT
		}
	}()

	slog.Info("Startup Complete!")

	<-quit
	slog.Info("Shutting Down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := w.Shutdown(ctx); err != nil {
		slog.Error("Error during shutdown", "error", err)
		os.Exit(2)
	}

	slog.Info("Goodbye!")
}
