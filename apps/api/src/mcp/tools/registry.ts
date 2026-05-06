import { z } from "zod";
import { toolRegistry } from "./instance";

export { ToolRegistry, toolRegistry } from "./instance";
export type { ToolDefinition } from "./instance";

toolRegistry.register({
  name: "hello_tool",
  description: "Sanity-check tool. Returns a greeting and the authenticated tenant ID.",
  inputSchema: z.object({ name: z.string().default("world") }),
  costMillicredits: 1,
  handler: async (input, ctx) => ({
    greeting: `Hello, ${input.name}!`,
    tenantId: ctx.tenantId,
    now: new Date().toISOString(),
  }),
});

// Side-effect imports: registering tools.
// Keep this block at the bottom of registry.ts; order matters for the registry.
import "./stealth-http";
import "./subfinder";
import "./dns-lookup";
import "./whois";
import "./ct";
import "./asn";
import "./reverse-dns";
import "./http-probe";
import "./takeover";
import "./tech-stack";
import "./exposed-assets";
import "./git-secrets";
import "./hibp";
import "./holehe";
import "./maigret";
import "./sherlock";
import "./theharvester";
import "./ghunt";
import "./shodan";
import "./censys";
import "./port-scan";
import "./wayback";
import "./github-user";
import "./reddit";
import "./sec-edgar";
import "./common-crawl";
import "./opencorporates";
import "./opensanctions";
import "./phone";
import "./intelx";
import "./dehashed";
import "./pdf-analyze";
import "./exif";
import "./reverse-image";
// People-finder / social OSINT
import "./hackernews";
import "./stackexchange";
import "./gravatar";
import "./github-emails";
import "./bluesky";
import "./keybase";
import "./mastodon";
import "./hunter-io";
import "./google-dork";
import "./twitter";
import "./linkedin";
import "./instagram";
// Enrichment / synthesis / vision (paid — auto-enable when keys are present)
import "./firecrawl";
import "./diffbot";
import "./synthesis";
import "./geo-vision";
// SOTA entity-resolution: urlscan.io 800M-scan archive, pivots by IP/ASN/cert/favicon
import "./urlscan";
// Favicon-mmh3 pivot — bug-bounty canonical: bridges Shodan/FOFA/ZoomEye/Censys hash space
import "./favicon-pivot";
// Typosquat / homoglyph IDN / lookalike enumeration — pure-Go dnstwist with parallel DNS
import "./typosquat";
// API endpoint discovery from JS bundles — LinkFinder-class regex extraction
import "./js-endpoint-extract";
// GraphQL introspection — recover full schema when introspection is left enabled
import "./graphql-introspection";
// Diffbot KG common-neighbor pathfinding — the explicit "are X and Y connected" tool
import "./entity-link-finder";
// Swagger / OpenAPI spec discovery — probes ~35 well-known paths
import "./swagger-openapi-finder";
// Wayback Machine endpoint extraction — third moat-feeding discovery channel
import "./wayback-endpoint-extract";
// AlienVault OTX passive DNS — fourth moat-feeding discovery channel (the "shadow" of real traffic)
import "./alienvault-otx";
// Shodan InternetDB — free, no-auth IP fingerprinting (ports/CPEs/CVEs/tags) — pairs with passive DNS
import "./shodan-internetdb";
// Tracker ID extraction — strongest non-DNS operator-binding signal (GA/FB/Hotjar/etc → ER pivots)
import "./tracker-extract";
// Tracker pivot — given a tracker ID, find every OTHER site running it (operator portfolio mapping)
import "./tracker-pivot";
// Tracker correlate — compare two URLs' tracker fingerprints, return same-operator verdict (no external deps)
import "./tracker-correlate";
// GitHub code search — find any string across 230M+ public repos with leak-severity classification
import "./github-code-search";
// CT brand watch — real-time CT log monitoring for newly-issued lookalike certs (free crt.sh API)
import "./ct-brand-watch";
// HackerTarget multi-endpoint recon — subdomain enum, reverse-IP, DNS, AS, whois (free, no key)
import "./hackertarget-recon";
// Postman public-workspace search — finds leaked API collections with hardcoded credentials
import "./postman-public-search";
// SPF/DMARC chain — recursive email-infra fingerprint via DNS (operator-binding ER signal)
import "./spf-dmarc-chain";
// Mail correlate — pairwise email-infra ER (mirror of tracker_correlate, email-side)
import "./mail-correlate";
// Prompt injection scanner — defensive scan for hidden directives targeting LLM agents
import "./prompt-injection-scanner";
// Well-known recon — one-shot probe of /.well-known/* + robots.txt + sitemap.xml + security.txt
import "./well-known-recon";
// Mobile app lookup — resolves iOS Bundle IDs + Android packages to full app metadata (pairs with well_known_recon)
import "./mobile-app-lookup";
// Docker Hub search — third leg of the leak-discovery triad (code + apis + containers)
import "./docker-hub-search";
// JWT decoder — pure-local parser, identifies issuer + claims when JWTs surface in leaks
import "./jwt-decoder";
// MCP endpoint finder — SOTA agent-vs-agent recon: probe + interrogate public MCP servers
import "./mcp-endpoint-finder";
// PyPI/npm search — fifth leg of the leak-discovery stack (registries → org-published packages)
import "./pypi-npm-search";
// Wikidata entity lookup — open knowledge graph access (foundational ER, complements Diffbot KG)
import "./wikidata-entity-lookup";
// GLEIF LEI lookup — corporate intelligence ER, legal entity registry
import "./gleif-lei-lookup";
// PGP key lookup — identity correlation via multi-UID PGP keys (alt emails on one key)
import "./pgp-key-lookup";
// GitLab search — seventh leak-discovery channel (mirrors github_code_search for GitLab orgs)
import "./gitlab-search";
// Status page finder — microservice topology + live ops intel from public status pages
import "./status-page-finder";
// CVE intel chain — NVD + EPSS + CISA KEV fan-out for vulnerability prioritization
import "./cve-intel-chain";
// Short URL unfurler — phishing-analysis primitive, follows redirect chains
import "./shorturl-unfurler";
// GitHub org intel — comprehensive GitHub org recon (trio with github_code_search + github_emails)
import "./github-org-intel";
// Unicode homoglyph normalize — IDN homoglyph attack detector for typosquat analysis
import "./unicode-homoglyph-normalize";
// Discord invite resolve — novel SOTA, public-API server-metadata recon
import "./discord-invite-resolve";
// Tracker extract rendered — chains firecrawl_scrape → tracker_extract for SPA support
import "./tracker-extract-rendered";
// Shopify storefront extract — pull /products.json catalog from any Shopify store
import "./shopify-storefront-extract";
// Reddit org intel — comprehensive search + author/subreddit aggregation
import "./reddit-org-intel";
// HackerNews search — Algolia-powered full-text across all HN
import "./hackernews-search";
// SSL cert chain inspect — live TLS handshake recon (pure Go, complements cert_transparency)
import "./ssl-cert-chain-inspect";
// Google dork helper — generates ~25 categorized dork templates for a target
import "./google-dork-helper";
// Google Trends lookup (py-worker) — interest-over-time + by-region + related queries
import "./google-trends-lookup";
// BigQuery trending now — official Google Trends dataset (no rate limits)
import "./bigquery-trending-now";
// BigQuery GH Archive — every public GitHub event since 2011
import "./bigquery-github-archive";
// BigQuery GDELT — global news intel from GDELT Global Knowledge Graph (1.7TB on BigQuery)
import "./bigquery-gdelt";
// ENS resolve — Web3 cross-platform identity (ENS + Lens + Farcaster + dotbit + social handles)
import "./ens-resolve";
// Bluesky starter pack extract — community-mapping via curated public lists
import "./bsky-starter-pack-extract";
// Onchain tx analysis — Ethereum tx history via BigQuery (pairs with ens_resolve for Web3 ER)
import "./onchain-tx-analysis";
// FindAGrave search — genealogical OSINT (relatives via deceased records, stealth-bypass Cloudflare)
import "./findagrave-search";
// Telegram channel resolve — public preview recon (subscribers, recent messages, verified flags)
import "./telegram-channel-resolve";
// BigQuery Wikipedia pageviews — cultural attention velocity (triangulates Trends + GDELT)
import "./bigquery-wikipedia-pageviews";
// BigQuery patents — global patent intelligence (assignee/inventor/keyword search)
import "./bigquery-patents";
// Nostr user lookup — censorship-resistant decentralized social (Web3 ER)
import "./nostr-user-lookup";
// OpenAlex search — academic ER (250M works, 100M authors, h-index, affiliations)
import "./openalex-search";
// Nominatim geocode — OpenStreetMap address ↔ lat/lng (geographic ER primitive)
import "./nominatim-geocode";
// arxiv search — preprint server (~2.5M papers, AI/ML/physics)
import "./arxiv-search";
// BigQuery Stack Overflow — tech-identity ER (user history + tag expertise)
import "./bigquery-stack-overflow";
// FEC donations lookup — political contribution records, employer/occupation ER (US only)
import "./fec-donations-lookup";
// Crossref paper search — ~140M scholarly works, ORCID linkage = hard cross-paper ER signal
import "./crossref-paper-search";
// Bio-link resolve — self-published cross-platform identity graph (linktr.ee/about.me/taplink.cc)
import "./bio-link-resolve";
// CourtListener — ~5M federal court opinions + RECAP dockets (litigation ER)
import "./courtlistener-search";
// ProPublica Nonprofit Explorer — 1.8M U.S. exempt orgs, IRS Form 990 ER
import "./propublica-nonprofit";
// NIH RePORTER — every NIH grant since 1985 (~3M grants, biomedical researcher ER)
import "./nih-reporter-search";
// OpenStreetMap Overpass — geographic feature query (geo ER + brand-trace + infra recon)
import "./osm-overpass-query";
// Reddit user intel — per-user deep dive (interest graph, timezone inference, self-disclosure mining)
import "./reddit-user-intel";
// HuggingFace Hub — AI/ML community ER (models/datasets/users/papers, lineage tracking)
import "./huggingface-hub-search";
// Fediverse WebFinger — universal ActivityPub identity resolver (Mastodon/Pleroma/Misskey/Lemmy/PixelFed/PeerTube)
import "./fediverse-webfinger";
// Wikipedia user intel — editor deep dive (interest graph, COI detection, sockpuppet flagging, timezone inference)
import "./wikipedia-user-intel";
// Wayback URL history — temporal recon, ownership-transition detection, dormancy gap analysis
import "./wayback-url-history";
// Steam community profile — gamer ER (real name + country + groups + VAC bans + custom_url cross-platform pivot)
import "./steam-profile-lookup";
// Stack Exchange network deep dive — per-user across 170+ sites, niche-interest disclosure
import "./stackexchange-user-intel";
// Lichess user lookup — chess identity ER (FIDE titles, real name, country, ratings, bio email/URL extraction)
import "./lichess-user-lookup";
// Reddit subreddit intel — community-level recon (top posters, top domains, controversy detection)
import "./reddit-subreddit-intel";
// DBLP CS publication index — author profile + cross-platform identity bridge (ORCID/Twitter/Scholar/etc URLs)
import "./dblp-search";
// ROR (Research Organization Registry) — canonical institutional ID system used by Crossref/OpenAlex/ORCID
import "./ror-org-lookup";
// NSF Awards — non-biomed federal research grants (complement to NIH RePORTER, ~50 years coverage)
import "./nsf-awards-search";
// IETF datatracker — RFC/draft authorship with per-doc affiliation + email evolution (cryptographer ER)
import "./ietf-datatracker-search";
// OSV.dev (Open Source Vulnerability) — package-ecosystem vuln search (npm/PyPI/Go/Cargo/Maven/etc.)
import "./osv-vuln-search";
// archive.org search + uploader trace — 50M+ items, document-archiving footprint ER
import "./internet-archive-search";
// Farcaster (Web3 social) — FID + cryptographically-verified Ethereum/Solana wallets
import "./farcaster-user-lookup";
// TruePeopleSearch lookup — family-tree OSINT via search-engine snippets (CF-blocked direct, indexed indirectly)
import "./truepeoplesearch-lookup";
// Generalized Tavily-bypass for any CF-blocked/paywalled site (LinkedIn/ZoomInfo/Glassdoor/Newspapers/etc)
import "./site-snippet-search";
// HN user intel — per-user deep dive (Firebase profile + Algolia stories/comments, interest graph, timezone inference)
import "./hackernews-user-intel";
// Firecrawl LLM-powered structured extraction (any URL → JSON fields via natural-language prompt or schema)
import "./firecrawl-extract";
// Firecrawl /map — single-call site URL discovery (subdomain enum + path prefix + high-value URL highlighting)
import "./firecrawl-map";
// Firecrawl /parse — Fire-PDF Rust engine for PDF/DOC/XLSX → markdown/JSON (court filings, FOIA, papers)
import "./firecrawl-parse";
// ScrapingBee fallback bypass — different proxy network for sites Firecrawl can't reach
import "./scrapingbee-fetch";
// PubMed E-utilities — biomed paper search (37M papers, ORCID + per-paper affiliation tracking, MeSH terms)
import "./pubmed-search";
// ClinicalTrials.gov — registered trials registry (PI + sponsor + multi-site location + enrollment)
import "./clinicaltrials-search";
// Google News RSS — current-events context (pairs with bigquery_gdelt for historical)
import "./google-news-recent";
// YouTube transcript extractor — caption-track parsing for interviews/talks/podcasts (text-searchable video)
import "./youtube-transcript";
// Multi-source obituary search — newspapers/legacy/tributearchive/dignitymemorial relative-list parsing
import "./obituary-search";
// Gemini 3.1 Pro built-in tools — Google Search grounding, URL context, native YouTube understanding
import "./gemini-tools";
// Gemini multimodal image analysis — 1-8 images per call, content reasoning + comparison + OCR
import "./gemini-image-analyze";
// Gemini code execution — Python sandbox for math/stats/parsing on agent-fetched data
import "./gemini-code-execution";
// Google Lens visual search — image-to-web index (SerpAPI primary, Serper.dev fallback)
import "./google-lens-search";
// IP intel — geo + ASN + ISP + proxy/hosting/mobile flags via ip-api.com (free, no-auth)
import "./ip-intel-lookup";
// Google Maps Places — text/details/nearby search with reviews + photos + phone/website (paid)
import "./google-maps-places";
// Hudson Rock Cavalier — infostealer log database lookup (free, 35M+ logs)
import "./hudsonrock-cavalier";
// Wikidata SPARQL + entity card — 110M-item structured knowledge graph w/ temporal qualifiers (free)
import "./wikidata-lookup";
// SEC EDGAR — full-text search + company filings + ticker→CIK (free, no-auth, 30M+ filings since 2001)
import "./sec-edgar-search";
// GitHub Advanced Search — commits/issues/users (separate surfaces from github_code_search)
import "./github-advanced-search";
// CISA KEV — federal known-exploited-vulnerability catalog with dueDate + ransomware flag (free)
import "./cisa-kev-lookup";
// EPSS — first.org Exploit Prediction Scoring System, probability + percentile (free, no-auth)
import "./epss-score";
// DefiLlama — DeFi hacks catalog + protocol metadata + TVL rankings (free, no-auth)
import "./defillama-intel";
// DocumentCloud — investigative journalism document repository, ~3M OCR'd docs (free, no-auth)
import "./documentcloud-search";
// Entity match — pure-compute name fuzzy matching + nickname expansion + username variations
import "./entity-match";
// NPI Registry — US healthcare provider DB (free, no-auth, 2.5M+ individuals)
import "./npi-registry-lookup";
// Federal Register — every federal regulation/notice/EO since 1994 (free, no-auth)
import "./federal-register-search";
// GovTrack — every US Congress bill, vote, and member since 1971 (free, no-auth)
import "./govtrack-search";
// Senate LDA — federal lobbying disclosures since 1995, free no-auth
import "./lda-lobbying-search";
// CFPB Consumer Complaints — 14.8M financial-services complaints, free no-auth
import "./cfpb-complaints-search";
// OpenFDA — drug recalls + labels + device events + food recalls (free, no-auth)
import "./openfda-search";
// Census Geocoder — US address normalization + coords + FIPS GEOIDs (free, no-auth)
import "./census-geocoder";
// Census ACS — demographic profile by tract GEOID (free, no-auth)
import "./census-acs-tract";
// Tor relay lookup — Onionoo Tor relay metadata + operator attribution (free, no-auth)
import "./tor-relay-lookup";
// VIN decoder + NHTSA recalls + model lookup (free, no-auth)
import "./vin-decoder";
// OpenLibrary — books + authors + ISBN lookup (free, no-auth)
import "./openlibrary-search";
// USAspending.gov — federal contracts/grants/loans since 2008 (free, no-auth)
import "./usaspending-search";
// MusicBrainz — music metadata, MBID + ISNI + cross-platform pivots (free, no-auth)
import "./musicbrainz-search";
// bioRxiv / medRxiv — biomedical preprint search (free, no-auth)
import "./biorxiv-search";
// ORCID — researcher identifier registry, 18M+ researchers (free, no-auth)
import "./orcid-search";
// USGS Earthquake catalog — temporal-spatial forensic OSINT (free, no-auth)
import "./usgs-earthquake-search";
// Open-Meteo weather — historical + current + air quality (free, no-auth)
import "./openmeteo-search";
// USNO astronomy — sun/moon ephemeris for forensic photo verification (free, no-auth)
import "./usno-astronomy";
// PubChem — chemistry/drug compound DB, 100M+ compounds (free, no-auth)
import "./pubchem-compound-lookup";
// Wikipedia — article-level summary/search/categories/revisions (free, no-auth)
import "./wikipedia-search";
// CoinGecko — crypto market data + coin detail + top markets (free, no-auth)
import "./coingecko-search";
// REST Countries — international country reference, 250+ countries (free, no-auth)
import "./rest-countries-lookup";
// TMDB — episode-level film/TV metadata (REQUIRES TMDB_API_KEY)
import "./tmdb-lookup";
// TVMaze — TV-show metadata (free, no-key) — fallback/cross-reference for TMDB
import "./tvmaze-lookup";
// Scryfall — Magic: The Gathering cards (free, no-key)
import "./scryfall-lookup";
// YGOPRODeck — Yu-Gi-Oh! cards (free, no-key)
import "./ygoprodeck-lookup";
// Trove (NLA Australia) — historical AU newspapers + books (REQUIRES TROVE_API_KEY)
import "./trove-search";
// Chronicling America (Library of Congress) — US historic newspapers (free, no-key)
import "./chronicling-america-search";
// Library of Congress catalog (loc.gov + id.loc.gov) — books / authority records (free, no-key)
import "./loc-catalog-search";
// Wikidata SPARQL — arbitrary structured queries over the entity graph (free, no-key)
import "./wikidata-sparql";
// OpenAlex author/work graph traversal — multi-hop academic chains (free, polite mailto)
import "./openalex-author-graph";
// Mathematics Genealogy Project — PhD supervisor chains for STEM (free, scrape-only)
import "./math-genealogy";
// WikiTree — community-curated global family tree (free, no-key)
import "./wikitree-lookup";
// ADB (Australian Dictionary of Biography) — authoritative AU biographical reference (free, scrape)
import "./adb-search";
// FamilySearch — LDS-maintained global genealogy database (REQUIRES FAMILYSEARCH_ACCESS_TOKEN)
import "./familysearch-lookup";
// HathiTrust Digital Library — ~17M digitized books/periodicals (free, no-key)
import "./hathitrust-search";
// Gallica (BnF) — ~10M+ digitized French/European archive (free, no-key)
import "./gallica-search";
// NPS NPGallery — US National Register of Historic Places (free, no-key)
import "./npgallery-search";
// NDL Japan Digital Collections — ~7.4M Japanese national library items (free, no-key)
import "./ndl-japan-search";
// Pokémon TCG database — free, optional POKEMONTCG_API_KEY for higher rate limits
import "./pokemon-tcg-lookup";
// Discogs — music release / tracklist database (free, optional DISCOGS_TOKEN)
import "./discogs-search";
// Setlist.fm — concert setlists (REQUIRES SETLISTFM_API_KEY)
import "./setlistfm-lookup";
// WorldCat (OCLC) — global library catalog (free public, optional WORLDCAT_API_KEY)
import "./worldcat-search";
// GeoNames — place-name database (free with GEONAMES_USERNAME)
import "./geonames-lookup";
// CIA World Factbook — country reference data (free, no-key, factbook.json mirror)
import "./cia-factbook";
// TikTok via tiktok-scraper7 RapidAPI (REQUIRES RAPID_API_KEY) — ported from vurvey-api
import "./tiktok-lookup";
// Twitter via twitter154 RapidAPI (REQUIRES RAPID_API_KEY) — cheaper alt to X API v2 Premium
import "./twitter-rapidapi";
// YouTube discovery via yt-api RapidAPI (REQUIRES RAPID_API_KEY) — complements youtube_transcript
import "./youtube-search-rapidapi";
// ICIJ Offshore Leaks Database — Pandora/Panama/Paradise papers, free no-key
import "./icij-offshore-leaks";
// Instagram via instagram120 RapidAPI (REQUIRES RAPID_API_KEY) — vurvey-port
import "./instagram-rapidapi";
// Browserbase headless-browser sessions (REQUIRES BROWSERBASE_API_KEY + BROWSERBASE_PROJECT_ID)
import "./browserbase-session";
// iNaturalist biodiversity API — free no-key
import "./inaturalist-search";
// GovInfo — US Federal Register / Public Laws / Congressional Record (REQUIRES GOVINFO_API_KEY)
import "./govinfo-search";
// ADS-B aircraft tracking via adsb.lol (free) + RapidAPI ADS-B Exchange tier
import "./adsb-lookup";
// AISHub vessel AIS (REQUIRES AISHUB_USERNAME)
import "./aishub-lookup";
// Encyclopedia of Life — biodiversity reference (free, no-key)
import "./eol-search";
// SerpAPI Google Scholar — paywalled academic literature (REQUIRES SERPAPI_KEY)
import "./serpapi-google-scholar";
// People Data Labs — identity + employment enrichment (REQUIRES PEOPLE_DATA_LABS_API_KEY)
import "./people-data-labs";
// Crunchbase — funding rounds + executive histories (REQUIRES CRUNCHBASE_API_KEY)
import "./crunchbase-lookup";
// SecurityTrails — historical WHOIS + DNS (REQUIRES SECURITYTRAILS_API_KEY)
import "./securitytrails-lookup";
// MarineTraffic — paid commercial vessel tracking (REQUIRES MARINETRAFFIC_API_KEY)
import "./marinetraffic-lookup";
// FlightAware AeroAPI — dominant commercial flight tracking (REQUIRES FLIGHTAWARE_API_KEY)
import "./flightaware-lookup";
// Sentinel Hub — Copernicus satellite imagery (REQUIRES SENTINEL_HUB_CLIENT_ID + SECRET)
import "./sentinel-hub-imagery";
// Brave Search — independent search index (REQUIRES BRAVE_SEARCH_API_KEY)
import "./brave-search";
// GraphQL clairvoyance — bypass disabled introspection via field-suggestion error abuse
import "./graphql-clairvoyance";
// THE MOAT — persist + query API-discovery findings across sessions
import "./api-endpoint-db";
// LLM consultation panel — multi-model cross-reference for hard OSINT
// questions. Spec: docs/specs/llm-panel-design.md
import "./panel-consult";
import "./panel-entity-resolution";
// Meta-tools last — they invoke everything above via toolRegistry.
import "./person-aggregate";
import "./domain-aggregate";
