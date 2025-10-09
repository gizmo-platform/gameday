package cmd

import (
	"context"
	"log/slog"
	nhttp "net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	_ "github.com/the-maldridge/authware/backend/htpasswd"

	"github.com/gizmo-platform/gameday/pkg/http"
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

	wg := new(sync.WaitGroup)

	w, err := http.NewServer(http.WithStartupWG(wg))
	if err != nil {
		slog.Error("Error initializing webserver", "error", err)
		os.Exit(2)
	}

	go func() {
		if err := w.Serve(":8080"); err != nil && err != nhttp.ErrServerClosed {
			slog.Error("Error binding webserver", "error", err)
			quit <- syscall.SIGINT
		}
	}()

	wg.Wait()
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
