package identity_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// An avatar's metadata is stripped before it is stored (imagemeta.Strip):
// Staff serves the avatar to the person's colleagues, so neither the user's
// own upload nor Staff's managed one keeps where and when it was taken.

// withExif is jpeg with an APP1 Exif segment right after its SOI, carrying a
// GPS-like marker string.
func withExif(jpeg []byte) []byte {
	payload := append([]byte("Exif\x00\x00"), []byte("GPS 59.91N 10.75E")...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	seg = append(seg, payload...)
	out := append([]byte{}, jpeg[:2]...)
	out = append(out, seg...)
	return append(out, jpeg[2:]...)
}

func TestAccountAvatar_AnUploadsExifIsStripped(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "exif@example.test"
	h.seedUser(t, email, userPassword, identity.RoleUserID)
	c := h.login(t, email, userPassword)

	plain := testJPEG(t, 4, 4)
	tagged := withExif(plain)
	ct, body := avatarUpload(t, "image/jpeg", tagged)
	if put := c.do(http.MethodPut, avatarPath, nil, rawBody(ct, body)); put.status != http.StatusOK {
		t.Fatalf("PUT %s: status %d body %s", avatarPath, put.status, put.body)
	}
	get := c.do(http.MethodGet, avatarPath, nil)
	if get.status != http.StatusOK || bytes.Contains(get.body, []byte("Exif")) || !bytes.Equal(get.body, plain) {
		t.Errorf("GET %s: status %d, %d bytes (Exif kept %v); want the JPEG without its APP1 segment",
			avatarPath, get.status, len(get.body), bytes.Contains(get.body, []byte("Exif")))
	}
}

func TestManagedAvatar_ItsExifIsStripped(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := h.insertUser(t, "managed-exif@example.test", identity.RoleUserID)
	plain := testJPEG(t, 4, 4)
	if err := h.accounts(t).SetManagedAvatar(context.Background(), systemActor, id, staffPhoto("image/jpeg", withExif(plain))); err != nil {
		t.Fatalf("SetManagedAvatar: %v", err)
	}
	if a := mustAvatar(t, h, id); a == nil || !bytes.Equal(a.Data, plain) {
		t.Errorf("the managed avatar = %+v, want the JPEG without its APP1 segment", a)
	}
}
