/**
 * Multi-LLM consultation panel.
 *
 * Six predefined panels (deep-reasoning, broad-knowledge, cjk-knowledge,
 * vision, fast-cheap, adversarial-redteam) and four consultation modes
 * (parallel-poll, synthesis, adversarial, roundtable). At server startup
 * the registry filters panels to those with ≥2 reachable members based on
 * which API keys are present in env.
 *
 * See `docs/specs/llm-panel-design.md` for the full design rationale.
 */
import { makeLLM, providerHasKey, type LLM, type LLMRequest, type Provider } from "./multi-provider";
import {
  ADVERSARIAL_DEFENSE_SYSTEM,
  ADVERSARIAL_JUDGE_SYSTEM,
  ADVERSARIAL_PROSECUTION_SYSTEM,
  quickAgreementScore,
  SYNTHESIS_SYSTEM,
  SYNTHESIS_USER_TEMPLATE,
} from "./panel-aggregators";

export type PanelId = "deep-reasoning" | "broad-knowledge" | "cjk-knowledge" | "vision" | "fast-cheap" | "adversarial-redteam";
export type Mode = "parallel-poll" | "synthesis" | "adversarial" | "roundtable";

interface PanelMember {
  subject: string;
  weight: number;
  /** Free-form tags ("reasoner", "cjk", "vision", "live-x", "code") for v2 routing. */
  specialty: string[];
}

interface PanelDef {
  id: PanelId;
  description: string;
  members: PanelMember[];
  defaultMode: Mode;
  defaultJudge: string;
}

/**
 * 2026-current panel composition. Refresh against artificialanalysis.ai +
 * arena.ai every quarter (see memory: always-verify-current-sota).
 */
const PANELS: Record<PanelId, PanelDef> = {
  "deep-reasoning": {
    id: "deep-reasoning",
    description: "Multi-hop inference, hypothesis generation, hard reasoning. Highest-II reasoners across providers.",
    members: [
      { subject: "openai@gpt-5.5", weight: 1.0, specialty: ["reasoner"] },
      { subject: "anthropic@claude-opus-4-7", weight: 1.0, specialty: ["reasoner"] },
      { subject: "openrouter@deepseek/deepseek-r1", weight: 0.85, specialty: ["reasoner", "open-weight"] },
      { subject: "openrouter@moonshotai/kimi-k2-thinking", weight: 0.85, specialty: ["reasoner", "open-weight"] },
    ],
    defaultMode: "synthesis",
    defaultJudge: "anthropic@claude-sonnet-4-6",
  },
  "broad-knowledge": {
    id: "broad-knowledge",
    description: "Factual recall about people / orgs / infra. Maximizes training-data diversity (US/EU/CN/live X).",
    members: [
      { subject: "openai@gpt-5.5", weight: 1.0, specialty: ["broad"] },
      { subject: "anthropic@claude-opus-4-7", weight: 1.0, specialty: ["broad"] },
      { subject: "gemini@gemini-3.1-pro-preview", weight: 1.0, specialty: ["broad"] },
      { subject: "openrouter@x-ai/grok-4.20", weight: 0.9, specialty: ["broad", "live-x"] },
      { subject: "openrouter@moonshotai/kimi-k2.6", weight: 0.85, specialty: ["broad", "open-weight"] },
    ],
    defaultMode: "synthesis",
    defaultJudge: "openai@gpt-5.4-mini",
  },
  "cjk-knowledge": {
    id: "cjk-knowledge",
    description: "Chinese / Japanese / Korean entities, platforms, languages. Western-trained models silently fail here.",
    members: [
      { subject: "openrouter@qwen/qwen3.6-max-preview", weight: 1.0, specialty: ["cjk", "open-weight"] },
      { subject: "openrouter@moonshotai/kimi-k2.6", weight: 1.0, specialty: ["cjk", "open-weight"] },
      { subject: "openrouter@xiaomi/mimo-v2.5-pro", weight: 0.9, specialty: ["cjk", "open-weight"] },
      { subject: "anthropic@claude-opus-4-7", weight: 0.7, specialty: ["broad"] },
    ],
    defaultMode: "synthesis",
    defaultJudge: "openrouter@qwen/qwen3.6-plus",
  },
  "vision": {
    id: "vision",
    description: "Image OSINT: geolocation, signage / plate reading, EXIF cross-check, reverse-image lead validation.",
    members: [
      { subject: "anthropic@claude-opus-4-7", weight: 1.0, specialty: ["vision"] },
      { subject: "openai@gpt-5.5", weight: 1.0, specialty: ["vision"] },
      { subject: "gemini@gemini-3.1-pro-preview", weight: 1.0, specialty: ["vision"] },
      { subject: "openrouter@qwen/qwen3.6-plus", weight: 0.85, specialty: ["vision", "open-weight"] },
      { subject: "openrouter@mistralai/pixtral-large-2411", weight: 0.85, specialty: ["vision", "open-weight"] },
    ],
    defaultMode: "synthesis",
    defaultJudge: "anthropic@claude-sonnet-4-6",
  },
  "fast-cheap": {
    id: "fast-cheap",
    description: "High-volume routine classification. Use when the question is easy but called many times.",
    members: [
      { subject: "anthropic@claude-haiku-4-5", weight: 1.0, specialty: ["cheap"] },
      { subject: "openai@gpt-5.4-mini", weight: 1.0, specialty: ["cheap"] },
      { subject: "gemini@gemini-3.1-flash-lite-preview", weight: 1.0, specialty: ["cheap"] },
    ],
    defaultMode: "parallel-poll",
    defaultJudge: "openai@gpt-5.4-mini",
  },
  "adversarial-redteam": {
    id: "adversarial-redteam",
    description: "Adversary-mindset hypothesis stress-testing. 'If this entity were hiding, what would they hide?'",
    members: [
      { subject: "anthropic@claude-opus-4-7", weight: 1.0, specialty: ["reasoner"] },
      { subject: "openai@gpt-5.5", weight: 1.0, specialty: ["reasoner"] },
      { subject: "openrouter@deepseek/deepseek-r1", weight: 0.9, specialty: ["reasoner", "less-aligned"] },
      { subject: "openrouter@x-ai/grok-4.20", weight: 0.9, specialty: ["less-aligned"] },
    ],
    defaultMode: "adversarial",
    defaultJudge: "anthropic@claude-opus-4-7",
  },
};

function memberAvailable(member: PanelMember): boolean {
  const provider = member.subject.split("@")[0]! as Provider;
  return providerHasKey(provider);
}

export function listAvailablePanels(): Array<{
  id: PanelId;
  description: string;
  available: boolean;
  member_count_total: number;
  member_count_available: number;
  available_modes: Mode[];
}> {
  return Object.values(PANELS).map((p) => {
    const total = p.members.length;
    const avail = p.members.filter(memberAvailable).length;
    return {
      id: p.id,
      description: p.description,
      available: avail >= 2,
      member_count_total: total,
      member_count_available: avail,
      // All four modes work as long as quorum is met. Specialized routing comes in v2.
      available_modes: avail >= 2 ? ["parallel-poll", "synthesis", "adversarial", "roundtable"] : [],
    };
  });
}

interface ConsultArgs {
  question: string;
  context?: string;
  panelId: PanelId;
  mode: Mode;
  imageB64?: string;
  imageMime?: string;
  /** Override the default judge for the run. */
  judge?: string;
  /** Per-call max tokens cap (defaults vary by mode). */
  maxTokens?: number;
}

interface MemberResponse {
  subject: string;
  ok: boolean;
  text: string;
  took_ms: number;
  error?: string;
  usage?: { input_tokens?: number; output_tokens?: number };
}

export interface ConsultResult {
  panel_id: PanelId;
  mode: Mode;
  individual: MemberResponse[];
  consensus?: string;
  agreement_score: number;
  disagreements?: string[];
  confidence_warnings?: string[];
  follow_ups?: string[];
  /** Adversarial-mode payload. */
  adversarial?: {
    prosecution: MemberResponse[];
    defense: MemberResponse[];
    judge_verdict: unknown;
  };
  /** Roundtable-mode payload. */
  roundtable?: {
    rounds: Array<{ round: number; responses: MemberResponse[] }>;
  };
  total_took_ms: number;
  estimated_cost_millicredits: number;
  judge_used?: string;
}

/**
 * Consult the panel. Returns a ConsultResult containing per-member responses,
 * synthesized consensus (when applicable), and disagreements.
 */
export async function consultPanel(args: ConsultArgs): Promise<ConsultResult> {
  const panel = PANELS[args.panelId];
  if (!panel) throw new Error(`unknown panel: ${args.panelId}`);
  const available = panel.members.filter(memberAvailable);
  if (available.length < 2) {
    throw new Error(
      `panel "${args.panelId}" needs ≥2 reachable members; have ${available.length} (set the missing API keys)`,
    );
  }

  const t0 = performance.now();
  let result: ConsultResult;

  switch (args.mode) {
    case "parallel-poll":
      result = await parallelPoll(panel, available, args);
      break;
    case "synthesis":
      result = await synthesisRun(panel, available, args);
      break;
    case "adversarial":
      result = await adversarialRun(panel, available, args);
      break;
    case "roundtable":
      result = await roundtableRun(panel, available, args);
      break;
  }

  result.total_took_ms = performance.now() - t0;
  result.estimated_cost_millicredits = estimateCost(result, args.mode);
  return result;
}

function membersToPrompt(question: string, context?: string): string {
  let p = question;
  if (context && context.trim().length > 0) {
    p += `\n\n[Context — raw OSINT findings or evidence]\n${context}`;
  }
  return p;
}

async function callOneMember(
  member: PanelMember,
  req: LLMRequest,
): Promise<MemberResponse> {
  const t0 = performance.now();
  const llm: LLM = makeLLM(member.subject);
  try {
    const r = await llm.call(req);
    return {
      subject: member.subject,
      ok: true,
      text: r.text,
      took_ms: r.took_ms,
      usage: r.usage,
    };
  } catch (e) {
    return {
      subject: member.subject,
      ok: false,
      text: "",
      took_ms: performance.now() - t0,
      error: (e as Error).message.slice(0, 300),
    };
  }
}

async function parallelPoll(
  panel: PanelDef,
  members: PanelMember[],
  args: ConsultArgs,
): Promise<ConsultResult> {
  const prompt = membersToPrompt(args.question, args.context);
  const req: LLMRequest = {
    prompt,
    imageB64: args.imageB64,
    imageMime: args.imageMime,
    temperature: 0,
    maxTokens: args.maxTokens ?? 1024,
  };
  const responses = await Promise.all(members.map((m) => callOneMember(m, req)));
  const okText = responses.filter((r) => r.ok).map((r) => r.text);
  return {
    panel_id: panel.id,
    mode: "parallel-poll",
    individual: responses,
    agreement_score: quickAgreementScore(okText),
    total_took_ms: 0,
    estimated_cost_millicredits: 0,
  };
}

async function synthesisRun(
  panel: PanelDef,
  members: PanelMember[],
  args: ConsultArgs,
): Promise<ConsultResult> {
  const polled = await parallelPoll(panel, members, args);
  const okResponses = polled.individual.filter((r) => r.ok);
  if (okResponses.length === 0) {
    return { ...polled, mode: "synthesis", consensus: "no panel members responded successfully" };
  }

  const judgeSubject = args.judge ?? panel.defaultJudge;
  const judge = makeLLM(judgeSubject);
  const responsesBlob = okResponses
    .map((r) => `── ${r.subject} ──\n${r.text}`)
    .join("\n\n");
  const userPrompt = SYNTHESIS_USER_TEMPLATE
    .replace("{question}", args.question)
    .replace("{context}", args.context ?? "(no additional context provided)")
    .replace("{responses}", responsesBlob);

  let consensus = "(judge call failed)";
  let disagreements: string[] | undefined;
  let confidenceWarnings: string[] | undefined;
  let followUps: string[] | undefined;
  let agreementScore = polled.agreement_score;
  try {
    const judged = await judge.call({
      system: SYNTHESIS_SYSTEM,
      prompt: userPrompt,
      jsonOutput: true,
      temperature: 0,
      maxTokens: 2048,
    });
    const parsed = safeJSON(judged.text) as {
      consensus?: string;
      agreement?: number;
      disagreements?: string[];
      confidence_warnings?: string[];
      follow_ups?: string[];
    };
    if (parsed) {
      consensus = parsed.consensus ?? consensus;
      if (typeof parsed.agreement === "number") agreementScore = parsed.agreement;
      disagreements = parsed.disagreements;
      confidenceWarnings = parsed.confidence_warnings;
      followUps = parsed.follow_ups;
    }
  } catch (e) {
    consensus = `(judge errored: ${(e as Error).message.slice(0, 120)})`;
  }

  return {
    ...polled,
    mode: "synthesis",
    consensus,
    agreement_score: agreementScore,
    disagreements,
    confidence_warnings: confidenceWarnings,
    follow_ups: followUps,
    judge_used: judgeSubject,
  };
}

async function adversarialRun(
  panel: PanelDef,
  members: PanelMember[],
  args: ConsultArgs,
): Promise<ConsultResult> {
  const half = Math.max(1, Math.floor(members.length / 2));
  const prosecutors = members.slice(0, half);
  const defenders = members.slice(half);

  const prosReq: LLMRequest = {
    system: ADVERSARIAL_PROSECUTION_SYSTEM,
    prompt: membersToPrompt(args.question, args.context),
    imageB64: args.imageB64,
    imageMime: args.imageMime,
    temperature: 0,
    maxTokens: args.maxTokens ?? 1024,
  };
  const prosecution = await Promise.all(prosecutors.map((m) => callOneMember(m, prosReq)));
  const prosBlob = prosecution.filter((r) => r.ok).map((r) => `── ${r.subject} ──\n${r.text}`).join("\n\n");

  const defReq: LLMRequest = {
    system: ADVERSARIAL_DEFENSE_SYSTEM,
    prompt: `${membersToPrompt(args.question, args.context)}\n\n[Prosecution case]\n${prosBlob}`,
    imageB64: args.imageB64,
    imageMime: args.imageMime,
    temperature: 0,
    maxTokens: args.maxTokens ?? 1024,
  };
  const defense = await Promise.all(defenders.map((m) => callOneMember(m, defReq)));
  const defBlob = defense.filter((r) => r.ok).map((r) => `── ${r.subject} ──\n${r.text}`).join("\n\n");

  const judgeSubject = args.judge ?? panel.defaultJudge;
  const judge = makeLLM(judgeSubject);
  let verdict: unknown = null;
  try {
    const judged = await judge.call({
      system: ADVERSARIAL_JUDGE_SYSTEM,
      prompt: `[Question]\n${args.question}\n\n[Prosecution]\n${prosBlob}\n\n[Defense]\n${defBlob}`,
      jsonOutput: true,
      temperature: 0,
      maxTokens: 2048,
    });
    verdict = safeJSON(judged.text);
  } catch (e) {
    verdict = { error: (e as Error).message.slice(0, 200) };
  }

  return {
    panel_id: panel.id,
    mode: "adversarial",
    individual: [...prosecution, ...defense],
    adversarial: { prosecution, defense, judge_verdict: verdict },
    agreement_score: quickAgreementScore([...prosecution, ...defense].filter((r) => r.ok).map((r) => r.text)),
    judge_used: judgeSubject,
    total_took_ms: 0,
    estimated_cost_millicredits: 0,
  };
}

async function roundtableRun(
  panel: PanelDef,
  members: PanelMember[],
  args: ConsultArgs,
): Promise<ConsultResult> {
  const basePrompt = membersToPrompt(args.question, args.context);
  const req1: LLMRequest = {
    prompt: basePrompt,
    imageB64: args.imageB64,
    imageMime: args.imageMime,
    temperature: 0,
    maxTokens: args.maxTokens ?? 800,
  };
  const round1 = await Promise.all(members.map((m) => callOneMember(m, req1)));

  const round1Blob = round1.filter((r) => r.ok).map((r) => `── ${r.subject} ──\n${r.text}`).join("\n\n");
  const req2: LLMRequest = {
    prompt: `${basePrompt}\n\n[Other panel members' first-pass responses]\n${round1Blob}\n\nNow critique the other responses and refine your own answer.`,
    imageB64: args.imageB64,
    imageMime: args.imageMime,
    temperature: 0,
    maxTokens: args.maxTokens ?? 800,
  };
  const round2 = await Promise.all(members.map((m) => callOneMember(m, req2)));

  // Round 3 = synthesis
  const judgeSubject = args.judge ?? panel.defaultJudge;
  const judge = makeLLM(judgeSubject);
  const round2Blob = round2.filter((r) => r.ok).map((r) => `── ${r.subject} ──\n${r.text}`).join("\n\n");
  let consensus = "(judge call failed)";
  let disagreements: string[] | undefined;
  try {
    const judged = await judge.call({
      system: SYNTHESIS_SYSTEM,
      prompt: SYNTHESIS_USER_TEMPLATE
        .replace("{question}", args.question)
        .replace("{context}", args.context ?? "")
        .replace("{responses}", `[Round 1]\n${round1Blob}\n\n[Round 2 with cross-critique]\n${round2Blob}`),
      jsonOutput: true,
      temperature: 0,
      maxTokens: 2048,
    });
    const parsed = safeJSON(judged.text) as { consensus?: string; disagreements?: string[] } | null;
    if (parsed) {
      consensus = parsed.consensus ?? consensus;
      disagreements = parsed.disagreements;
    }
  } catch (e) {
    consensus = `(judge errored: ${(e as Error).message.slice(0, 120)})`;
  }

  return {
    panel_id: panel.id,
    mode: "roundtable",
    individual: [...round1, ...round2],
    roundtable: {
      rounds: [
        { round: 1, responses: round1 },
        { round: 2, responses: round2 },
      ],
    },
    consensus,
    disagreements,
    agreement_score: quickAgreementScore(round2.filter((r) => r.ok).map((r) => r.text)),
    judge_used: judgeSubject,
    total_took_ms: 0,
    estimated_cost_millicredits: 0,
  };
}

function safeJSON(text: string): unknown {
  let s = text.trim();
  const fence = s.match(/```(?:json)?\s*\n?([\s\S]*?)\n?```/);
  if (fence) s = fence[1]!;
  const obj = s.match(/\{[\s\S]*\}/);
  if (obj) s = obj[0];
  try { return JSON.parse(s); } catch { return null; }
}

/** Rough estimate; the registry pre-deducts `costMillicredits`. True-up via usage in v2. */
function estimateCost(result: ConsultResult, mode: Mode): number {
  const memberCalls = result.individual.length;
  const judgeCall = mode === "synthesis" || mode === "adversarial" || mode === "roundtable" ? 1 : 0;
  // Assume ~12 millicredits per member call (input+output for ~1k tokens at mid-tier rates), 8 for judge.
  return Math.ceil(memberCalls * 12 + judgeCall * 8);
}

export const PANEL_REGISTRY = PANELS;
