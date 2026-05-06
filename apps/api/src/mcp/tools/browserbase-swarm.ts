import { z } from "zod";
import { toolRegistry } from "./instance";
import { createBrowserbaseSession, invokeBrowserbaseFunction } from "../../browserbase/client";

const task = z.object({
  id: z.string().min(1).optional(),
  url: z.string().url(),
  instruction: z.string().min(3),
  depends_on: z.array(z.string()).default([]),
});

const input = z.object({
  goal: z.string().min(3),
  mode: z.enum(["create_sessions", "invoke_function"]).default("create_sessions"),
  tasks: z.array(task).min(1).max(12),
  function_id: z.string().min(1).optional().describe("Browserbase Function ID. Falls back to BROWSERBASE_SWARM_FUNCTION_ID."),
  proxies: z.boolean().default(true),
  solve_captchas: z.boolean().default(true),
  keep_alive: z.boolean().default(false),
  context_id: z.string().optional().describe("Browserbase context ID for shared auth/cookies across sessions."),
});

toolRegistry.register({
  name: "browserbase_swarm",
  description:
    "**Browserbase browser-agent swarm coordinator — REQUIRES BROWSERBASE_API_KEY and BROWSERBASE_PROJECT_ID.** Creates one isolated real Chrome session per worker task, or invokes a deployed Browserbase Function for each worker. Use for goals that need parallel browser interaction, logged-in contexts, CAPTCHA-capable sessions, or page actions that Search/Fetch cannot complete. Outputs worker assignments, session/function metadata, dependencies, and typed entities.",
  inputSchema: input,
  costMillicredits: 25,
  handler: async (i) => {
    const started = Date.now();
    const functionId = i.function_id || process.env.BROWSERBASE_SWARM_FUNCTION_ID;
    if (i.mode === "invoke_function" && !functionId) {
      throw new Error("function_id or BROWSERBASE_SWARM_FUNCTION_ID required for invoke_function mode");
    }

    const workers = await Promise.all(
      i.tasks.map(async (raw, index) => {
        const id = raw.id || `worker-${index + 1}`;
        if (i.mode === "invoke_function") {
          const invocation = await invokeBrowserbaseFunction(functionId!, {
            goal: i.goal,
            task_id: id,
            url: raw.url,
            instruction: raw.instruction,
            depends_on: raw.depends_on,
          });
          return { id, ...raw, function_id: functionId, invocation };
        }

        const session = await createBrowserbaseSession({
          url: raw.url,
          instruction: raw.instruction,
          proxies: i.proxies,
          solveCaptchas: i.solve_captchas,
          keepAlive: i.keep_alive,
          contextId: i.context_id,
          metadata: {
            tool: "browserbase_swarm",
            goal: i.goal,
            task_id: id,
            depends_on: raw.depends_on,
          },
        });
        return { id, ...raw, session };
      }),
    );

    return {
      goal: i.goal,
      mode: i.mode,
      workers,
      coordination: {
        strategy: "fan_out_then_synthesize",
        max_parallel_workers: i.tasks.length,
        dependency_edges: workers.flatMap((w) => w.depends_on.map((dep) => ({ from: dep, to: w.id }))),
        synthesis_instruction:
          "Wait for all independent workers, resolve dependent workers after their prerequisites, then merge findings by URL, timestamp, and cited browser evidence.",
      },
      entities: workers.map((w) => ({
        kind: i.mode === "invoke_function" ? "browserbase_function_invocation" : "browser_session",
        url: w.url,
        name: w.id,
        description: w.instruction,
        attributes: {
          goal: i.goal,
          depends_on: w.depends_on,
          session_id: "session" in w ? w.session.id : undefined,
          connect_url: "session" in w ? w.session.connectUrl : undefined,
          invocation_id: "invocation" in w ? w.invocation.id : undefined,
          function_id: "function_id" in w ? w.function_id : undefined,
        },
      })),
      highlight_findings: [
        `Assigned ${workers.length} Browserbase worker(s) for goal: ${i.goal}`,
        i.mode === "invoke_function"
          ? "Invoked deployed Browserbase Function once per worker task."
          : "Created isolated browser sessions for downstream Stagehand/Playwright/browser-use control.",
      ],
      source: "browserbase.com",
      tookMs: Date.now() - started,
    };
  },
});
