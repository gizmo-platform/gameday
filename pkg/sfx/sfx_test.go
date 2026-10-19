package sfx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gopxl/beep/v2"
)

// embeddedSounds are the names the shipped library must provide.
var embeddedSounds = []string{
	"end-of-match",
	"match-almost-over",
	"match-fault",
	"start-of-match",
}

// testRate is the sample rate generated sounds are rendered at.  It
// differs from the output rate, so playback exercises resampling.
const testRate = beep.SampleRate(11025)

// wavBytes renders a sine tone as PCM16 mono WAV bytes at the given
// sample rate and duration.
func wavBytes(t *testing.T, rate beep.SampleRate, dur time.Duration, freq float64) []byte {
	t.Helper()

	total := rate.N(dur)
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, [4]byte{'R', 'I', 'F', 'F'})
	binary.Write(&buf, binary.LittleEndian, uint32(36+total*2))
	binary.Write(&buf, binary.LittleEndian, [4]byte{'W', 'A', 'V', 'E'})
	binary.Write(&buf, binary.LittleEndian, [4]byte{'f', 'm', 't', ' '})
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // PCM
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // mono
	binary.Write(&buf, binary.LittleEndian, uint32(rate))
	binary.Write(&buf, binary.LittleEndian, uint32(rate*2))
	binary.Write(&buf, binary.LittleEndian, uint16(2))  // frame size
	binary.Write(&buf, binary.LittleEndian, uint16(16)) // bits
	binary.Write(&buf, binary.LittleEndian, [4]byte{'d', 'a', 't', 'a'})
	binary.Write(&buf, binary.LittleEndian, uint32(total*2))
	for i := range total {
		v := int32(0.5 * math.Sin(float64(i)*freq) * (1 << 15))
		binary.Write(&buf, binary.LittleEndian, uint16(v))
	}
	return buf.Bytes()
}

// testFS returns a library holding one valid sound.
func testFS(t *testing.T) fstest.MapFS {
	t.Helper()
	return fstest.MapFS{"tone.wav": &fstest.MapFile{
		Data: wavBytes(t, testRate, 100*time.Millisecond, 0.1),
	}}
}

// writeWAV writes a generated sound into dir.
func writeWAV(t *testing.T, dir, name string) {
	t.Helper()

	data := wavBytes(t, testRate, 80*time.Millisecond, 0.1)
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// fakeOutput stands in for the audio device.  It mirrors the real one's
// contract: Play replaces whatever was playing, so a single sound is
// audible at a time.
type fakeOutput struct {
	plays  int
	stops  int
	closes int

	// current is the streamer last handed to Play, mirroring a speaker
	// that dropped everything before it.
	current beep.Streamer

	// opened counts how many times the sink was created.
	opened int
	// err, when set, is returned by every attempt to open the sink.
	err error

	// done is closed by Stop and Close, which is how a blocked
	// PlayAndWait is released.
	done chan struct{}
}

func newFakeOutput() *fakeOutput {
	return &fakeOutput{done: make(chan struct{})}
}

func (o *fakeOutput) asOption() func() (output, error) {
	return func() (output, error) {
		o.opened++
		if o.err != nil {
			return nil, o.err
		}
		return o, nil
	}
}

func (o *fakeOutput) Play(s beep.Streamer) {
	o.plays++
	o.current = s
}

// PlayAndWait mirrors the real output's blocking contract: it records
// the streamer and stays blocked until something silences the fake,
// which is what a test uses to observe that a caller waited.
func (o *fakeOutput) PlayAndWait(s beep.Streamer) {
	o.Play(s)
	<-o.done
}

func (o *fakeOutput) Stop() {
	o.stops++
	o.current = nil
	o.silence()
}

func (o *fakeOutput) Close() error {
	o.closes++
	o.current = nil
	o.silence()
	return nil
}

// silence unblocks any PlayAndWait sitting on the fake.  It is safe to
// call more than once, since a stopped and then closed output is a
// normal sequence.
func (o *fakeOutput) silence() {
	select {
	case <-o.done:
	default:
		close(o.done)
	}
}

// samples drains the streamer handed to the output, returning how many
// samples it held.
func (o *fakeOutput) samples() int {
	if o.current == nil {
		return 0
	}
	var (
		total int
		buf   = make([][2]float64, 512)
	)
	for {
		n, ok := o.current.Stream(buf)
		total += n
		if !ok {
			return total
		}
	}
}

// TestEmbeddedLibraryListsAndPlaysShippedSounds checks the sounds checked
// in next to the package are all discoverable and playable.
func TestEmbeddedLibraryListsAndPlaysShippedSounds(t *testing.T) {
	t.Setenv(EnvDir, "")

	p := New()
	if got, want := p.Sounds(), embeddedSounds; !slices.Equal(got, want) {
		t.Errorf("Sounds() = %v, want %v", got, want)
	}

	for _, name := range embeddedSounds {
		t.Run(name, func(t *testing.T) {
			out := newFakeOutput()
			p := New()
			p.openOutput = out.asOption()
			if err := p.Play(name); err != nil {
				t.Fatalf("Play(%q): %v", name, err)
			}
			if n := out.samples(); n == 0 {
				t.Errorf("Play(%q) produced no samples", name)
			}
		})
	}
}

// TestNewUsesEmbeddedLibrary checks that with no source option and no
// GAMEDAY_SFX the player reads the compiled-in sounds.
func TestNewUsesEmbeddedLibrary(t *testing.T) {
	t.Setenv(EnvDir, "")

	if p := New(); p.src != libraryFS {
		t.Error("player did not use the embedded library")
	}
}

// TestNewReadsDirectoryFromEnvironment checks GAMEDAY_SFX replaces the
// embedded library with the files in the named directory.
func TestNewReadsDirectoryFromEnvironment(t *testing.T) {
	dir := t.TempDir()
	writeWAV(t, dir, "custom-alarm.wav")
	writeWAV(t, dir, "notes.txt")
	t.Setenv(EnvDir, dir)

	p := New()
	if got, want := p.Sounds(), []string{"custom-alarm"}; !slices.Equal(got, want) {
		t.Errorf("Sounds() = %v, want %v", got, want)
	}

	out := newFakeOutput()
	p.openOutput = out.asOption()
	if err := p.Play("custom-alarm"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if out.plays != 1 {
		t.Errorf("plays = %d, want 1", out.plays)
	}
}

// TestNewIgnoresUnusableEnvironmentDirectory checks that GAMEDAY_SFX
// pointing at anything but a readable directory falls back to the
// embedded library rather than leaving the player with no sounds.
func TestNewIgnoresUnusableEnvironmentDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, dir := range map[string]string{
		"missing": filepath.Join(t.TempDir(), "missing"),
		"not-dir": file,
		"empty":   "",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(EnvDir, dir)

			p := New()
			if len(p.Sounds()) != len(embeddedSounds) {
				t.Errorf("Sounds() = %v, want the embedded sounds", p.Sounds())
			}
		})
	}
}

// TestWithDirOverridesEnvironment checks WithDir wins over GAMEDAY_SFX,
// and that an unusable WithDir directory keeps the previous source.
func TestWithDirOverridesEnvironment(t *testing.T) {
	dir := t.TempDir()
	writeWAV(t, dir, "site-sound.wav")

	env := t.TempDir()
	writeWAV(t, env, "env-sound.wav")
	t.Setenv(EnvDir, env)

	if got, want := New().Sounds(), []string{"env-sound"}; !slices.Equal(got, want) {
		t.Errorf("Sounds() = %v, want %v", got, want)
	}
	if got, want := New(WithDir(dir)).Sounds(), []string{"site-sound"}; !slices.Equal(got, want) {
		t.Errorf("WithDir Sounds() = %v, want %v", got, want)
	}

	fallback := New(WithDir(filepath.Join(t.TempDir(), "missing")))
	if got, want := fallback.Sounds(), []string{"env-sound"}; !slices.Equal(got, want) {
		t.Errorf("unusable WithDir Sounds() = %v, want %v", got, want)
	}
}

// TestWithFSOverridesEnvironment checks WithFS takes precedence over both
// the embedded library and GAMEDAY_SFX.
func TestWithFSOverridesEnvironment(t *testing.T) {
	dir := t.TempDir()
	writeWAV(t, dir, "from-disk.wav")
	t.Setenv(EnvDir, dir)

	if got, want := New(WithFS(testFS(t))).Sounds(), []string{"tone"}; !slices.Equal(got, want) {
		t.Errorf("Sounds() = %v, want %v", got, want)
	}
}

// TestPlayResamplesToOutputRate checks a sound recorded at a different
// sample rate reaches the output as the output's rate, so its length in
// time is preserved.
func TestPlayResamplesToOutputRate(t *testing.T) {
	const dur = 100 * time.Millisecond

	p := New(WithFS(testFS(t)))
	out := newFakeOutput()
	p.openOutput = out.asOption()

	if err := p.Play("tone"); err != nil {
		t.Fatalf("Play: %v", err)
	}

	want := outputSampleRate.N(dur)
	if got := out.samples(); got < want/2 || got > want*2 {
		t.Errorf("played %d samples, want about %d", got, want)
	}
}

// TestPlayInterruptsPreviousSound checks a second Play replaces the first
// sound at the output rather than adding to it, which is what keeps a
// single sound audible.
func TestPlayInterruptsPreviousSound(t *testing.T) {
	library := fstest.MapFS{
		"slow.wav": &fstest.MapFile{Data: wavBytes(t, testRate, 100*time.Millisecond, 0.01)},
		"fast.wav": &fstest.MapFile{Data: wavBytes(t, testRate, 100*time.Millisecond, 0.9)},
	}

	p := New(WithFS(library))
	out := newFakeOutput()
	p.openOutput = out.asOption()

	if err := p.Play("slow"); err != nil {
		t.Fatalf("Play(slow): %v", err)
	}
	first := out.current
	if err := p.Play("fast"); err != nil {
		t.Fatalf("Play(fast): %v", err)
	}

	if out.current == first {
		t.Fatal("the second sound did not replace the first")
	}
	if out.plays != 2 {
		t.Errorf("plays = %d, want 2", out.plays)
	}
}

// TestPlayUnknownSound checks an unknown name reports ErrNotFound without
// opening the audio device.
func TestPlayUnknownSound(t *testing.T) {
	p := New(WithFS(testFS(t)))
	out := newFakeOutput()
	p.openOutput = out.asOption()

	for _, name := range []string{"nope", "tone.mp3", "", "sub/dir/tone"} {
		if err := p.Play(name); !errors.Is(err, ErrNotFound) {
			t.Errorf("Play(%q) = %v, want ErrNotFound", name, err)
		}
	}
	if out.opened != 0 {
		t.Errorf("audio output opened %d times, want 0", out.opened)
	}
}

// TestPlayAcceptsExtension checks a name may be given with its extension.
func TestPlayAcceptsExtension(t *testing.T) {
	p := New(WithFS(testFS(t)))
	out := newFakeOutput()
	p.openOutput = out.asOption()

	if err := p.Play("tone.wav"); err != nil {
		t.Fatalf("Play(tone.wav): %v", err)
	}
	if out.plays != 1 {
		t.Errorf("plays = %d, want 1", out.plays)
	}
}

// TestPlayDecodesOnce checks the samples are cached, so repeated plays do
// not re-read and re-decode the file.
func TestPlayDecodesOnce(t *testing.T) {
	src := &countingFS{fsys: testFS(t)}
	p := New(WithFS(src))
	out := newFakeOutput()
	p.openOutput = out.asOption()

	if err := p.Play("tone"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	// The first play decoded the samples; the plays below must be served
	// from the cache and read nothing.
	src.reads = 0
	for range 4 {
		if err := p.Play("tone"); err != nil {
			t.Fatalf("Play: %v", err)
		}
	}
	if src.reads != 0 {
		t.Errorf("sound read %d bytes after being cached, want 0", src.reads)
	}
	if out.plays != 5 {
		t.Errorf("plays = %d, want 5", out.plays)
	}
}

// TestPlayBrokenSound checks a file that is not playable audio is
// reported, and that the failure is not cached: a corrected file becomes
// playable without a restart.
func TestPlayBrokenSound(t *testing.T) {
	library := fstest.MapFS{
		"broken.wav": &fstest.MapFile{Data: []byte("this is not audio")},
	}

	p := New(WithFS(library))
	out := newFakeOutput()
	p.openOutput = out.asOption()

	if err := p.Play("broken"); err == nil {
		t.Fatal("Play of a broken sound succeeded")
	}
	if out.opened != 0 {
		t.Errorf("audio output opened %d times, want 0", out.opened)
	}

	library["broken.wav"] = &fstest.MapFile{Data: wavBytes(t, testRate, 50*time.Millisecond, 0.1)}
	if err := p.Play("broken"); err != nil {
		t.Fatalf("Play of the repaired sound: %v", err)
	}
}

// TestPlayReportsAudioFailure checks a Play that cannot reach the audio
// output says so, wrapping the device's own reason, and that the player
// remains usable afterwards.
func TestPlayReportsAudioFailure(t *testing.T) {
	unusable := errors.New("no such device")

	p := New(WithFS(testFS(t)))
	opened := 0
	p.openOutput = func() (output, error) {
		opened++
		return nil, unusable
	}

	for range 2 {
		if err := p.Play("tone"); !errors.Is(err, unusable) {
			t.Errorf("Play = %v, want it to wrap %v", err, unusable)
		}
	}
	if opened != 2 {
		t.Errorf("audio output opened %d times, want the provider asked each time", opened)
	}

	// A stopped player is unaffected by the failure.
	if err := p.Stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

// TestSinkRemembersUnusableDevice checks an audio device that reports it
// is unusable is opened once for the whole process, so a headless machine
// neither retries the hardware on every sound nor stays quiet without a
// reason to log.
func TestSinkRemembersUnusableDevice(t *testing.T) {
	unusable := errors.New("no such device")
	opens := 0
	sink := &speakerSink{
		initFn: func() error {
			opens++
			return unusable
		},
		closeFn: func() {},
		timeout: time.Second,
	}

	for range 3 {
		if _, err := sink.get(); !errors.Is(err, unusable) {
			t.Errorf("get = %v, want %v", err, unusable)
		}
	}
	if opens != 1 {
		t.Errorf("device opened %d times, want 1", opens)
	}
}

// TestSinkAnswersAfterTimeout checks a device that is merely slow does not
// silence the player forever: the caller that waited too long is released
// with a timeout, and the next one gets the sound.
func TestSinkAnswersAfterTimeout(t *testing.T) {
	ready := make(chan struct{})
	sink := &speakerSink{
		initFn: func() error {
			<-ready
			return nil
		},
		closeFn: func() {},
		timeout: 20 * time.Millisecond,
	}

	if _, err := sink.get(); !errors.Is(err, errOpenTimeout) {
		t.Errorf("get while opening = %v, want %v", err, errOpenTimeout)
	}

	close(ready)

	// Give the opening goroutine a moment to record its answer.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if out, err := sink.get(); err == nil {
			if out == nil {
				t.Fatal("get returned no output with no error")
			}
			break
		} else if !errors.Is(err, errOpenTimeout) {
			t.Fatalf("get after the device answered = %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("the device never became ready")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestSinkClose checks a closed sink hands out nothing further, and that
// closing a device that never opened does not try to close a device that
// was never opened.
func TestSinkClose(t *testing.T) {
	closes := 0
	sink := &speakerSink{
		initFn:  func() error { return nil },
		closeFn: func() { closes++ },
		timeout: time.Second,
	}

	sink.close()
	if closes != 0 {
		t.Errorf("closeFn called %d times without an opened device", closes)
	}
	if _, err := sink.get(); !errors.Is(err, errOutputClosed) {
		t.Errorf("get after close = %v, want %v", err, errOutputClosed)
	}

	out, err := sink.get()
	if err == nil && out != nil {
		t.Error("a closed sink handed out an output")
	}
}

// TestSinkClosesOpenedDevice checks the device is shut down once it has
// been opened successfully.
func TestSinkClosesOpenedDevice(t *testing.T) {
	closes := 0
	sink := &speakerSink{
		initFn:  func() error { return nil },
		closeFn: func() { closes++ },
		timeout: time.Second,
	}

	out, err := sink.get()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := out.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if closes != 1 {
		t.Errorf("device closed %d times, want 1", closes)
	}

	// Closing through the sink directly after the output is idle is a
	// no-op rather than a second device close.
	sink.close()
	if closes != 1 {
		t.Errorf("device closed %d times, want 1", closes)
	}
}

// TestStop checks Stop silences the output, and is harmless before
// anything has played.
func TestStop(t *testing.T) {
	p := New(WithFS(testFS(t)))
	out := newFakeOutput()
	p.openOutput = out.asOption()

	if err := p.Stop(); err != nil {
		t.Fatalf("Stop before any Play: %v", err)
	}
	if out.stops != 0 {
		t.Errorf("stops = %d, want 0 while no output exists", out.stops)
	}

	if err := p.Play("tone"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if out.stops != 1 {
		t.Errorf("stops = %d, want 1", out.stops)
	}
	if out.current != nil {
		t.Error("output still holds a streamer after Stop")
	}
}

// TestClose checks the player is inert once closed.
func TestClose(t *testing.T) {
	p := New(WithFS(testFS(t)))
	out := newFakeOutput()
	p.openOutput = out.asOption()

	if err := p.Play("tone"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if out.closes != 1 {
		t.Errorf("closes = %d, want 1", out.closes)
	}

	for name, call := range map[string]func() error{
		"Play":  func() error { return p.Play("tone") },
		"Stop":  p.Stop,
		"Close": p.Close,
	} {
		if err := call(); !errors.Is(err, ErrClosed) {
			t.Errorf("%s after Close = %v, want ErrClosed", name, err)
		}
	}
	if out.closes != 1 {
		t.Errorf("closes = %d, want the output closed once", out.closes)
	}
}

// TestCloseWithoutOutput checks closing a player that never played
// anything succeeds without opening the audio device.
func TestCloseWithoutOutput(t *testing.T) {
	p := New(WithFS(testFS(t)))
	out := newFakeOutput()
	p.openOutput = out.asOption()

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if out.opened != 0 {
		t.Errorf("audio output opened %d times, want 0", out.opened)
	}
}

// TestPlayConcurrently checks concurrent Play calls are safe and leave
// exactly one sound at the output.
func TestPlayConcurrently(t *testing.T) {
	p := New(WithFS(testFS(t)))
	out := newFakeOutput()
	p.openOutput = out.asOption()

	done := make(chan error)
	for range 8 {
		go func() { done <- p.Play("tone") }()
	}
	for range 8 {
		if err := <-done; err != nil {
			t.Fatalf("Play: %v", err)
		}
	}

	if out.opened != 1 {
		t.Errorf("audio output opened %d times, want 1", out.opened)
	}
	if out.plays != 8 {
		t.Errorf("plays = %d, want 8", out.plays)
	}
}

// countingFS counts the bytes read out of a sound library, which is how
// the tests tell a decoded sound apart from a cached one.
type countingFS struct {
	fsys  fs.FS
	reads int
}

func (c *countingFS) Open(name string) (fs.File, error) {
	f, err := c.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	return &countingFile{File: f, fs: c}, nil
}

type countingFile struct {
	fs.File
	fs *countingFS
}

func (c *countingFile) Read(p []byte) (int, error) {
	n, err := c.File.Read(p)
	c.fs.reads += n
	return n, err
}

// TestPlayReturnsImmediately checks Play does not wait for the sound,
// which is what lets a caller keep working while it plays.
func TestPlayReturnsImmediately(t *testing.T) {
	p := New(WithFS(testFS(t)))
	out := newFakeOutput()
	p.openOutput = out.asOption()

	if err := p.Play("tone"); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if out.plays != 1 {
		t.Errorf("plays = %d, want 1", out.plays)
	}
}

// TestPlayAndWaitBlocksUntilPlaybackEnds checks the caller of
// PlayAndWait is held until the sound has finished, which is what keeps
// a play-and-exit command from cutting the sound off.
func TestPlayAndWaitBlocksUntilPlaybackEnds(t *testing.T) {
	p := New(WithFS(testFS(t)))
	out := newFakeOutput()
	p.openOutput = out.asOption()

	done := make(chan error, 1)
	go func() { done <- p.PlayAndWait("tone") }()

	select {
	case err := <-done:
		t.Fatalf("PlayAndWait returned before playback ended (err %v)", err)
	case <-time.After(100 * time.Millisecond):
	}

	// Unblocking the wait stands in for the sound finishing.
	out.silence()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("PlayAndWait: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PlayAndWait did not return after playback was silenced")
	}

	if out.plays != 1 {
		t.Errorf("plays = %d, want 1", out.plays)
	}
}

// TestPlayAndWaitReportsAnUnknownSound checks waiting playback resolves
// the name before it opens the audio output, like Play does.
func TestPlayAndWaitReportsAnUnknownSound(t *testing.T) {
	p := New(WithFS(testFS(t)))
	out := newFakeOutput()
	p.openOutput = out.asOption()

	err := p.PlayAndWait("nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("PlayAndWait error = %v, want ErrNotFound", err)
	}
	if out.opened != 0 {
		t.Error("the audio output was opened for a sound that does not exist")
	}
}

// TestDurationReportsHowLongASoundPlaysFor checks a caller can ask how
// long a sound runs, which is what a display or a wait needs.
func TestDurationReportsHowLongASoundPlaysFor(t *testing.T) {
	p := New(WithFS(testFS(t)))

	dur, err := p.Duration("tone")
	if err != nil {
		t.Fatalf("Duration: %v", err)
	}
	// testFS holds a 100 ms sound.
	if dur < 90*time.Millisecond || dur > 110*time.Millisecond {
		t.Errorf("Duration = %s, want about 100ms", dur)
	}

	if _, err := p.Duration("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Duration error = %v, want ErrNotFound", err)
	}
}

// TestDurationOfShippedSounds checks every shipped sound is long enough
// to be heard across an arena and short enough not to step on the match.
func TestDurationOfShippedSounds(t *testing.T) {
	t.Setenv(EnvDir, "")

	p := New()
	for _, name := range embeddedSounds {
		dur, err := p.Duration(name)
		if err != nil {
			t.Errorf("Duration(%q): %v", name, err)
			continue
		}
		if dur <= 0 {
			t.Errorf("Duration(%q) = %s, want a positive length", name, dur)
		}
		if dur > time.Minute {
			t.Errorf("Duration(%q) = %s, want a short announcement", name, dur)
		}
	}
}
