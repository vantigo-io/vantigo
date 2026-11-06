package products

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/products/gen"
	"github.com/vantigo-io/vantigo/server/internal/products/store"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// This file is the images of a product (messenger design N13, N15):
// getProductsByIdImages, postProductsByIdImages,
// patchProductsByIdImagesByImageId, deleteProductsByIdImagesByImageId and
// getProductsByIdImagesByImageIdContent, with the catalog's two calls beside
// them. The upload is normalised first (images_normalise.go) and its two
// files stored before the row's transaction (objects.go).
//
// Every write is one transaction that first takes the product's image lock
// (LockProductImages), so a product's images, their order and their one
// primary change one writer at a time. The primary rules: the first image
// uploaded is primary; making another primary clears the old one first,
// since the partial unique index is checked row by row; deleting the primary
// promotes the next image by sort_order. sort_order is kept as the place in
// the order, from 0 and without gaps, renumbered whenever an image is
// moved or deleted.

const (
	// imageAltMaxLength is the longest an alt text may be, in characters.
	imageAltMaxLength = 250
	// imageCacheControl is what a served image carries: it is named by a
	// fresh random key and never replaced in place, but the row can be
	// deleted, so only the browser's own cache, and briefly.
	imageCacheControl = "private, max-age=300"

	useCommunications = "communications"
	useMarketing      = "marketing"
	invalidImageTitle = "Invalid image"
)

// imageUses are the uses an image may be marked for, in the order they are
// stored.
var imageUses = []string{useCommunications, useMarketing}

// normaliseSlots bounds how many uploads are decoded and re-encoded at once in
// this process (maxConcurrentNormalisations): a semaphore, held for the
// normalisation alone and never while the store or the database is called.
var normaliseSlots = make(chan struct{}, maxConcurrentNormalisations)

// imageForm is the multipart upload as read: how many files it carried, the
// bytes of the one, and whether the body was too large or unreadable.
type imageForm struct {
	files      int
	data       []byte
	tooLarge   bool
	unreadable bool
}

// readImageForm reads the multipart field file of an upload, at most
// maxImageBytes+1 bytes of it — enough to tell "too large" from "just
// within" — and skips any other part. A read the router's own cap stops
// (http.MaxBytesError) is too large as well, and any other failure an
// unreadable body.
func readImageForm(mr *multipart.Reader) imageForm {
	var f imageForm
	if mr == nil {
		f.unreadable = true
		return f
	}
	fail := func(err error) imageForm {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			f.tooLarge = true
		} else {
			f.unreadable = true
		}
		return f
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return f
		}
		if err != nil {
			return fail(err)
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}
		f.files++
		data, err := io.ReadAll(io.LimitReader(part, maxImageBytes+1))
		_ = part.Close()
		if err != nil {
			return fail(err)
		}
		if len(data) > maxImageBytes {
			f.tooLarge = true
			return f
		}
		f.data = data
	}
}

// imageProblem is an image refusal as the contract's problem, with its code.
func imageProblem(status int, code, title, detail string) gen.ProductImageProblem {
	s := int32(status)
	return gen.ProductImageProblem{Code: &code, Title: &title, Detail: &detail, Status: &s}
}

func storageUnavailable() gen.ProductImageProblem {
	return imageProblem(http.StatusServiceUnavailable, codeStorageUnavailable, "Image storage is unavailable",
		"The image store could not be reached. Nothing was changed; try again.")
}

// imageResponse is an image row on the wire.
func imageResponse(r store.ProductsProductImage) gen.ProductImageResponse {
	uses := make([]gen.ProductImageResponseUses, 0, len(r.Uses))
	for _, u := range r.Uses {
		uses = append(uses, gen.ProductImageResponseUses(u))
	}
	out := gen.ProductImageResponse{
		Id: r.ID, ProductId: r.ProductID, ContentType: r.ContentType, ByteSize: r.ByteSize,
		Width: r.Width, Height: r.Height, SortOrder: r.SortOrder, IsPrimary: r.IsPrimary,
		Uses: uses, CreatedAt: r.CreatedAt,
	}
	if r.AltText != nil && *r.AltText != "" {
		out.AltText = r.AltText
	}
	return out
}

// GetProductsByIdImages List a product's images
// (GET /api/v1/products/{id}/images)
func (s *server) GetProductsByIdImages(ctx context.Context, req gen.GetProductsByIdImagesRequestObject) (gen.GetProductsByIdImagesResponseObject, error) {
	q := store.New(s.deps.Pool)
	exists, err := q.ProductExists(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("products: product exists: %w", err)
	}
	if !exists {
		return gen.GetProductsByIdImages404Response{}, nil
	}
	rows, err := q.ListProductImages(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("products: list images: %w", err)
	}
	out := make(gen.GetProductsByIdImages200JSONResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, imageResponse(r))
	}
	return out, nil
}

// PostProductsByIdImages Upload an image of a product
// (POST /api/v1/products/{id}/images)
//
// The product is looked up first (404), then the file judged and normalised
// (400 for a form that is no form, 422 with the image's code for a file
// that is refused). The two files are written before the transaction — a
// store that cannot write is 503 with nothing recorded — and removed again
// when the transaction does not commit. The first image of a product is
// primary, and a new one is last in the order.
func (s *server) PostProductsByIdImages(ctx context.Context, req gen.PostProductsByIdImagesRequestObject) (gen.PostProductsByIdImagesResponseObject, error) {
	exists, err := store.New(s.deps.Pool).ProductExists(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("products: product exists: %w", err)
	}
	if !exists {
		return gen.PostProductsByIdImages404Response{}, nil
	}
	form := readImageForm(req.Body)
	switch {
	case form.tooLarge:
		return gen.PostProductsByIdImages422ApplicationProblemPlusJSONResponse(imageProblem(
			http.StatusUnprocessableEntity, codeImageTooLarge, "Image too large", "The file is larger than 10 MiB.")), nil
	case form.unreadable:
		return gen.PostProductsByIdImages400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(invalidImageTitle,
			map[string][]string{"file": {"The request's body could not be read as a multipart form."}})), nil
	case form.files > 1:
		return gen.PostProductsByIdImages400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(invalidImageTitle,
			map[string][]string{"file": {"A request uploads one image; this one carried more than one file."}})), nil
	case form.files == 0 || len(form.data) == 0:
		return gen.PostProductsByIdImages400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(invalidImageTitle,
			map[string][]string{"file": {"An image file is required."}})), nil
	}
	// Decoding a 40 megapixel picture takes hundreds of megabytes, so only
	// normaliseSlots uploads are normalised at once; the others wait, on the
	// request's own context, so a client that goes away stops waiting.
	select {
	case normaliseSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	img, refusal := normaliseImage(form.data)
	<-normaliseSlots
	if refusal != nil {
		return gen.PostProductsByIdImages422ApplicationProblemPlusJSONResponse(
			imageProblem(http.StatusUnprocessableEntity, refusal.code, refusal.title, refusal.detail)), nil
	}

	key, thumbKey := imageObjectKeys(req.Id)
	if err := s.objects.put(ctx, key, bytes.NewReader(img.data), img.contentType); err != nil {
		s.deps.Logger.ErrorContext(ctx, "products: an image could not be written to the store", "product_id", req.Id, "error", err.Error())
		return gen.PostProductsByIdImages503ApplicationProblemPlusJSONResponse(storageUnavailable()), nil
	}
	if err := s.objects.put(ctx, thumbKey, bytes.NewReader(img.thumb), img.contentType); err != nil {
		s.removeObjects(ctx, req.Id, key)
		s.deps.Logger.ErrorContext(ctx, "products: an image's thumbnail could not be written to the store", "product_id", req.Id, "error", err.Error())
		return gen.PostProductsByIdImages503ApplicationProblemPlusJSONResponse(storageUnavailable()), nil
	}

	principal, _ := contracts.PrincipalFrom(ctx)
	var saved store.ProductsProductImage
	errProductGone := errors.New("products: the product is gone")
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if err := txq.LockProductImages(ctx, req.Id); err != nil {
			return fmt.Errorf("lock images: %w", err)
		}
		if exists, err := txq.ProductExists(ctx, req.Id); err != nil {
			return fmt.Errorf("product exists: %w", err)
		} else if !exists {
			return errProductGone
		}
		next, err := txq.NextProductImageSortOrder(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("next sort order: %w", err)
		}
		hasPrimary, err := txq.ProductHasPrimaryImage(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("has primary: %w", err)
		}
		saved, err = txq.InsertProductImage(ctx, store.InsertProductImageParams{
			ProductID: req.Id, StorageKey: key, ThumbStorageKey: thumbKey, ContentType: img.contentType,
			ByteSize: int32(len(img.data)), Width: int32(img.width), Height: int32(img.height), Sha256: img.sha256,
			SortOrder: next, IsPrimary: !hasPrimary, Uses: []string{useCommunications},
			Now: s.deps.Clock(), CreatedBy: principal.UserID,
		})
		if err != nil {
			return fmt.Errorf("insert image: %w", err)
		}
		return nil
	})
	if err != nil {
		s.removeObjects(ctx, req.Id, key, thumbKey)
		if errors.Is(err, errProductGone) {
			return gen.PostProductsByIdImages404Response{}, nil
		}
		return nil, fmt.Errorf("products: save image: %w", err)
	}
	return gen.PostProductsByIdImages201JSONResponse(imageResponse(saved)), nil
}

// parsedImageUpdate is a validated ProductImageUpdateRequest.
type parsedImageUpdate struct {
	altText   *string
	uses      []string
	setUses   bool
	sortOrder *int32
	primary   *bool
}

// validateImageUpdate checks every field that needs nothing but the request,
// writing field errors into errs in this module's style.
func validateImageUpdate(body gen.ProductImageUpdateRequest, errs map[string][]string) parsedImageUpdate {
	out := parsedImageUpdate{sortOrder: body.SortOrder, primary: body.IsPrimary}
	if body.AltText != nil {
		alt := strings.TrimSpace(*body.AltText)
		if utf8.RuneCountInString(alt) > imageAltMaxLength {
			errs["altText"] = []string{fmt.Sprintf("'altText' must be at most %d characters.", imageAltMaxLength)}
		}
		out.altText = &alt
	}
	if body.Uses != nil {
		out.setUses = true
		out.uses = []string{}
		for _, u := range *body.Uses {
			if !slices.Contains(imageUses, string(u)) {
				errs["uses"] = []string{fmt.Sprintf("'uses' may hold 'communications' and 'marketing', but held '%s'.", u)}
				break
			}
			if !slices.Contains(out.uses, string(u)) {
				out.uses = append(out.uses, string(u))
			}
		}
	}
	if body.SortOrder != nil && *body.SortOrder < 0 {
		errs["sortOrder"] = []string{"'sortOrder' must be 0 or more."}
	}
	return out
}

// PatchProductsByIdImagesByImageId Change an image of a product
// (PATCH /api/v1/products/{id}/images/{imageId})
//
// The alt text and the uses are replaced when sent; sortOrder moves the
// image to that place and renumbers the others; isPrimary true makes it the
// primary image, false is refused for the primary one.
func (s *server) PatchProductsByIdImagesByImageId(ctx context.Context, req gen.PatchProductsByIdImagesByImageIdRequestObject) (gen.PatchProductsByIdImagesByImageIdResponseObject, error) {
	if req.Body == nil {
		return gen.PatchProductsByIdImagesByImageId400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(invalidImageTitle,
			map[string][]string{"": {"A request body is required."}})), nil
	}
	errs := map[string][]string{}
	parsed := validateImageUpdate(*req.Body, errs)
	if len(errs) > 0 {
		return gen.PatchProductsByIdImagesByImageId400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(invalidImageTitle, errs)), nil
	}

	var saved store.ProductsProductImage
	var refused map[string][]string
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if err := txq.LockProductImages(ctx, req.Id); err != nil {
			return fmt.Errorf("lock images: %w", err)
		}
		current, err := txq.GetProductImage(ctx, store.GetProductImageParams{ID: req.ImageId, ProductID: req.Id})
		if err != nil {
			return err
		}
		if parsed.primary != nil && !*parsed.primary && current.IsPrimary {
			refused = map[string][]string{"isPrimary": {"The primary image stays primary until another image is made primary."}}
			return nil
		}

		alt, uses := current.AltText, current.Uses
		if parsed.altText != nil {
			alt = parsed.altText
			if *alt == "" {
				alt = nil
			}
		}
		if parsed.setUses {
			uses = parsed.uses
		}
		if saved, err = txq.UpdateProductImage(ctx, store.UpdateProductImageParams{ID: current.ID, AltText: alt, Uses: uses}); err != nil {
			return fmt.Errorf("update image: %w", err)
		}
		if parsed.primary != nil && *parsed.primary && !current.IsPrimary {
			if err := txq.ClearPrimaryProductImage(ctx, req.Id); err != nil {
				return fmt.Errorf("clear primary: %w", err)
			}
			if err := txq.SetPrimaryProductImage(ctx, current.ID); err != nil {
				return fmt.Errorf("set primary: %w", err)
			}
		}
		if parsed.sortOrder != nil {
			if err := s.moveImage(ctx, txq, req.Id, current.ID, int(*parsed.sortOrder)); err != nil {
				return err
			}
		}
		if saved, err = txq.GetProductImage(ctx, store.GetProductImageParams{ID: current.ID, ProductID: req.Id}); err != nil {
			return fmt.Errorf("reread image: %w", err)
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PatchProductsByIdImagesByImageId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("products: change image: %w", err)
	}
	if refused != nil {
		return gen.PatchProductsByIdImagesByImageId400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(invalidImageTitle, refused)), nil
	}
	return gen.PatchProductsByIdImagesByImageId200JSONResponse(imageResponse(saved)), nil
}

// moveImage puts the image at place among the product's, the others keeping
// their order, and renumbers them all from 0 without gaps; only the rows
// whose number changes are written.
func (s *server) moveImage(ctx context.Context, txq *store.Queries, productID, imageID int32, place int) error {
	rows, err := txq.ListProductImages(ctx, productID)
	if err != nil {
		return fmt.Errorf("list images: %w", err)
	}
	ordered := make([]store.ProductsProductImage, 0, len(rows))
	var moving store.ProductsProductImage
	for _, r := range rows {
		if r.ID == imageID {
			moving = r
			continue
		}
		ordered = append(ordered, r)
	}
	place = min(max(place, 0), len(ordered))
	ordered = slices.Insert(ordered, place, moving)
	return renumberImages(ctx, txq, ordered)
}

// renumberImages gives images, in the order given, the sort orders 0, 1, 2…
func renumberImages(ctx context.Context, txq *store.Queries, images []store.ProductsProductImage) error {
	for i, r := range images {
		if int(r.SortOrder) == i {
			continue
		}
		if err := txq.SetProductImageSortOrder(ctx, store.SetProductImageSortOrderParams{ID: r.ID, SortOrder: int32(i)}); err != nil {
			return fmt.Errorf("renumber images: %w", err)
		}
	}
	return nil
}

// DeleteProductsByIdImagesByImageId Delete an image of a product
// (DELETE /api/v1/products/{id}/images/{imageId})
//
// The row goes in a transaction that also promotes the next image by
// sort_order when it was the primary and closes the gap in the order; once
// that has committed, the two objects are removed. An object that cannot be
// removed is logged and stays behind unreferenced.
func (s *server) DeleteProductsByIdImagesByImageId(ctx context.Context, req gen.DeleteProductsByIdImagesByImageIdRequestObject) (gen.DeleteProductsByIdImagesByImageIdResponseObject, error) {
	var gone store.ProductsProductImage
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if err := txq.LockProductImages(ctx, req.Id); err != nil {
			return fmt.Errorf("lock images: %w", err)
		}
		var err error
		if gone, err = txq.GetProductImage(ctx, store.GetProductImageParams{ID: req.ImageId, ProductID: req.Id}); err != nil {
			return err
		}
		if err := txq.DeleteProductImage(ctx, gone.ID); err != nil {
			return fmt.Errorf("delete image: %w", err)
		}
		rest, err := txq.ListProductImages(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("list images: %w", err)
		}
		if gone.IsPrimary && len(rest) > 0 {
			if err := txq.SetPrimaryProductImage(ctx, rest[0].ID); err != nil {
				return fmt.Errorf("promote image: %w", err)
			}
		}
		return renumberImages(ctx, txq, rest)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteProductsByIdImagesByImageId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("products: delete image: %w", err)
	}
	s.removeObjects(ctx, req.Id, gone.StorageKey, gone.ThumbStorageKey)
	return gen.DeleteProductsByIdImagesByImageId204Response{}, nil
}

// imageDownload sets the headers a served image carries before the generated
// response writes its type, its length and the bytes: cached privately,
// never sniffed, and no disposition — it is shown in place.
type imageDownload struct {
	body gen.GetProductsByIdImagesByImageIdContentResponseObject
}

func (r imageDownload) VisitGetProductsByIdImagesByImageIdContentResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", imageCacheControl)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	return r.body.VisitGetProductsByIdImagesByImageIdContentResponse(w)
}

// GetProductsByIdImagesByImageIdContent Stream an image of a product
// (GET /api/v1/products/{id}/images/{imageId}/content)
//
// The row is read on the pool, the object after it; size=thumb streams the
// thumbnail. An image the product does not have, and one whose object the
// store no longer holds, is the bare 404; a store that cannot be read is 503.
func (s *server) GetProductsByIdImagesByImageIdContent(ctx context.Context, req gen.GetProductsByIdImagesByImageIdContentRequestObject) (gen.GetProductsByIdImagesByImageIdContentResponseObject, error) {
	thumb := false
	if req.Params.Size != nil && *req.Params.Size != "" {
		if *req.Params.Size != "thumb" {
			return gen.GetProductsByIdImagesByImageIdContent400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(invalidImageTitle,
				map[string][]string{"size": {"'size' must be 'thumb' or left out."}})), nil
		}
		thumb = true
	}
	row, err := store.New(s.deps.Pool).GetProductImage(ctx, store.GetProductImageParams{ID: req.ImageId, ProductID: req.Id})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetProductsByIdImagesByImageIdContent404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("products: get image: %w", err)
	}
	key, length := row.StorageKey, int64(row.ByteSize)
	if thumb {
		key, length = row.ThumbStorageKey, 0
	}
	body, err := s.objects.get(ctx, key)
	if errors.Is(err, storage.ErrNotExist) {
		return gen.GetProductsByIdImagesByImageIdContent404Response{}, nil
	}
	if err != nil {
		s.deps.Logger.ErrorContext(ctx, "products: an image could not be read from the store", "product_id", req.Id, "error", err.Error())
		return gen.GetProductsByIdImagesByImageIdContent503ApplicationProblemPlusJSONResponse(storageUnavailable()), nil
	}
	if row.ContentType == "image/png" {
		return imageDownload{body: gen.GetProductsByIdImagesByImageIdContent200ImagepngResponse{Body: body, ContentLength: length}}, nil
	}
	return imageDownload{body: gen.GetProductsByIdImagesByImageIdContent200ImagejpegResponse{Body: body, ContentLength: length}}, nil
}

// productImage is a row as the contract's ProductImage, which names the
// picture and never where it is kept.
func productImage(r store.ProductsProductImage) contracts.ProductImage {
	alt := ""
	if r.AltText != nil {
		alt = *r.AltText
	}
	return contracts.ProductImage{
		ID: r.ID, ProductID: r.ProductID, AltText: alt, ContentType: r.ContentType,
		Width: r.Width, Height: r.Height, Uses: slices.Clone(r.Uses), SortOrder: r.SortOrder,
	}
}

// ProductImages is the product's images marked for use, in their order
// (messenger design N15); a use that is none of the two, and a product with
// no images or none at all, is empty.
func (c *catalog) ProductImages(ctx context.Context, productID int32, use string) ([]contracts.ProductImage, error) {
	if !slices.Contains(imageUses, use) {
		return nil, nil
	}
	rows, err := c.q.ListProductImagesByUse(ctx, store.ListProductImagesByUseParams{ProductID: productID, Use: use})
	if err != nil {
		return nil, fmt.Errorf("products: catalog product images: %w", err)
	}
	out := make([]contracts.ProductImage, 0, len(rows))
	for _, r := range rows {
		out = append(out, productImage(r))
	}
	return out, nil
}

// OpenProductImage streams an image's normalised bytes with its description.
// An image that does not exist, or whose object the store no longer holds,
// is (nil, zero, nil): gone, not a failure.
func (c *catalog) OpenProductImage(ctx context.Context, imageID int32) (io.ReadCloser, contracts.ProductImage, error) {
	row, err := c.q.GetProductImageByID(ctx, imageID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, contracts.ProductImage{}, nil
	}
	if err != nil {
		return nil, contracts.ProductImage{}, fmt.Errorf("products: catalog open product image: %w", err)
	}
	body, err := c.objects.get(ctx, row.StorageKey)
	if errors.Is(err, storage.ErrNotExist) {
		return nil, contracts.ProductImage{}, nil
	}
	if err != nil {
		return nil, contracts.ProductImage{}, fmt.Errorf("products: catalog open product image: %w", err)
	}
	return body, productImage(row), nil
}
