# Subdomain Face-Off

**Family:** `adopt-subdomain` · **Reference:** Black Lantern Security 2022 face-off (tesla.com)

## What it measures

Recall, precision, and runtime of passive+light-active subdomain enumeration against a fixed target. Reproduces the Black Lantern Security 2022 methodology so our numbers compare directly to their published results.

## Methodology (locked)

- **Target:** `tesla.com` (per the published reference). We mirror their target so our numbers can be compared apples-to-apples.
- **Mode:** Passive enumeration only. No paid API keys. Default tool configurations.
- **Validation:** A subdomain only counts if it resolves to at least one of: `A`, `AAAA`, `MX`, `TXT`, `NS`, `SOA`, `SRV`, `CNAME`. Validation is performed by the harness, not by the tool — so each tool is graded on what its raw output produces, not on what it filters internally.
- **Wordlist (for tools that brute-force):** `subdomains-top1million-5000.txt` from SecLists.
- **Wall clock:** 15-minute hard timeout per tool.

## Subjects under test

| Subject | Driver | Notes |
|---|---|---|
| `subfinder@2.14.0` | CLI binary, `subfinder -d <target> -silent` | Same library `apps/go-worker` uses, so this is a proxy for `osint-agent`'s `subfinder_passive` |
| `amass@v4` | CLI, `amass enum -passive -d <target>` | Reference comparison |
| `bbot@2.x` | CLI, `bbot -t <target> -p subdomain-enum -y` | Most aggressive in the 2022 study |
| `osint-agent` (`subfinder_passive`) | MCP tool via direct registry call | Full path through our wrapper, inherits any future enrichment |
| `osint-agent` (`domain_aggregate`) | MCP meta-tool | Fans out to multiple sources, the headline number we want to beat |

## Scoring

Set-based against ground truth. Ground truth = union of all subjects' validated outputs (best practical proxy in absence of an authoritative list — same convention the reference study used). For each subject we report:

- `recall = |found ∩ ground_truth| / |ground_truth|`
- `precision = |found ∩ ground_truth| / |found|` (≈ 1.0 since GT is the union; we still record it because it would drop if the tool emits non-resolving cruft that DNS-validation strips)
- `f1` (headline)
- `runtime_s`

## Headline historical numbers (Black Lantern 2022, tesla.com)

| Tool | Subdomains | Runtime |
|---|---|---|
| BBOT | 409 | 12m 19s |
| TheHarvester | 376 | 7m 10s |
| Subfinder | 373 | 1m 17s |
| Amass | 342 | 8m 42s |
| OneForAll | 312 | 2m 4s |
| Sublist3r | 46 | 3m 39s |

We beat the reference if `osint-agent`'s `domain_aggregate` exceeds 409 validated subdomains in <12m wall-clock.

## Where this lights up regressions

Any change to `apps/api/src/mcp/tools/subfinder.ts`, `apps/go-worker/internal/tools/dns.go`, or the upstream subfinder library version pin can regress this benchmark. The weekly CI cron catches it.

## Source

- [Black Lantern Security – Subdomain Enumeration Tool Face-off (2022)](https://blog.blacklanternsecurity.com/p/subdomain-enumeration-tool-face-off)
