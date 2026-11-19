package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The demo company's brand (brands design, delivery B-A): the default brand is
// named for the showcase company, with a generated mark for light and for dark
// backgrounds. The marks are drawn here, so no binary file is committed.
const (
	demoBrandName  = "Vantigo Demo AS"
	demoBrandTitle = "Vantigo Demo"
)

// demoBrandMark is a 240 x 80 mark: a rounded-looking square and three bars,
// drawn in ink on a transparent ground (ink is chosen for the background the
// mark is meant for).
func demoBrandMark(square, bars color.NRGBA) ([]byte, error) {
	img := image.NewNRGBA(image.Rect(0, 0, 240, 80))
	for y := range 80 {
		for x := range 240 {
			switch {
			case x < 80:
				img.SetNRGBA(x, y, square)
			case x >= 96 && (x-96)%48 < 32 && y >= 24 && y < 56:
				img.SetNRGBA(x, y, bars)
			}
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// seedDemoBrand names and publishes the default brand for the demo company.
func seedDemoBrand(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, cfg *config.Config) error {
	light, err := demoBrandMark(color.NRGBA{0x4f, 0x46, 0xe5, 0xff}, color.NRGBA{0x18, 0x20, 0x33, 0xff})
	if err != nil {
		return fmt.Errorf("draw the demo mark: %w", err)
	}
	dark, err := demoBrandMark(color.NRGBA{0xa5, 0xb4, 0xfc, 0xff}, color.NRGBA{0xdd, 0xe1, 0xef, 0xff})
	if err != nil {
		return fmt.Errorf("draw the demo mark: %w", err)
	}
	d := module.Deps{Pool: pool, Config: cfg, Logger: logger, Clock: seedClock}
	applied, err := identity.SeedBrand(ctx, d, identity.BrandSeed{
		Name: demoBrandName, Title: demoBrandTitle,
		Marks: map[string][]byte{"mark_light": light, "mark_dark": dark},
	})
	if err != nil {
		return err
	}
	if applied {
		logger.Info("development seed: the default brand is named for the demo company", "brand", demoBrandName)
	}
	return nil
}
