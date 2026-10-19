package sfx

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/speaker"
)

const (
	// outputSampleRate is the sample rate the audio output is opened
	// at.  Sounds are resampled to it on playback, since the library
	// mixes sample rates (the whistles are 11 kHz, the voice
	// recording 44.1 kHz).
	outputSampleRate = beep.SampleRate(48000)

	// outputBufferDuration is how much audio the output buffers ahead
	// of playback.  Latency barely matters for an arena announcement;
	// a comfortable buffer keeps playback glitch-free while the web
	// server is busy.
	outputBufferDuration = 250 * time.Millisecond

	// resampleQuality is the quality used when a sound's sample rate
	// differs from the output's.  Six is "very good" per the beep
	// documentation, and the sounds are short enough that the cost is
	// irrelevant.
	resampleQuality = 6

	// openTimeout bounds how long playing waits for the audio device to
	// answer.  A sound server that is configured but wedged can hold a
	// device open indefinitely, and a stuck sound card must not be able
	// to stall whoever asked for the sound.
	openTimeout = 5 * time.Second
)

var (
	// errOutputClosed is returned once the audio output has been
	// released.  The audio library cannot reopen the device in the same
	// process, so asking for it again is a use-after-close, not a
	// transient failure.
	errOutputClosed = errors.New("sfx: audio output is closed")

	// errOpenTimeout is returned when the audio device has not answered
	// within openTimeout.  Unlike a device that reported it is unusable,
	// this is not remembered: the next sound tries again.
	errOpenTimeout = errors.New("sfx: audio device did not answer in time")
)

// audioSink hands out the process-wide audio output.
//
// The audio library owns one device per process and refuses to open it
// twice, so every Player in the process shares it through here.
var audioSink = &speakerSink{
	initFn: func() error {
		return speaker.Init(outputSampleRate, outputSampleRate.N(outputBufferDuration))
	},
	closeFn: speaker.Close,
	timeout: openTimeout,
}

// speakerSink opens the audio device once and hands out the result.
//
// A device that reports it is unusable (no sound card, no sound server)
// is remembered, so a headless machine neither retries the hardware on
// every sound nor stays quiet without a word in the log.  A device that
// merely failed to answer in time is tried again for the next sound.
type speakerSink struct {
	initFn  func() error
	closeFn func()
	timeout time.Duration

	mu sync.Mutex
	// ready is non-nil once an opening attempt has started, and is
	// closed when that attempt produces its result.
	ready chan struct{}
	// err is the outcome of the opening attempt.  It is written once,
	// before ready is closed, so anyone who has observed ready closing
	// may read it.
	err    error
	closed bool
}

// get returns something to play sound through, opening the device on
// first use.
func (s *speakerSink) get() (output, error) {
	s.mu.Lock()
	switch {
	case s.closed:
		s.mu.Unlock()
		return nil, errOutputClosed
	case s.ready == nil:
		s.ready = make(chan struct{})
		go s.open()
	}
	ready := s.ready
	s.mu.Unlock()

	timer := time.NewTimer(s.timeout)
	defer timer.Stop()

	select {
	case <-ready:
		if s.err != nil {
			return nil, s.err
		}
		return speakerOutput{sink: s}, nil
	case <-timer.C:
		return nil, fmt.Errorf("%w: %s", errOpenTimeout, s.timeout)
	}
}

// open opens the device and records the outcome for everyone waiting.
func (s *speakerSink) open() {
	s.err = s.initFn()
	if s.err != nil {
		slog.Warn("Sound effects are disabled", "error", s.err)
	}
	close(s.ready)
}

// close shuts the audio device down.
//
// A device whose opening is still outstanding is left to the process
// exit: closing means shutdown, and waiting on it here could hang the
// shutdown for as long as the device is wedged.
func (s *speakerSink) close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}
	s.closed = true
	if s.ready == nil {
		return
	}
	select {
	case <-s.ready:
		if s.err == nil {
			s.closeFn()
		}
	default:
	}
}

// speakerOutput plays sounds through the machine's audio hardware.
type speakerOutput struct {
	sink *speakerSink
}

// Play starts s playing, cutting off whatever was playing before it.
func (o speakerOutput) Play(s beep.Streamer) {
	// The speaker mixes together everything it is given, so the
	// previous sound has to be dropped explicitly to keep one sound
	// audible at a time.
	speaker.Clear()
	speaker.Play(s)
}

// PlayAndWait starts s playing, cutting off whatever was playing
// before it, and blocks until the sound has finished reaching the
// driver.
func (o speakerOutput) PlayAndWait(s beep.Streamer) {
	speaker.Clear()
	speaker.PlayAndWait(s)
}

// Stop discards whatever is playing.
func (speakerOutput) Stop() {
	speaker.Clear()
}

// Close shuts the audio device down.  It is process-wide: the audio
// library owns one device for the whole program, so this stops every
// player in the process, which is what a shutdown wants.
func (o speakerOutput) Close() error {
	o.sink.close()
	return nil
}
