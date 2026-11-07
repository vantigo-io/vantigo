package communications_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is a product in a message (Messenger design N16-N18, delivery C):
// a reply with cards made from a product's images marked for Communications,
// the copy it makes before its transaction, the public media route Meta fetches
// the images from, the carousel the outbox sends, and a Buy answer resolved to
// its product. The product catalog is a fake with images a test marks.

// fakeCatalog is the product catalog's images: ProductImages answers those of a
// product marked for a use, OpenProductImage streams one by id. Every other
// method of contracts.ProductCatalog is the embedded nil interface's: a call
// would panic, and the module reads nothing else.
type fakeCatalog struct {
	contracts.ProductCatalog
	mu     sync.Mutex
	images map[int32]fakeCatalogImage
}

type fakeCatalogImage struct {
	image   contracts.ProductImage
	content []byte
}

func newFakeCatalog() *fakeCatalog { return &fakeCatalog{images: map[int32]fakeCatalogImage{}} }

// add puts an image of a product in the catalog, marked for communications or
// not, and answers its id.
func (c *fakeCatalog) add(id, product int32, order int32, communications bool, content string) int32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	uses := []string{"marketing"}
	if communications {
		uses = []string{"communications"}
	}
	c.images[id] = fakeCatalogImage{
		image:   contracts.ProductImage{ID: id, ProductID: product, ContentType: "image/jpeg", Width: 800, Height: 600, Uses: uses, SortOrder: order},
		content: []byte(content),
	}
	return id
}

// setContentType changes the media type the catalog says an image has.
func (c *fakeCatalog) setContentType(id int32, contentType string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	i := c.images[id]
	i.image.ContentType = contentType
	c.images[id] = i
}

// remove deletes an image: the product's images change after the message.
func (c *fakeCatalog) remove(id int32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.images, id)
}

func (c *fakeCatalog) ProductImages(_ context.Context, product int32, use string) ([]contracts.ProductImage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []contracts.ProductImage
	for _, i := range c.images {
		if i.image.ProductID != product {
			continue
		}
		for _, u := range i.image.Uses {
			if u == use {
				out = append(out, i.image)
			}
		}
	}
	return out, nil
}

func (c *fakeCatalog) OpenProductImage(_ context.Context, id int32) (io.ReadCloser, contracts.ProductImage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	i, ok := c.images[id]
	if !ok {
		return nil, contracts.ProductImage{}, nil
	}
	return io.NopCloser(bytes.NewReader(i.content)), i.image, nil
}

const cardsPublicBase = "https://pay.example.com"

// newCardChat is a chat whose provider fetches card images from the public host,
// beside a catalog with product 7's three images — two marked for
// communications, one for marketing only — and product 8's one.
func newCardChat(t *testing.T, opts ...modtest.Option) (chatFixture, *fakeCatalog) {
	t.Helper()
	catalog := newFakeCatalog()
	catalog.add(71, 7, 0, true, "IMG71")
	catalog.add(72, 7, 1, true, "IMG72")
	catalog.add(73, 7, 2, false, "IMG73")
	catalog.add(81, 8, 0, true, "IMG81")
	f := newChat(t, append([]modtest.Option{
		modtest.WithProducts(catalog), modtest.WithEnv("PUBLIC_BASE_URL", cardsPublicBase),
	}, opts...)...)
	caps := f.fake.Capabilities()
	caps.PublicMedia = true
	f.fake.SetCapabilities(caps)
	return f, catalog
}

// card is one card of a request.
func card(title string, image int32, choices ...map[string]any) map[string]any {
	c := map[string]any{"title": title, "productImageId": image}
	if len(choices) > 0 {
		c["choices"] = choices
	}
	return c
}

func buy() map[string]any { return map[string]any{"id": "buy", "title": "Buy"} }

func cardsOf(cards ...map[string]any) map[string]any {
	return map[string]any{"cards": numbered(cards)}
}

// numbered gives each card's choices ids of their own, as the composer does: buy-<card index> for the buy
// choice and <id>-<card index> for any other, since a choice id is unique within its message.
func numbered(cards []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(cards))
	for i, c := range cards {
		cc := map[string]any{}
		for k, v := range c {
			cc[k] = v
		}
		if choices, ok := c["choices"].([]map[string]any); ok {
			renamed := make([]map[string]any, 0, len(choices))
			for _, ch := range choices {
				id := fmt.Sprintf("%v-%d", ch["id"], i)
				renamed = append(renamed, map[string]any{"id": id, "title": ch["title"]})
			}
			cc["choices"] = renamed
		}
		out = append(out, cc)
	}
	return out
}

// cardAttachments is a message's card attachments, newest last.
func (f chatFixture) cardAttachments(t *testing.T, conversation string, message uuid.UUID) []string {
	t.Helper()
	var out []string
	for _, m := range f.detail(t, conversation).Messages {
		if m.Id != message.String() {
			continue
		}
		for _, a := range m.Attachments {
			if a.Role == "card" {
				out = append(out, a.Id)
			}
		}
	}
	return out
}

// cleanupRecords is how many card images are in the cleanup ledger in status.
func (f chatFixture) cleanupRecords(t *testing.T, status string) int {
	t.Helper()
	return f.h.Count(t, `SELECT count(*) FROM communications.attachment_cleanup_records WHERE storage_key LIKE 'cards/%' AND status = $1`, status)
}

// TestReplyCards_ACarouselOfProductImages: a reply with three cards of a
// product's images is queued; each image is copied into Communications' storage
// as a card attachment, the cards in the thread answer their attachment, and the
// message keeps the product for a Buy answer.
func TestReplyCards_ACarouselOfProductImages(t *testing.T) {
	t.Parallel()
	f, catalog := newCardChat(t)
	catalog.add(74, 7, 3, true, "IMG74")
	conv := f.chat(t)

	message := f.mustReply(t, conv, map[string]any{"textBody": "Have a look", "components": map[string]any{
		"cards": numbered([]map[string]any{
			card("Chair", 71, buy()), card("Chair, side", 72, buy(), map[string]any{"id": "more", "title": "Tell me more"}), card("Chair, back", 74),
		}),
		"answerOptions": []map[string]any{{"id": "later", "title": "Later"}},
	}})

	cards := f.cardAttachments(t, conv, message)
	if len(cards) != 3 {
		t.Fatalf("card attachments = %d, want 3", len(cards))
	}
	var got *chatMessageJSON
	d := f.detail(t, conv)
	for i := range d.Messages {
		if d.Messages[i].Id == message.String() {
			got = &d.Messages[i]
		}
	}
	if got == nil || got.Components == nil || got.Components.Cards == nil || len(*got.Components.Cards) != 3 {
		t.Fatalf("components = %+v, want three cards", got)
	}
	for i, c := range *got.Components.Cards {
		if c.AttachmentId == nil || *c.AttachmentId != cards[i] {
			t.Errorf("card %d attachment = %v, want %s", i, c.AttachmentId, cards[i])
		}
	}
	if got.Components.AnswerOptions == nil || len(*got.Components.AnswerOptions) != 1 {
		t.Errorf("components = %+v, want the answer option beside the cards", got.Components)
	}
	if product := modtest.One[int32](t, f.h, `SELECT (channel_metadata_json->>'productId')::int FROM communications.conversation_messages WHERE id = $1`, message); product != 7 {
		t.Errorf("product id kept on the message = %d, want 7", product)
	}
	// The bytes are Communications' own: the download answers them.
	r := f.c.Do(http.MethodGet, "/api/v1/communications/attachments/"+cards[0]+"/download", nil)
	if r.Status != http.StatusOK || string(r.Body) != "IMG71" {
		t.Errorf("card download = %d %q, want 200 and the image", r.Status, r.Body)
	}
	if n := f.cleanupRecords(t, "owned"); n != 3 {
		t.Errorf("owned cleanup records of card images = %d, want 3", n)
	}
}

// TestReplyCards_TenCardsAreTaken: the carousel of the design holds 10 cards; an
// 11th is refused with the limits' 422.
func TestReplyCards_TenCardsAreTaken(t *testing.T) {
	t.Parallel()
	f, catalog := newCardChat(t)
	conv := f.chat(t)
	var cards []map[string]any
	for i := range 11 {
		catalog.add(int32(100+i), 7, int32(10+i), true, fmt.Sprintf("IMG%d", i))
		cards = append(cards, card(fmt.Sprintf("Card %d", i), int32(100+i), buy()))
	}
	r := f.reply(conv, uuid.NewString(), map[string]any{"textBody": "All of them", "components": map[string]any{"cards": numbered(cards)}})
	wantCode(t, r, http.StatusUnprocessableEntity, "components_invalid")
	if n := f.cleanupRecords(t, "pending") + f.cleanupRecords(t, "staged") + f.cleanupRecords(t, "owned"); n != 0 {
		t.Errorf("card objects after a refused request = %d, want none: it was refused before any copy", n)
	}
	id := f.mustReply(t, conv, map[string]any{"textBody": "Ten of them", "components": map[string]any{"cards": numbered(cards[:10])}})
	if got := len(f.cardAttachments(t, conv, id)); got != 10 {
		t.Errorf("card attachments = %d, want 10", got)
	}
}

// TestReplyCards_Refusals: an image not marked for Communications, one that is
// gone, images of two products, a card with no image and an instance with no
// public host are refused, each with its code.
func TestReplyCards_Refusals(t *testing.T) {
	t.Parallel()
	f, catalog := newCardChat(t)
	catalog.add(82, 8, 1, true, "<svg/>")
	catalog.setContentType(82, "image/svg+xml")
	catalog.add(83, 8, 2, true, "GIF")
	catalog.setContentType(83, "image/gif")
	conv := f.chat(t)
	cases := []struct {
		name, code string
		components map[string]any
		field      string
	}{
		{"marked for marketing only", "product_image_unavailable", cardsOf(card("Chair", 73, buy())), ""},
		{"an SVG", "product_image_unavailable", cardsOf(card("Lamp", 82, buy())), ""},
		{"a GIF", "product_image_unavailable", cardsOf(card("Lamp", 83, buy())), ""},
		{"no such image", "product_image_unavailable", cardsOf(card("Chair", 999, buy())), ""},
		{"the second image is not marked", "product_image_unavailable", cardsOf(card("Chair", 71), card("Chair", 73)), ""},
		{"two products", "components_invalid", cardsOf(card("Chair", 71), card("Lamp", 81)), "cards[1].productImageId"},
		{"the same choice id on two cards", "components_invalid", map[string]any{"cards": []map[string]any{
			card("Chair", 71, buy()), card("Chair", 72, buy())}}, "cards[1].choices[0].id"},
		{"no image", "components_invalid", cardsOf(map[string]any{"title": "Chair"}), "cards[0].productImageId"},
		{"four choices", "components_invalid", cardsOf(card("Chair", 71, buy(),
			map[string]any{"id": "a", "title": "A"}, map[string]any{"id": "b", "title": "B"}, map[string]any{"id": "c", "title": "C"})), "cards[0].choices"},
		{"an 81-character subtitle", "components_invalid", cardsOf(map[string]any{"title": "Chair", "productImageId": 71, "subtitle": strings.Repeat("a", 81)}), "cards[0].subtitle"},
	}
	for _, tc := range cases {
		r := f.reply(conv, uuid.NewString(), map[string]any{"textBody": "Hi", "components": tc.components})
		if r.Status != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d body %s, want 422", tc.name, r.Status, r.Body)
			continue
		}
		var got commErrorJSON
		r.JSON(&got)
		if got.Error.Code != tc.code {
			t.Errorf("%s: code = %q, want %q", tc.name, got.Error.Code, tc.code)
		}
		if tc.field != "" && len(got.field(tc.field)) == 0 {
			t.Errorf("%s: fields = %v, want %q named", tc.name, got.Error.Fields, tc.field)
		}
	}
	// What was copied before a refusal is queued for deletion, none of it owned.
	if n := f.cleanupRecords(t, "owned"); n != 0 {
		t.Errorf("owned card objects after refusals = %d, want none", n)
	}
	if got := f.h.Count(t, `SELECT count(*) FROM communications.conversation_messages WHERE direction = 'outbound'`); got != 0 {
		t.Errorf("outbound messages after refusals = %d, want none", got)
	}
}

// TestReplyCards_NoPublicHost: a provider that fetches card images from the
// public host cannot be sent a carousel by an instance that has none.
func TestReplyCards_NoPublicHost(t *testing.T) {
	t.Parallel()
	catalog := newFakeCatalog()
	catalog.add(71, 7, 0, true, "IMG71")
	f := newChat(t, modtest.WithProducts(catalog))
	caps := f.fake.Capabilities()
	caps.PublicMedia = true
	f.fake.SetCapabilities(caps)
	conv := f.chat(t)
	r := f.reply(conv, uuid.NewString(), map[string]any{"textBody": "Hi", "components": cardsOf(card("Chair", 71, buy()))})
	wantCode(t, r, http.StatusUnprocessableEntity, "public_url_missing")

	// A provider that is handed the bytes needs no public host.
	caps.PublicMedia = false
	f.fake.SetCapabilities(caps)
	f.mustReply(t, conv, map[string]any{"textBody": "Hi", "components": cardsOf(card("Chair", 71, buy()))})
}

// TestReplyCards_ARefusalAfterTheCopyQueuesTheObjects: a reply refused after its
// images were copied — here a staged attachment that is not ready — leaves no
// owned object: the copies are queued for deletion.
func TestReplyCards_ARefusalAfterTheCopyQueuesTheObjects(t *testing.T) {
	t.Parallel()
	f, _ := newCardChat(t)
	conv := f.chat(t)
	r := f.reply(conv, uuid.NewString(), map[string]any{
		"textBody": "Hi", "attachmentIds": []string{uuid.NewString()}, "components": cardsOf(card("Chair", 71, buy()), card("Chair", 72, buy())),
	})
	wantCode(t, r, http.StatusConflict, "attachments_not_ready")
	if owned, pending := f.cleanupRecords(t, "owned"), f.cleanupRecords(t, "pending"); owned != 0 || pending != 2 {
		t.Errorf("card objects: owned %d, pending deletion %d, want none owned and both queued", owned, pending)
	}
}

// TestReplyCards_TheFingerprintIncludesTheImages: a replay of a key with another
// product image is the existing mismatch refusal, the same request replays.
func TestReplyCards_TheFingerprintIncludesTheImages(t *testing.T) {
	t.Parallel()
	f, _ := newCardChat(t)
	conv := f.chat(t)
	key := uuid.NewString()
	body := map[string]any{"textBody": "Hi", "components": cardsOf(card("Chair", 71, buy()))}
	if r := f.reply(conv, key, body); r.Status != http.StatusCreated {
		t.Fatalf("first reply: %d %s", r.Status, r.Body)
	}
	if r := f.reply(conv, key, body); r.Status != http.StatusOK {
		t.Errorf("same request again: status %d body %s, want 200", r.Status, r.Body)
	}
	other := map[string]any{"textBody": "Hi", "components": cardsOf(card("Chair", 72, buy()))}
	wantCode(t, f.reply(conv, key, other), http.StatusConflict, "idempotency_key_reused")
	if n := f.cleanupRecords(t, "owned"); n != 1 {
		t.Errorf("owned card images = %d, want the one reply's", n)
	}
}

// TestReplyCards_TheMessageKeepsWhatWasSent: a product's image deleted or
// unmarked after the reply leaves the message's card as it was: the copy is
// Communications' own.
func TestReplyCards_TheMessageKeepsWhatWasSent(t *testing.T) {
	t.Parallel()
	f, catalog := newCardChat(t)
	conv := f.chat(t)
	message := f.mustReply(t, conv, map[string]any{"textBody": "Hi", "components": cardsOf(card("Chair", 71, buy()))})
	catalog.remove(71)
	cards := f.cardAttachments(t, conv, message)
	if len(cards) != 1 {
		t.Fatalf("card attachments = %d, want 1", len(cards))
	}
	if r := f.c.Do(http.MethodGet, "/api/v1/communications/attachments/"+cards[0]+"/download", nil); r.Status != http.StatusOK || string(r.Body) != "IMG71" {
		t.Errorf("download after the product's image was deleted = %d %q, want the image", r.Status, r.Body)
	}
	// And the outbox still sends the carousel.
	f.drainOutbox(t, f.outbox())
	if sent := f.fake.Sent(); len(sent) != 2 || len(sent[1].Part.Cards) != 1 {
		t.Errorf("sent = %+v, want the text and the carousel", sent)
	}
}

// TestOutboxCards_ACarouselIsSentWithPublicImageAddresses: for a provider that
// fetches from the public host the cards carry an address on it with a token that
// holds for the lifetime, and not the bytes.
func TestOutboxCards_ACarouselIsSentWithPublicImageAddresses(t *testing.T) {
	t.Parallel()
	f, _ := newCardChat(t)
	conv := f.chat(t)
	message := f.mustReply(t, conv, map[string]any{"textBody": "Hi", "components": cardsOf(card("Chair", 71, buy()), card("Chair, side", 72))})
	f.drainOutbox(t, f.outbox())
	sent := f.fake.Sent()
	if len(sent) != 2 {
		t.Fatalf("parts = %d, want the text and the carousel", len(sent))
	}
	cards := f.cardAttachments(t, conv, message)
	carousel := sent[1].Part.Cards
	if len(carousel) != 2 {
		t.Fatalf("carousel = %+v, want two cards", carousel)
	}
	for i, c := range carousel {
		if c.Image != nil || !strings.HasPrefix(c.ImageURL, cardsPublicBase+"/api/v1/communications/public-media/"+cards[i]+".") {
			t.Errorf("card %d = %+v, want an address for attachment %s and no bytes", i, c, cards[i])
		}
		r := f.h.Client(t).Do(http.MethodGet, strings.TrimPrefix(c.ImageURL, cardsPublicBase), nil)
		if r.Status != http.StatusOK {
			t.Errorf("card %d: fetching its own address = %d, want 200", i, r.Status)
		}
	}
	if len(carousel[0].Choices) != 1 || carousel[0].Choices[0].ID != "buy-0" {
		t.Errorf("first card choices = %+v, want buy", carousel[0].Choices)
	}
}

// TestPublicMedia_TheRoute: a valid token serves the image with the headers of
// N18, HEAD answers the same without the body, and every other token is the bare
// 404: expired, a wrong MAC, another attachment, a file attachment, an inbound
// attachment and a provider that does not ask for public media.
func TestPublicMedia_TheRoute(t *testing.T) {
	t.Parallel()
	f, _ := newCardChat(t)
	conv := f.chat(t)
	// An inbound attachment: the participant sent a picture.
	f.fake.AddFile("pic", "pic.png", "image/png", []byte("INBOUND"))
	f.fake.Push(f.channel.Address, contracts.MessengerEvent{
		Kind: contracts.MessengerEventMessage, Participant: "4242", At: f.h.Now().Add(time.Second), PlatformMessageID: "pic-1",
		Attachments: []contracts.MessengerAttachmentRef{{Ref: "pic", FileName: "pic.png", ContentType: "image/png"}},
	})
	f.sync(t)
	file := f.stage(t, conv, "file.png", []byte("FILE"))
	message := f.mustReply(t, conv, map[string]any{"textBody": "Hi", "attachmentIds": []string{file}, "components": cardsOf(card("Chair", 71, buy()))})
	card := f.cardAttachments(t, conv, message)[0]

	var inbound, fileAttachment string
	for _, m := range f.detail(t, conv).Messages {
		for _, a := range m.Attachments {
			switch {
			case m.Direction == "inbound":
				inbound = a.Id
			case a.Role == "file":
				fileAttachment = a.Id
			}
		}
	}
	if inbound == "" || fileAttachment == "" {
		t.Fatalf("fixtures: inbound %q, file %q", inbound, fileAttachment)
	}

	box := f.h.Deps().Secrets
	token := func(id string, exp time.Time) string {
		tok, err := communications.PublicMediaTokenForTest(box, uuid.MustParse(id), exp)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	valid := f.h.Now().Add(time.Hour)
	anon := f.h.Client(t)
	get := func(method, tok string) *modtest.Response {
		return anon.Do(method, "/api/v1/communications/public-media/"+tok, nil)
	}

	r := get(http.MethodGet, token(card, valid))
	if r.Status != http.StatusOK || string(r.Body) != "IMG71" {
		t.Fatalf("valid token: %d %q, want 200 and the image", r.Status, r.Body)
	}
	if r.Header("Content-Type") != "image/jpeg" || r.Header("Referrer-Policy") != "no-referrer" || r.Header("Cache-Control") != "private, max-age=86400" {
		t.Errorf("headers = %q %q %q, want the image's type, no-referrer and private, max-age=86400",
			r.Header("Content-Type"), r.Header("Referrer-Policy"), r.Header("Cache-Control"))
	}
	if h := get(http.MethodHead, token(card, valid)); h.Status != http.StatusOK || len(h.Body) != 0 || h.Header("Content-Type") != "image/jpeg" {
		t.Errorf("HEAD: %d with %d body bytes and type %q, want 200, no body and the type", h.Status, len(h.Body), h.Header("Content-Type"))
	}

	good := token(card, valid)
	parts := strings.Split(good, ".")
	refused := map[string]string{
		"expired":           token(card, f.h.Now().Add(-time.Second)),
		"a wrong MAC":       parts[0] + "." + parts[1] + "." + strings.Repeat("A", len(parts[2])),
		"another expiry":    parts[0] + ".9999999999." + parts[2],
		"another one's":     token(uuid.NewString(), valid),
		"a file attachment": token(fileAttachment, valid),
		"an inbound one":    token(inbound, valid),
		"not a token":       "x",
		"too many parts":    good + ".x",
		"an empty one":      "..",
	}
	for name, tok := range refused {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			if r := get(method, tok); r.Status != http.StatusNotFound || len(r.Body) != 0 {
				t.Errorf("%s (%s): %d %q, want a bare 404", name, method, r.Status, r.Body)
			}
		}
	}

	// An object that is gone is a 404 for GET and HEAD alike.
	key := modtest.One[string](t, f.h, `SELECT storage_key FROM communications.message_attachments WHERE id = $1`, card)
	f.h.Exec(t, `UPDATE communications.message_attachments SET storage_key = 'cards/missing' WHERE id = $1`, card)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		if r := get(method, good); r.Status != http.StatusNotFound {
			t.Errorf("a missing object (%s): %d, want 404", method, r.Status)
		}
	}
	f.h.Exec(t, `UPDATE communications.message_attachments SET storage_key = $2 WHERE id = $1`, card, key)

	// A provider that is handed the bytes serves no public media.
	caps := f.fake.Capabilities()
	caps.PublicMedia = false
	f.fake.SetCapabilities(caps)
	if r := get(http.MethodGet, good); r.Status != http.StatusNotFound {
		t.Errorf("provider without public media: %d, want 404", r.Status)
	}
	caps.PublicMedia = true
	f.fake.SetCapabilities(caps)
	if r := get(http.MethodGet, good); r.Status != http.StatusOK {
		t.Errorf("public media again: %d, want 200", r.Status)
	}

	// The token stops holding at its expiry, and the lifetime of a send's token is
	// the design's 7 days.
	f.h.Advance(time.Hour + time.Second)
	if r := get(http.MethodGet, good); r.Status != http.StatusNotFound {
		t.Errorf("after the expiry: %d, want 404", r.Status)
	}
	if communications.PublicMediaTokenLifetimeForTest != 7*24*time.Hour {
		t.Errorf("token lifetime = %v, want 7 days", communications.PublicMediaTokenLifetimeForTest)
	}
}

// TestPublicMedia_NoLogOfTheToken: a fetch is logged with the attachment id and
// never the token.
func TestPublicMedia_NoLogOfTheToken(t *testing.T) {
	t.Parallel()
	f, _ := newCardChat(t)
	conv := f.chat(t)
	message := f.mustReply(t, conv, map[string]any{"textBody": "Hi", "components": cardsOf(card("Chair", 71, buy()))})
	card := f.cardAttachments(t, conv, message)[0]
	tok, err := communications.PublicMediaTokenForTest(f.h.Deps().Secrets, uuid.MustParse(card), f.h.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if r := f.h.Client(t).Do(http.MethodGet, "/api/v1/communications/public-media/"+tok, nil); r.Status != http.StatusOK {
		t.Fatalf("fetch: %d", r.Status)
	}
	// The request log records every path as it is, as for the calendar feed; the
	// module's own line for the fetch is what names the attachment.
	mac := strings.Split(tok, ".")[2]
	var lines []string
	for _, line := range strings.Split(f.h.Logs(), "\n") {
		if strings.Contains(line, "a card image was fetched") {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 || !strings.Contains(lines[0], card) {
		t.Errorf("the module's log lines of the fetch = %q, want one with the attachment id %s", lines, card)
	}
	for _, line := range lines {
		if strings.Contains(line, mac) {
			t.Error("the module's log line holds the token's MAC")
		}
	}
}

// TestBuyAnswer_ResolvesItsProduct: a Buy answer to a card shows the product the
// message was about, with the card's image; an answer to a message that showed no
// product does not.
func TestBuyAnswer_ResolvesItsProduct(t *testing.T) {
	t.Parallel()
	f, _ := newCardChat(t)
	conv := f.chat(t)
	message := f.mustReply(t, conv, map[string]any{"textBody": "Hi", "components": cardsOf(card("Chair", 71, buy()), card("Chair, side", 72, buy()))})
	plain := f.mustReply(t, conv, map[string]any{"textBody": "Anything else?", "components": yesNo()})
	cards := f.cardAttachments(t, conv, message)

	answer := func(id, text string, a *contracts.MessengerAnswer) {
		f.h.Advance(2 * time.Minute)
		f.fake.Push(f.channel.Address, contracts.MessengerEvent{
			Kind: contracts.MessengerEventMessage, Participant: "4242", At: f.h.Now(), PlatformMessageID: id, Text: text, Answer: a,
		})
		f.sync(t)
	}
	answer("b1", "Buy", &contracts.MessengerAnswer{MessageID: message.String(), ChoiceID: "buy-1"})
	answer("b2", "Yes", &contracts.MessengerAnswer{MessageID: plain.String(), ChoiceID: "yes"})

	byText := map[string]chatMessageJSON{}
	for _, m := range f.detail(t, conv).Messages {
		if m.TextBody != nil {
			byText[*m.TextBody] = m
		}
	}
	a := byText["Buy"].AnswerTo
	if a == nil || a.ChoiceId != "buy-1" || a.Product == nil || a.Product.ProductId != 7 || a.Product.Name != "Chair, side" ||
		a.Product.AttachmentId == nil || *a.Product.AttachmentId != cards[1] {
		t.Errorf("Buy answer = %+v, want product 7 and the title and image of the card it was given on (the second)", a)
	}
	if a := byText["Yes"].AnswerTo; a == nil || a.Product != nil {
		t.Errorf("an answer to a message with no product = %+v, want no product", a)
	}
}
