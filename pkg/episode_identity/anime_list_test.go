package episode_identity

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const fairyTailAnimeListFixture = `<?xml version="1.0" encoding="utf-8"?>
<anime-list>
  <anime anidbid="6662" tvdbid="114801" defaulttvdbseason="a" tmdbtv="46261" tmdbseason="a">
    <name>Fairy Tail</name>
    <mapping-list>
      <mapping anidbseason="1" tvdbseason="1" start="1" end="48" offset="0"/>
      <mapping anidbseason="1" tvdbseason="2" start="49" end="96" offset="-48"/>
    </mapping-list>
  </anime>
  <anime anidbid="13295" tvdbid="114801" defaulttvdbseason="a" episodeoffset="277" tmdbtv="46261" tmdbseason="a">
    <name>Fairy Tail (2018)</name>
    <mapping-list>
      <mapping anidbseason="1" tvdbseason="8" start="1" end="51" offset="0"/>
    </mapping-list>
  </anime>
</anime-list>`

func TestAnimeListResolverMapsFairyTailS08E11ToAbsolute288(t *testing.T) {
	resolver, err := ParseAnimeList(strings.NewReader(fairyTailAnimeListFixture))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := resolver.Resolve(context.Background(), Request{
		IDs: ExternalIDs{TMDB: "46261"}, Season: 8, Episode: 11,
	})
	if err != nil {
		t.Fatal(err)
	}
	if identity.AbsoluteEpisode != 288 || identity.IDs.AniDB != "13295" || identity.Confidence != 1 {
		t.Fatalf("unexpected identity: %#v", identity)
	}
}

func TestAnimeListResolverInvertsSeasonOffset(t *testing.T) {
	resolver, err := ParseAnimeList(strings.NewReader(fairyTailAnimeListFixture))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := resolver.Resolve(context.Background(), Request{
		IDs: ExternalIDs{TVDB: "114801"}, Season: 2, Episode: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if identity.AbsoluteEpisode != 51 {
		t.Fatalf("absolute episode = %d, want 51", identity.AbsoluteEpisode)
	}
}

func TestAnimeListResolverAbstainsWithoutMapping(t *testing.T) {
	resolver, err := ParseAnimeList(strings.NewReader(fairyTailAnimeListFixture))
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolver.Resolve(context.Background(), Request{
		IDs: ExternalIDs{TMDB: "46261"}, Season: 9, Episode: 1,
	})
	if !errors.Is(err, ErrNoMapping) {
		t.Fatalf("error = %v, want ErrNoMapping", err)
	}
}

func TestAnimeListResolverFallsBackToNormalizedTitle(t *testing.T) {
	resolver, err := ParseAnimeList(strings.NewReader(fairyTailAnimeListFixture))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := resolver.Resolve(context.Background(), Request{
		SeriesName: "Fairy Tail (2018)", Season: 8, Episode: 11,
	})
	if err != nil {
		t.Fatal(err)
	}
	if identity.AbsoluteEpisode != 288 || identity.Confidence != 0.92 || identity.IDs.AniDB != "13295" {
		t.Fatalf("unexpected title identity: %#v", identity)
	}
}
