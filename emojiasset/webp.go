package emojiasset

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
)

func little24(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 }
func put24(b []byte, n int) { b[0] = byte(n); b[1] = byte(n >> 8); b[2] = byte(n >> 16) }

// WebP's existing decoder supports the VP8/VP8L frame codecs. Split ANMF only
// after checking all lengths/rectangles; decode each frame using that decoder.
// This avoids invoking a general-purpose multimedia/network subprocess.
func decodeWebP(data []byte, width, height int) (image.Image, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" || uint64(binary.LittleEndian.Uint32(data[4:]))+8 != uint64(len(data)) {
		return nil, errors.New("invalid WebP container length")
	}
	animated := false
	seenAnim := false
	frames, pixels := 0, 0
	var preview *image.NRGBA
	var background color.NRGBA
	for offset := 12; offset < len(data); {
		if len(data)-offset < 8 {
			return nil, errors.New("truncated WebP chunk")
		}
		length := uint64(binary.LittleEndian.Uint32(data[offset+4:]))
		padded := length + (length & 1)
		if padded > uint64(len(data)-offset-8) {
			return nil, errors.New("truncated WebP chunk")
		}
		chunk := data[offset+8 : offset+8+int(length)]
		kind := string(data[offset : offset+4])
		offset += 8 + int(padded)
		switch kind {
		case "VP8X":
			if len(chunk) != 10 {
				return nil, errors.New("invalid WebP extended header")
			}
			animated = chunk[0]&2 != 0
		case "ANIM":
			if !animated || seenAnim || len(chunk) != 6 {
				return nil, errors.New("invalid WebP animation header")
			}
			seenAnim = true
			background = color.NRGBA{R: chunk[2], G: chunk[1], B: chunk[0], A: chunk[3]}
		case "ANMF":
			if !animated || !seenAnim || len(chunk) < 16 {
				return nil, errors.New("invalid WebP animation frame")
			}
			x, y, w, h := little24(chunk)*2, little24(chunk[3:])*2, little24(chunk[6:])+1, little24(chunk[9:])+1
			frames++
			pixels += w * h
			if w > MaxDimension || h > MaxDimension || x+w > width || y+h > height || frames > maxFrames || pixels > maxAnimationPixels || chunk[15]&0xfc != 0 {
				return nil, errors.New("WebP animation exceeds rectangle or frame budget")
			}
			frame, err := decodeWebPFrame(chunk[16:], w, h)
			if err != nil {
				return nil, err
			}
			if preview == nil {
				preview = image.NewNRGBA(image.Rect(0, 0, width, height))
				draw.Draw(preview, preview.Bounds(), image.NewUniform(background), image.Point{}, draw.Src)
				op := draw.Over
				if chunk[15]&2 != 0 {
					op = draw.Src
				}
				draw.Draw(preview, image.Rect(x, y, x+w, y+h), frame, frame.Bounds().Min, op)
			}
		}
	}
	if animated {
		if preview == nil {
			return nil, errors.New("WebP animation has no frames")
		}
		return preview, nil
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	return decoded, err
}

func decodeWebPFrame(payload []byte, width, height int) (image.Image, error) {
	alpha, images := false, 0
	for offset := 0; offset < len(payload); {
		if len(payload)-offset < 8 {
			return nil, errors.New("truncated WebP frame chunk")
		}
		length := uint64(binary.LittleEndian.Uint32(payload[offset+4:]))
		padded := length + (length & 1)
		if padded > uint64(len(payload)-offset-8) {
			return nil, errors.New("truncated WebP frame")
		}
		switch string(payload[offset : offset+4]) {
		case "ALPH":
			if alpha || images != 0 {
				return nil, errors.New("invalid WebP alpha order")
			}
			alpha = true
		case "VP8 ", "VP8L":
			images++
		default:
			return nil, errors.New("unsupported WebP animation frame chunk")
		}
		offset += 8 + int(padded)
	}
	if images != 1 {
		return nil, errors.New("WebP frame must have one image")
	}
	header := make([]byte, 30)
	copy(header, "RIFF")
	binary.LittleEndian.PutUint32(header[4:], uint32(22+len(payload)))
	copy(header[8:], "WEBPVP8X")
	binary.LittleEndian.PutUint32(header[16:], 10)
	if alpha {
		header[20] = 16
	}
	put24(header[24:], width-1)
	put24(header[27:], height-1)
	wire := append(header, payload...)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(wire))
	if err != nil {
		return nil, fmt.Errorf("invalid WebP frame: %w", err)
	}
	if cfg.Width != width || cfg.Height != height {
		return nil, errors.New("WebP frame dimensions mismatch")
	}
	frame, _, err := image.Decode(bytes.NewReader(wire))
	if err != nil {
		return nil, fmt.Errorf("invalid WebP frame: %w", err)
	}
	return frame, nil
}
