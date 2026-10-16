package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type rgb struct {
	r, g, b float64
}

func hexRGB(v uint32) rgb {
	return rgb{
		r: float64((v>>16)&0xFF) / 255.0,
		g: float64((v>>8)&0xFF) / 255.0,
		b: float64(v&0xFF) / 255.0,
	}
}

func (c rgb) scale(f float64) rgb {
	return rgb{
		r: clamp01(c.r * f),
		g: clamp01(c.g * f),
		b: clamp01(c.b * f),
	}
}

func mixRGB(a, b rgb, t float64) rgb {
	t = clamp01(t)
	return rgb{
		r: a.r + (b.r-a.r)*t,
		g: a.g + (b.g-a.g)*t,
		b: a.b + (b.b-a.b)*t,
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// smoothCoverage converts a signed distance d (negative inside, positive outside)
// into an anti-aliased alpha coverage in [0, 1].
func smoothCoverage(d float64) float64 {
	const edge = 0.85
	if d <= -edge {
		return 1
	}
	if d >= edge {
		return 0
	}
	t := (d + edge) / (2 * edge)
	return 1 - (t * t * (3 - 2*t))
}

func sdCircle(px, py, cx, cy, r float64) float64 {
	return math.Hypot(px-cx, py-cy) - r
}

func sdEllipse(px, py, cx, cy, rx, ry float64) float64 {
	dx := (px - cx) / rx
	dy := (py - cy) / ry
	d := math.Hypot(dx, dy)
	return (d - 1.0) * math.Min(rx, ry)
}

func sdRoundBox(px, py, cx, cy, hx, hy, r float64) float64 {
	dx := math.Abs(px-cx) - hx + r
	dy := math.Abs(py-cy) - hy + r
	ox := math.Max(dx, 0)
	oy := math.Max(dy, 0)
	return math.Hypot(ox, oy) + math.Min(math.Max(dx, dy), 0) - r
}

func sdSegment(px, py, ax, ay, bx, by, r float64) float64 {
	pax, pay := px-ax, py-ay
	bax, bay := bx-ax, by-ay
	h := clamp01((pax*bax + pay*bay) / (bax*bax + bay*bay))
	return math.Hypot(pax-bax*h, pay-bay*h) - r
}

type hairStyle int

const (
	hairBob hairStyle = iota
	hairShortPart
	hairBun
	hairWavyLong
	hairCurly
	hairFade
)

type demoAvatarStyle struct {
	bgTop      uint32
	bgBottom   uint32
	skin       uint32
	hair       uint32
	shirt      uint32
	jacket     uint32 // 0 if no outer blazer/jacket
	hairKind   hairStyle
	glasses    bool
	beard      bool
	hasTie     bool
	earrings   bool
	smileCurve float64
}

var demoAvatarStyles = [20]demoAvatarStyle{
	// 1. Astrid Lindqvist — Managing Partner
	{bgTop: 0xD8F3DC, bgBottom: 0x95D5B2, skin: 0xF4C9B1, hair: 0xD9A05B, shirt: 0xF8F9FA, jacket: 0x1D3557, hairKind: hairBob, earrings: true, smileCurve: 5.5},
	// 2. Henrik Solberg — Practice Director
	{bgTop: 0xE0ECFF, bgBottom: 0xA8C5F0, skin: 0xEFC1A6, hair: 0x4A3426, shirt: 0xEBF2FA, jacket: 0x2B2D42, hairKind: hairShortPart, glasses: true, smileCurve: 4.5},
	// 3. Ingrid Nygård — Principal Architect
	{bgTop: 0xEDE0F5, bgBottom: 0xC8B6E2, skin: 0xF5CEB8, hair: 0x3B2314, shirt: 0x2A6F62, jacket: 0, hairKind: hairBun, glasses: true, earrings: true, smileCurve: 5.0},
	// 4. Magnus Bakken — Delivery Lead
	{bgTop: 0xFFF1D6, bgBottom: 0xF4D19B, skin: 0xEBB89B, hair: 0xB88646, shirt: 0x264653, jacket: 0, hairKind: hairShortPart, beard: true, smileCurve: 5.0},
	// 5. Sofie Haugen — Finance & Operations Manager
	{bgTop: 0xFCE4EC, bgBottom: 0xF4B8C8, skin: 0xF6D0BA, hair: 0x8D3B24, shirt: 0xFFF8F6, jacket: 0x5E2129, hairKind: hairWavyLong, earrings: true, smileCurve: 5.5},
	// 6. Erik Johansen — Regional Lead West
	{bgTop: 0xD6EEF8, bgBottom: 0x90C8E0, skin: 0xE6B295, hair: 0x241C18, shirt: 0xE9ECEF, jacket: 0x344E41, hairKind: hairShortPart, beard: true, smileCurve: 4.5},
	// 7. Nora Berg — Senior Cloud Consultant
	{bgTop: 0xE8EAF6, bgBottom: 0xB6BFE8, skin: 0xF2C6AE, hair: 0x2C1E1A, shirt: 0x3F37C9, jacket: 0, hairKind: hairWavyLong, smileCurve: 6.0},
	// 8. Jakob Dahl — Senior Billing Controller
	{bgTop: 0xE6F2E0, bgBottom: 0xB5D6A7, skin: 0xEDC0A4, hair: 0x5C3D2E, shirt: 0xF1FAEE, jacket: 0x354F52, hairKind: hairShortPart, glasses: true, hasTie: true, smileCurve: 4.0},
	// 9. Emilie Lund — Lead Product Designer
	{bgTop: 0xFFE8D6, bgBottom: 0xF4BFA0, skin: 0xF7D2BD, hair: 0xB54A2A, shirt: 0xBC4749, jacket: 0, hairKind: hairBob, glasses: true, earrings: true, smileCurve: 6.0},
	// 10. Jonas Moen — Senior Platform Engineer
	{bgTop: 0xDDE7F0, bgBottom: 0x9FB8CE, skin: 0xE2AB8C, hair: 0x221A17, shirt: 0xD97706, jacket: 0, hairKind: hairCurly, beard: true, smileCurve: 5.0},
	// 11. Thea Hagen — Energy Domain Specialist
	{bgTop: 0xD9F4F6, bgBottom: 0x8ED8DE, skin: 0xF4CBB4, hair: 0xE0AC69, shirt: 0xF8F9FA, jacket: 0x006D77, hairKind: hairWavyLong, earrings: true, smileCurve: 5.5},
	// 12. Sander Lie — Senior Backend Engineer
	{bgTop: 0xE5E7EB, bgBottom: 0xB0B8C5, skin: 0xECBBA0, hair: 0x33261E, shirt: 0x4A2545, jacket: 0, hairKind: hairFade, glasses: true, smileCurve: 4.5},
	// 13. Frida Eide — Management Consultant
	{bgTop: 0xFDE2E4, bgBottom: 0xF2B5BC, skin: 0xF3C8B0, hair: 0x5A3825, shirt: 0xFFFFFF, jacket: 0x1D3557, hairKind: hairBob, smileCurve: 5.5},
	// 14. Tobias Strand — HR & Compliance Lead
	{bgTop: 0xE2ECE9, bgBottom: 0xADC7BF, skin: 0xE9B79B, hair: 0x6C6A66, shirt: 0xF4F1DE, jacket: 0x3D405B, hairKind: hairShortPart, beard: true, smileCurve: 5.0},
	// 15. Maja Holm — Customer Success Lead
	{bgTop: 0xFFF3CD, bgBottom: 0xF7D08A, skin: 0xD99B78, hair: 0x1F1715, shirt: 0xE05A47, jacket: 0, hairKind: hairCurly, earrings: true, smileCurve: 6.0},
	// 16. Oliver Aas — Data & Analytics Engineer
	{bgTop: 0xE0E7FF, bgBottom: 0xA5B4FC, skin: 0xF1C5AC, hair: 0x7A5234, shirt: 0x0F766E, jacket: 0, hairKind: hairShortPart, glasses: true, smileCurve: 4.8},
	// 17. Amalie Vik — Project Consultant
	{bgTop: 0xD1FAE5, bgBottom: 0x86EFAC, skin: 0xF6D1BC, hair: 0xD49B54, shirt: 0x1E40AF, jacket: 0, hairKind: hairBun, smileCurve: 5.8},
	// 18. Kasper Myhre — Fullstack Engineer
	{bgTop: 0xF5E6D3, bgBottom: 0xDFBE99, skin: 0xEAB99D, hair: 0x4E3424, shirt: 0x4D5E3E, jacket: 0, hairKind: hairCurly, smileCurve: 5.2},
	// 19. Hedda Bø — People & Culture Partner
	{bgTop: 0xF3E8FF, bgBottom: 0xD8B4FE, skin: 0xF3C9B2, hair: 0x231B18, shirt: 0xFAF5FF, jacket: 0xB45309, hairKind: hairBob, earrings: true, smileCurve: 5.5},
	// 20. Mathias Rask — Internal Auditor
	{bgTop: 0xE0F2FE, bgBottom: 0x93C5FD, skin: 0xEEBFA4, hair: 0x1C1917, shirt: 0xF8FAFC, jacket: 0x1E293B, hairKind: hairFade, glasses: true, hasTie: true, smileCurve: 3.8},
}

// renderDemoAvatarPNG generates a 256x256 anti-aliased portrait illustration
// for the given persona index without requiring external binary assets.
func renderDemoAvatarPNG(personaIdx int) ([]byte, error) {
	const size = 256
	style := demoAvatarStyles[personaIdx%len(demoAvatarStyles)]

	bgTop := hexRGB(style.bgTop)
	bgBot := hexRGB(style.bgBottom)
	skin := hexRGB(style.skin)
	skinShadow := skin.scale(0.86)
	skinBlush := mixRGB(skin, hexRGB(0xE57373), 0.25)
	hair := hexRGB(style.hair)
	hairDark := hair.scale(0.78)
	hairHighlight := mixRGB(hair, hexRGB(0xFFFFFF), 0.14)
	shirt := hexRGB(style.shirt)
	jacket := hexRGB(style.jacket)
	eyeColor := hexRGB(0x1E2229)
	white := hexRGB(0xFFFFFF)
	mouthColor := hexRGB(0x8C3B3B)
	gold := hexRGB(0xF59E0B)

	img := image.NewRGBA(image.Rect(0, 0, size, size))

	blend := func(dst rgb, src rgb, alpha float64) rgb {
		if alpha <= 0 {
			return dst
		}
		if alpha >= 1 {
			return src
		}
		return mixRGB(dst, src, alpha)
	}

	for y := 0; y < size; y++ {
		fy := float64(y) + 0.5
		ty := fy / float64(size)
		for x := 0; x < size; x++ {
			fx := float64(x) + 0.5

			// 1. Background gradient + subtle soft halo behind head.
			col := mixRGB(bgTop, bgBot, ty)
			haloDist := sdCircle(fx, fy, 128, 118, 92)
			if haloDist < 0 {
				col = blend(col, white, 0.12*(1.0+haloDist/92.0))
			}

			// 2. Back hair layer for long / bob / bun hairstyles.
			switch style.hairKind {
			case hairWavyLong:
				dBack := math.Min(
					sdRoundBox(fx, fy, 128, 136, 62, 68, 30),
					math.Min(
						sdCircle(fx, fy, 74, 162, 24),
						sdCircle(fx, fy, 182, 162, 24),
					),
				)
				col = blend(col, hairDark, smoothCoverage(dBack))
			case hairBob:
				dBack := sdRoundBox(fx, fy, 128, 118, 60, 50, 28)
				col = blend(col, hairDark, smoothCoverage(dBack))
			case hairBun:
				dBun := sdCircle(fx, fy, 128, 42, 24)
				col = blend(col, hairDark, smoothCoverage(dBun))
			}

			// 3. Torso / shoulders.
			dTorso := sdEllipse(fx, fy, 128, 256, 86, 72)
			torsoCov := smoothCoverage(dTorso)
			if torsoCov > 0 {
				torsoCol := shirt
				if style.jacket != 0 {
					// Outer blazer with V-opening showing shirt in the middle.
					vWidth := math.Max(0, (242.0-fy)*0.44)
					dShirtV := math.Abs(fx-128) - vWidth
					torsoCol = blend(jacket, shirt, smoothCoverage(dShirtV))
					// Subtle lapel line
					dLapel := math.Abs(math.Abs(fx-128)-vWidth-4.0) - 1.5
					if fy > 186 {
						torsoCol = blend(torsoCol, jacket.scale(0.78), smoothCoverage(dLapel)*0.7)
					}
				}
				if style.hasTie && fy > 194 {
					dTie := math.Abs(fx-128) - (5.5 + (fy-194)*0.05)
					torsoCol = blend(torsoCol, hexRGB(0x991B1B), smoothCoverage(dTie))
				}
				col = blend(col, torsoCol, torsoCov)
			}

			// 4. Neck + chin cast shadow.
			dNeck := sdRoundBox(fx, fy, 128, 174, 20, 26, 10)
			neckCov := smoothCoverage(dNeck)
			if neckCov > 0 {
				neckCol := skin
				dChinShadow := sdEllipse(fx, fy, 128, 152, 28, 20)
				neckCol = blend(neckCol, skinShadow, smoothCoverage(dChinShadow))
				col = blend(col, neckCol, neckCov)
			}

			// 5. Ears + optional earrings.
			dEarL := sdCircle(fx, fy, 78, 118, 11)
			dEarR := sdCircle(fx, fy, 178, 118, 11)
			col = blend(col, skinShadow, smoothCoverage(math.Min(dEarL, dEarR)))
			if style.earrings {
				dRingL := sdCircle(fx, fy, 77, 130, 3.2)
				dRingR := sdCircle(fx, fy, 179, 130, 3.2)
				col = blend(col, gold, smoothCoverage(math.Min(dRingL, dRingR)))
			}

			// 6. Face base.
			dFace := sdRoundBox(fx, fy, 128, 112, 48, 56, 44)
			faceCov := smoothCoverage(dFace)
			if faceCov > 0 {
				faceCol := skin
				// Subtle cheek blush.
				dBlushL := sdEllipse(fx, fy, 98, 125, 11, 6.5)
				dBlushR := sdEllipse(fx, fy, 158, 125, 11, 6.5)
				faceCol = blend(faceCol, skinBlush, smoothCoverage(math.Min(dBlushL, dBlushR))*0.55)

				// Optional beard / facial hair on lower jaw.
				if style.beard && fy > 122 {
					dBeardOuter := sdRoundBox(fx, fy, 128, 115, 48.5, 54, 44)
					dMouthCutout := sdEllipse(fx, fy, 128, 138, 18, 10)
					beardCov := smoothCoverage(dBeardOuter) * smoothCoverage(124.0-fy) * (1.0 - smoothCoverage(dMouthCutout)*0.85)
					faceCol = blend(faceCol, hairDark, beardCov*0.88)
				}
				col = blend(col, faceCol, faceCov)
			}

			// 7. Eyes, eyebrows, nose, and mouth.
			dEyeL := sdCircle(fx, fy, 107, 110, 4.3)
			dEyeR := sdCircle(fx, fy, 149, 110, 4.3)
			col = blend(col, eyeColor, smoothCoverage(math.Min(dEyeL, dEyeR)))

			dSpecL := sdCircle(fx, fy, 105.6, 108.6, 1.4)
			dSpecR := sdCircle(fx, fy, 147.6, 108.6, 1.4)
			col = blend(col, white, smoothCoverage(math.Min(dSpecL, dSpecR)))

			// Eyebrows.
			dBrowL := sdSegment(fx, fy, 98, 98, 115, 96.5, 2.2)
			dBrowR := sdSegment(fx, fy, 141, 96.5, 158, 98, 2.2)
			col = blend(col, hairDark, smoothCoverage(math.Min(dBrowL, dBrowR)))

			// Nose shadow.
			dNose := sdSegment(fx, fy, 128, 110, 126, 123, 2.0)
			dNoseTip := sdSegment(fx, fy, 126, 123, 131, 124, 2.0)
			col = blend(col, skinShadow.scale(0.92), smoothCoverage(math.Min(dNose, dNoseTip))*0.8)

			// Warm smile arc.
			if fx >= 114 && fx <= 142 {
				nx := (fx - 128.0) / 14.0
				smileY := 136.0 + style.smileCurve*(1.0-nx*nx)
				dSmile := math.Hypot(0, fy-smileY) - 2.1
				// Taper ends of smile.
				if math.Abs(nx) <= 1.0 {
					col = blend(col, mouthColor, smoothCoverage(dSmile))
				}
			}

			// 8. Optional glasses.
			if style.glasses {
				frameCol := hexRGB(0x2B2D42)
				dLensL := math.Abs(sdRoundBox(fx, fy, 107, 110, 14, 10, 5)) - 1.6
				dLensR := math.Abs(sdRoundBox(fx, fy, 149, 110, 14, 10, 5)) - 1.6
				dBridge := sdSegment(fx, fy, 121, 109, 135, 109, 1.5)
				dTempleL := sdSegment(fx, fy, 80, 109, 93, 109, 1.4)
				dTempleR := sdSegment(fx, fy, 163, 109, 176, 109, 1.4)
				dGlass := math.Min(math.Min(dLensL, dLensR), math.Min(dBridge, math.Min(dTempleL, dTempleR)))
				col = blend(col, frameCol, smoothCoverage(dGlass))
			}

			// 9. Front hair / top hair layer.
			dHairFront := 1e6
			switch style.hairKind {
			case hairBob, hairWavyLong:
				// Side-swept bangs across the forehead.
				dTop := sdEllipse(fx, fy, 128, 74, 52, 24)
				dSweepL := sdEllipse(fx, fy, 96, 84, 32, 20)
				dSweepR := sdEllipse(fx, fy, 160, 86, 26, 18)
				dHairFront = math.Min(dTop, math.Min(dSweepL, dSweepR))
			case hairBun:
				dTop := sdEllipse(fx, fy, 128, 73, 50, 22)
				dPartL := sdEllipse(fx, fy, 98, 82, 26, 14)
				dPartR := sdEllipse(fx, fy, 158, 82, 26, 14)
				dHairFront = math.Min(dTop, math.Min(dPartL, dPartR))
			case hairShortPart:
				dTop := sdRoundBox(fx, fy, 128, 72, 50, 19, 16)
				dSideL := sdRoundBox(fx, fy, 82, 92, 6, 18, 5)
				dSideR := sdRoundBox(fx, fy, 174, 92, 6, 18, 5)
				dHairFront = math.Min(dTop, math.Min(dSideL, dSideR))
			case hairCurly:
				dHairFront = math.Min(
					sdCircle(fx, fy, 128, 66, 24),
					math.Min(
						math.Min(sdCircle(fx, fy, 100, 72, 21), sdCircle(fx, fy, 156, 72, 21)),
						math.Min(sdCircle(fx, fy, 84, 88, 16), sdCircle(fx, fy, 172, 88, 16)),
					),
				)
			case hairFade:
				dTop := sdRoundBox(fx, fy, 128, 70, 47, 16, 14)
				dHairFront = dTop
			}
			hairCov := smoothCoverage(dHairFront)
			if hairCov > 0 {
				hCol := hair
				// Subtle top shine highlight.
				dShine := sdEllipse(fx, fy, 118, 64, 24, 7)
				hCol = blend(hCol, hairHighlight, smoothCoverage(dShine)*0.6)
				col = blend(col, hCol, hairCov)
			}

			img.SetRGBA(x, y, color.RGBA{
				R: uint8(math.Round(clamp01(col.r) * 255)),
				G: uint8(math.Round(clamp01(col.g) * 255)),
				B: uint8(math.Round(clamp01(col.b) * 255)),
				A: 255,
			})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func seedDemoAvatars(ctx context.Context, tx pgx.Tx, now time.Time, userIDs []uuid.UUID) error {
	for i, uid := range userIDs {
		pngBytes, err := renderDemoAvatarPNG(i)
		if err != nil {
			return fmt.Errorf("render demo avatar %d: %w", i, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO identity.profile_avatars (user_id, data, content_type, version, updated_at)
			VALUES ($1, $2, 'image/png', 1, $3)
			ON CONFLICT (user_id) DO NOTHING
		`, uid, pngBytes, now); err != nil {
			return fmt.Errorf("insert demo avatar %d: %w", i, err)
		}
	}
	return nil
}

func backfillDemoAvatarsIfMissing(ctx context.Context, tx pgx.Tx, now time.Time) error {
	for i, m := range demoStaffMembers {
		var uid uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT id FROM identity.users WHERE normalized_email = $1
		`, strings.ToUpper(m.email)).Scan(&uid)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}

		var hasAvatar bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM identity.profile_avatars WHERE user_id = $1)
		`, uid).Scan(&hasAvatar); err != nil {
			return err
		}
		if hasAvatar {
			continue
		}

		pngBytes, err := renderDemoAvatarPNG(i)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO identity.profile_avatars (user_id, data, content_type, version, updated_at)
			VALUES ($1, $2, 'image/png', 1, $3)
			ON CONFLICT (user_id) DO NOTHING
		`, uid, pngBytes, now); err != nil {
			return err
		}
	}
	return nil
}
