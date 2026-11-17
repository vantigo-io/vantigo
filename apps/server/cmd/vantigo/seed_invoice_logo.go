package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"slices"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// demoInvoicePhone is the demo seller's phone (invoices phase 6 design I4).
const demoInvoicePhone = "+47 22 12 34 56"

// demoLogo is the demo seller's logo as Invoices names it (invoices phase 6
// design I1): its key within Invoices' storage scope and the hash of its
// bytes.
type demoLogo struct{ key, sha256 string }

// demoInvoiceLogoPNG is a small generated logo: a blue square and three
// orange bars on a transparent ground, 240 × 80 pixels.
func demoInvoiceLogoPNG() ([]byte, error) {
	img := image.NewNRGBA(image.Rect(0, 0, 240, 80))
	blue, orange := color.NRGBA{0x1f, 0x4e, 0x79, 0xff}, color.NRGBA{0xe8, 0x7d, 0x1e, 0xff}
	for y := range 80 {
		for x := range 240 {
			switch {
			case x < 80:
				img.SetNRGBA(x, y, blue)
			case x >= 96 && (x-96)%48 < 32 && y >= 24 && y < 56:
				img.SetNRGBA(x, y, orange)
			}
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// storeDemoInvoiceLogo puts the demo logo in Invoices' object store, under
// the content-addressed key the module itself uses, and answers it — or nil
// when Invoices is off or no object store is configured, so no document
// names a logo it could not read. It runs before the seed's transaction: a
// put is idempotent, and an object nobody names costs nothing.
func storeDemoInvoiceLogo(ctx context.Context, cfg *config.Config) (*demoLogo, error) {
	if cfg == nil || cfg.StorageProvider == "" || !slices.Contains(cfg.Modules, "invoices") {
		return nil, nil
	}
	data, err := demoInvoiceLogoPNG()
	if err != nil {
		return nil, fmt.Errorf("draw the demo logo: %w", err)
	}
	base, err := storage.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("build the object store: %w", err)
	}
	scoped, err := storage.NewScope(base, "invoices")
	if err != nil {
		return nil, fmt.Errorf("scope the object store: %w", err)
	}
	sum := sha256.Sum256(data)
	logo := &demoLogo{sha256: hex.EncodeToString(sum[:])}
	logo.key = "branding/logo-" + logo.sha256 + ".png"
	if err := scoped.Put(ctx, logo.key, bytes.NewReader(data), "image/png"); err != nil {
		return nil, fmt.Errorf("store the demo logo: %w", err)
	}
	return logo, nil
}
