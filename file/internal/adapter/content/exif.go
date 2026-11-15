package content

import (
	"encoding/binary"
)

const (
	orientationUnset       = 0
	orientationTopLeft     = 1
	orientationTopRight    = 2
	orientationBottomRight = 3
	orientationBottomLeft  = 4
	orientationLeftTop     = 5
	orientationRightTop    = 6
	orientationRightBottom = 7
	orientationLeftBottom  = 8
)

const (
	jpegMarkerPrefix   = 0xFF
	jpegStartOfImage   = 0xD8
	jpegApp1           = 0xE1
	jpegStartOfScan    = 0xDA
	exifOrientationTag = 0x0112
	exifShortType      = 3
)

func exifOrientation(body []byte) int {
	segment, ok := findExifSegment(body)
	if !ok {
		return orientationUnset
	}

	if len(segment) < 8 {
		return orientationUnset
	}

	var order binary.ByteOrder

	switch {
	case segment[0] == 'I' && segment[1] == 'I':
		order = binary.LittleEndian
	case segment[0] == 'M' && segment[1] == 'M':
		order = binary.BigEndian
	default:
		return orientationUnset
	}

	if order.Uint16(segment[2:4]) != 42 {
		return orientationUnset
	}

	offset := int(order.Uint32(segment[4:8]))
	if offset < 8 || offset+2 > len(segment) {
		return orientationUnset
	}

	count := int(order.Uint16(segment[offset : offset+2]))
	entries := segment[offset+2:]

	for i := range count {
		start := i * 12
		if start+12 > len(entries) {
			return orientationUnset
		}

		entry := entries[start : start+12]

		if order.Uint16(entry[0:2]) != exifOrientationTag {
			continue
		}

		if order.Uint16(entry[2:4]) != exifShortType {
			return orientationUnset
		}

		value := int(order.Uint16(entry[8:10]))
		if value < orientationTopLeft || value > orientationLeftBottom {
			return orientationUnset
		}

		return value
	}

	return orientationUnset
}

func findExifSegment(body []byte) ([]byte, bool) {
	if len(body) < 4 || body[0] != jpegMarkerPrefix || body[1] != jpegStartOfImage {
		return nil, false
	}

	for cursor := 2; cursor+4 <= len(body); {
		if body[cursor] != jpegMarkerPrefix {
			return nil, false
		}

		marker := body[cursor+1]
		if marker == jpegStartOfScan {
			return nil, false
		}

		length := int(binary.BigEndian.Uint16(body[cursor+2 : cursor+4]))
		if length < 2 || cursor+2+length > len(body) {
			return nil, false
		}

		payload := body[cursor+4 : cursor+2+length]

		if marker == jpegApp1 && len(payload) > 6 &&
			string(payload[:6]) == "Exif\x00\x00" {
			return payload[6:], true
		}

		cursor += 2 + length
	}

	return nil, false
}
