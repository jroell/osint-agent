package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GeminiCodeExecResult is one execution iteration (code + output).
type GeminiCodeExecResult struct {
	Code   string `json:"code"`
	Output string `json:"output,omitempty"`
}

// GeminiCodeExecOutput is the response.
type GeminiCodeExecOutput struct {
	Prompt            string                  `json:"prompt"`
	Model             string                  `json:"model"`
	Executions        []GeminiCodeExecResult  `json:"executions,omitempty"`
	AnswerText        string                  `json:"answer_text,omitempty"`
	HighlightFindings []string                `json:"highlight_findings"`
	Source            string                  `json:"source"`
	TookMs            int64                   `json:"tookMs"`
}

// raw response shape with code-execution parts
type geminiCodeRespPart struct {
	Text                string                 `json:"text,omitempty"`
	ExecutableCode      *geminiExecutableCode  `json:"executableCode,omitempty"`
	CodeExecutionResult *geminiCodeExecResult  `json:"codeExecutionResult,omitempty"`
}

type geminiExecutableCode struct {
	Language string `json:"language"`
	Code     string `json:"code"`
}

type geminiCodeExecResult struct {
	Outcome string `json:"outcome"`
	Output  string `json:"output"`
}

type geminiCodeRespRaw struct {
	Candidates []struct {
		Content struct {
			Parts []geminiCodeRespPart `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// GeminiCodeExecution lets the agent run Python computations on data via
// Gemini's built-in code_execution tool. Gemini iteratively writes Python,
// executes it, observes the output, and may write more Python if needed.
//
// Why this matters for ER:
//   - Pairs with every other tool: fetch data via X → compute summary
//     stats / dedupe / sort / filter / format via gemini_code_execution.
//   - Strong for: date arithmetic ("how many days between these two
//     events"), numerical aggregation ("mean/median/percentile of these
//     values"), string parsing ("extract all email addresses from this
//     text and count by domain"), simple data wrangling.
//   - Returns: each (code, output) execution iteration + final text
//     explanation. Investigators can audit the exact code Gemini ran
//     for verification.
//
// Use cases:
//   - Cross-reference deduplication: pass a list of names from multiple
//     sources, get a deduplicated list with provenance counts.
//   - Numerical OSINT: compute "median age at death" from obituary data,
//     or "median grant amount" from NIH RePORTER results.
//   - Date arithmetic: "this person was 12 in 2003, when did they
//     graduate from college?"
//   - Pattern extraction: "extract all phone numbers from this text and
//     identify which area codes are Cincinnati."
//
// REQUIRES GOOGLE_AI_API_KEY (or GEMINI_API_KEY).
func GeminiCodeExecution(ctx context.Context, input map[string]any) (*GeminiCodeExecOutput, error) {
	prompt, _ := input["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, fmt.Errorf("input.prompt required")
	}

	// Optional: prepend data context
	dataContext, _ := input["data_context"].(string)
	if dataContext != "" {
		prompt = "Context data:\n" + dataContext + "\n\nTask: " + prompt
	}

	model, _ := input["model"].(string)
	model = strings.TrimSpace(model)
	if model == "" {
		model = "gemini-3.1-pro-preview"
	}

	out := &GeminiCodeExecOutput{
		Prompt: prompt,
		Model:  model,
		Source: "ai.google.dev/gemini-api (code_execution tool)",
	}
	start := time.Now()

	body := map[string]any{
		"contents": []any{map[string]any{
			"parts": []any{map[string]any{"text": prompt}},
		}},
		"tools": []any{map[string]any{"code_execution": map[string]any{}}},
	}
	bodyBytes, _ := json.Marshal(body)

	apiKey, err := geminiAPIKey()
	if err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)
	req, _ := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1")

	cli := &http.Client{Timeout: 240 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini: %w", err)
	}
	defer resp.Body.Close()
	rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("gemini %d: %s", resp.StatusCode, hfTruncate(string(rawBody), 400))
	}

	var parsed geminiCodeRespRaw
	if err := json.Unmarshal(rawBody, &parsed); err != nil {
		return nil, fmt.Errorf("gemini decode: %w", err)
	}
	if parsed.Error.Message != "" {
		return nil, fmt.Errorf("gemini api: %s", parsed.Error.Message)
	}
	if len(parsed.Candidates) == 0 {
		return nil, fmt.Errorf("gemini returned no candidates")
	}

	// Walk the parts in order. Group adjacent (code, result) pairs into Executions.
	parts := parsed.Candidates[0].Content.Parts
	textBuilder := strings.Builder{}
	pendingCode := ""
	for _, p := range parts {
		if p.ExecutableCode != nil {
			pendingCode = p.ExecutableCode.Code
		}
		if p.CodeExecutionResult != nil {
			out.Executions = append(out.Executions, GeminiCodeExecResult{
				Code:   pendingCode,
				Output: p.CodeExecutionResult.Output,
			})
			pendingCode = ""
		}
		if p.Text != "" {
			textBuilder.WriteString(p.Text)
			textBuilder.WriteString("\n")
		}
	}
	// Catch trailing pending code without result
	if pendingCode != "" {
		out.Executions = append(out.Executions, GeminiCodeExecResult{Code: pendingCode})
	}
	out.AnswerText = strings.TrimSpace(textBuilder.String())

	out.HighlightFindings = []string{
		fmt.Sprintf("✓ Gemini %s ran %d code-execution iterations (%d chars in answer)", model, len(out.Executions), len(out.AnswerText)),
	}
	for i, e := range out.Executions {
		codePreview := e.Code
		if len(codePreview) > 100 {
			codePreview = codePreview[:100] + "..."
		}
		outputPreview := e.Output
		if len(outputPreview) > 100 {
			outputPreview = outputPreview[:100] + "..."
		}
		out.HighlightFindings = append(out.HighlightFindings,
			fmt.Sprintf("  iter %d code: %s", i+1, strings.ReplaceAll(codePreview, "\n", " ↵ ")))
		if outputPreview != "" {
			out.HighlightFindings = append(out.HighlightFindings,
				fmt.Sprintf("  iter %d output: %s", i+1, strings.ReplaceAll(outputPreview, "\n", " ↵ ")))
		}
	}
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}
