package cmd

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"syscall"

	"github.com/gogpu/systray"
	"github.com/spf13/cobra"

	"github.com/gizmo-platform/gameday/pkg/buildinfo"
)

var (
	onsiteTrayCmd = &cobra.Command{
		Use:   "tray",
		Short: "tray - Start a system tray applet",
		Long:  onsiteTrayCmdLongDocs,
		Run:   onsiteTrayCmdRun,
	}

	onsiteTrayCmdLongDocs = `tray

Tray starts a system tray applet for gameday.  The applet is purely an
indicator and does not start the webserver or any other gameday services.
It shows the build information of the running binary and provides an option
to exit.`
)

func init() {
	onsiteCmd.AddCommand(onsiteTrayCmd)
}

func onsiteTrayCmdRun(c *cobra.Command, args []string) {
	buildInfo := fmt.Sprintf("Gameday %s", buildinfo.Version)

	tray := systray.New()
	tray.SetAppName("Gameday")

	menu := systray.NewMenu()
	menu.Add(buildInfo, nil).SetDisabled(true)
	menu.AddSeparator()
	menu.Add("Exit", func() {
		// Remove releases the tray and unblocks Run().  It runs on its
		// own goroutine because menu callbacks are dispatched from the
		// platform event loop, which Remove may need to join.
		go func() {
			tray.Remove()
		}()
	})

	icon := trayIcon()
	tray.SetIcon(icon).
		SetTemplateIcon(icon).
		SetTooltip("Gameday").
		SetMenu(menu)

	// Ctrl-C and friends quit the applet just like the menu entry does.
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		tray.Remove()
	}()

	tray.Show()

	slog.Info("Tray applet started", "version", buildinfo.Version)
	if err := tray.Run(); err != nil {
		slog.Error("Tray applet error", "error", err)
		os.Exit(2)
	}
	slog.Info("Goodbye!")
}

// trayIcon renders the tray icon: a bold, italic capital G on a transparent
// background.  The Linux StatusNotifierItem path carries only a raw pixmap
// (IconName/IconThemePath are left empty by systray), so the desktop never
// tints it and the glyph ships in a light gray that stays legible on the
// dark panels GTK uses by default; macOS recolors it from the alpha channel
// because the same PNG is passed to SetTemplateIcon.  The glyph is drawn
// analytically (a ring with a right-side aperture, a centre bar, and a right
// stem, sheared for italic) and supersampled for anti-aliasing, so the
// binary needs no embedded font or image assets.
func trayIcon() []byte {
	const (
		size    = 32
		samples = 4
	)

	s := float64(size)
	cx := 0.5 * s
	cy := 0.5 * s
	outerR := 0.40 * s
	innerR := 0.22 * s
	stroke := outerR - innerR
	const shear = 0.25
	const aperture = 30.0 * math.Pi / 180
	stemReach := outerR * math.Sin(aperture)

	inGlyph := func(px, py float64) bool {
		// Un-shear the sample point; the glyph is defined upright.
		gx := px - shear*(cy-py)

		dx := gx - cx
		dy := py - cy

		// Ring, open on the right side.
		r := math.Hypot(dx, dy)
		if r >= innerR && r <= outerR && math.Abs(math.Atan2(-dy, dx)) >= aperture {
			return true
		}

		// Centre bar running from the middle to the right edge.
		if gx >= cx && gx <= cx+outerR && math.Abs(py-cy) <= 0.5*stroke {
			return true
		}

		// Right stem joining the bar down to where the ring resumes.
		if gx >= cx+outerR-stroke && gx <= cx+outerR && py >= cy-0.5*stroke && py <= cy+stemReach {
			return true
		}

		return false
	}

	// Light gray glyph: dark tray panels are the common case and the pixmap
	// is displayed unmodified on Linux.
	glyph := color.NRGBA{R: 238, G: 238, B: 238, A: 255}

	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			hits := 0
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					px := float64(x) + (float64(sx)+0.5)/samples
					py := float64(y) + (float64(sy)+0.5)/samples
					if inGlyph(px, py) {
						hits++
					}
				}
			}
			img.SetNRGBA(x, y, color.NRGBA{
				R: glyph.R,
				G: glyph.G,
				B: glyph.B,
				A: uint8(hits * 255 / (samples * samples)),
			})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		// Encoding a static in-memory image cannot fail in practice;
		// return nil so the tray simply comes up without an icon.
		return nil
	}
	return buf.Bytes()
}
