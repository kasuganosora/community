package local

import (
	"bytes"
	"context"
	"image"
	"image/gif"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderGIFKeepsTheCharacterReadable(t *testing.T) {
	raw, err := renderGIF(42, 'A', false)
	require.NoError(t, err)
	anim, err := gif.DecodeAll(bytes.NewReader(raw))
	require.NoError(t, err)
	require.Len(t, anim.Image, frameCount)

	withInk := 0
	for _, img := range anim.Image {
		if countIndex(img, idxInk) > 15 && countIndex(img, idxLow) > 15 {
			withInk++
		}
		assert.Equal(t, 15, anim.Delay[0])
	}
	assert.Equal(t, frameCount, withInk, "the character stays on every frame so a person can read it")
}

func TestBlackMaskIsOnlyPartOfTheLetter(t *testing.T) {
	raw, err := renderGIF(11, 'R', false)
	require.NoError(t, err)
	anim, err := gif.DecodeAll(bytes.NewReader(raw))
	require.NoError(t, err)
	dark, light := 0, 0
	img := anim.Image[4]
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			switch img.ColorIndexAt(x, y) {
			case idxInk:
				dark++
			case idxLow:
				light++
			}
		}
	}
	assert.Greater(t, dark, 30, "the high-contrast piece has to remain after the gray is dropped")
	assert.Greater(t, light, 30, "the rest of the letter stays light enough to vanish in a contrast crush")
}

func TestLetterMovesAroundAndWaves(t *testing.T) {
	spot := func(seed int64) (int, int) {
		raw, err := renderGIF(seed, 'R', false)
		require.NoError(t, err)
		anim, err := gif.DecodeAll(bytes.NewReader(raw))
		require.NoError(t, err)
		cx, cy, n := inkCentroid(anim.Image[0])
		require.Greater(t, n, 20)
		return int(cx), int(cy)
	}
	ax, ay := spot(3)
	bx, by := spot(9)
	assert.True(t, ax != bx || ay != by, "the letter should not always sit in the same place")

	raw, err := renderGIF(5, 'K', false)
	require.NoError(t, err)
	anim, err := gif.DecodeAll(bytes.NewReader(raw))
	require.NoError(t, err)
	assert.NotEqual(t, inkSig(anim.Image[0]), inkSig(anim.Image[8]), "the wave should move the letter while it plays")
}

func inkSig(img *image.Paletted) string {
	sig := make([]byte, 0, 64)
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			c := img.ColorIndexAt(x, y)
			if c == idxInk || c == idxLow {
				sig = append(sig, byte(x), byte(y), c)
			}
		}
	}
	return string(sig)
}

func TestInkStaysPutWhileTheClipPlays(t *testing.T) {
	raw, err := renderGIF(42, 'A', false)
	require.NoError(t, err)
	anim, err := gif.DecodeAll(bytes.NewReader(raw))
	require.NoError(t, err)

	var cx0, cy0 float64
	for i, img := range anim.Image {
		cx, cy, n := inkCentroid(img)
		require.Greater(t, n, 20)
		if i == 0 {
			cx0, cy0 = cx, cy
			continue
		}
		assert.InDelta(t, cx0, cx, 14, "the glyph should not jump sideways")
		assert.InDelta(t, cy0, cy, 14, "the glyph should not jump up and down")
	}
}

func inkCentroid(img *image.Paletted) (cx, cy float64, n int) {
	var sx, sy int
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			if img.ColorIndexAt(x, y) != idxInk && img.ColorIndexAt(x, y) != idxLow {
				continue
			}
			sx += x
			sy += y
			n++
		}
	}
	if n == 0 {
		return 0, 0, 0
	}
	return float64(sx) / float64(n), float64(sy) / float64(n), n
}

func TestNormalizeFoldsFullWidth(t *testing.T) {
	assert.Equal(t, "AB3K92", normalize(" ａｂ３ｋ　９２ "))
	assert.Equal(t, "K7M2P32", normalize("Ｋ７Ｍ２Ｐ３２"))
}

func TestPuzzleIsFiveCharacters(t *testing.T) {
	seen := map[int]struct{}{}
	for seed := int64(1); seed <= 40; seed++ {
		glyphs, trap := puzzleFromSeed(seed)
		assert.Len(t, glyphs, Total)
		assert.GreaterOrEqual(t, trap, 0)
		assert.Less(t, trap, Total)
		seen[trap] = struct{}{}
		for i := 0; i < len(glyphs); i++ {
			assert.Contains(t, alphabet, string(glyphs[i]))
		}
	}
	assert.Greater(t, len(seen), 1, "the unreadable character has to move")
}

func TestFrameDelayLeavesTimeToRead(t *testing.T) {
	for i := 0; i < 12; i++ {
		d := frameDelay()
		assert.GreaterOrEqual(t, d, 3*time.Second)
		assert.LessOrEqual(t, d, 5*time.Second)
	}
}

func TestSweepStillMoves(t *testing.T) {
	raw, err := renderGIF(3, 'K', false)
	require.NoError(t, err)
	anim, err := gif.DecodeAll(bytes.NewReader(raw))
	require.NoError(t, err)
	rowOf := func(img *image.Paletted) int {
		best, bestN := 0, 0
		for y := 0; y < img.Rect.Dy(); y++ {
			n := 0
			for x := 0; x < img.Rect.Dx(); x++ {
				if img.ColorIndexAt(x, y) == idxDistractor {
					n++
				}
			}
			if n > bestN {
				best, bestN = y, n
			}
		}
		return best
	}
	assert.NotEqual(t, rowOf(anim.Image[0]), rowOf(anim.Image[8]))
}

func TestPatternFillsTheFrame(t *testing.T) {
	raw, err := renderGIF(9, 'M', false)
	require.NoError(t, err)
	anim, err := gif.DecodeAll(bytes.NewReader(raw))
	require.NoError(t, err)
	img := anim.Image[4]
	regions := []image.Rectangle{
		image.Rect(0, 0, frameW, frameH/5),
		image.Rect(0, frameH*4/5, frameW, frameH),
		image.Rect(0, 0, frameW/6, frameH),
		image.Rect(frameW*5/6, 0, frameW, frameH),
	}
	for _, r := range regions {
		n := 0
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				if img.ColorIndexAt(x, y) != idxBg {
					n++
				}
			}
		}
		assert.Greater(t, n, 30, "the field has to reach every edge, not sit in the middle")
	}
}

func TestAnswerOverlapsTheLightLayer(t *testing.T) {
	raw, err := renderGIF(9, 'M', false)
	require.NoError(t, err)
	anim, err := gif.DecodeAll(bytes.NewReader(raw))
	require.NoError(t, err)
	inkMin, inkMax := frameH, 0
	colorMin, colorMax := frameH, 0
	colors := map[uint8]int{}
	poison := 0
	for _, img := range anim.Image {
		for y := 0; y < img.Rect.Dy(); y++ {
			for x := 0; x < img.Rect.Dx(); x++ {
				switch img.ColorIndexAt(x, y) {
				case idxInk:
					if y < inkMin {
						inkMin = y
					}
					if y > inkMax {
						inkMax = y
					}
				case idxBlue, idxCoral, idxGreen, idxPurple:
					colors[img.ColorIndexAt(x, y)]++
					if y < colorMin {
						colorMin = y
					}
					if y > colorMax {
						colorMax = y
					}
				case idxPoison:
					poison++
				}
			}
		}
	}
	assert.GreaterOrEqual(t, len(colors), 3)
	assert.Greater(t, poison, 40, "a false instruction is written into the picture")
	overlap := inkMin <= colorMax && colorMin <= inkMax
	assert.True(t, overlap, "the dark glyph has to sit on top of the light letters, not in a separate band")
}

func TestFramesAreSpacedAndOneShotCannotSkipAhead(t *testing.T) {
	ctx := context.Background()
	mem := NewMemoryStore()
	current := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	mem.now = func() time.Time { return current }
	origDelay := frameDelay
	t.Cleanup(func() { timeNow = time.Now; frameDelay = origDelay })
	timeNow = func() time.Time { return current }
	frameDelay = func() time.Duration { return 2 * time.Second }

	started, err := start(ctx, mem, "203.0.113.8")
	require.NoError(t, err)

	first, err := frame(ctx, mem, started.ID, started.Bind)
	require.NoError(t, err)
	assert.NotEmpty(t, first.GIF)
	assert.Equal(t, 1, first.Index)
	assert.False(t, first.Done)

	second, err := frame(ctx, mem, started.ID, started.Bind)
	require.NoError(t, err)
	assert.Empty(t, second.GIF)
	assert.GreaterOrEqual(t, second.Wait, time.Second)
	assert.Equal(t, 1, second.Index)

	_, err = verify(ctx, mem, started.ID, started.Bind, "AAAAA0", "", "00")
	assert.ErrorIs(t, err, ErrEarly)

	current = current.Add(2 * time.Second)
	second, err = frame(ctx, mem, started.ID, started.Bind)
	require.NoError(t, err)
	assert.NotEmpty(t, second.GIF)
	assert.Equal(t, 2, second.Index)
	assert.NotEqual(t, first.GIF, second.GIF)

	_, err = frame(ctx, mem, started.ID, "someone-else")
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestAnswerIsSingleUse(t *testing.T) {
	ctx := context.Background()
	mem := NewMemoryStore()
	current := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	mem.now = func() time.Time { return current }
	origDelay := frameDelay
	t.Cleanup(func() { timeNow = time.Now; frameDelay = origDelay; UseStore(nil) })
	timeNow = func() time.Time { return current }
	frameDelay = func() time.Duration { return 0 }

	started, err := start(ctx, mem, "203.0.113.9")
	require.NoError(t, err)
	raw, err := mem.Get(ctx, chKey(started.ID))
	require.NoError(t, err)
	ch, err := decode(raw, started.Bind)
	require.NoError(t, err)
	glyphs, trap := puzzleFromSeed(ch.Seed)
	nonce := solvePow(t, ch.PowSalt)

	for i := 0; i < Total; i++ {
		got, err := frame(ctx, mem, started.ID, started.Bind)
		require.NoError(t, err)
		require.NotEmpty(t, got.GIF)
	}
	_, err = verify(ctx, mem, started.ID, started.Bind, "nope", "", nonce)
	assert.ErrorIs(t, err, ErrWrong)
	bad := "00"
	if powOK(ch.PowSalt, bad, powBits) {
		bad = "ff"
	}
	_, err = verify(ctx, mem, started.ID, started.Bind, glyphs, "", bad)
	assert.ErrorIs(t, err, ErrPow)

	_, err = verify(ctx, mem, started.ID, started.Bind, glyphs, "", nonce)
	assert.ErrorIs(t, err, ErrWrong, "reading the unreadable character is the bot tell")

	human := []byte(glyphs)
	if human[trap] == 'A' {
		human[trap] = 'B'
	} else {
		human[trap] = 'A'
	}
	token, err := verify(ctx, mem, started.ID, started.Bind, string(human), "", nonce)
	require.NoError(t, err)
	assert.Contains(t, token, "local.")

	UseStore(mem)
	t.Cleanup(func() { UseStore(nil) })
	ok, err := ConsumeToken(ctx, token)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = ConsumeToken(ctx, token)
	require.NoError(t, err)
	assert.False(t, ok)
}

func countIndex(img *image.Paletted, idx uint8) int {
	n := 0
	for _, p := range img.Pix {
		if p == idx {
			n++
		}
	}
	return n
}

func solvePow(t *testing.T, salt string) string {
	t.Helper()
	for i := 0; i < 1<<18; i++ {
		nonce := strconv.FormatInt(int64(i), 16)
		if powOK(salt, nonce, powBits) {
			return nonce
		}
	}
	t.Fatal("proof of work not found")
	return ""
}
