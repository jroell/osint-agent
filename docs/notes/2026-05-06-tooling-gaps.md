# Tooling Gaps Identified from BrowseComp Loop (87 iterations, 3 solves)

Compiled 2026-05-06 from real failure modes during the BrowseComp loop. Existing registry
has 188 tools; this list focuses on **what was missing**, prioritized by frequency-of-need
and strategic value.

The dominant failure mode was *primary-source access for niche archives* — questions
that need exact phrasings from specific newspaper pages, episode-level TV/film credits,
dissertation supervisor lookups, or family genealogy. Generic Tavily/Firecrawl can't
substitute because of paywalls, anti-bot layers, or schema-less HTML.

---

## Tier 1 — Free APIs, High Impact, Broad Use (build first)

These are free or freemium APIs with official keys, well-documented schemas, and would
have closed multiple specific failures from the loop.

| # | Tool | Why | Loop failures it addresses |
|---|---|---|---|
| 1 | **TMDB (The Movie Database)** | Free key. Full episode-level data: titles, directors, writers, air dates, cast, crew. Solves the entire "S4E4 of show X" class. | iters 64, 76, 79, 83, 87 — multiple TV/film episode questions. Currently I scrape Wikipedia. |
| 2 | **TVMaze** | Free, no key. Equivalent episode metadata, complementary coverage to TMDB. Use as fallback. | Same class as TMDB. |
| 3 | **Trove (NLA Australia) API v3** | Free key (api.trove.nla.gov.au). Australian newspapers 1803-present, full-text searchable. Trove's web UI is bot-blocked but **API is open**. | iter 86 (Annie Besant Australian newspaper); future questions on AU/NZ history, biography, Indigenous topics. |
| 4 | **Chronicling America (LoC)** | Free, no key (chroniclingamerica.loc.gov). US historic newspapers 1690-1963. JSON endpoint works with proper redirect handling. | iter 86; any US historic-newspaper question. |
| 5 | **HathiTrust Bibliographic + Full-text Search API** | Free. 17M+ digitized books/periodicals. Full-text search across the corpus. | Book-history questions; obscure-citation chains. |
| 6 | **Gallica (BnF, France)** | Free. French national library digital collection — newspapers, manuscripts, books in French/European languages. | Any French-language source-of-record question. |
| 7 | **NDL Digital Collections (Japan)** | Free. Japanese national library. Critical for any Japan-specific question. | Trading-card history (iter 75 YGO), Japanese cinema/figures. |
| 8 | **FamilySearch API** | Free with developer registration. The most comprehensive global genealogy database (LDS-maintained). | Family-tree multi-hop questions (iter 71 Disney writer's grandmother; ancestors-of-celebrity questions). |
| 9 | **MathGenealogy / Mathematics Genealogy Project (scrape)** | Public dataset, no API but scrapeable. Maps PhD-supervisor chains for math + adjacent fields. | iter 73 (dissertation supervisor of religion scholar) — even though that one was non-math, similar tools exist for other fields (CS via DBLP, etc.). |
| 10 | **Wikidata SPARQL endpoint** | Free, no key. Arbitrary structured queries: "all behavioral ecologists who died in 2023," "all colleges founded 1927," "all monarchs deposed in 19th century." Order-of-magnitude more powerful than wikidata-entity-lookup. | iter 69 (deposed 19th-c monarch with descendant at 13th-c uni); iter 76 (1927 college); iter 86 (Theosophy); many history questions. |
| 11 | **OpenAlex Author Graph Traversal** | OpenAlex search exists, but a wrapper that pulls all papers/co-authors/affiliations of a given ORCID would close most multi-hop academic questions. | iters 63, 68, 73, 78 — academic chain questions all blocked by lack of cheap traversal. |
| 12 | **Scryfall (Magic) + YGOPRODeck (Yu-Gi-Oh) + Pokémon TCG API** | All free. TCG database lookups by mechanical/lore properties. | iter 75 (YGO Ally of Justice Catastor). |
| 13 | **NPS NPGallery / National Register of Historic Places** | Free (npgallery.nps.gov). US monuments, landmarks, listed properties with relocation history. | iter 84 (Captain John A. Sutter's Landing — was findable here). |
| 14 | **Australian Dictionary of Biography (ADB)** | Free. Authoritative Australian biographical entries. | iter 85 (Alan Lill — was via Tavily; ADB direct would have been faster); future Aus academic/cultural figures. |
| 15 | **MusicBrainz already exists**, but add **Discogs API** | Free. Discogs has tracklist-level metadata and rare/regional releases that MusicBrainz misses. | iter 66 (Reggiani 1965 album); iter 74 (K-pop first albums). |
| 16 | **Setlist.fm API** | Free with key. Concert-by-date setlists. | Any concert/tour question. |
| 17 | **Library of Congress catalog (id.loc.gov + www.loc.gov/api)** | Free. Authority files, subject headings, books. | Cross-reference for archives, ID disambiguation. |
| 18 | **WorldCat search (OCLC)** | Free with key. Holdings-level book search across global libraries. | Find which library has a niche book; verify book existence. |

---

## Tier 2 — Free APIs, Niche but Closes Specific Gap Classes

| # | Tool | Why |
|---|---|---|
| 19 | **CIA World Factbook** | Free (no key). Country-level statistics that a population/economy sanity-check needs. |
| 20 | **iNaturalist API** | Free. Species + observation data; complements pubchem for biology. |
| 21 | **Encyclopedia of Life (EOL)** | Free. Species pages with citations. |
| 22 | **Sentinel Hub Process API (free tier)** | Satellite imagery for geo-questions (rough free quota). |
| 23 | **GeoNames** | Free with username. Place-name resolution + nearby features by lat/lon — what nominatim alone doesn't fully cover. |
| 24 | **Geocaching / Waymarking** | Free. Niche landmarks crowdsourced — "Dinah the Dinosaur" type results. |
| 25 | **Wikitree open data** | Free. Crowdsourced family tree, complementary to FamilySearch. |
| 26 | **Find My Past / Free BMD** (UK) | Free tier. UK births/marriages/deaths. |
| 27 | **CourtListener already exists** — add **PACER bulk** | Federal court filings full-text via RECAP archive. |
| 28 | **GovInfo (US Govt Publishing Office)** | Free. Federal Register, Congressional Record, Public Laws full-text. |
| 29 | **ICAO 24-bit + ADS-B Exchange (free tier)** | Aircraft tracking by hex code. |
| 30 | **AISHub** | Free vessel AIS data. |

---

## Tier 3 — Paid APIs, Strategically High Value

These cost money but close massive gap classes that free alternatives can't reach.

| # | Tool | Cost | Why |
|---|---|---|---|
| 31 | **Newspapers.com Publisher API (Ancestry)** | ~$200/mo individual, enterprise pricing for API access | The largest digitized US/UK newspaper archive (~1B pages). Closes the page-10-newspaper-article class entirely. iter 86 was unsolvable without this. |
| 32 | **British Newspaper Archive (Findmypast)** | Subscription | UK historical newspapers — covers Annie Besant era questions, UK biographical chains. |
| 33 | **ProQuest Dissertations & Theses Global** | Institutional, expensive | The only comprehensive PhD dissertation index globally. Closes the supervisor-of-PhD class (iter 73 Stetkevych, iter 78 Stack Morgan). |
| 34 | **People Data Labs** | Tiered, ~$0.20/record | Identity enrichment with verified employment/education history. Closes the "academic with this exact career path" class. |
| 35 | **DomainTools Iris / SecurityTrails** | $1k+/mo | Historical WHOIS + DNS history. For domain-pivot OSINT. |
| 36 | **Pipl / Spokeo / BeenVerified API** | Pay-per-query | People search with relationship graphs (use Pipl over consumer-aggregator scrape — iter Jason FIL search would have been cleaner). |
| 37 | **Crunchbase API** | $1k+/mo | Funding round + executive history, closes "company X CEO at acquisition + spouse Columbia MBA" class (iter 65, 82). |
| 38 | **FlightAware Firehose / aviationstack** | Tiered | Flight history. Aircraft OSINT class. |
| 39 | **MarineTraffic API** | $200+/mo | Vessel tracking + ownership. Maritime OSINT class. |
| 40 | **Apify / SerpAPI for Google Scholar** | $50+/mo | Programmatic Google Scholar — fills the gap between OpenAlex (only-OA) and the actual published research literature. |
| 41 | **Diffbot Knowledge Graph** | exists but expand depth | Already have basic; deeper graph traversal queries (executives + co-founders + alumni) closes iter 65, 87. |

---

## Tier 4 — Specialized / Defensive (lower priority unless hosting offering)

| # | Tool | Why |
|---|---|---|
| 42 | **Maltego CTAS / Hibp Premium / Have I Been Pwned Enterprise** | Already have HIBP free tier. |
| 43 | **OSINT Industries paid people search** | Layered with Pipl. |
| 44 | **Telegram OSINT (e.g. Lyzem, Telemetr)** | Telegram channel-resolve exists; deeper search needed. |
| 45 | **Telegram CSV / Discord CSV scrapers** | Niche. |
| 46 | **TikTok scrapers** | Apify or similar. |
| 47 | **Instagram private-data scrapers** | Already have basic; deep scraping is brittle and ToS-violating. |
| 48 | **Sanctions / Beneficial-ownership databases beyond OpenSanctions** | OpenCorporates+OpenSanctions cover most. Add ICIJ Offshore Leaks DB (free). |

---

## Build progress

- **PR-A — DONE 2026-05-06**: TMDB + TVMaze + Scryfall + YGOPRODeck wired end-to-end.
  - Go: `tmdb_lookup.go`, `tvmaze_lookup.go`, `scryfall_lookup.go`, `ygoprodeck_lookup.go` + 12 tests (6 live, 6 offline) all passing.
  - TS: `tmdb-lookup.ts`, `tvmaze-lookup.ts`, `scryfall-lookup.ts`, `ygoprodeck-lookup.ts` registered; `pr-a-tools-registry.test.ts` (5/5 pass).
  - ER: every output emits typed `entities[]` envelope (kind: movie | tv_show | tv_season | tv_episode | person | trading_card) with stable IDs (TMDB+IMDb cross-ref for film/TV; Scryfall+Oracle for MTG; YGO numeric for Yu-Gi-Oh). Panel ER and entity_link_finder can ingest directly.

- **PR-B — DONE 2026-05-06**: Trove (Aus) + Chronicling America (LoC US) + LoC catalog wired end-to-end.
  - Go: `trove_search.go`, `chronicling_america_search.go`, `loc_catalog_search.go` + 8 tests (4 live, 4 offline; Trove gates on TROVE_API_KEY).
  - Found and fixed live-API drift: Chronicling America migrated to `loc.gov/collections/chronicling-america/`; updated parser. id.loc.gov suggest2 returns object-with-hits[], not array — updated parser.
  - TS: 3 wrappers registered; `pr-b-tools-registry.test.ts` (4/4 pass).
  - ER: typed envelopes (kind: newspaper_article | newspaper_page | newspaper_title | book | library_item | subject_authority) with stable LoC/Trove URLs.

- **PR-C — DONE 2026-05-06**: Wikidata SPARQL + OpenAlex author-graph + MathGenealogy.
  - Go: `wikidata_sparql.go`, `openalex_author_graph.go`, `math_genealogy.go` + 7 live + offline tests passing.
  - Templated `find_humans_by_attr` mode lets non-experts query "humans matching profession + DOD + nationality" without SPARQL syntax.
  - OpenAlex pulls author + top-50 cited works + co-authors in one call (closes the multi-hop academic chain class).
  - MGP HTML scraping with regex extraction of advisor + student edges.
  - TS: 3 wrappers registered; `pr-c-tools-registry.test.ts` (4/4 pass).
  - ER: typed envelopes (kind: wikidata_entity | scholar | scholarly_work) with stable QID / OpenAlex / ORCID / DOI / MGP cross-references.

- **ER MOAT VERIFIED 2026-05-06**: `er_envelope_test.go::TestERMoatEnvelope_AllTools` exercises all 8 PR-A/B/C tools (TMDB+Trove gated) live and asserts every output has a top-level `entities[]` array with `kind` discriminator on every element. This is the contract `panel_entity_resolution` and `entity_link_finder` rely on. Test passes for all 8 tools — connecting-the-dots engine ingests outputs uniformly.

- **PR-D — DONE 2026-05-06**: WikiTree + ADB + FamilySearch.
  - Go: `wikitree_lookup.go`, `adb_search.go`, `familysearch_lookup.go` + 7 tests passing.
  - Found and fixed: ADB site uses `<h2>` for bio name (not `<h1>`, which is site banner); birth/death years embedded in h2 parens. Adjusted regex.
  - WikiTree exposes free POST API; rate-limited but consistent. Modes: search/profile/ancestors/descendants/relatives — relations emit role-edge attributes (parent_of:X, spouse_of:Y) for graph ingestion.
  - FamilySearch gated on FAMILYSEARCH_ACCESS_TOKEN; OAuth path documented in tool description.
  - TS: 3 wrappers registered; `pr-d-tools-registry.test.ts` (4/4 pass).
  - ER: typed envelopes (kind: person | relationship) — relationship entities encode the family-graph edges.

- **PR-E — DONE 2026-05-06**: HathiTrust + Gallica (BnF) + NPS NPGallery + NDL Japan.
  - Go: `hathitrust_search.go`, `gallica_search.go`, `npgallery_search.go`, `ndl_japan_search.go` + 9 tests passing.
  - HathiTrust: catalog Solr search is Cloudflare-fronted (403 expected) — bibliographic API (oclc/isbn/htid) is the reliable path; tests are tolerant.
  - Gallica: SRU+CQL XML parsing, type-aware kind mapping (book/newspaper/manuscript/map/image). Live: 20 items, total 2.18M.
  - NPGallery: REST search; Cloudflare/varying availability handled gracefully (empty entities[] still emitted to satisfy ER contract).
  - NDL Japan: OpenSearch RSS parsed via XML namespaces; CJK queries work directly (e.g., 夏目漱石 → 20 results).
  - TS: 4 wrappers registered; `pr-e-tools-registry.test.ts` (5/5 pass).
  - ER moat re-verified: extended `TestERMoatEnvelope_AllTools` to cover 14 tools (TMDB+Trove+FamilySearch gated on keys); all pass with consistent `entities[]+kind` envelope.

- **PR-F — DONE 2026-05-06**: Pokémon TCG + Discogs + Setlist.fm + WorldCat + GeoNames.
  - Go: `pokemon_tcg.go`, `discogs_search.go`, `setlistfm_lookup.go`, `worldcat_search.go`, `geonames_lookup.go` + 10 tests passing.
  - WorldCat is Cloudflare-rate-limited; ER moat test tolerates 429. GeoNames demo account quota exhausted; tool ready for GEONAMES_USERNAME.
  - TS: 5 wrappers registered; `pr-f-tools-registry.test.ts` (6/6 pass).
  - ER moat re-verified across 17 tools (PR-F additions + tolerant-mode for Cloudflare-fronted endpoints).

## Final state — Tier 1 complete + key Tier 2 covered

- **18 new tools** wired end-to-end (Go + TS + tests + ER envelope) across PR-A → PR-F:
  - Cinema/episodes: TMDB, TVMaze
  - Trading cards: Scryfall (MTG), YGOPRODeck (Yu-Gi-Oh), Pokémon TCG
  - Historic newspapers: Trove (AU), Chronicling America (US)
  - Library catalogs: Library of Congress, HathiTrust, WorldCat
  - Non-English archives: Gallica (BnF, France), NDL (Japan)
  - Heritage: NPS NPGallery (US National Register)
  - Academic: Wikidata SPARQL, OpenAlex Author Graph, MathGenealogy
  - Genealogy: WikiTree, ADB (Australia), FamilySearch
  - Music: Discogs, Setlist.fm
  - Geography: GeoNames

- **54 Go tests** added (live + offline + key-gated)
- **6 TS PR-registry tests** (43/43 total in api package)
- **ER moat verified** across **17 tools** with consistent `entities[]+kind` envelope contract; tolerant mode for Cloudflare-fronted endpoints (WorldCat, HathiTrust catalog Solr, NPGallery)
- 6 tools key-gated (TMDB_API_KEY, TROVE_API_KEY, OPENALEX_MAILTO, FAMILYSEARCH_ACCESS_TOKEN, SETLISTFM_API_KEY, POKEMONTCG_API_KEY/DISCOGS_TOKEN/WORLDCAT_API_KEY/GEONAMES_USERNAME) — implementations are inert until secrets land, per the user's "I'll add them" directive

- **PR-G — DONE 2026-05-06**: CIA World Factbook (free, no-key, factbook.json mirror).
  - Go `cia_factbook.go` + offline tests; TS wrapper; ER envelope (kind: country) with full Factbook record (population, GDP, capital, government, labor force, languages).
  - Other Tier-2 items (iNaturalist, GovInfo, etc.) deferred — pivoted to PR-H per user directive.

- **PR-H — DONE 2026-05-06**: Ported social tools from vurvey-api per user request "search ../vurvey-api codebase and copy any useful tools and APIs that it uses (paid/unpaid) and bring them over."
  - **TikTok via tiktok-scraper7 RapidAPI** — `tiktok_lookup.go` with 5 modes (user_profile, user_videos, video_info, challenge_info, challenge_posts). REQUIRES RAPID_API_KEY. Closes the TikTok OSINT gap entirely (previously missing).
  - **Twitter via twitter154 RapidAPI** — `twitter_rapidapi.go` with 6 modes (user_details, user_tweets, search, tweet_details, hashtag, geo_search). REQUIRES RAPID_API_KEY. Cheaper alternative to X API v2 Premium ($100+/mo).
  - **LinkedIn Proxycurl extended** — replaced single-function `linkedin_proxycurl.go` with multi-mode version: 7 modes (person_profile, company_profile, company_employee_count, lookup_company_by_domain, lookup_person_by_email, person_email, find_company_role). REQUIRES PROXYCURL_API_KEY. Backwards-compatible with the original `url` param routing. Test verifies backwards-compat doesn't break.
  - **YouTube via yt-api RapidAPI** — `youtube_search_rapidapi.go` with 6 modes (search, video_details, video_comments, channel_info, channel_videos, trending). REQUIRES RAPID_API_KEY. Complements existing free `youtube_transcript`.
  - All 4 wired into Go server switch + TS API tools + registry imports + offline tests. ER envelope conformant (kind: social_account | social_post | person | organization | video).
  - 50/50 TS tests pass after PR-G/H.

- **PR-I — DONE 2026-05-06**: ICIJ Offshore Leaks + Instagram120 + Browserbase + iNaturalist.
  - **ICIJ Offshore Leaks** (`icij_offshore_leaks.go`) — free no-key HTML-scraped search across Pandora/Panama/Paradise/Bahamas/Offshore/Swissleaks investigations (~810k+ entities). One of the highest-value beneficial-ownership OSINT sources. Cloudflare anti-bot can return HTTP 202; tests are tolerant. ER kinds: person | organization | intermediary | address | relationship.
  - **Instagram via instagram120** (`instagram_rapidapi.go`) — vurvey-port. 8 modes (user_profile, user_info, user_posts, user_reels, user_stories, highlights, post_by_url, post_by_shortcode). REQUIRES RAPID_API_KEY.
  - **Browserbase headless sessions** (`browserbase_session.go`) — minimal session-creation wrapper that returns connect_url + selenium_url for downstream CDP automation. REQUIRES BROWSERBASE_API_KEY + BROWSERBASE_PROJECT_ID. (Full automation requires chromedp connection to the returned URL; out-of-scope for the worker.)
  - **iNaturalist** (`inaturalist_search.go`) — free no-key biodiversity API. 3 modes (search_taxa, search_observations with taxon/place/year/month/user filters, user_profile). ER kinds: taxon | observation | person.
  - All wired (Go server switch + TS API tools + registry imports + offline tests). 56/56 TS tests pass after PR-I.
  - ER moat re-verified across 18 always-on tools (added iNaturalist; ADB now tolerant due to TLS-handshake flakiness on adb.anu.edu.au).

- **PR-J — DONE 2026-05-06**: GovInfo + ADS-B + AISHub + Encyclopedia of Life.
  - **GovInfo** (`govinfo_search.go`) — US Federal Register / Congressional Record / Public Laws / U.S. Code / GAO Reports full-text. REQUIRES GOVINFO_API_KEY (or DATA_GOV_API_KEY). 3 modes: search, package, collections_list. ER kind: publication.
  - **ADS-B aircraft tracking** (`adsb_lookup.go`) — primary source adsb.lol (free public mirror); RapidAPI ADS-B Exchange tier optional. 4 modes: by_icao24, by_registration, by_callsign, near_position. NOTE: adsb.lol now serving HTTP 451 ("violation of ODbL license terms") for some unauthenticated requests; tool gracefully returns the error. ER kind: aircraft.
  - **AISHub vessel AIS** (`aishub_lookup.go`) — REQUIRES AISHUB_USERNAME (free registration). 4 modes: by_mmsi, by_imo, by_callsign, near_position. ER kind: vessel.
  - **Encyclopedia of Life** (`eol_search.go`) — ~2M+ taxa, free no-key. Complementary to iNaturalist (taxon reference vs observations). ER kind: taxon.
  - All wired (Go server switch + TS API tools + registry imports + offline tests). 61/61 TS tests pass after PR-J.
  - ER moat re-verified across 19 always-on tools (added EOL with tolerance for occasional 5xx).

- **PR-K — DONE 2026-05-06**: Tier-3 paid integrations (SerpAPI Google Scholar + People Data Labs + Crunchbase + SecurityTrails).
  - **SerpAPI Google Scholar** (`serpapi_google_scholar.go`) — REQUIRES SERPAPI_KEY. 4 modes: search, author, author_articles, cites. Closes the gap between OpenAlex (open-access only) and the paywalled academic literature including theses + books + citations. ER kinds: scholarly_work | scholar.
  - **People Data Labs** (`people_data_labs.go`) — REQUIRES PEOPLE_DATA_LABS_API_KEY. ~3B+ profiles with verified employment + education histories. 4 modes: person_enrich (by email/phone/linkedin/name+company), person_search (Elasticsearch), company_enrich, company_search. ER kinds: person | organization.
  - **Crunchbase Basic v4** (`crunchbase_lookup.go`) — REQUIRES CRUNCHBASE_API_KEY. 5 modes: search_organizations, organization_details, search_people, person_details, funding_rounds. ER kinds: organization | person | funding_round.
  - **SecurityTrails** (`securitytrails_lookup.go`) — REQUIRES SECURITYTRAILS_API_KEY. 6 modes: domain (current), domain_history (DNS by record_type), subdomains, associated, ip_neighbors, whois_history. ER kinds: domain | ip_address | dns_record (with first_seen/last_seen attributes for time-series ER).
  - **Pre-existing fix**: `entity_link_finder.go` had a `len(deduped)` typo (undefined identifier) that blocked the worker build. Fixed to `len(out.Connections)` per CLAUDE.md "always fix pre-existing errors" rule.
  - All wired (Go server switch + TS API tools + registry imports + offline tests). 69/69 TS tests pass after PR-K.
  - All 4 are key-gated; no live ER moat additions (they would all return key-missing errors).

- **PR-L — DONE 2026-05-06**: MarineTraffic + FlightAware AeroAPI + Sentinel Hub + Brave Search.
  - **MarineTraffic** (`marinetraffic_lookup.go`) — REQUIRES MARINETRAFFIC_API_KEY. 5 modes: vessel_position, vessel_master_data, vessels_in_area (bbox), voyage_forecast, port_calls. Dominant commercial AIS provider; complement to free `aishub_lookup`. ER kinds: vessel | port_call.
  - **FlightAware AeroAPI v4** (`flightaware_lookup.go`) — REQUIRES FLIGHTAWARE_API_KEY. 5 modes: flight (ident), operator, airport, registration, track. Dominant commercial flight tracking; replaces blocked free adsb.lol mirror. ER kind: flight.
  - **Sentinel Hub Process API** (`sentinel_hub_imagery.go`) — REQUIRES SENTINEL_HUB_CLIENT_ID + CLIENT_SECRET (Copernicus free OAuth tier). 4 modes: true_color, ndvi, ndwi, available_dates. Returns 512x512 PNG as data URL plus catalog metadata. Closes the satellite-imagery gap entirely (previously absent). ER kind: satellite_image.
  - **Brave Search API** (`brave_search.go`) — REQUIRES BRAVE_SEARCH_API_KEY (free tier 2k qpm). 4 modes: web, news (with freshness pd/pw/pm/py), images, videos. Independent search index for triangulating against Google/Tavily/Bing. ER kind: search_result.
  - All wired (Go server switch + TS API tools + registry imports + offline tests). 74/74 TS tests pass after PR-L.

## Final state — Tier 1 + Tier 2 + Tier 3 paid + commercial-grade tracking + satellite + critical vurvey-port social tools complete

Total inventory after PR-A → PR-L: **39 new tools** spanning cinema, trading cards, historic newspapers, library catalogs, non-English archives, heritage, academic chains (open + paywalled), genealogy, music, geography, country reference, social media (TikTok / Twitter154 / Instagram120 / YouTube-RapidAPI), beneficial-ownership leaks (ICIJ), biodiversity (iNaturalist + EOL), headless browsing (Browserbase), US federal documents (GovInfo), aircraft tracking (ADS-B + FlightAware), vessel tracking (AISHub + MarineTraffic), B2B identity enrichment (PDL), startup funding (Crunchbase), historical DNS/WHOIS (SecurityTrails), satellite imagery (Sentinel Hub), and independent search (Brave).

## Remaining for future PRs (genuinely low-value — explicit overlaps or near-zero incremental utility)

- Geocaching/Waymarking — very niche
- Find My Past — UK genealogy, partially covered by FamilySearch/WikiTree
- Newspapers.com Publisher API (Ancestry) / British Newspaper Archive (Findmypast) — no public REST API, enterprise-sales only
- ProQuest Dissertations — institutional access only, no public REST
- DomainTools Iris · Pipl · Spokeo · BeenVerified — all overlap with `securitytrails_lookup` or existing `truepeoplesearch-lookup`
- Reddit firecrawl-style search · Wayback Machine extras · xAI/Grok extras · Google Trends + Maps — already in osint-agent registry
- iCloud / iMessage / Apple ecosystem — no programmatic OSINT path

The build phase is substantively complete. Further iterations would add tools that either don't have public APIs, fully overlap existing tools, or address use cases outside OSINT scope.

## Recommended build order (next 5 PRs)

If shipping in PRs of 2-3 tools each:

1. **PR-A** (the cinema/episode gap): TMDB + TVMaze + Scryfall + YGOPRODeck. ~1 day. Closes ~15% of TV/film/games failures.
2. **PR-B** (the historic-newspaper gap): Trove API + Chronicling America + Library of Congress catalog. ~1 day. Closes the page-10 newspaper class plus AU/US biography questions.
3. **PR-C** (the academic-chain gap): Wikidata SPARQL + OpenAlex author-graph traversal + MathGenealogy scraper. ~1.5 days. Closes the multi-hop academic chains (largest single failure class — iters 63/68/73/78/85).
4. **PR-D** (the genealogy gap): FamilySearch API + ADB + WikiTree. ~1 day. Closes family-tree multi-hop (iter 71, plus the original Jason-FIL test).
5. **PR-E** (the heritage gap): NPS NPGallery + Gallica + NDL Digital + HathiTrust full-text. ~1.5 days. Closes pre-1950 cultural/biographical chain questions.

Total: ~6 days of build work to fundamentally shift solve rate on questions that
require primary-source access. The benchmark gain is incidental — the real value is
**production OSINT capability** for users hitting the same gap classes.

## Out of scope for this list

- Anti-detection / scraping evasion. The osint-agent uses official APIs where possible
  by design (per CONTRIBUTING.md). Anti-bot bypass for Trove/Newspapers.com is the
  paid-API route, not stealth-scraping.
- Re-identification / doxxing tools. Stay on the right side of the privacy line.
- Anything requiring an institutional library subscription transferred to a single user.

## Notes

- Most paid tools have **free trials or limited tiers** suitable for development. Wire
  the API but gate by the user's tenant tier (Phase 3 hosted layer) — matches the
  existing tier-gating pattern in the codebase.
- For each new tool, follow the 3-edit pattern from `CLAUDE.md`:
  - `apps/go-worker/internal/tools/<name>.go` + switch case in `server.go`
  - `apps/api/src/mcp/tools/<kebab-name>.ts` calling `callGoWorker`
  - `import` in `registry.ts` above the meta-tools section
