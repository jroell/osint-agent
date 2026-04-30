package server

import (
	"crypto/ed25519"
	"encoding/hex"
	"net/http"
	"os"
	"time"

	"github.com/jroell/osint-agent/apps/go-worker/internal/tools"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

type ToolRequest struct {
	RequestID string         `json:"requestId"`
	TenantID  string         `json:"tenantId"`
	UserID    string         `json:"userId"`
	Tool      string         `json:"tool"`
	Input     map[string]any `json:"input"`
	TimeoutMs int            `json:"timeoutMs"`
}

type ToolResponse struct {
	RequestID string      `json:"requestId"`
	OK        bool        `json:"ok"`
	Output    interface{} `json:"output,omitempty"`
	Error     *ToolError  `json:"error,omitempty"`
	Telemetry Telemetry   `json:"telemetry"`
}

type ToolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Telemetry struct {
	TookMs    int64  `json:"tookMs"`
	CacheHit  bool   `json:"cacheHit"`
	ProxyUsed string `json:"proxyUsed,omitempty"`
}

func NewServer() *echo.Echo {
	pubHex := os.Getenv("WORKER_PUBLIC_KEY_HEX")
	if pubHex == "" {
		panic("WORKER_PUBLIC_KEY_HEX required")
	}
	pubBytes, err := hex.DecodeString(pubHex)
	if err != nil || len(pubBytes) != ed25519.PublicKeySize {
		panic("invalid WORKER_PUBLIC_KEY_HEX")
	}

	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.BodyLimit("5MB"))
	e.GET("/healthz", func(c echo.Context) error {
		return c.JSON(200, map[string]string{"ok": "true", "service": "go-worker"})
	})

	authed := e.Group("", RequireSigned(SignedAuthConfig{PublicKey: ed25519.PublicKey(pubBytes)}))
	authed.POST("/tool", handleTool)

	return e
}

func handleTool(c echo.Context) error {
	var req ToolRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	start := time.Now()

	var (
		out interface{}
		err error
	)
	switch req.Tool {
	case "subfinder_passive":
		out, err = tools.Subfinder(c.Request().Context(), req.Input)
	case "dns_lookup_comprehensive":
		out, err = tools.DNS(c.Request().Context(), req.Input)
	case "whois_query":
		out, err = tools.Whois(c.Request().Context(), req.Input)
	case "cert_transparency_query":
		out, err = tools.CertTransparency(c.Request().Context(), req.Input)
	case "asn_lookup":
		out, err = tools.ASNLookup(c.Request().Context(), req.Input)
	case "reverse_dns":
		out, err = tools.ReverseDNS(c.Request().Context(), req.Input)
	case "http_probe":
		out, err = tools.HTTPProbe(c.Request().Context(), req.Input)
	case "takeover_check":
		out, err = tools.TakeoverCheck(c.Request().Context(), req.Input)
	case "tech_stack_fingerprint":
		out, err = tools.TechStackFingerprint(c.Request().Context(), req.Input)
	case "exposed_asset_find":
		out, err = tools.ExposedAssetFind(c.Request().Context(), req.Input)
	case "leaked_secret_git_scan":
		out, err = tools.LeakedSecretGitScan(c.Request().Context(), req.Input)
	case "pwned_password_check":
		out, err = tools.PwnedPasswordCheck(c.Request().Context(), req.Input)
	case "hibp_breach_lookup":
		out, err = tools.HIBPBreachLookup(c.Request().Context(), req.Input)
	case "shodan_search":
		out, err = tools.ShodanSearch(c.Request().Context(), req.Input)
	case "censys_search":
		out, err = tools.CensysSearch(c.Request().Context(), req.Input)
	case "port_scan_passive":
		out, err = tools.PortScanPassive(c.Request().Context(), req.Input)
	case "wayback_history":
		out, err = tools.WaybackHistory(c.Request().Context(), req.Input)
	case "github_user_profile":
		out, err = tools.GitHubUserProfile(c.Request().Context(), req.Input)
	case "reddit_query":
		out, err = tools.RedditQuery(c.Request().Context(), req.Input)
	case "sec_edgar_filing_search":
		out, err = tools.SECEdgarFilingSearch(c.Request().Context(), req.Input)
	case "common_crawl_lookup":
		out, err = tools.CommonCrawlLookup(c.Request().Context(), req.Input)
	case "opencorporates_search":
		out, err = tools.OpenCorporatesSearch(c.Request().Context(), req.Input)
	case "opensanctions_screen":
		out, err = tools.OpenSanctionsScreen(c.Request().Context(), req.Input)
	case "phone_numverify":
		out, err = tools.PhoneNumverify(c.Request().Context(), req.Input)
	case "intelx_search":
		out, err = tools.IntelxSearch(c.Request().Context(), req.Input)
	case "dehashed_search":
		out, err = tools.DehashedSearch(c.Request().Context(), req.Input)
	case "reverse_image_search":
		out, err = tools.ReverseImageSearch(c.Request().Context(), req.Input)
	case "hackernews_user":
		out, err = tools.HackerNewsUser(c.Request().Context(), req.Input)
	case "stackexchange_user":
		out, err = tools.StackExchangeUser(c.Request().Context(), req.Input)
	case "gravatar_lookup":
		out, err = tools.GravatarLookup(c.Request().Context(), req.Input)
	case "github_commit_emails":
		out, err = tools.GitHubCommitEmails(c.Request().Context(), req.Input)
	case "bluesky_user":
		out, err = tools.BlueskyUser(c.Request().Context(), req.Input)
	case "keybase_lookup":
		out, err = tools.KeybaseLookup(c.Request().Context(), req.Input)
	case "mastodon_user_lookup":
		out, err = tools.MastodonUserLookup(c.Request().Context(), req.Input)
	case "hunter_io_email_finder":
		out, err = tools.HunterIOEmailFinder(c.Request().Context(), req.Input)
	case "google_dork_search":
		out, err = tools.GoogleDorkSearch(c.Request().Context(), req.Input)
	case "twitter_user":
		out, err = tools.TwitterUser(c.Request().Context(), req.Input)
	case "linkedin_proxycurl":
		out, err = tools.LinkedInProxycurl(c.Request().Context(), req.Input)
	case "instagram_user":
		out, err = tools.InstagramUser(c.Request().Context(), req.Input)
	case "firecrawl_scrape":
		out, err = tools.FirecrawlScrape(c.Request().Context(), req.Input)
	case "firecrawl_search":
		out, err = tools.FirecrawlSearch(c.Request().Context(), req.Input)
	case "diffbot_extract":
		out, err = tools.DiffbotExtract(c.Request().Context(), req.Input)
	case "diffbot_kg_query":
		out, err = tools.DiffbotKGQuery(c.Request().Context(), req.Input)
	case "tavily_search":
		out, err = tools.TavilySearch(c.Request().Context(), req.Input)
	case "perplexity_search":
		out, err = tools.PerplexitySearch(c.Request().Context(), req.Input)
	case "grok_x_search":
		out, err = tools.GrokXSearch(c.Request().Context(), req.Input)
	case "ip_geolocate":
		out, err = tools.IPGeolocate(c.Request().Context(), req.Input)
	case "google_places_search":
		out, err = tools.GooglePlacesSearch(c.Request().Context(), req.Input)
	case "openai_vision_describe":
		out, err = tools.OpenAIVisionDescribe(c.Request().Context(), req.Input)
	case "google_vision_analyze":
		out, err = tools.GoogleVisionAnalyze(c.Request().Context(), req.Input)
	case "urlscan_search":
		out, err = tools.URLScanSearch(c.Request().Context(), req.Input)
	case "favicon_pivot":
		out, err = tools.FaviconPivot(c.Request().Context(), req.Input)
	case "typosquat_scan":
		out, err = tools.TyposquatScan(c.Request().Context(), req.Input)
	case "js_endpoint_extract":
		out, err = tools.JSEndpointExtract(c.Request().Context(), req.Input)
	case "graphql_introspection":
		out, err = tools.GraphQLIntrospection(c.Request().Context(), req.Input)
	case "entity_link_finder":
		out, err = tools.EntityLinkFinder(c.Request().Context(), req.Input)
	case "swagger_openapi_finder":
		out, err = tools.SwaggerOpenAPIFinder(c.Request().Context(), req.Input)
	case "wayback_endpoint_extract":
		out, err = tools.WaybackEndpointExtract(c.Request().Context(), req.Input)
	case "alienvault_otx_passive_dns":
		out, err = tools.AlienVaultOTXPassiveDNS(c.Request().Context(), req.Input)
	case "shodan_internetdb":
		out, err = tools.ShodanInternetDB(c.Request().Context(), req.Input)
	case "tracker_extract":
		out, err = tools.TrackerExtract(c.Request().Context(), req.Input)
	case "graphql_clairvoyance":
		out, err = tools.GraphQLClairvoyance(c.Request().Context(), req.Input)
	case "tracker_pivot":
		out, err = tools.TrackerPivot(c.Request().Context(), req.Input)
	case "tracker_correlate":
		out, err = tools.TrackerCorrelate(c.Request().Context(), req.Input)
	case "github_code_search":
		out, err = tools.GitHubCodeSearch(c.Request().Context(), req.Input)
	case "ct_brand_watch":
		out, err = tools.CTBrandWatch(c.Request().Context(), req.Input)
	case "hackertarget_recon":
		out, err = tools.HackerTargetRecon(c.Request().Context(), req.Input)
	case "postman_public_search":
		out, err = tools.PostmanPublicSearch(c.Request().Context(), req.Input)
	case "spf_dmarc_chain":
		out, err = tools.SPFDMARCChain(c.Request().Context(), req.Input)
	case "mail_correlate":
		out, err = tools.MailCorrelate(c.Request().Context(), req.Input)
	case "prompt_injection_scanner":
		out, err = tools.PromptInjectionScanner(c.Request().Context(), req.Input)
	case "well_known_recon":
		out, err = tools.WellKnownRecon(c.Request().Context(), req.Input)
	case "mobile_app_lookup":
		out, err = tools.MobileAppLookup(c.Request().Context(), req.Input)
	case "docker_hub_search":
		out, err = tools.DockerHubSearch(c.Request().Context(), req.Input)
	case "jwt_decoder":
		out, err = tools.JWTDecoder(c.Request().Context(), req.Input)
	case "mcp_endpoint_finder":
		out, err = tools.MCPEndpointFinder(c.Request().Context(), req.Input)
	case "pypi_npm_search":
		out, err = tools.PypiNpmSearch(c.Request().Context(), req.Input)
	case "wikidata_entity_lookup":
		out, err = tools.WikidataEntityLookup(c.Request().Context(), req.Input)
	case "gleif_lei_lookup":
		out, err = tools.GleifLEILookup(c.Request().Context(), req.Input)
	case "pgp_key_lookup":
		out, err = tools.PGPKeyLookup(c.Request().Context(), req.Input)
	case "gitlab_search":
		out, err = tools.GitLabSearch(c.Request().Context(), req.Input)
	case "status_page_finder":
		out, err = tools.StatusPageFinder(c.Request().Context(), req.Input)
	case "cve_intel_chain":
		out, err = tools.CVEIntelChain(c.Request().Context(), req.Input)
	case "shorturl_unfurler":
		out, err = tools.ShortURLUnfurler(c.Request().Context(), req.Input)
	case "github_org_intel":
		out, err = tools.GitHubOrgIntel(c.Request().Context(), req.Input)
	case "unicode_homoglyph_normalize":
		out, err = tools.UnicodeHomoglyphNormalize(c.Request().Context(), req.Input)
	case "discord_invite_resolve":
		out, err = tools.DiscordInviteResolve(c.Request().Context(), req.Input)
	case "shopify_storefront_extract":
		out, err = tools.ShopifyStorefrontExtract(c.Request().Context(), req.Input)
	case "reddit_org_intel":
		out, err = tools.RedditOrgIntel(c.Request().Context(), req.Input)
	case "hackernews_search":
		out, err = tools.HackerNewsSearch(c.Request().Context(), req.Input)
	case "ssl_cert_chain_inspect":
		out, err = tools.SSLCertChainInspect(c.Request().Context(), req.Input)
	case "google_dork_helper":
		out, err = tools.GoogleDorkHelper(c.Request().Context(), req.Input)
	case "bigquery_trending_now":
		out, err = tools.BigQueryTrendingNow(c.Request().Context(), req.Input)
	case "bigquery_github_archive":
		out, err = tools.BigQueryGitHubArchive(c.Request().Context(), req.Input)
	case "bigquery_gdelt":
		out, err = tools.BigQueryGDELT(c.Request().Context(), req.Input)
	case "ens_resolve":
		out, err = tools.ENSResolve(c.Request().Context(), req.Input)
	case "bsky_starter_pack_extract":
		out, err = tools.BskyStarterPackExtract(c.Request().Context(), req.Input)
	case "onchain_tx_analysis":
		out, err = tools.OnchainTxAnalysis(c.Request().Context(), req.Input)
	case "findagrave_search":
		out, err = tools.FindAGraveSearch(c.Request().Context(), req.Input)
	case "telegram_channel_resolve":
		out, err = tools.TelegramChannelResolve(c.Request().Context(), req.Input)
	case "bigquery_wikipedia_pageviews":
		out, err = tools.BigQueryWikipediaPageviews(c.Request().Context(), req.Input)
	case "bigquery_patents":
		out, err = tools.BigQueryPatents(c.Request().Context(), req.Input)
	case "nostr_user_lookup":
		out, err = tools.NostrUserLookup(c.Request().Context(), req.Input)
	case "openalex_search":
		out, err = tools.OpenAlexSearch(c.Request().Context(), req.Input)
	case "nominatim_geocode":
		out, err = tools.NominatimGeocode(c.Request().Context(), req.Input)
	case "arxiv_search":
		out, err = tools.ArxivSearch(c.Request().Context(), req.Input)
	case "bigquery_stack_overflow":
		out, err = tools.BigQueryStackOverflow(c.Request().Context(), req.Input)
	case "fec_donations_lookup":
		out, err = tools.FECDonationsLookup(c.Request().Context(), req.Input)
	case "crossref_paper_search":
		out, err = tools.CrossrefPaperSearch(c.Request().Context(), req.Input)
	case "bio_link_resolve":
		out, err = tools.BioLinkResolve(c.Request().Context(), req.Input)
	case "courtlistener_search":
		out, err = tools.CourtListenerSearch(c.Request().Context(), req.Input)
	case "propublica_nonprofit":
		out, err = tools.ProPublicaNonprofit(c.Request().Context(), req.Input)
	case "nih_reporter_search":
		out, err = tools.NIHReporterSearch(c.Request().Context(), req.Input)
	case "osm_overpass_query":
		out, err = tools.OSMOverpassQuery(c.Request().Context(), req.Input)
	case "reddit_user_intel":
		out, err = tools.RedditUserIntel(c.Request().Context(), req.Input)
	case "huggingface_hub_search":
		out, err = tools.HuggingFaceHubSearch(c.Request().Context(), req.Input)
	case "fediverse_webfinger":
		out, err = tools.FediverseWebFinger(c.Request().Context(), req.Input)
	case "wikipedia_user_intel":
		out, err = tools.WikipediaUserIntel(c.Request().Context(), req.Input)
	case "wayback_url_history":
		out, err = tools.WaybackURLHistory(c.Request().Context(), req.Input)
	case "steam_profile_lookup":
		out, err = tools.SteamProfileLookup(c.Request().Context(), req.Input)
	case "stackexchange_user_intel":
		out, err = tools.StackExchangeUserIntel(c.Request().Context(), req.Input)
	case "lichess_user_lookup":
		out, err = tools.LichessUserLookup(c.Request().Context(), req.Input)
	case "reddit_subreddit_intel":
		out, err = tools.RedditSubredditIntel(c.Request().Context(), req.Input)
	case "dblp_search":
		out, err = tools.DBLPSearch(c.Request().Context(), req.Input)
	case "ror_org_lookup":
		out, err = tools.RorOrgLookup(c.Request().Context(), req.Input)
	case "nsf_awards_search":
		out, err = tools.NSFAwardsSearch(c.Request().Context(), req.Input)
	case "ietf_datatracker_search":
		out, err = tools.IETFDataTrackerSearch(c.Request().Context(), req.Input)
	case "osv_vuln_search":
		out, err = tools.OSVVulnSearch(c.Request().Context(), req.Input)
	case "internet_archive_search":
		out, err = tools.InternetArchiveSearch(c.Request().Context(), req.Input)
	case "farcaster_user_lookup":
		out, err = tools.FarcasterUserLookup(c.Request().Context(), req.Input)
	case "truepeoplesearch_lookup":
		out, err = tools.TruePeopleSearchLookup(c.Request().Context(), req.Input)
	case "site_snippet_search":
		out, err = tools.SiteSnippetSearch(c.Request().Context(), req.Input)
	case "hackernews_user_intel":
		out, err = tools.HackerNewsUserIntel(c.Request().Context(), req.Input)
	case "firecrawl_extract":
		out, err = tools.FirecrawlExtract(c.Request().Context(), req.Input)
	case "firecrawl_map":
		out, err = tools.FirecrawlMap(c.Request().Context(), req.Input)
	case "firecrawl_parse":
		out, err = tools.FirecrawlParse(c.Request().Context(), req.Input)
	case "scrapingbee_fetch":
		out, err = tools.ScrapingBeeFetch(c.Request().Context(), req.Input)
	case "pubmed_search":
		out, err = tools.PubMedSearch(c.Request().Context(), req.Input)
	case "clinicaltrials_search":
		out, err = tools.ClinicalTrialsSearch(c.Request().Context(), req.Input)
	case "google_news_recent":
		out, err = tools.GoogleNewsRecent(c.Request().Context(), req.Input)
	case "youtube_transcript":
		out, err = tools.YouTubeTranscript(c.Request().Context(), req.Input)
	case "obituary_search":
		out, err = tools.ObituarySearch(c.Request().Context(), req.Input)
	case "gemini_search_grounded":
		out, err = tools.GeminiSearchGrounded(c.Request().Context(), req.Input)
	case "gemini_url_context":
		out, err = tools.GeminiURLContext(c.Request().Context(), req.Input)
	case "gemini_youtube_understanding":
		out, err = tools.GeminiYouTubeUnderstanding(c.Request().Context(), req.Input)
	case "gemini_image_analyze":
		out, err = tools.GeminiImageAnalyze(c.Request().Context(), req.Input)
	case "gemini_code_execution":
		out, err = tools.GeminiCodeExecution(c.Request().Context(), req.Input)
	case "google_lens_search":
		out, err = tools.GoogleLensSearch(c.Request().Context(), req.Input)
	case "ip_intel_lookup":
		out, err = tools.IPIntelLookup(c.Request().Context(), req.Input)
	case "ip_intel_batch":
		out, err = tools.IPIntelBatchLookup(c.Request().Context(), req.Input)
	case "google_maps_places":
		out, err = tools.GoogleMapsPlaces(c.Request().Context(), req.Input)
	case "hudsonrock_cavalier":
		out, err = tools.HudsonRockCavalier(c.Request().Context(), req.Input)
	case "wikidata_lookup":
		out, err = tools.WikidataLookup(c.Request().Context(), req.Input)
	case "sec_edgar_search":
		out, err = tools.SECEdgarSearch(c.Request().Context(), req.Input)
	case "github_advanced_search":
		out, err = tools.GitHubAdvancedSearch(c.Request().Context(), req.Input)
	case "cisa_kev_lookup":
		out, err = tools.CISAKEVLookup(c.Request().Context(), req.Input)
	case "epss_score":
		out, err = tools.EPSSScore(c.Request().Context(), req.Input)
	case "defillama_intel":
		out, err = tools.DefillamaIntel(c.Request().Context(), req.Input)
	case "documentcloud_search":
		out, err = tools.DocumentCloudSearch(c.Request().Context(), req.Input)
	case "entity_match":
		out, err = tools.EntityMatch(c.Request().Context(), req.Input)
	case "npi_registry_lookup":
		out, err = tools.NPIRegistryLookup(c.Request().Context(), req.Input)
	case "federal_register_search":
		out, err = tools.FederalRegisterSearch(c.Request().Context(), req.Input)
	case "govtrack_search":
		out, err = tools.GovTrackSearch(c.Request().Context(), req.Input)
	case "lda_lobbying_search":
		out, err = tools.LDALobbyingSearch(c.Request().Context(), req.Input)
	case "cfpb_complaints_search":
		out, err = tools.CFPBComplaintsSearch(c.Request().Context(), req.Input)
	case "openfda_search":
		out, err = tools.OpenFDASearch(c.Request().Context(), req.Input)
	case "census_geocoder":
		out, err = tools.CensusGeocoder(c.Request().Context(), req.Input)
	case "census_acs_tract":
		out, err = tools.CensusACSTract(c.Request().Context(), req.Input)
	case "tor_relay_lookup":
		out, err = tools.TorRelayLookup(c.Request().Context(), req.Input)
	case "vin_decoder":
		out, err = tools.VINDecoder(c.Request().Context(), req.Input)
	case "openlibrary_search":
		out, err = tools.OpenLibrarySearch(c.Request().Context(), req.Input)
	case "usaspending_search":
		out, err = tools.USASpendingSearch(c.Request().Context(), req.Input)
	case "musicbrainz_search":
		out, err = tools.MusicBrainzSearch(c.Request().Context(), req.Input)
	case "biorxiv_search":
		out, err = tools.BioRxivSearch(c.Request().Context(), req.Input)
	case "orcid_search":
		out, err = tools.ORCIDSearch(c.Request().Context(), req.Input)
	case "usgs_earthquake_search":
		out, err = tools.USGSEarthquakeSearch(c.Request().Context(), req.Input)
	case "openmeteo_search":
		out, err = tools.OpenMeteoSearch(c.Request().Context(), req.Input)
	case "usno_astronomy":
		out, err = tools.USNOAstronomy(c.Request().Context(), req.Input)
	case "pubchem_compound_lookup":
		out, err = tools.PubChemCompoundLookup(c.Request().Context(), req.Input)
	case "wikipedia_search":
		out, err = tools.WikipediaSearch(c.Request().Context(), req.Input)
	case "coingecko_search":
		out, err = tools.CoinGeckoSearch(c.Request().Context(), req.Input)
	default:
		return c.JSON(http.StatusOK, ToolResponse{
			RequestID: req.RequestID,
			OK:        false,
			Error:     &ToolError{Code: "unknown_tool", Message: req.Tool},
			Telemetry: Telemetry{TookMs: time.Since(start).Milliseconds()},
		})
	}

	resp := ToolResponse{
		RequestID: req.RequestID,
		OK:        err == nil,
		Telemetry: Telemetry{TookMs: time.Since(start).Milliseconds()},
	}
	if err != nil {
		resp.Error = &ToolError{Code: "tool_failure", Message: err.Error()}
	} else {
		resp.Output = out
	}
	return c.JSON(http.StatusOK, resp)
}
