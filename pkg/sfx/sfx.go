// Package sfx plays the short sounds an event runs on (match start,
// hurry warning, end of match) through the audio output of the machine
// gameday runs on.
//
// The sounds ship inside the binary.  Only one is audible at a time:
// playing a sound cuts off whatever was playing before it, because an
// arena announcement should interrupt the previous one rather than queue
// behind it.
//
// A sound is decoded on its first use and its samples are kept, so
// repeating it is cheap and a broken file is reported by the call that
// hit it rather than by whichever call happened to load it.  A machine
// with no audio hardware reports the failure once and stays quiet
// afterwards, so a missing sound device never interrupts an event.
//
// Set GAMEDAY_SFX to a directory to play that directory's files instead
// of the compiled-in ones, which lets a site use its own recordings
// without rebuilding anything.
package sfx

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/wav"
)

// EnvDir names the environment variable that points the player at a
// directory of sound files instead of the compiled-in library.  It is
// read once, by New.
const EnvDir = "GAMEDAY_SFX"

// The names Play accepts.  Each is the base name of a file in the sound
// library, without its extension.
const (
	StartOfMatch    = "start-of-match"
	MatchAlmostOver = "match-almost-over"
	MatchFault      = "match-fault"
	EndOfMatch      = "end-of-match"
)

var (
	// ErrNotFound is returned when the sound library has no file for a
	// requested name.
	ErrNotFound = errors.New("sfx: sound not found")

	// ErrClosed is returned by Play, Stop, and Close after the player
	// has been closed.
	ErrClosed = errors.New("sfx: player is closed")
)

// fileExts are the file types a sound library may hold, in the order
// they are tried when a name is given without an extension.
var fileExts = []string{".wav"}

// Player plays one-shot sounds, one at a time.
//
// Create a Player with New.  It is safe for concurrent use.
type Player struct {
	// src holds the sound files, rooted at the files themselves so that
	// names carry no directory prefix.
	src fs.FS

	// srcSet records that an option chose the source, so the
	// environment no longer gets a say.  It is tracked rather than
	// inferred from src, since fs.FS values are not always comparable.
	srcSet bool

	// quality is the resampling quality for sounds recorded at a
	// different sample rate than the audio output's.
	quality int

	// openOutput provides something to play sound through.  Tests
	// replace it to keep audio off the machine's hardware.
	openOutput func() (output, error)

	mu sync.Mutex
	// decoded caches the samples of every sound loaded so far, keyed by
	// file name within src.
	decoded map[string]*beep.Buffer
	// out is the audio output, opened on first use.  Whether a failed
	// attempt is remembered is the audio output's decision, not the
	// player's.
	out    output
	closed bool
}

// Option configures a Player as it is created.
type Option func(*Player)

// WithFS plays the sounds held by fsys, whose root holds the sound files
// directly.  It replaces the compiled-in library, and takes precedence
// over the GAMEDAY_SFX directory.
func WithFS(fsys fs.FS) Option {
	return func(p *Player) {
		p.src = fsys
		p.srcSet = true
	}
}

// WithDir plays the sounds in the directory dir on disk, replacing the
// compiled-in library.  A dir that is not a readable directory is
// reported and leaves the previous source in place.
func WithDir(dir string) Option {
	return func(p *Player) {
		if !isReadableDir(dir) {
			slog.Warn("Sound effect directory is unusable, keeping embedded sounds",
				"dir", dir)
			return
		}
		p.src = os.DirFS(dir)
		p.srcSet = true
	}
}

// New creates a Player.
//
// Sounds come from the compiled-in library unless an option supplies
// another source or GAMEDAY_SFX names a directory.  The audio output is
// not opened until the first Play.
func New(opts ...Option) *Player {
	p := &Player{
		src:        libraryFS,
		quality:    resampleQuality,
		openOutput: audioSink.get,
		decoded:    map[string]*beep.Buffer{},
	}
	for _, opt := range opts {
		opt(p)
	}

	// An explicit source wins over the environment.
	if p.srcSet {
		return p
	}
	if dir := os.Getenv(EnvDir); dir != "" {
		if !isReadableDir(dir) {
			slog.Warn("Ignoring sound effect directory, keeping embedded sounds",
				EnvDir, dir)
			return p
		}
		slog.Info("Playing sound effects from disk", EnvDir, dir)
		p.src = os.DirFS(dir)
	}
	return p
}

// Play starts name playing, immediately cutting off any sound already
// playing.  It returns once playback is queued, not when the sound
// finishes.
//
// A name may be given with or without its file extension, so both
// Play(StartOfMatch) and Play("start-of-match.wav") work.
func (p *Player) Play(name string) error {
	return p.play(name, false)
}

// PlayAndWait is Play, extended to wait for the sound to finish
// playing.  It is what a command that plays one sound and exits wants:
// returning before playback is done would truncate the sound, and on a
// program that exits right away it can cut the sound off entirely.
//
// Stop discards what is playing without unblocking the wait, so do not
// combine the two.
func (p *Player) PlayAndWait(name string) error {
	return p.play(name, true)
}

func (p *Player) play(name string, wait bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrClosed
	}

	// Resolve and decode before touching the audio output, so a typo in
	// a sound name cannot open the device.
	stream, err := p.streamLocked(name)
	if err != nil {
		return err
	}
	out, err := p.outputLocked()
	if err != nil {
		return fmt.Errorf("sfx: cannot play %s: %w", name, err)
	}

	if wait {
		// The wait happens with the player's lock held.  Nothing else
		// the same program can do to the sound matters here, and a
		// caller that waits wants this one sound to be the only thing
		// the process is doing.
		out.PlayAndWait(stream)
		return nil
	}
	out.Play(stream)
	return nil
}

// Duration reports how long name plays for.  A name the library does
// not have yields an error.
func (p *Player) Duration(name string) (time.Duration, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	buf, err := p.bufferLocked(name)
	if err != nil {
		return 0, err
	}
	return buf.Format().SampleRate.D(buf.Len()), nil
}

// Stop silences the player.  Nothing is left queued, so the next Play
// starts from the beginning of its own sound.
func (p *Player) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrClosed
	}
	if p.out != nil {
		p.out.Stop()
	}
	return nil
}

// Close releases the audio output.  The player is unusable afterwards
// and reports ErrClosed.
//
// The audio device itself belongs to the process, so closing it stops
// every player in the program; Close is meant for shutdown.
func (p *Player) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrClosed
	}
	p.closed = true
	if p.out == nil {
		return nil
	}
	out := p.out
	p.out = nil
	return out.Close()
}

// Sounds lists the names Play accepts, sorted and without their file
// extensions.  A source that cannot be read yields no names.
func (p *Player) Sounds() []string {
	entries, err := fs.ReadDir(p.src, ".")
	if err != nil {
		slog.Warn("Cannot list sound effects", "error", err)
		return nil
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(path.Ext(entry.Name()))
		if !slices.Contains(fileExts, ext) {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ext)
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// output is somewhere to play sound.  The implementation backed by the
// machine's audio hardware lives in speaker.go.
type output interface {
	// Play starts s playing, replacing whatever was playing before it.
	Play(s beep.Streamer)
	// PlayAndWait starts s playing, replacing whatever was playing
	// before it, and blocks until s has finished.
	PlayAndWait(s beep.Streamer)
	// Stop discards whatever is playing.
	Stop()
	Close() error
}

// outputLocked returns the audio output, opening it on first use.  A
// failure is not cached here: the audio output itself decides whether a
// failed attempt rules out the next sound.  The caller must hold p.mu.
func (p *Player) outputLocked() (output, error) {
	if p.out != nil {
		return p.out, nil
	}
	out, err := p.openOutput()
	if err != nil {
		return nil, err
	}
	p.out = out
	return out, nil
}

// streamLocked builds the samples to play for name, resampled to the
// audio output's rate.  The caller must hold p.mu.
func (p *Player) streamLocked(name string) (beep.Streamer, error) {
	buf, err := p.bufferLocked(name)
	if err != nil {
		return nil, err
	}

	var stream beep.Streamer = buf.Streamer(0, buf.Len())
	if from := buf.Format().SampleRate; from != outputSampleRate {
		stream = beep.Resample(p.quality, from, outputSampleRate, stream)
	}
	return stream, nil
}

// bufferLocked returns the samples of name, decoding and caching them on
// first use.  The caller must hold p.mu.
func (p *Player) bufferLocked(name string) (*beep.Buffer, error) {
	file, err := p.resolveLocked(name)
	if err != nil {
		return nil, err
	}
	if buf, ok := p.decoded[file]; ok {
		return buf, nil
	}

	data, err := fs.ReadFile(p.src, file)
	if err != nil {
		return nil, fmt.Errorf("sfx: cannot read %s: %w", file, err)
	}
	stream, format, err := wav.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("sfx: cannot decode %s: %w", file, err)
	}
	defer stream.Close()

	buf := beep.NewBuffer(format)
	buf.Append(stream)
	if stream.Err() != nil {
		return nil, fmt.Errorf("sfx: cannot read %s: %w", file, stream.Err())
	}
	if buf.Len() == 0 {
		return nil, fmt.Errorf("sfx: %s holds no audio", file)
	}

	p.decoded[file] = buf
	return buf, nil
}

// resolveLocked finds the file in the sound library for name, which may
// be given with or without an extension.
func (p *Player) resolveLocked(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: no name given", ErrNotFound)
	}

	candidates := []string{name}
	if path.Ext(name) == "" {
		for _, ext := range fileExts {
			candidates = append(candidates, name+ext)
		}
	}
	for _, candidate := range candidates {
		if fi, err := fs.Stat(p.src, candidate); err == nil && !fi.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrNotFound, name)
}

// isReadableDir reports whether name is an existing directory.
func isReadableDir(name string) bool {
	fi, err := os.Stat(name)
	return err == nil && fi.IsDir()
}
