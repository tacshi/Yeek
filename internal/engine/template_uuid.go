package engine

import (
	"crypto/md5" // #nosec G501 -- UUID version 3 is defined with MD5 (RFC 9562).
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- UUID version 5 is defined with SHA-1 (RFC 9562).
	"fmt"
	"hash"
	"strings"
	"time"
	"uuid"
)

// gregorianOffset is the 100-nanosecond intervals from 1582-10-15 to 1970-01-01.
const gregorianOffset = 0x01B21DD213814000

// timeUUID is a UUID of version 1 or 6 at t, with a random clock sequence
// and a random node, as the uuid package Yaak uses makes them.
func timeUUID(version int, t time.Time) string {
	var u [16]byte
	_, _ = rand.Read(u[8:])
	ticks := uint64(t.UnixNano()/100) + gregorianOffset // #nosec G115 -- times after 1970.
	switch version {
	case 1:
		low, mid, high := uint32(ticks), uint16(ticks>>32), uint16(ticks>>48)&0x0fff // #nosec G115 -- the fields take those bits.
		u[0], u[1], u[2], u[3] = byte(low>>24), byte(low>>16), byte(low>>8), byte(low)
		u[4], u[5] = byte(mid>>8), byte(mid)
		u[6], u[7] = byte(high>>8)|0x10, byte(high)
	case 6:
		high, mid, low := uint32(ticks>>28), uint16(ticks>>12), uint16(ticks)&0x0fff // #nosec G115 -- the fields take those bits.
		u[0], u[1], u[2], u[3] = byte(high>>24), byte(high>>16), byte(high>>8), byte(high)
		u[4], u[5] = byte(mid>>8), byte(mid)
		u[6], u[7] = byte(low>>8)|0x60, byte(low)
	}
	u[8] = u[8]&0x3f | 0x80
	u[10] |= 0x01 // a random node is marked multicast
	return format(u)
}

// nameUUID is a UUID of version 3 (MD5) or 5 (SHA-1) for name in namespace.
func nameUUID(version int, name, namespace string) (string, error) {
	ns, err := uuid.Parse(strings.TrimSpace(namespace))
	if err != nil {
		return "", fmt.Errorf("namespace must be a valid UUID: %w", err)
	}
	var h hash.Hash
	if version == 3 {
		h = md5.New() // #nosec G401 -- required by UUID version 3.
	} else {
		h = sha1.New() // #nosec G401 -- required by UUID version 5.
	}
	h.Write(ns[:])
	h.Write([]byte(name))
	var u [16]byte
	copy(u[:], h.Sum(nil))
	u[6] = u[6]&0x0f | byte(version<<4)
	u[8] = u[8]&0x3f | 0x80
	return format(u), nil
}

func format(u [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

// uuidTimestamp is uuid.v6's optional timestamp, as JavaScript's Date
// parses the usual forms; anything else is now.
func uuidTimestamp(text string) time.Time {
	text = strings.TrimSpace(text)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02", time.RFC1123, time.RFC1123Z} {
		if t, err := time.Parse(layout, text); err == nil {
			return t
		}
	}
	return time.Now()
}
