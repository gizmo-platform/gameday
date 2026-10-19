package cmd

import (
	"bytes"
	"testing"
	"testing/fstest"

	"github.com/gizmo-platform/gameday/pkg/sfx"
)

func TestNormalizeSoundName(t *testing.T) {
	cases := []struct {
		given string
		want  string
	}{
		{"start-of-match", "start-of-match"},
		{"start_of_match", "start-of-match"},
		{"StartOfMatch", "start-of-match"},
		{"STARTOFMATCH", "startofmatch"},
		{"start-of-match.wav", "start-of-match"},
		{"  end-of-match  ", "end-of-match"},
		{"MatchAlmostOver", "match-almost-over"},
		{"match-fault", "match-fault"},
		{"Beep", "beep"},
		{"", ""},
	}
	for _, c := range cases {
		if got := normalizeSoundName(c.given); got != c.want {
			t.Errorf("normalizeSoundName(%q) = %q, want %q", c.given, got, c.want)
		}
	}
}

// sfxTestPlayer returns a player over an in-memory library holding the
// shipped names, with no audio device involved.
func sfxTestPlayer(t *testing.T) *sfx.Player {
	t.Helper()

	empty := fstest.MapFS{
		"start-of-match.wav":    &fstest.MapFile{},
		"match-almost-over.wav": &fstest.MapFile{},
		"match-fault.wav":       &fstest.MapFile{},
		"end-of-match.wav":      &fstest.MapFile{},
	}
	return sfx.New(sfx.WithFS(empty))
}

func TestResolveSoundAcceptsTheSpellingsAnOperatorTypes(t *testing.T) {
	player := sfxTestPlayer(t)

	for _, given := range []string{
		"start-of-match",
		"start_of_match",
		"StartOfMatch",
		"start-of-match.wav",
		" START-OF-MATCH ",
	} {
		got, err := resolveSound(player, given)
		if err != nil {
			t.Errorf("resolveSound(%q): %v", given, err)
			continue
		}
		if got != sfx.StartOfMatch {
			t.Errorf("resolveSound(%q) = %q, want %q", given, got, sfx.StartOfMatch)
		}
	}
}

func TestResolveSoundReportsAnUnknownName(t *testing.T) {
	player := sfxTestPlayer(t)

	_, err := resolveSound(player, "klaxon")
	if err == nil {
		t.Fatal("an unknown sound should not resolve")
	}
	// The message must name what was asked for, since it is shown
	// alongside the list of what is available.
	if got := err.Error(); got != `no sound named "klaxon"` {
		t.Errorf("error = %q", got)
	}
}

func TestListSounds(t *testing.T) {
	player := sfxTestPlayer(t)

	var buf bytes.Buffer
	listSounds(&buf, player)

	want := "end-of-match\nmatch-almost-over\nmatch-fault\nstart-of-match\n"
	if got := buf.String(); got != want {
		t.Errorf("listSounds wrote %q, want %q", got, want)
	}
}

func TestSFxCmdShape(t *testing.T) {
	// The command's contract: one optional argument, and a --list flag
	// for discovering the names.
	if got, want := onsiteSFxCmd.Name(), "sfx"; got != want {
		t.Errorf("name = %q, want %q", got, want)
	}
	if err := onsiteSFxCmd.ValidateArgs([]string{"start-of-match"}); err != nil {
		t.Errorf("one argument should be accepted: %v", err)
	}
	if err := onsiteSFxCmd.ValidateArgs(nil); err != nil {
		t.Errorf("no argument should be accepted: %v", err)
	}
	if err := onsiteSFxCmd.ValidateArgs([]string{"a", "b"}); err == nil {
		t.Error("two arguments should be refused")
	}
	if onsiteSFxCmd.Flags().Lookup("list") == nil {
		t.Error("the --list flag is missing")
	}
	// The command must be reachable as "onsite sfx".
	var found bool
	for _, sub := range onsiteCmd.Commands() {
		if sub == onsiteSFxCmd {
			found = true
		}
	}
	if !found {
		t.Error("sfx is not registered under the onsite command")
	}
}

func TestAnnouncerSoundsAreListedForTesting(t *testing.T) {
	// Every sound the announcer can be told to play must be in the
	// library, otherwise a menu entry or a command line reference would
	// fail at the moment an operator needed it.
	player := sfx.New()
	available := map[string]bool{}
	for _, name := range player.Sounds() {
		available[name] = true
	}

	for _, name := range []string{
		sfx.StartOfMatch,
		sfx.MatchAlmostOver,
		sfx.MatchFault,
		sfx.EndOfMatch,
	} {
		if !available[name] {
			t.Errorf("the library is missing %q", name)
		}
	}
}
