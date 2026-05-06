import { z } from "zod";
import { toolRegistry } from "./instance";
import { consultPanel, safeJSON, type PanelId } from "../../llm/panel";
import { ER_PANEL_SYSTEM } from "../../llm/panel-aggregators";

const findingSchema = z.object({
  source_tool: z.string().describe("Which OSINT tool produced this finding (e.g. 'github_user_profile')."),
  type: z.enum(["email", "handle", "phone", "name", "domain", "ip", "url", "other"]),
  value: z.string(),
  evidence: z.record(z.string(), z.unknown()).optional().describe("Additional fields the source tool emitted alongside the value."),
});

const input = z.object({
  findings: z.array(findingSchema).min(2).max(200).describe(
    "List of findings from one or more OSINT tools. Typical input is the raw output of person_aggregate or domain_aggregate, normalized into one row per finding.",
  ),
  seed_subject: z.string().optional().describe(
    "Known anchor identity (name / email / handle) to bias clustering toward. Optional but improves accuracy noticeably.",
  ),
  panel: z.enum(["deep-reasoning", "broad-knowledge", "cjk-knowledge"]).default("deep-reasoning"),
});

toolRegistry.register({
  name: "panel_entity_resolution",
  description:
    "ENTITY RESOLUTION specialist: takes raw OSINT findings (typically from person_aggregate / domain_aggregate / your own collected leads) and asks the LLM panel to cluster them into groups likely to refer to the same real-world entity. Output: clusters with confidence scores and evidence, unclustered findings the panel couldn't link, and manual_review_flags where panel members split (those need human judgment). Run THIS AFTER fan-out tools, not before — it interprets raw outputs, it doesn't fetch them. Best panel: deep-reasoning for English-speaking targets; cjk-knowledge for Chinese/Japanese/Korean targets.",
  inputSchema: input,
  costMillicredits: 200,
  handler: async (i) => {
    const findingsBlob = JSON.stringify(i.findings, null, 2);
    const seed = i.seed_subject ? `\n\n[Known anchor identity]: ${i.seed_subject}` : "";
    const question = `${ER_PANEL_SYSTEM}${seed}\n\nCluster the following findings:\n\n\`\`\`json\n${findingsBlob}\n\`\`\``;
    const result = await consultPanel({
      question,
      panelId: i.panel as PanelId,
      mode: "synthesis",
    });

    // Try to extract structured clusters from the consensus payload using the
    // shared safeJSON helper that also recovers token-truncated responses.
    // The legacy inline parser missed truncated/repaired cases, dropping ~half
    // of large clustering outputs. See test/safe-json-coverage.test.ts.
    const structured: unknown = result.consensus ? safeJSON(result.consensus) : null;

    return {
      structured,
      raw_consensus: result.consensus,
      agreement_score: result.agreement_score,
      disagreements: result.disagreements,
      manual_review_recommended: (result.agreement_score ?? 0) < 0.5,
      individual_responses: result.individual,
      panel_used: result.panel_id,
      cost_millicredits: result.estimated_cost_millicredits,
      total_took_ms: result.total_took_ms,
    };
  },
});
