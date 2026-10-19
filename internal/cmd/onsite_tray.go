package cmd

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/gogpu/systray"
	"github.com/spf13/cobra"

	"github.com/gizmo-platform/gameday/pkg/announcer"
	"github.com/gizmo-platform/gameday/pkg/buildinfo"
	"github.com/gizmo-platform/gameday/pkg/sfx"
)

var (
	onsiteTrayCmd = &cobra.Command{
		Use:   "tray",
		Short: "tray - Start a system tray applet",
		Long:  onsiteTrayCmdLongDocs,
		Run:   onsiteTrayCmdRun,
	}

	onsiteTrayCmdLongDocs = `tray

Tray starts a system tray applet for gameday.  The applet does not start
the webserver or any other gameday services.

It shows the build information of the running binary, plays the match
clock's sound effects through this machine's audio output, and offers a
menu entry to play any individual sound as a test.  Listening for the
clock is toggled from the same menu; the server to listen to is named by
GAMEDAY_SERVER and defaults to localhost on GAMEDAY_PORT.  Use Exit to
quit.`
)

func init() {
	onsiteCmd.AddCommand(onsiteTrayCmd)
}

func onsiteTrayCmdRun(c *cobra.Command, args []string) {
	buildInfo := fmt.Sprintf("Gameday %s", buildinfo.Version)

	player := sfx.New()
	defer player.Close()

	// The clock listener lives as long as the applet does; cancelling it
	// here stops it when the applet exits however that happened.
	ctx, stopClock := context.WithCancel(context.Background())
	defer stopClock()
	clock := newClockPlayer(ctx, player)

	tray := systray.New()
	tray.SetAppName("Gameday")

	menu := systray.NewMenu()
	menu.Add(buildInfo, nil).SetDisabled(true)
	menu.AddSeparator()

	if clock.CanRun() {
		// The box starts checked: whoever started the applet on the
		// machine wired to the speakers wants it making sound without
		// having to remember to turn it on.
		var listen *systray.MenuItem
		listen = menu.AddCheckbox("Play clock sounds", clock.IsOn(), func() {
			// The library does not flip a checkbox by itself, so the
			// controller decides the new state and the menu shows it.
			listen.SetChecked(clock.toggle())
		})
	}
	addTestSoundMenu(menu, player)

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

// clockPlayer is the tray's switch for announcing the match clock.  It
// keeps the on/off logic away from the menu so it can be reasoned about,
// and so a listener that dies is not something the menu has to know.
type clockPlayer struct {
	// ctx is the applet's lifetime; a listener is cancelled before it.
	ctx context.Context

	// listen announces the clock until the context it is given ends.  A
	// nil listen means there is nothing to announce.
	listen func(ctx context.Context)

	mu     sync.Mutex
	cancel context.CancelFunc // non-nil only while announcing
	on     bool
	wg     sync.WaitGroup
}

// newClockPlayer returns a switch wired to the clock sounds of the server
// the environment names.  Announcing starts immediately, matching the
// checked box the menu opens with.
func newClockPlayer(ctx context.Context, player *sfx.Player) *clockPlayer {
	// Validate the address up front so a typo is reported by the command
	// that was given it, not by a retry loop that runs forever.
	if err := announcer.New(announcer.WithPlayer(player)).Validate(); err != nil {
		slog.Error("Cannot announce the match clock, tray is indicator only",
			"error", err)
		return &clockPlayer{}
	}

	return startClockPlayer(ctx, func(ctx context.Context) {
		a := announcer.New(announcer.WithPlayer(player))
		slog.Info("Announcing the match clock", "server", a.Server())
		if err := a.Run(ctx); err != nil && ctx.Err() == nil {
			slog.Error("Announcer stopped", "error", err)
		}
	})
}

// startClockPlayer returns a switch that listens by calling listen, and
// switches it on.  Splitting it out of newClockPlayer keeps the switching
// testable without an announcer or a sound device.
func startClockPlayer(ctx context.Context, listen func(ctx context.Context)) *clockPlayer {
	c := &clockPlayer{ctx: ctx, listen: listen}
	c.mu.Lock()
	c.setOn(true)
	c.mu.Unlock()
	return c
}

// CanRun reports whether there is a clock this switch can announce.
func (c *clockPlayer) CanRun() bool {
	return c.listen != nil
}

// IsOn reports whether sounds are currently being announced.
func (c *clockPlayer) IsOn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.on
}

// toggle flips the state and reports the new one, which is what the
// caller writes back to the menu's checkbox.
func (c *clockPlayer) toggle() bool {
	c.mu.Lock()
	c.setOn(!c.on)
	on := c.on
	c.mu.Unlock()
	return on
}

// stop turns the listener off, waiting for it to finish.
func (c *clockPlayer) stop() {
	c.mu.Lock()
	c.setOn(false)
	c.mu.Unlock()
	c.wg.Wait()
}

// setOn makes the state be on, starting or stopping the listener as
// needed.  The caller must hold c.mu.
func (c *clockPlayer) setOn(on bool) {
	if on == c.on {
		return
	}

	if !on {
		c.on = false
		if c.cancel != nil {
			c.cancel()
			c.cancel = nil
		}
		return
	}
	if c.listen == nil {
		return
	}
	run, cancel := context.WithCancel(c.ctx)
	c.cancel = cancel
	c.on = true
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.listen(run)
	}()
}

// addTestSoundMenu adds a submenu that plays each sound immediately, so
// an operator can set levels and confirm the right output is configured
// without starting a match to hear one.
func addTestSoundMenu(menu *systray.Menu, player *sfx.Player) {
	sounds := player.Sounds()
	if len(sounds) == 0 {
		slog.Warn("No sound effects are available to test")
		return
	}

	test := systray.NewMenu()
	for _, name := range sounds {
		name := name
		test.Add(titleCase(name), func() {
			// Playback happens on its own goroutine: menu callbacks run
			// on the platform event loop, which a sound must not occupy.
			go func() {
				if err := player.Play(name); err != nil {
					slog.Warn("Could not play sound", "sound", name, "error", err)
				}
			}()
		})
	}
	menu.AddSubmenu("Test sounds", test)
}

// titleCase renders a dash-separated sound name the way a menu label
// reads, so "start-of-match" becomes "Start Of Match".
func titleCase(name string) string {
	words := strings.Split(strings.TrimSuffix(name, ".wav"), "-")
	for i, w := range words {
		if w != "" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
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
