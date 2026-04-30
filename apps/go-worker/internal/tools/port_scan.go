package tools

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

type PortScanOutput struct {
	Target  string `json:"target"`
	Open    []int  `json:"open"`
	Scanned int    `json:"scanned"`
	TookMs  int64  `json:"tookMs"`
	Note    string `json:"note"`
}

// "top 100" classic-Nmap-style ports — covers the 95th percentile of services
// found on real-world hosts. Bigger lists are available; for an MCP tool that
// favors speed and surprise-free output, this is the right cardinality.
var top100Ports = []int{
	7, 9, 13, 21, 22, 23, 25, 26, 37, 53, 79, 80, 81, 88, 106, 110, 111, 113, 119, 135,
	139, 143, 144, 179, 199, 389, 427, 443, 444, 445, 465, 513, 514, 515, 543, 544, 548,
	554, 587, 631, 646, 873, 990, 993, 995, 1025, 1026, 1027, 1028, 1029, 1110, 1433, 1720,
	1723, 1755, 1900, 2000, 2001, 2049, 2121, 2717, 3000, 3128, 3306, 3389, 3986, 4000,
	4001, 4002, 4045, 5000, 5001, 5060, 5101, 5190, 5357, 5432, 5631, 5666, 5800, 5900,
	5901, 6000, 6001, 6646, 7070, 8000, 8008, 8009, 8080, 8081, 8443, 8888, 9100, 9999,
	10000, 32768, 49152, 49154,
}

// PortScanPassive runs a concurrent TCP-connect probe against the resolved IP(s)
// of `target` (a hostname or IP). Reports which top-100 well-known ports accept
// a connection within `timeout_ms`. Stdlib only — no SYN scanning, no root
// required, no naabu. Note this IS active probing; the "passive" label in the
// design doc refers to the spec phase, not the network behavior.
func PortScanPassive(ctx context.Context, input map[string]any) (*PortScanOutput, error) {
	target, _ := input["target"].(string)
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, errors.New("input.target required (hostname or IP)")
	}
	concurrency := 64
	if v, ok := input["concurrency"].(float64); ok && v > 0 {
		concurrency = int(v)
	}
	perPortTimeoutMs := 1500
	if v, ok := input["per_port_timeout_ms"].(float64); ok && v >= 200 {
		perPortTimeoutMs = int(v)
	}
	// Optional caller-supplied port list overrides the top-100 default.
	ports := top100Ports
	if pv, ok := input["ports"].([]interface{}); ok && len(pv) > 0 {
		ports = make([]int, 0, len(pv))
		for _, p := range pv {
			if n, ok := p.(float64); ok && n > 0 && n < 65536 {
				ports = append(ports, int(n))
			}
		}
	}

	start := time.Now()
	timeout := time.Duration(perPortTimeoutMs) * time.Millisecond

	var (
		open []int
		mu   sync.Mutex
		wg   sync.WaitGroup
		sem  = make(chan struct{}, concurrency)
	)

	for _, p := range ports {
		wg.Add(1)
		sem <- struct{}{}
		go func(port int) {
			defer wg.Done()
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			d := net.Dialer{Timeout: timeout}
			conn, err := d.DialContext(cctx, "tcp", fmt.Sprintf("%s:%d", target, port))
			if err != nil {
				return
			}
			_ = conn.Close()
			mu.Lock()
			open = append(open, port)
			mu.Unlock()
		}(p)
	}
	wg.Wait()
	sort.Ints(open)

	return &PortScanOutput{
		Target:  target,
		Open:    open,
		Scanned: len(ports),
		TookMs:  time.Since(start).Milliseconds(),
		Note:    "TCP-connect probe of well-known ports — actively connects to the target. Only run against hosts you have explicit authorization to probe.",
	}, nil
}
