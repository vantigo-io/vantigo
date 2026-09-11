package identity_test

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

const avatarPath = "/api/v1/identity/account/avatar"

// testPNG and testJPEG encode a tiny image with the standard library's own
// encoders, per the brief: no binary fixture is committed.
func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	return buf.Bytes()
}

func testJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("encode JPEG: %v", err)
	}
	return buf.Bytes()
}

// avatarUpload builds a multipart/form-data body with one part named
// "avatar", the contract's only documented field, declared as contentType.
func avatarUpload(t *testing.T, contentType string, data []byte) (ct string, body []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	writeAvatarPart(t, w, contentType, data)
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return w.FormDataContentType(), buf.Bytes()
}

// avatarUploadWithFiller is avatarUpload with a large "filler" field ahead
// of "avatar", so the whole request — not just the image — crosses
// maxAvatarRequestBytes even though the avatar part alone would not.
func avatarUploadWithFiller(t *testing.T, fillerBytes int, contentType string, data []byte) (ct string, body []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	filler, err := w.CreateFormField("filler")
	if err != nil {
		t.Fatalf("create filler field: %v", err)
	}
	if _, err := filler.Write(make([]byte, fillerBytes)); err != nil {
		t.Fatalf("write filler field: %v", err)
	}
	writeAvatarPart(t, w, contentType, data)
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return w.FormDataContentType(), buf.Bytes()
}

func writeAvatarPart(t *testing.T, w *multipart.Writer, contentType string, data []byte) {
	t.Helper()
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="avatar"; filename="avatar"`)
	header.Set("Content-Type", contentType)
	part, err := w.CreatePart(header)
	if err != nil {
		t.Fatalf("create avatar part: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write avatar part: %v", err)
	}
}

// Ported from IdentityAccountEndpointsTests.Avatar_IsPrivateValidatedAndDeletedWithoutCrossUserAccess
// (the self-service half: a PNG upload, its headers, a JPEG replace, and
// delete. The cross-user and owner-avatar-read assertions in the .NET test
// belong to Task 11's owner endpoints).
func TestAccountAvatar_UploadPNGAndJPEGServeAndDelete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "avatar@example.test"
	h.seedUser(t, email, userPassword, identity.RoleUserID)
	c := h.login(t, email, userPassword)

	png := testPNG(t, 4, 4)
	ct, body := avatarUpload(t, "image/png", png)
	put := c.do(http.MethodPut, avatarPath, nil, rawBody(ct, body))
	var uploaded struct {
		Uploaded bool   `json:"uploaded"`
		Url      string `json:"url"`
	}
	put.json(&uploaded)
	if put.status != http.StatusOK || !uploaded.Uploaded || uploaded.Url != avatarPath {
		t.Fatalf("PUT %s: status %d body %s", avatarPath, put.status, put.body)
	}

	if acc := getAccount(t, c); acc.AvatarUrl == nil || *acc.AvatarUrl != avatarPath {
		t.Errorf("account avatarUrl = %v, want %q", acc.AvatarUrl, avatarPath)
	}

	get := c.do(http.MethodGet, avatarPath, nil)
	if get.status != http.StatusOK || !bytes.Equal(get.body, png) {
		t.Fatalf("GET %s: status %d, %d bytes, want the uploaded PNG (%d bytes)", avatarPath, get.status, len(get.body), len(png))
	}
	if ctype := get.header("Content-Type"); ctype != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ctype)
	}
	if cc := get.header("Cache-Control"); !strings.Contains(strings.ToLower(cc), "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if xcto := get.header("X-Content-Type-Options"); xcto != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", xcto)
	}
	if cd := get.header("Content-Disposition"); cd != "inline" {
		t.Errorf("Content-Disposition = %q, want inline", cd)
	}

	// POST replaces it with a JPEG (both methods share UploadAvatar).
	jpg := testJPEG(t, 4, 4)
	ct, body = avatarUpload(t, "image/jpeg", jpg)
	post := c.do(http.MethodPost, avatarPath, nil, rawBody(ct, body))
	if post.status != http.StatusOK {
		t.Fatalf("POST %s: status %d body %s", avatarPath, post.status, post.body)
	}
	replaced := c.do(http.MethodGet, avatarPath, nil)
	if replaced.status != http.StatusOK || replaced.header("Content-Type") != "image/jpeg" || !bytes.Equal(replaced.body, jpg) {
		t.Fatalf("GET %s after replace: status %d content-type %q", avatarPath, replaced.status, replaced.header("Content-Type"))
	}

	del := c.do(http.MethodDelete, avatarPath, nil)
	if del.status != http.StatusNoContent {
		t.Fatalf("DELETE %s: status %d", avatarPath, del.status)
	}
	if gone := c.do(http.MethodGet, avatarPath, nil); gone.status != http.StatusNotFound {
		t.Errorf("GET %s after delete: status %d, want 404", avatarPath, gone.status)
	}
	if acc := getAccount(t, c); acc.AvatarUrl != nil {
		t.Errorf("account avatarUrl = %v after delete, want nil", acc.AvatarUrl)
	}
}

// Ported from IdentityAccountEndpointsTests.Avatar_IsPrivateValidatedAndDeletedWithoutCrossUserAccess
// (its malformed-image and wrong-content-type cases), plus a declared/
// sniffed mismatch and the per-image size bound the .NET test also covers.
func TestAccountAvatar_RejectsMalformedMismatchedAndOversizedUploads(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "reject@example.test"
	h.seedUser(t, email, userPassword, identity.RoleUserID)
	c := h.login(t, email, userPassword)

	cases := []struct {
		name        string
		contentType string
		data        []byte
	}{
		{"malformed image data", "image/png", []byte("not-an-image")},
		{"an unsupported type entirely", "image/svg+xml", []byte("<svg xmlns='http://www.w3.org/2000/svg'></svg>")},
		{"declared type disagrees with the sniffed one", "image/jpeg", testPNG(t, 4, 4)},
		{"an image over the 5 MB bound", "image/png", make([]byte, (5*1024*1024)+1)},
	}
	for _, c2 := range cases {
		ct, body := avatarUpload(t, c2.contentType, c2.data)
		if r := c.do(http.MethodPut, avatarPath, nil, rawBody(ct, body)); r.status != http.StatusBadRequest || r.code() != "invalid_avatar" {
			t.Errorf("%s: status %d code %q, want 400 invalid_avatar", c2.name, r.status, r.code())
		}
	}

	// None of the rejected uploads left an avatar behind.
	if r := c.do(http.MethodGet, avatarPath, nil); r.status != http.StatusNotFound {
		t.Errorf("GET %s after every rejection: status %d, want 404", avatarPath, r.status)
	}
}

// TestAccountAvatar_RequestOverTheOuterCapIsRejected proves the router's
// avatar body cap (avatarBodyLimits; decision: the contract documents no
// 413 for these operations, so an oversized request answers the same 400
// invalid_avatar an oversized or malformed image gets): a request whose
// total body crosses maxAvatarRequestBytes (5 MiB + 64 KiB) is refused
// even though the avatar part itself, read alone, would pass.
func TestAccountAvatar_RequestOverTheOuterCapIsRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "outercap@example.test"
	h.seedUser(t, email, userPassword, identity.RoleUserID)
	c := h.login(t, email, userPassword)

	const fillerBytes = 5*1024*1024 + 64*1024 + 4096 // past the 5 MiB + 64 KiB request cap
	ct, body := avatarUploadWithFiller(t, fillerBytes, "image/png", testPNG(t, 4, 4))

	if r := c.do(http.MethodPut, avatarPath, nil, rawBody(ct, body)); r.status != http.StatusBadRequest || r.code() != "invalid_avatar" {
		t.Errorf("status %d code %q, want 400 invalid_avatar", r.status, r.code())
	}
}

// TestAccountAvatar_UploadsPastTheDefaultBodyCapAreAccepted proves the
// avatar operations' BodyLimits override replaces the router's 1 MiB
// default (module.DefaultMaxBodyBytes) rather than sitting behind it: an
// upload of 2 MiB, well inside maxAvatarRequestBytes, is stored through
// both POST and PUT.
func TestAccountAvatar_UploadsPastTheDefaultBodyCapAreAccepted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "largeupload@example.test"
	h.seedUser(t, email, userPassword, identity.RoleUserID)
	c := h.login(t, email, userPassword)

	const fillerBytes = 2 * 1024 * 1024 // past the 1 MiB default, inside the avatar cap
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		ct, body := avatarUploadWithFiller(t, fillerBytes, "image/png", testPNG(t, 4, 4))
		r := c.do(method, avatarPath, nil, rawBody(ct, body))
		var got struct {
			Uploaded bool `json:"uploaded"`
		}
		r.json(&got)
		if r.status != http.StatusOK || !got.Uploaded {
			t.Errorf("%s %d-byte upload: status %d body %s, want 200 uploaded", method, len(body), r.status, r.body)
		}
	}
}

// TestAccountAvatar_ANonMultipartBodyIsAnInvalidRequest: a body that is not
// multipart/form-data never reaches the avatar checks; the generated
// wrapper cannot build a multipart reader, and the decode failure answers
// 400 invalid_request, on POST and PUT alike. The request's content type is
// deliberately not the documented one, so the exchange skips validation.
func TestAccountAvatar_ANonMultipartBodyIsAnInvalidRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "notmultipart@example.test"
	h.seedUser(t, email, userPassword, identity.RoleUserID)
	c := h.login(t, email, userPassword)

	for _, method := range []string{http.MethodPost, http.MethodPut} {
		r := c.do(method, avatarPath, nil, rawBody("application/json", []byte(`{"avatar":"not a file"}`)),
			skipContract("the request is deliberately not the documented multipart/form-data"))
		if r.status != http.StatusBadRequest || r.code() != "invalid_request" {
			t.Errorf("%s a JSON body: status %d code %q, want 400 invalid_request", method, r.status, r.code())
		}
	}
	if r := c.do(http.MethodGet, avatarPath, nil); r.status != http.StatusNotFound {
		t.Errorf("GET %s after the refusals: status %d, want 404", avatarPath, r.status)
	}
}

// TestAccountAvatar_APartWithoutAContentTypeIsInvalid: the "avatar" part
// must declare the type its magic bytes have; a part that declares none —
// here a genuine PNG — is refused 400 invalid_avatar, as a wrongly
// declared one is.
func TestAccountAvatar_APartWithoutAContentTypeIsInvalid(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "nocontenttype@example.test"
	h.seedUser(t, email, userPassword, identity.RoleUserID)
	c := h.login(t, email, userPassword)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreatePart(textproto.MIMEHeader{"Content-Disposition": {`form-data; name="avatar"; filename="avatar.png"`}})
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(testPNG(t, 4, 4)); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	if r := c.do(http.MethodPut, avatarPath, nil, rawBody(w.FormDataContentType(), buf.Bytes())); r.status != http.StatusBadRequest || r.code() != "invalid_avatar" {
		t.Errorf("a part without a Content-Type: status %d code %q, want 400 invalid_avatar", r.status, r.code())
	}
	if r := c.do(http.MethodGet, avatarPath, nil); r.status != http.StatusNotFound {
		t.Errorf("GET %s after the refusal: status %d, want 404", avatarPath, r.status)
	}
}
