package live

import (
	"encoding/binary"
	"errors"
)

// Binary frames (knowledge design K25) carry a document's bytes on the same
// socket as the JSON frames: kind (1 byte), topic length (2 bytes, big
// endian), the topic (UTF-8, the L8 grammar), and the payload, the rest.
//
// Client to server:
//
//	KindUpdate    a Yjs update, at most MaxDocUpdate
//	KindSeed      the article version it was built from (8 bytes, big endian), then a whole document, at most MaxDocState
//	KindAwareness y-protocols' awareness update, at most MaxDocAwareness
//	KindCompact   upTo (8 bytes, big endian) then a whole document, at most MaxDocState + 8
//
// Server to client: KindUpdate carries seq (8 bytes, big endian) then the
// update; KindAwareness carries the update as received.
const (
	KindUpdate    byte = 0x01
	KindSeed      byte = 0x02
	KindAwareness byte = 0x03
	KindCompact   byte = 0x04
)

// The per-kind size limits of a payload (K25, K27).
const (
	MaxDocUpdate    = 256 << 10
	MaxDocState     = 2 << 20
	MaxDocAwareness = 4 << 10
)

const (
	// maxTopicBytes is the longest topic a binary frame names (K25).
	maxTopicBytes = 80
	// binaryHeader is the kind and the topic length.
	binaryHeader = 3
	// maxBinaryFrame is the socket's read limit (K25): the largest payload
	// plus the largest header. A text frame is held to maxClientFrame by the
	// hub, not by the read limit.
	maxBinaryFrame = 4<<20 + binaryHeader + maxTopicBytes
)

var errMalformedFrame = errors.New("live: malformed binary frame")

// isBinaryFrame says whether an outgoing frame is binary: every JSON frame
// starts with '{', every binary kind is below 0x20.
func isBinaryFrame(frame []byte) bool { return len(frame) > 0 && frame[0] < 0x20 }

// appendBinary encodes a binary frame.
func appendBinary(kind byte, topic string, parts ...[]byte) []byte {
	n := binaryHeader + len(topic)
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	out = append(out, kind, byte(len(topic)>>8), byte(len(topic)))
	out = append(out, topic...)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// decodeBinary splits a binary frame; a header that does not fit, a topic
// over the limit or outside the grammar is errMalformedFrame.
func decodeBinary(b []byte) (kind byte, topic string, payload []byte, err error) {
	if len(b) < binaryHeader {
		return 0, "", nil, errMalformedFrame
	}
	n := int(binary.BigEndian.Uint16(b[1:3]))
	if n == 0 || n > maxTopicBytes || len(b) < binaryHeader+n {
		return 0, "", nil, errMalformedFrame
	}
	topic = string(b[binaryHeader : binaryHeader+n])
	if !ValidTopic(topic) {
		return 0, "", nil, errMalformedFrame
	}
	switch b[0] {
	case KindUpdate, KindSeed, KindAwareness, KindCompact:
	default:
		return 0, "", nil, errMalformedFrame
	}
	return b[0], topic, b[binaryHeader+n:], nil
}

// frameDocUpdate is the server's update frame: seq, then the update.
func frameDocUpdate(topic string, seq int64, data []byte) []byte {
	var s [8]byte
	binary.BigEndian.PutUint64(s[:], uint64(seq))
	return appendBinary(KindUpdate, topic, s[:], data)
}

// frameDocAwareness is the server's awareness frame.
func frameDocAwareness(topic string, data []byte) []byte {
	return appendBinary(KindAwareness, topic, data)
}
