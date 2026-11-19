package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/db"
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

// The two demo brands beside the default (brands design B-B), with the people
// who show them. Nordlys is assigned by the e-mail domain nordlys.example and
// Fjordkraft Demo by an access group; Kari's address is Nordlys's and she is in
// the group too, so she matches two brands and the account menu offers her a
// switcher, while Jonas sees Fjordkraft Demo alone and everyone else the default.
const (
	demoNordlysDomain  = "nordlys.example"
	demoNordlysUser    = "kari.nordlys@nordlys.example"
	demoFjordkraftTeam = "Fjordkraft demo team"
	demoFjordkraftUser = "jonas.moen@vantigo.local"
)

// seedDemoBrands adds Nordlys and Fjordkraft Demo, the group and the people. It
// is idempotent: a brand is added once, by name.
func seedDemoBrands(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, cfg *config.Config) error {
	hash, err := seedPasswordHash(demoDefaultPassword)
	if err != nil {
		return err
	}
	now := seedClock()
	var groupID, kariID uuid.UUID
	err = db.WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO identity.access_groups (id, display_name, source, is_active, version, created_at, updated_at)
			VALUES ($1, $2, 'local', true, $3, $4, $4)
			ON CONFLICT (display_name) DO UPDATE SET updated_at = identity.access_groups.updated_at
			RETURNING id`, uuid.New(), demoFjordkraftTeam, uuid.New(), now).Scan(&groupID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO identity.users (id, email, normalized_email, email_confirmed, display_name, password_hash, version, created_at, updated_at)
			VALUES ($1, $2, $3, true, 'Kari Nordlys', $4, $5, $6, $6)
			ON CONFLICT (normalized_email) DO NOTHING`, uuid.New(), demoNordlysUser, strings.ToUpper(demoNordlysUser), hash, uuid.New(), now); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT id FROM identity.users WHERE normalized_email = $1`, strings.ToUpper(demoNordlysUser)).Scan(&kariID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, kariID, identity.RoleUserID); err != nil {
			return err
		}
		// The people in the group: Kari, and Jonas of the demo staff (when he exists).
		for _, member := range []string{strings.ToUpper(demoNordlysUser), strings.ToUpper(demoFjordkraftUser)} {
			if _, err := tx.Exec(ctx, `
				INSERT INTO identity.access_group_memberships (group_id, user_id, source, is_upstream_present, membership_override)
				SELECT $1, u.id, 'local', false, 'force_member' FROM identity.users u WHERE u.normalized_email = $2
				ON CONFLICT DO NOTHING`, groupID, member); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("seed the demo brands' people: %w", err)
	}

	d := module.Deps{Pool: pool, Config: cfg, Logger: logger, Clock: seedClock}
	teal, err := demoBrandMark(color.NRGBA{0x0f, 0x76, 0x6e, 0xff}, color.NRGBA{0x18, 0x20, 0x33, 0xff})
	if err != nil {
		return err
	}
	tealDark, err := demoBrandMark(color.NRGBA{0x5e, 0xea, 0xd4, 0xff}, color.NRGBA{0xdd, 0xe1, 0xef, 0xff})
	if err != nil {
		return err
	}
	amber, err := demoBrandMark(color.NRGBA{0xb4, 0x53, 0x09, 0xff}, color.NRGBA{0x18, 0x20, 0x33, 0xff})
	if err != nil {
		return err
	}
	amberDark, err := demoBrandMark(color.NRGBA{0xfc, 0xd3, 0x4d, 0xff}, color.NRGBA{0xdd, 0xe1, 0xef, 0xff})
	if err != nil {
		return err
	}
	for _, b := range []identity.DemoBrand{
		{Name: "Nordlys", Title: "Nordlys", Primary: "#0f766e",
			Marks: map[string][]byte{"mark_light": teal, "mark_dark": tealDark},
			Rules: [][2]string{{"domain", demoNordlysDomain}}},
		{Name: "Fjordkraft Demo", Title: "Fjordkraft", Primary: "#b45309",
			Marks: map[string][]byte{"mark_light": amber, "mark_dark": amberDark},
			Rules: [][2]string{{"access_group", groupID.String()}}},
	} {
		applied, err := identity.SeedDemoBrand(ctx, d, b)
		if err != nil {
			return err
		}
		if applied {
			logger.Info("development seed: a demo brand was added", "brand", b.Name)
		}
	}
	return nil
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
