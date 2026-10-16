package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

//go:embed avatars/*.jpg
var demoAvatarsFS embed.FS

var (
	demoAvatarOnce  sync.Once
	demoAvatarFiles [][]byte
	demoAvatarErr   error
)

func loadDemoAvatars() ([][]byte, error) {
	demoAvatarOnce.Do(func() {
		entries, err := fs.ReadDir(demoAvatarsFS, "avatars")
		if err != nil {
			demoAvatarErr = fmt.Errorf("read embedded avatars dir: %w", err)
			return
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jpg") {
				continue
			}
			data, err := demoAvatarsFS.ReadFile("avatars/" + entry.Name())
			if err != nil {
				demoAvatarErr = fmt.Errorf("read embedded avatar %s: %w", entry.Name(), err)
				return
			}
			demoAvatarFiles = append(demoAvatarFiles, data)
		}
		if len(demoAvatarFiles) < len(demoStaffMembers) {
			demoAvatarErr = fmt.Errorf("embedded avatars count = %d, want at least %d", len(demoAvatarFiles), len(demoStaffMembers))
		}
	})
	return demoAvatarFiles, demoAvatarErr
}

func demoAvatarBytes(personaIdx int) ([]byte, error) {
	avatars, err := loadDemoAvatars()
	if err != nil {
		return nil, err
	}
	return avatars[personaIdx%len(avatars)], nil
}

func seedDemoAvatars(ctx context.Context, tx pgx.Tx, now time.Time, userIDs []uuid.UUID) error {
	for i, uid := range userIDs {
		jpgBytes, err := demoAvatarBytes(i)
		if err != nil {
			return fmt.Errorf("load demo avatar %d: %w", i, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO identity.profile_avatars (user_id, data, content_type, version, updated_at)
			VALUES ($1, $2, 'image/jpeg', 1, $3)
			ON CONFLICT (user_id) DO UPDATE
			SET data = EXCLUDED.data,
			    content_type = EXCLUDED.content_type,
			    version = identity.profile_avatars.version + 1,
			    updated_at = EXCLUDED.updated_at
		`, uid, jpgBytes, now); err != nil {
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

		var contentType string
		err = tx.QueryRow(ctx, `
			SELECT content_type FROM identity.profile_avatars WHERE user_id = $1
		`, uid).Scan(&contentType)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && contentType == "image/jpeg" {
			continue
		}

		jpgBytes, err := demoAvatarBytes(i)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO identity.profile_avatars (user_id, data, content_type, version, updated_at)
			VALUES ($1, $2, 'image/jpeg', 1, $3)
			ON CONFLICT (user_id) DO UPDATE
			SET data = EXCLUDED.data,
			    content_type = EXCLUDED.content_type,
			    version = identity.profile_avatars.version + 1,
			    updated_at = EXCLUDED.updated_at
		`, uid, jpgBytes, now); err != nil {
			return err
		}
	}
	return nil
}
