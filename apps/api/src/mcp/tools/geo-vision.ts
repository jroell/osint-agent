import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

// IP geolocation
toolRegistry.register({
  name: "ip_geolocate",
  description:
    "Geolocate an IP address: country/region/city/lat-lng/ISP/ASN/timezone, plus mobile/proxy/hosting flags. Free path uses ip-api.com (no key, 45/min). Set IP_API_KEY for ipapi.com pro tier with security flags (proxy/Tor detection).",
  inputSchema: z.object({ ip: z.string().min(2) }),
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "ip_geolocate", input: i, timeoutMs: 15_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "ip_geolocate failed");
    return res.output;
  },
});

// Google Places search
toolRegistry.register({
  name: "google_places_search",
  description:
    "Find places by free-text query via Google Maps Places (TextSearch). Returns name, formatted address, rating, place_id, types, business status, lat/lng. Excellent for resolving 'company name' → physical office address(es). REQUIRES GOOGLE_MAPS_API_KEY (or GOOGLE_API_KEY).",
  inputSchema: z.object({ query: z.string().min(2) }),
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "google_places_search", input: i, timeoutMs: 20_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "google_places_search failed");
    return res.output;
  },
});

// OpenAI Vision — describe an image
toolRegistry.register({
  name: "openai_vision_describe",
  description:
    "Send an image URL to OpenAI's gpt-4o-mini (or override with `model`) and return a free-form description. Default prompt asks for visible text (verbatim), landmarks, distinguishing features, brands/logos, license plates, timestamps. Tunable via `prompt`. REQUIRES OPENAI_API_KEY.",
  inputSchema: z.object({
    url: z.string().url(),
    model: z.string().default("gpt-4o-mini"),
    prompt: z.string().optional(),
    max_tokens: z.number().int().min(100).max(4000).default(800),
  }),
  costMillicredits: 8,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "openai_vision_describe", input: i, timeoutMs: 75_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "openai_vision_describe failed");
    return res.output;
  },
});

// Google Vision — labels + safe search + faces + text + landmarks + logos
toolRegistry.register({
  name: "google_vision_analyze",
  description:
    "Run Google Vision API on an image URL and return: labels (15), safe-search ratings, face detection (10 faces, no identification), OCR text, landmark detection (5), logo detection (5). Complements openai_vision_describe — Vision API is structured, gpt-4o-mini is descriptive. REQUIRES GOOGLE_API_KEY (Cloud Vision API enabled).",
  inputSchema: z.object({ url: z.string().url() }),
  costMillicredits: 6,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "google_vision_analyze", input: i, timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "google_vision_analyze failed");
    return res.output;
  },
});
