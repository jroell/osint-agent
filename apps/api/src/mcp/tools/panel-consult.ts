import { z } from "zod";
import { toolRegistry } from "./instance";
import { consultPanel, listAvailablePanels, type Mode, type PanelId } from "../../llm/panel";

const PANEL_IDS = ["deep-reasoning", "broad-knowledge", "cjk-knowledge", "vision", "fast-cheap", "adversarial-redteam"] as const;
const MODES = ["parallel-poll", "synthesis", "adversarial", "roundtable"] as const;

const input = z.object({
  question: z.string().min(10).describe("The question to put to the panel. Be specific."),
  context: z.string().optional().describe(
    "Raw OSINT findings, tool outputs, or evidence the panel should reason over. Pasted verbatim into each member's prompt.",
  ),
  panel: z.enum(PANEL_IDS).default("deep-reasoning").describe(
    "Which expert panel to consult. deep-reasoning = highest-II reasoners. broad-knowledge = max training-data diversity. cjk-knowledge = Chinese/Japanese/Korean entities. vision = image OSINT. fast-cheap = high-volume routine. adversarial-redteam = stress-test hypotheses.",
  ),
  mode: z.enum(MODES).default("synthesis").describe(
    "parallel-poll = each member answers independently, returns all opinions. synthesis = parallel + judge synthesizes consensus and surfaces disagreements (default). adversarial = half panel argues for a hypothesis, half attacks it, judge rules. roundtable = 3-round group discussion with cross-critique.",
  ),
  image_b64: z.string().optional().describe("Optional base64-encoded image (use vision panel)."),
  image_mime: z.string().optional().describe("MIME type of image, e.g. 'image/jpeg'."),
});

toolRegistry.register({
  name: "panel_consult",
  description:
    "Consult a multi-LLM expert panel for hard OSINT questions: multi-hop entity resolution, cross-platform connection inference, hypothesis generation, hallucination cross-checking, adversary-mindset stress-testing. Use when single-tool output isn't enough — when you have N findings and need to know which connect to the same entity, or when a vague clue could lead in 5 directions and you want diverse expert opinions before chasing one. Six panels available (deep-reasoning, broad-knowledge, cjk-knowledge, vision, fast-cheap, adversarial-redteam) and four modes (parallel-poll, synthesis, adversarial, roundtable). Auto-disabled panels: those whose member providers don't have keys in env. Returns: per-member responses, synthesized consensus, agreement_score 0..1, disagreements list (where models split), confidence_warnings (claims only one model made — likely hallucinations), follow_ups (concrete next OSINT steps).",
  inputSchema: input,
  costMillicredits: 100,
  handler: async (i) => {
    return await consultPanel({
      question: i.question,
      context: i.context,
      panelId: i.panel as PanelId,
      mode: i.mode as Mode,
      imageB64: i.image_b64,
      imageMime: i.image_mime,
    });
  },
});

const listInput = z.object({});
toolRegistry.register({
  name: "panel_list",
  description:
    "List the available LLM consultation panels. Use this BEFORE panel_consult to see which panels have quorum (≥2 reachable members) given the operator's API keys. Returns each panel's id, description, and how many members are reachable.",
  inputSchema: listInput,
  costMillicredits: 1,
  handler: async () => listAvailablePanels(),
});
