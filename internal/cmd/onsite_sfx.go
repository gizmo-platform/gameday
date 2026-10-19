package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/gizmo-platform/gameday/pkg/announcer"
	"github.com/gizmo-platform/gameday/pkg/sfx"
)

var (
	onsiteSFxCmd = &cobra.Command{
		Use:   "sfx [sound]",
		Short: "sfx - Play the match clock's sound effects",
		Long:  onsiteSFxCmdLongDocs,
		Args:  cobra.MaximumNArgs(1),
		Run:   onsiteSFxCmdRun,
	}

	onsiteSFxCmdLongDocs = `sfx [sound]

Sound effects are how an arena knows the clock moved.  This command puts
gameday in charge of playing them through the audio output of the machine
it runs on, which is the machine wired to the speakers.

With the name of a sound, sfx plays that one sound and exits.  Use it to
check the levels and that the right output is configured before the first
match:

  gameday onsite sfx start-of-match

With no argument, sfx subscribes to the event stream of a running gameday
server and plays the sound of every clock event as it happens: the start
of a match, the hurry warning, the end of a match, and the fault of a
cancelled run.  It keeps running until interrupted, and reconnects on its
own if the server goes away.

The server to listen to is named by GAMEDAY_SERVER, e.g.
"http://192.168.1.10:8080"; it defaults to localhost on GAMEDAY_PORT.
Set GAMEDAY_SFX to a directory of .wav files to play your own recordings
in place of the compiled-in ones.  List the playable names with:

  gameday onsite sfx --list`

	sfxList bool
)

func init() {
	onsiteCmd.AddCommand(onsiteSFxCmd)
	onsiteSFxCmd.Flags().BoolVar(&sfxList, "list", false,
		"list the playable sound names and exit")
}

func onsiteSFxCmdRun(c *cobra.Command, args []string) {
	player := sfx.New()
	defer player.Close()

	if sfxList {
		listSounds(c.OutOrStdout(), player)
		return
	}

	if len(args) == 1 {
		name, err := resolveSound(player, args[0])
		if err != nil {
			slog.Error("Unknown sound", "sound", args[0], "error", err,
				"available", strings.Join(player.Sounds(), ","))
			os.Exit(2)
		}
		// Wait for playback to finish: exiting while the sound is still
		// going would cut it off.
		if err := player.PlayAndWait(name); err != nil {
			slog.Error("Could not play sound", "sound", name, "error", err)
			os.Exit(2)
		}
		return
	}

	runAnnouncer(player)
}

// runAnnouncer plays the clock's events until interrupted.
func runAnnouncer(player *sfx.Player) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a := announcer.New(announcer.WithPlayer(player))
	if err := a.Validate(); err != nil {
		slog.Error("Bad event server", "error", err)
		os.Exit(2)
	}

	slog.Info("Announcing the match clock, press Ctrl-C to stop",
		"server", a.Server(), "types", strings.Join(announcer.EventTypes(), ","))
	if err := a.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("Announcer stopped", "error", err)
		os.Exit(2)
	}
	slog.Info("Goodbye!")
}

// listSounds prints the playable sound names, one per line.
func listSounds(w io.Writer, player *sfx.Player) {
	for _, name := range player.Sounds() {
		fmt.Fprintln(w, name)
	}
}

// resolveSound turns a name given on the command line into a name the
// sound library has, so "start-of-match", "start_of_match", "StartOfMatch"
// and "end-of-match.wav" all find a sound.
func resolveSound(player *sfx.Player, given string) (string, error) {
	want := normalizeSoundName(given)
	for _, name := range player.Sounds() {
		if name == want {
			return name, nil
		}
	}
	return "", fmt.Errorf("no sound named %q", given)
}

// normalizeSoundName reduces a sound name typed by a person to the
// library's spelling: lower case, dashes as separators, no extension.
func normalizeSoundName(given string) string {
	// The case of the original is needed to spot the word boundaries of
	// the Go constant spellings, so the extension is trimmed rather than
	// the whole name lower-cased.
	s := strings.TrimSpace(given)
	s = strings.TrimSuffix(s, ".wav")

	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			// A lower-case letter before an upper-case one is a word
			// boundary, so "StartOfMatch" splits into "start-of-match".
			if i > 0 {
				prev := rune(s[i-1])
				if prev >= 'a' && prev <= 'z' {
					b.WriteByte('-')
				}
			}
			r += 'a' - 'A'
		}
		if r == '_' {
			r = '-'
		}
		b.WriteRune(r)
	}
	return b.String()
}
