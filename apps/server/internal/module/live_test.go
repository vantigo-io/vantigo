package module

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/live"
)

func allowTopic(kind string, presence bool) live.Topic {
	return live.Topic{Kind: kind, Presence: presence, Authorize: func(context.Context, string) error { return nil }}
}

// The enabled modules' topics land in the registry the caller preset, with
// the user directory; a disabled module's do not (live design L9).
func TestCompose_CollectsTheEnabledModulesLiveTopics(t *testing.T) {
	reg := live.NewRegistry()
	_, err := compose(
		Deps{Access: &fakeAccess{}, Config: &config.Config{Modules: []string{"alpha"}}, Live: reg},
		fakeLoad(map[string]string{"alpha": alphaContract, "beta": betaContract}),
		Module{Name: "alpha", Mount: staticHandler("alpha"), LiveTopics: func(d Deps) []live.Topic {
			if d.Live != reg {
				t.Error("LiveTopics did not get the preset registry on its Deps")
			}
			return []live.Topic{allowTopic("alpha.list", false), allowTopic("alpha.item", true)}
		}},
		Module{Name: "beta", Mount: staticHandler("beta"), LiveTopics: func(Deps) []live.Topic {
			return []live.Topic{allowTopic("beta.list", false)}
		}},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	got := reg.Kinds()
	slices.Sort(got)
	if want := []string{"alpha.item", "alpha.list"}; !slices.Equal(got, want) {
		t.Errorf("kinds = %v, want %v", got, want)
	}
}

func TestCompose_RefusesABadLiveTopic(t *testing.T) {
	for name, tc := range map[string]struct {
		mods []Module
		want string
	}{
		"another module's prefix": {
			mods: []Module{{Name: "alpha", Mount: staticHandler("alpha"), LiveTopics: func(Deps) []live.Topic {
				return []live.Topic{allowTopic("beta.list", false)}
			}}},
			want: "not prefixed by its module",
		},
		"outside the grammar": {
			mods: []Module{{Name: "alpha", Mount: staticHandler("alpha"), LiveTopics: func(Deps) []live.Topic {
				return []live.Topic{allowTopic("alpha.List", false)}
			}}},
			want: "does not follow",
		},
		"declared twice": {
			mods: []Module{{Name: "alpha", Mount: staticHandler("alpha"), LiveTopics: func(Deps) []live.Topic {
				return []live.Topic{allowTopic("alpha.list", false), allowTopic("alpha.list", true)}
			}}},
			want: "duplicate topic kind",
		},
		"no Authorize": {
			mods: []Module{{Name: "alpha", Mount: staticHandler("alpha"), LiveTopics: func(Deps) []live.Topic {
				return []live.Topic{{Kind: "alpha.list"}}
			}}},
			want: "has no Authorize",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(map[string]string{"alpha": alphaContract}), tc.mods...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("compose: err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}
