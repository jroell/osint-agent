package tools

import (
	"context"
	"strings"
	"testing"
)

// TestEntityMatch_SocialCanonicalizeDedup is the proof-of-improvement
// test for iteration 16.
//
// The defect (third in the dedup-primitive trilogy after iter-14 email
// and iter-15 phone): tools that surface social-platform identities
// (sherlock, maigret, holehe, ghunt, person_aggregate, panel_entity_resolution,
// keybase, gravatar, twitter_user, twitter_rapidapi, instagram_user,
// instagram_rapidapi, tiktok_lookup, github_user, reddit_user_intel,
// linkedin_proxycurl, bluesky, mastodon, threads, hackernews_user_intel)
// emit handles in many surface forms — bare "@johndoe", "JohnDoe",
// profile URLs ("https://twitter.com/JohnDoe", "https://x.com/johndoe/",
// "twitter.com/johndoe?ref=foo", "https://twitter.com/intent/user?screen_name=JohnDoe"),
// platform-specific shortcuts ("u/spez", "/u/spez"), and Mastodon-style
// fediverse handles. person_aggregate dedups by literal string, so the
// same identity shows up as 3-6 distinct findings, breaking the
// cross-platform graph join the social-graph layer needs.
//
// The fix: a new social_canonicalize mode on entity_match that:
//   - parses URLs (schemed and scheme-less) and extracts platform + handle
//   - handles intent / share / mobile / nitter / x.com mirror hosts
//   - rejects non-profile URL shapes (twitter /home, /search, github /settings)
//   - lowercases handles for case-insensitive platforms (almost all)
//   - emits "<platform>:<handle>" as the strongest dedup primary key
//
// Quantitative metric: dedup count on a curated fixture where 8 unique
// social identities are written in 28 surface forms. Before:
// ~26-28 distinct strings (lowercase+trim collapses only pure-case).
// After: exactly 8 distinct keys.
func TestEntityMatch_SocialCanonicalizeDedup(t *testing.T) {
	groups := [][]string{
		// 1. Twitter/X handle "JohnDoe" in 5 URL-shaped surface forms.
		// Bare "@JohnDoe" is intentionally excluded — without a platform
		// hint it correctly resolves to "unknown:johndoe", because a bare
		// handle is genuinely ambiguous between Twitter / IG / TikTok / etc.
		{
			"https://twitter.com/JohnDoe",
			"https://x.com/JohnDoe",
			"https://www.twitter.com/JohnDoe/",
			"twitter.com/johndoe",
			"https://twitter.com/intent/user?screen_name=JohnDoe",
		},
		// 2. Instagram "PhotoFan99" in 3 URL-shaped surface forms.
		{
			"https://instagram.com/PhotoFan99",
			"https://www.instagram.com/photofan99/",
			"https://instagram.com/photofan99?hl=en",
		},
		// 3. TikTok "@dancer.dan" in 3 forms.
		{
			"https://www.tiktok.com/@dancer.dan",
			"https://tiktok.com/@Dancer.Dan",
			"https://www.tiktok.com/@dancer.dan/video/7123456789",
		},
		// 4. LinkedIn slug in 3 forms.
		{
			"https://www.linkedin.com/in/john-doe-12345/",
			"https://linkedin.com/in/JOHN-DOE-12345",
			"linkedin.com/in/john-doe-12345/details/experience",
		},
		// 5. GitHub user in 4 forms.
		{
			"https://github.com/Octocat",
			"https://github.com/octocat/",
			"github.com/octocat",
			"http://www.github.com/Octocat",
		},
		// 6. Reddit "spez" in 4 forms.
		{
			"https://www.reddit.com/user/spez",
			"https://old.reddit.com/u/SPEZ",
			"u/spez",
			"/u/Spez",
		},
		// 7. Mastodon "@gargron@mastodon.social" in 3 forms.
		{
			"@Gargron@mastodon.social",
			"gargron@mastodon.social",
			"https://mastodon.social/@gargron",
		},
		// 8. Bluesky handle (DID-like FQDN form).
		{
			"https://bsky.app/profile/jay.bsky.team",
			"https://bsky.app/profile/JAY.bsky.team",
		},
	}

	expectedKeys := len(groups) // 8

	allInputs := []string{}
	for _, g := range groups {
		allInputs = append(allInputs, g...)
	}

	// LEGACY: lowercase + trim — most generous baseline; insufficient.
	legacy := map[string]bool{}
	for _, s := range allInputs {
		legacy[strings.ToLower(strings.TrimSpace(s))] = true
	}

	// NEW: route through entity_match.social_canonicalize.
	canonical := map[string]bool{}
	groupHits := make([]map[string]bool, len(groups))
	for i := range groupHits {
		groupHits[i] = map[string]bool{}
	}
	failedToParse := []string{}

	for gi, g := range groups {
		for _, s := range g {
			res, err := EntityMatch(context.Background(), map[string]any{
				"mode":   "social_canonicalize",
				"social": s,
			})
			if err != nil {
				t.Fatalf("EntityMatch(%q): %v", s, err)
			}
			if !res.SocialValid {
				failedToParse = append(failedToParse, s)
				continue
			}
			canonical[res.SocialKey] = true
			groupHits[gi][res.SocialKey] = true
		}
	}

	t.Logf("Social canonicalization dedup on %d surface forms (%d true identities):",
		len(allInputs), expectedKeys)
	t.Logf("  legacy   (ToLower+TrimSpace only):       %d distinct", len(legacy))
	t.Logf("  new      (entity_match social-canonical): %d distinct", len(canonical))
	dedupRate := 1.0 - float64(len(canonical))/float64(len(legacy))
	t.Logf("  dedup-rate improvement:                  %.1f%% reduction in apparent identity count", dedupRate*100)

	for gi, hits := range groupHits {
		if len(hits) > 1 {
			collapsed := []string{}
			for k := range hits {
				collapsed = append(collapsed, k)
			}
			t.Errorf("group %d (e.g. %q) split into %d distinct keys: %v",
				gi, groups[gi][0], len(hits), collapsed)
		} else if len(hits) == 0 {
			t.Errorf("group %d (e.g. %q) produced no canonical key — every variant failed to parse",
				gi, groups[gi][0])
		}
	}

	if len(canonical) != expectedKeys {
		t.Errorf("got %d canonical keys, want exactly %d", len(canonical), expectedKeys)
	}
	if len(failedToParse) > 0 {
		t.Errorf("failed to parse: %v", failedToParse)
	}
	if len(canonical) >= len(legacy) {
		t.Errorf("canonicalization produced no improvement over legacy (%d ≥ %d)",
			len(canonical), len(legacy))
	}
	if len(legacy) < 3*len(canonical) {
		t.Errorf("legacy distinct count %d < 3× canonical (%d) — fixture isn't multi-form enough",
			len(legacy), len(canonical))
	}
}

// TestEntityMatch_SocialCanonicalize_PlatformExtraction pins exact
// platform+handle resolution for representative cases. This is what
// the cross-platform graph layer actually keys on — getting it wrong
// silently merges or splits identities.
func TestEntityMatch_SocialCanonicalize_PlatformExtraction(t *testing.T) {
	type want struct {
		platform string
		handle   string
		valid    bool
	}
	cases := map[string]want{
		// Twitter/X
		"https://twitter.com/JohnDoe":                         {"twitter", "johndoe", true},
		"https://x.com/JohnDoe":                               {"twitter", "johndoe", true},
		"https://twitter.com/intent/user?screen_name=JohnDoe": {"twitter", "johndoe", true},
		"https://twitter.com/home":                            {"", "", false}, // reserved
		// Instagram
		"https://instagram.com/photofan99": {"instagram", "photofan99", true},
		"https://instagram.com/p/abc123":   {"", "", false}, // post, not profile
		// TikTok
		"https://www.tiktok.com/@dancer.dan": {"tiktok", "dancer.dan", true},
		"https://www.tiktok.com/discover":    {"", "", false},
		// LinkedIn
		"https://www.linkedin.com/in/john-doe-12345/": {"linkedin", "john-doe-12345", true},
		"https://www.linkedin.com/company/anthropic":  {"linkedin-company", "anthropic", true},
		// GitHub
		"https://github.com/octocat":  {"github", "octocat", true},
		"https://github.com/settings": {"", "", false},
		// Reddit
		"https://www.reddit.com/user/spez": {"reddit", "spez", true},
		"https://old.reddit.com/u/Spez":    {"reddit", "spez", true},
		"https://reddit.com/r/programming": {"", "", false},
		// YouTube
		"https://www.youtube.com/@MrBeast":                         {"youtube", "mrbeast", true},
		"https://www.youtube.com/channel/UCX6OQ3DkcsbYNE6H8uQQuVA": {"youtube-channel", "UCX6OQ3DkcsbYNE6H8uQQuVA", true},
		// Mastodon
		"@gargron@mastodon.social":         {"mastodon", "gargron@mastodon.social", true},
		"https://mastodon.social/@gargron": {"mastodon", "gargron@mastodon.social", true},
		// Bluesky
		"https://bsky.app/profile/jay.bsky.team": {"bluesky", "jay.bsky.team", true},
		// Bare handle without hint → unknown
		"@somebody": {"unknown", "somebody", true},
	}
	for in, w := range cases {
		res, err := EntityMatch(context.Background(), map[string]any{
			"mode":   "social_canonicalize",
			"social": in,
		})
		if err != nil {
			t.Errorf("%q: unexpected error %v", in, err)
			continue
		}
		if res.SocialValid != w.valid {
			t.Errorf("%q: valid=%v, want %v (got platform=%q handle=%q)",
				in, res.SocialValid, w.valid, res.SocialPlatform, res.SocialHandle)
			continue
		}
		if !w.valid {
			continue
		}
		if res.SocialPlatform != w.platform {
			t.Errorf("%q: platform=%q, want %q", in, res.SocialPlatform, w.platform)
		}
		if res.SocialHandle != w.handle {
			t.Errorf("%q: handle=%q, want %q", in, res.SocialHandle, w.handle)
		}
	}
}

// TestEntityMatch_SocialCanonicalize_PlatformHint exercises the hint
// path where the input is a bare handle and the platform is supplied
// explicitly (e.g., from the calling tool's known context).
func TestEntityMatch_SocialCanonicalize_PlatformHint(t *testing.T) {
	cases := []struct {
		raw      string
		hint     string
		platform string
		key      string
	}{
		{"@JohnDoe", "twitter", "twitter", "twitter:johndoe"},
		{"JohnDoe", "twitter", "twitter", "twitter:johndoe"},
		{"@octocat", "github", "github", "github:octocat"},
		{"spez", "reddit", "reddit", "reddit:spez"},
	}
	for _, c := range cases {
		res, err := EntityMatch(context.Background(), map[string]any{
			"mode":     "social_canonicalize",
			"social":   c.raw,
			"platform": c.hint,
		})
		if err != nil {
			t.Errorf("%q (hint=%s): %v", c.raw, c.hint, err)
			continue
		}
		if !res.SocialValid || res.SocialPlatform != c.platform || res.SocialKey != c.key {
			t.Errorf("%q (hint=%s): valid=%v platform=%q key=%q, want valid=true platform=%q key=%q",
				c.raw, c.hint, res.SocialValid, res.SocialPlatform, res.SocialKey, c.platform, c.key)
		}
	}
}
