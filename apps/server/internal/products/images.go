package products

import (
	"context"
	"errors"
	"io"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/products/gen"
)

// errImagesNotBuilt stands in for the image operations until the server lane
// of messenger delivery P replaces this file (the seam commit compiles, and
// answers nothing).
var errImagesNotBuilt = errors.New("products: images are not built yet")

func (s *server) GetProductsByIdImages(context.Context, gen.GetProductsByIdImagesRequestObject) (gen.GetProductsByIdImagesResponseObject, error) {
	return nil, errImagesNotBuilt
}

func (s *server) PostProductsByIdImages(context.Context, gen.PostProductsByIdImagesRequestObject) (gen.PostProductsByIdImagesResponseObject, error) {
	return nil, errImagesNotBuilt
}

func (s *server) DeleteProductsByIdImagesByImageId(context.Context, gen.DeleteProductsByIdImagesByImageIdRequestObject) (gen.DeleteProductsByIdImagesByImageIdResponseObject, error) {
	return nil, errImagesNotBuilt
}

func (s *server) PatchProductsByIdImagesByImageId(context.Context, gen.PatchProductsByIdImagesByImageIdRequestObject) (gen.PatchProductsByIdImagesByImageIdResponseObject, error) {
	return nil, errImagesNotBuilt
}

func (s *server) GetProductsByIdImagesByImageIdContent(context.Context, gen.GetProductsByIdImagesByImageIdContentRequestObject) (gen.GetProductsByIdImagesByImageIdContentResponseObject, error) {
	return nil, errImagesNotBuilt
}

func (c *catalog) ProductImages(context.Context, int32, string) ([]contracts.ProductImage, error) {
	return nil, errImagesNotBuilt
}

func (c *catalog) OpenProductImage(context.Context, int32) (io.ReadCloser, contracts.ProductImage, error) {
	return nil, contracts.ProductImage{}, errImagesNotBuilt
}
