package sfx

import (
	"embed"
	"io/fs"
)

// sounds holds the checked-in sound library.
//
//go:embed all:sounds
var sounds embed.FS

// libraryFS is the sound library compiled into the binary, rooted at the
// sound files themselves so that callers address them as
// "start-of-match.wav" rather than "sounds/start-of-match.wav".
var libraryFS fs.FS = mustSub("sounds")

func mustSub(dir string) fs.FS {
	sub, err := fs.Sub(sounds, dir)
	if err != nil {
		panic("sfx: embedded sound library is missing: " + err.Error())
	}
	return sub
}
