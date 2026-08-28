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

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"

	_ "github.com/gizmo-platform/gameday/modules/best"
	_ "github.com/gizmo-platform/gameday/modules/game"
	_ "github.com/gizmo-platform/gameday/modules/team"
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

	w, err := web.NewServer(web.WithDB(d))
	if err != nil {
		slog.Error("Error initializing webserver", "error", err)
		os.Exit(2)
	}

	_, modDeps := modules.ResolveModules("onsite", d, w)

	for name, handle := range modDeps {
		slog.Info("Mounting module", "module", name)
		w.Mount(path.Join("/ui/mod", name), handle.Router())
		w.AddNavElement(handle.NavList(path.Join("/ui/mod", name))...)
		w.AddTemplateLoader(handle.TemplateLoader())

		if err := handle.Migrate(); err != nil {
			slog.Error("Error migrating", "module", name, "error", err)
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
