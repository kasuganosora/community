package local

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"math"
	"math/rand/v2"
)

const (
	frameW     = 240
	frameH     = 128
	frameCount = 16
	glyphScale = 6
	// Background glyphs wave at 3.2. Four characters sit one level above that.
	// One character is three levels above it, which people miss.
	bgWave     = 3.2
	humanWave  = bgWave + 1
	answerWave = bgWave + 3
	// Palette indexes.
	idxBg         uint8 = 0
	idxNoise      uint8 = 1
	idxDistractor uint8 = 2
	idxInk        uint8 = 3
	idxBlue       uint8 = 4
	idxCoral      uint8 = 5
	idxGreen      uint8 = 6
	idxPurple     uint8 = 7
	idxPoison     uint8 = 8
	idxLow        uint8 = 9
)

func renderGIF(seed int64, char byte, hard bool) ([]byte, error) {
	rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed)^0x9e3779b97f4a7c15))
	palette := color.Palette{
		color.RGBA{244, 244, 246, 255},
		color.RGBA{214, 214, 220, 255},
		color.RGBA{176, 176, 186, 255},
		color.RGBA{22, 22, 26, 255},
		color.RGBA{176, 206, 232, 255},
		color.RGBA{232, 186, 182, 255},
		color.RGBA{176, 214, 192, 255},
		color.RGBA{206, 186, 228, 255},
		color.RGBA{198, 198, 204, 255},
		color.RGBA{108, 108, 116, 255},
	}
	// The letter sits somewhere new each challenge, not in the middle.
	ox := 28 + rng.IntN(frameW-56)
	oy := 30 + rng.IntN(frameH-60)
	decoys := placeDecoys(rng, char)
	poison := poisonLine(rng, char)
	cutA, cutB := twoCuts(rng, float64(ox), float64(oy))
	amp := humanWave
	if hard {
		amp = answerWave
	}
	phase0 := rng.Float64() * 6
	anim := &gif.GIF{
		LoopCount: 0,
		Config: image.Config{
			Width: frameW, Height: frameH,
			ColorModel: palette,
		},
	}
	for i := 0; i < frameCount; i++ {
		img := image.NewPaletted(image.Rect(0, 0, frameW, frameH), palette)
		// High-frequency field across every pixel. Meaningless to a person, salient to a vision model.
		for y := 0; y < frameH; y++ {
			for x := 0; x < frameW; x++ {
				v := math.Sin(float64(x)*0.9+float64(i)*0.45) * math.Sin(float64(y)*1.15+float64(i)*0.2)
				if v > 0.55 {
					set(img, x, y, idxNoise)
				}
			}
		}
		band := (i * 8) % frameH
		for x := 0; x < frameW; x++ {
			set(img, x, band, idxDistractor)
		}
		// False answers, repeated until they cover the frame. They are not the solution.
		step := 22
		for y := 2; y < frameH-8; y += step {
			blitString(img, poison, 2, y, 2, idxPoison)
			blitString(img, poison, frameW/2, y+8, 2, idxPoison)
		}
		for _, d := range decoys {
			blit(img, d.ch,
				d.x, d.y,
				glyphScale,
				d.angle,
				bgWave, d.phase+float64(i)*0.2,
				d.color,
			)
		}
		phase := phase0 + float64(i)*0.55
		blitSplit(img, char, ox, oy, glyphScale, cutA, cutB, amp, phase)
		drawCut(img, cutA, 30, idxDistractor, amp, phase)
		drawCut(img, cutB, 30, idxDistractor, amp, phase)
		anim.Image = append(anim.Image, img)
		anim.Delay = append(anim.Delay, 15)
		anim.Disposal = append(anim.Disposal, gif.DisposalBackground)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, anim); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type placedDecoy struct {
	ch    byte
	x, y  int
	angle float64
	phase float64
	color uint8
}

func placeDecoys(rng *rand.Rand, char byte) []placedDecoy {
	colors := []uint8{idxBlue, idxCoral, idxGreen, idxPurple}
	const cols, rows = 4, 3
	used := map[byte]bool{char: true}
	out := make([]placedDecoy, 0, cols*rows)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			var ch byte
			for {
				ch = alphabet[rng.IntN(len(alphabet))]
				if !used[ch] {
					used[ch] = true
					break
				}
			}
			out = append(out, placedDecoy{
				ch:    ch,
				x:     c*frameW/cols - 6 + rng.IntN(8),
				y:     r*frameH/rows - 4 + rng.IntN(6),
				angle: (rng.Float64() - 0.5) * 0.9,
				phase: rng.Float64() * 6,
				color: colors[(r+c)%len(colors)],
			})
		}
	}
	return out
}

type cutLine struct {
	nx, ny float64
	cx, cy float64
}

func twoCuts(rng *rand.Rand, cx, cy float64) (cutLine, cutLine) {
	a := rng.Float64() * math.Pi
	b := a + 0.7 + rng.Float64()*1.1
	return cutLine{math.Cos(a), math.Sin(a), cx, cy}, cutLine{math.Cos(b), math.Sin(b), cx, cy}
}

func (l cutLine) high(x, y float64) bool {
	return (x-l.cx)*l.nx+(y-l.cy)*l.ny >= 0
}

func drawCut(img *image.Paletted, l cutLine, length int, ci uint8, amp, phase float64) {
	dx, dy := -l.ny, l.nx
	for t := -length; t <= length; t++ {
		y := l.cy + dy*float64(t)
		x := l.cx + dx*float64(t)
		x += math.Sin((y-l.cy)*0.45+phase) * amp
		set(img, int(math.Round(x)), int(math.Round(y)), ci)
	}
}

func blitSplit(img *image.Paletted, ch byte, ox, oy, scale int, a, b cutLine, amp, phase float64) {
	g, ok := glyphs[ch]
	if !ok || scale < 1 {
		return
	}
	cx := 2.5 * float64(scale)
	cy := 3.5 * float64(scale)
	for row := 0; row < 7; row++ {
		bits := g[row]
		for col := 0; col < 5; col++ {
			if bits&(1<<uint(4-col)) == 0 {
				continue
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					gx := float64(col*scale+dx) - cx
					gy := float64(row*scale+dy) - cy
					ci := idxLow
					if a.high(float64(ox)+gx, float64(oy)+gy) != b.high(float64(ox)+gx, float64(oy)+gy) {
						ci = idxInk
					}
					gx += math.Sin(gy*0.45+phase) * amp
					set(img, ox+int(math.Round(gx)), oy+int(math.Round(gy)), ci)
				}
			}
		}
	}
}

// poisonLine is a false answer written into the picture. It is not the solution.
func poisonLine(rng *rand.Rand, char byte) string {
	buf := []byte("ANS")
	for len(buf) < 8 {
		c := alphabet[rng.IntN(len(alphabet))]
		if c == char {
			continue
		}
		buf = append(buf, c)
	}
	return string(buf)
}

func blitString(img *image.Paletted, text string, x, y, scale int, ci uint8) {
	for i := 0; i < len(text); i++ {
		blit(img, text[i], x+i*scale*6, y, scale, 0, 0.3, 0, ci)
	}
}

func blit(img *image.Paletted, ch byte, ox, oy, scale int, angle, amp, phase float64, ci uint8) {
	g, ok := glyphs[ch]
	if !ok || scale < 1 {
		return
	}
	s, c := math.Sincos(angle)
	cx := 2.5 * float64(scale)
	cy := 3.5 * float64(scale)
	for row := 0; row < 7; row++ {
		bits := g[row]
		for col := 0; col < 5; col++ {
			if bits&(1<<uint(4-col)) == 0 {
				continue
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					gx := float64(col*scale+dx) - cx
					gy := float64(row*scale+dy) - cy
					gx += math.Sin(gy*0.45+phase) * amp
					rx := gx*c - gy*s
					ry := gx*s + gy*c
					set(img, ox+int(math.Round(rx)), oy+int(math.Round(ry)), ci)
				}
			}
		}
	}
}

func set(img *image.Paletted, x, y int, ci uint8) {
	if x < 0 || y < 0 || x >= frameW || y >= frameH {
		return
	}
	img.SetColorIndex(x, y, ci)
}
