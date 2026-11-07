package communications

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is what a reply with cards does before its transaction (Messenger
// design N16): each card's productImageId is checked through the product
// catalog's images marked for Communications, its bytes are copied into this
// module's storage with the cleanup record written first, and the card
// attachments are created in the transaction. The message keeps what was sent,
// even when the product's images change later.

// productUseCommunications is the use a product image is marked for to be
// attached to a message (N13).
const productUseCommunications = "communications"

// cardImageMaxBytes is the largest product image a card copies. Products
// normalises its images well below it; the bound keeps a misbehaving catalog
// from filling memory.
const cardImageMaxBytes = 8 << 20

// cardCopy is one product image copied into this module's storage: the object
// is stored and reserved, and the card attachment row waits for the reply's
// transaction.
type cardCopy struct {
	id          uuid.UUID
	key         string
	fileName    string
	contentType string
	hash        string
	size        int64
}

// cardImages is a reply's copied card images with the product they are of.
type cardImages struct {
	copies    []cardCopy
	productID int32
}

// productImagesOf is the catalog's images of a product marked for use; a call
// out of the module, never made inside a transaction.
func (s *server) productImagesOf(ctx context.Context, productID int32, use string) ([]contracts.ProductImage, error) {
	if err := noteContractCall(ctx, "ProductCatalog.ProductImages"); err != nil {
		return nil, err
	}
	return s.deps.Products.ProductImages(ctx, productID, use)
}

// openProductImage streams an image of the catalog; (nil, zero, nil) when it is
// gone.
func (s *server) openProductImage(ctx context.Context, imageID int32) (io.ReadCloser, contracts.ProductImage, error) {
	if err := noteContractCall(ctx, "ProductCatalog.OpenProductImage"); err != nil {
		return nil, contracts.ProductImage{}, err
	}
	return s.deps.Products.OpenProductImage(ctx, imageID)
}

// cardsRefusal is what a reply's cards break before anything is read of the
// catalog: the shape the images need (every card names a product image, and the
// instance has a public host for the provider to fetch them from), or nil.
func cardsRefusal(c *gen.MessageComponents, caps contracts.MessengerCapabilities, publicBaseURL string) gen.PostCommunicationsConversationsByIdReplyResponseObject {
	if c == nil || c.Cards == nil {
		return nil
	}
	errs := map[string][]string{}
	for i, card := range *c.Cards {
		if card.ProductImageId == nil {
			errs[fmt.Sprintf("cards[%d].productImageId", i)] = []string{"A card shows a product image."}
		}
	}
	if len(errs) > 0 {
		return gen.PostCommunicationsConversationsByIdReply422JSONResponse(fieldsErrorBody(
			"components_invalid", "The answer options or cards break the channel's limits.", errs))
	}
	if caps.PublicMedia && publicBaseURL == "" {
		return gen.PostCommunicationsConversationsByIdReply422JSONResponse(flatErrorBody(
			"public_url_missing", "The instance has no public host for the platform to fetch a card's image from."))
	}
	return nil
}

// copyCardImages checks each card's image and copies it (N16): through
// ProductImages(product, "communications"), so one that is gone, or no longer
// marked for Communications, is 422 product_image_unavailable; opened and stored
// with the cleanup record written first. One product per message: images of two
// products are 422 components_invalid. On a refusal or an error whatever was
// copied is queued for deletion.
func (s *server) copyCardImages(ctx context.Context, conversationID uuid.UUID, cards []gen.MessageComponentCard, now time.Time) (*cardImages, gen.PostCommunicationsConversationsByIdReplyResponseObject, error) {
	unavailable := gen.PostCommunicationsConversationsByIdReply422JSONResponse(flatErrorBody(
		"product_image_unavailable", "A product image on a card is gone, or is no longer marked for use in messages."))
	if s.deps.Products == nil {
		return nil, unavailable, nil
	}
	out := &cardImages{}
	marked := map[int32][]contracts.ProductImage{}
	fail := func(refusal gen.PostCommunicationsConversationsByIdReplyResponseObject, err error) (*cardImages, gen.PostCommunicationsConversationsByIdReplyResponseObject, error) {
		s.forgetCardImages(ctx, out.copies, now)
		return nil, refusal, err
	}
	for i, card := range cards {
		imageID := *card.ProductImageId
		body, image, err := s.openProductImage(ctx, imageID)
		if err != nil {
			return fail(nil, fmt.Errorf("communications: open a product image: %w", err))
		}
		if body == nil {
			return fail(unavailable, nil)
		}
		list, known := marked[image.ProductID]
		if !known {
			list, err = s.productImagesOf(ctx, image.ProductID, productUseCommunications)
			if err != nil {
				_ = body.Close()
				return fail(nil, fmt.Errorf("communications: list a product's images: %w", err))
			}
			marked[image.ProductID] = list
		}
		if !slices.ContainsFunc(list, func(p contracts.ProductImage) bool { return p.ID == imageID }) {
			_ = body.Close()
			return fail(unavailable, nil)
		}
		if i > 0 && image.ProductID != out.productID {
			_ = body.Close()
			return fail(gen.PostCommunicationsConversationsByIdReply422JSONResponse(fieldsErrorBody(
				"components_invalid", "The answer options or cards break the channel's limits.",
				map[string][]string{fmt.Sprintf("cards[%d].productImageId", i): {"The cards of a message show one product."}})), nil)
		}
		out.productID = image.ProductID
		content, err := io.ReadAll(io.LimitReader(body, cardImageMaxBytes+1))
		_ = body.Close()
		if err != nil {
			return fail(nil, fmt.Errorf("communications: read a product image: %w", err))
		}
		if len(content) == 0 || len(content) > cardImageMaxBytes {
			return fail(unavailable, nil)
		}
		id := uuid.New()
		contentType := sanitizedAttachmentContentType(image.ContentType)
		c := cardCopy{
			id: id, key: fmt.Sprintf("cards/%s/%s", hexN(conversationID), hexN(id)),
			fileName:    fmt.Sprintf("product-%d-image-%d%s", image.ProductID, image.ID, extensionOf(contentType)),
			contentType: contentType, hash: attachmentFingerprint(content), size: int64(len(content)),
		}
		// The reservation is committed before the object is written, so a crash
		// between the two leaves a record the cleanup worker reclaims.
		if err := reserveObjectKey(ctx, s.deps.Pool, c.key, now); err != nil {
			return fail(nil, fmt.Errorf("communications: reserve a card image: %w", err))
		}
		out.copies = append(out.copies, c)
		if err := s.store.Put(ctx, c.key, bytes.NewReader(content), c.contentType); err != nil {
			return fail(nil, fmt.Errorf("communications: store a card image: %w", err))
		}
	}
	return out, nil, nil
}

// extensionOf is the file extension of a product image's media type.
func extensionOf(contentType string) string {
	switch contentType {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	}
	return ""
}

// forgetCardImages queues copied card images for deletion: the reply that
// copied them was refused or did not commit, so they are no one's.
func (s *server) forgetCardImages(ctx context.Context, copies []cardCopy, now time.Time) {
	if len(copies) == 0 {
		return
	}
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		for _, c := range copies {
			if err := queueObjectForDeletion(ctx, q, c.key, nil, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil && s.deps.Logger != nil {
		s.deps.Logger.LogAttrs(ctx, slog.LevelWarn, "communications: a refused reply's card images could not be queued for deletion; their reservations expire",
			slog.String("error", err.Error()))
	}
}
