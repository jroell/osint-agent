package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// bqQuery shells out to the `bq` CLI to run a SQL query against BigQuery.
// Requires the worker host to have gcloud authenticated. Production
// deployment will need ADC + the Cloud BigQuery Go client; for development
// + the cron-loop session this is sufficient.
//
// Returns parsed JSON response or an error. Includes 60s timeout.
func bqQuery(ctx context.Context, sql string, maxRows int) ([]map[string]any, error) {
	if maxRows <= 0 {
		maxRows = 100
	}
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "bq",
		"query",
		"--use_legacy_sql=false",
		"--format=json",
		fmt.Sprintf("--max_rows=%d", maxRows),
		"--quiet",
		"--headless",
		sql,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Try to extract a useful error message from bq's output
		errMsg := strings.TrimSpace(string(out))
		if errMsg == "" {
			errMsg = err.Error()
		}
		// Detect common auth failures
		if strings.Contains(errMsg, "credentials") || strings.Contains(errMsg, "auth") {
			return nil, fmt.Errorf("bq cli auth failed: %s — run `gcloud auth application-default login` or set GOOGLE_APPLICATION_CREDENTIALS", truncate(errMsg, 250))
		}
		return nil, fmt.Errorf("bq query failed: %s", truncate(errMsg, 400))
	}
	// bq's --quiet still emits a "Waiting on..." line on stderr; we use
	// CombinedOutput so it gets folded into stdout. Filter for the JSON array.
	body := string(out)
	startIdx := strings.Index(body, "[")
	if startIdx < 0 {
		// Empty result or malformed
		return []map[string]any{}, nil
	}
	endIdx := strings.LastIndex(body, "]")
	if endIdx < startIdx {
		return []map[string]any{}, nil
	}
	jsonBody := body[startIdx : endIdx+1]
	var rows []map[string]any
	if err := json.Unmarshal([]byte(jsonBody), &rows); err != nil {
		return nil, fmt.Errorf("bq json parse failed: %w (body sample: %s)", err, truncate(jsonBody, 200))
	}
	return rows, nil
}
