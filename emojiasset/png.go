package emojiasset

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/draw"
	"image/png"
)

type pngFrame struct {
	x, y, w, h int
	blend      byte
	parts      [][]byte
}

func pngChunk(kind string, data []byte) []byte {
	b := make([]byte, len(data)+12)
	binary.BigEndian.PutUint32(b, uint32(len(data)))
	copy(b[4:], kind)
	copy(b[8:], data)
	binary.BigEndian.PutUint32(b[len(b)-4:], crc32.ChecksumIEEE(b[4:len(b)-4]))
	return b
}

// APNG's frame streams use the standard PNG decoder. Validate and decode every
// bounded frame, preserving original animation bytes while rendering its actual
// first frame (which can differ from the PNG default image).
func decodePNG(data []byte, width, height int) (image.Image, error) {
	var header []byte
	var shared [][]byte
	var frames []pngFrame
	declared, pixels, sequence := uint32(0), uint64(0), uint32(0)
	ended, seenIDAT := false, false
	for offset := 8; offset < len(data); {
		if len(data)-offset < 12 {
			return nil, errors.New("truncated PNG chunk")
		}
		length := uint64(binary.BigEndian.Uint32(data[offset:]))
		if length > uint64(len(data)-offset-12) {
			return nil, errors.New("truncated PNG chunk")
		}
		end := offset + int(length) + 12
		raw := data[offset:end]
		kind := string(raw[4:8])
		chunk := raw[8 : len(raw)-4]
		if ended || crc32.ChecksumIEEE(raw[4:len(raw)-4]) != binary.BigEndian.Uint32(raw[len(raw)-4:]) {
			return nil, errors.New("invalid PNG chunk checksum or trailer")
		}
		switch kind {
		case "IHDR":
			if len(chunk) != 13 || header != nil {
				return nil, errors.New("invalid PNG header")
			}
			header = append([]byte(nil), chunk...)
		case "PLTE", "tRNS":
			shared = append(shared, raw)
		case "acTL":
			if declared != 0 || seenIDAT || len(chunk) != 8 {
				return nil, errors.New("invalid APNG animation header")
			}
			declared = binary.BigEndian.Uint32(chunk)
			if declared == 0 || declared > maxFrames {
				return nil, errors.New("APNG frame count exceeds limit")
			}
		case "fcTL":
			if declared == 0 || len(chunk) != 26 || binary.BigEndian.Uint32(chunk) != sequence {
				return nil, errors.New("invalid APNG frame sequence")
			}
			sequence++
			w, h, x, y := binary.BigEndian.Uint32(chunk[4:]), binary.BigEndian.Uint32(chunk[8:]), binary.BigEndian.Uint32(chunk[12:]), binary.BigEndian.Uint32(chunk[16:])
			pixels += uint64(w) * uint64(h)
			if w == 0 || h == 0 || w > MaxDimension || h > MaxDimension || uint64(x)+uint64(w) > uint64(width) || uint64(y)+uint64(h) > uint64(height) || len(frames) >= maxFrames || pixels > maxAnimationPixels || chunk[24] > 2 || chunk[25] > 1 {
				return nil, errors.New("APNG animation exceeds rectangle or frame budget")
			}
			frames = append(frames, pngFrame{x: int(x), y: int(y), w: int(w), h: int(h), blend: chunk[25]})
		case "IDAT":
			seenIDAT = true
			if len(frames) > 0 {
				if len(frames) != 1 {
					return nil, errors.New("invalid APNG default frame order")
				}
				frames[0].parts = append(frames[0].parts, chunk)
			}
		case "fdAT":
			if len(frames) == 0 || len(chunk) < 4 || binary.BigEndian.Uint32(chunk) != sequence {
				return nil, errors.New("invalid APNG frame data sequence")
			}
			sequence++
			i := len(frames) - 1
			frames[i].parts = append(frames[i].parts, chunk[4:])
		case "IEND":
			ended = true
		}
		offset = end
	}
	if !ended || header == nil {
		return nil, errors.New("incomplete PNG")
	}
	fallback, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if declared == 0 {
		return fallback, nil
	}
	if len(frames) != int(declared) {
		return nil, errors.New("APNG frame count mismatch")
	}
	var preview *image.NRGBA
	for index, frame := range frames {
		if len(frame.parts) == 0 {
			return nil, errors.New("APNG frame has no pixels")
		}
		frameHeader := append([]byte(nil), header...)
		binary.BigEndian.PutUint32(frameHeader, uint32(frame.w))
		binary.BigEndian.PutUint32(frameHeader[4:], uint32(frame.h))
		wire := append([]byte("\x89PNG\r\n\x1a\n"), pngChunk("IHDR", frameHeader)...)
		for _, chunk := range shared {
			wire = append(wire, chunk...)
		}
		for _, part := range frame.parts {
			wire = append(wire, pngChunk("IDAT", part)...)
		}
		wire = append(wire, pngChunk("IEND", nil)...)
		decoded, err := png.Decode(bytes.NewReader(wire))
		if err != nil {
			return nil, err
		}
		if index == 0 {
			preview = image.NewNRGBA(image.Rect(0, 0, width, height))
			op := draw.Src
			if frame.blend == 1 {
				op = draw.Over
			}
			draw.Draw(preview, image.Rect(frame.x, frame.y, frame.x+frame.w, frame.y+frame.h), decoded, decoded.Bounds().Min, op)
		}
	}
	return preview, nil
}
