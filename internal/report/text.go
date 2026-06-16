package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// WriteJSON serialises the report as pretty-printed JSON.
func WriteJSON(w io.Writer, r Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteText renders a plain-text report meant to be pasted into an ISP ticket
// or a chat — readable without any tool, no colors, no terminal codes.
func WriteText(w io.Writer, r Report) error {
	var b strings.Builder
	fmt.Fprintf(&b, "net-test %s — отчёт о соединении\n", r.Version)
	fmt.Fprintf(&b, "сгенерирован: %s\n", r.GeneratedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "цель: %s\n", r.Target)
	fmt.Fprintf(&b, "длительность измерений: %.1fs\n", float64(r.DurationMs)/1000)

	if r.Ping != nil {
		p := r.Ping
		fmt.Fprintf(&b, "\nПинг (%d проб):\n", p.Sent)
		if p.Sent > 0 {
			fmt.Fprintf(&b, "  средний RTT: %.1f ms\n", p.AvgMs)
			fmt.Fprintf(&b, "  лучший / худший: %.1f / %.1f ms\n", p.BestMs, p.WorstMs)
			fmt.Fprintf(&b, "  потери: %.1f%% (%d / %d)\n", p.LossPct, p.Sent-p.Recv, p.Sent)
			fmt.Fprintf(&b, "  джиттер: %.1f ms\n", p.JitterMs)
		}
		if p.Err != "" {
			fmt.Fprintf(&b, "  ⚠ ошибка: %s\n", p.Err)
		}
	}

	if r.Trace != nil {
		fmt.Fprintf(&b, "\nМаршрут (%d хопов):\n", len(r.Trace.Hops))
		if len(r.Trace.Hops) > 0 {
			writeHops(&b, r.Trace.Hops)
		}
		if r.Trace.Err != "" {
			fmt.Fprintf(&b, "  ⚠ ошибка: %s\n", r.Trace.Err)
		}
		if len(r.Trace.Diagnosis) > 0 {
			fmt.Fprintln(&b, "\nДиагноз:")
			for _, s := range r.Trace.Diagnosis {
				mark := "OK"
				if !s.Healthy {
					mark = "!!"
				}
				fmt.Fprintf(&b, "  [%s] %-30s %s\n", mark, s.Label, hopRangeText(s))
				if !s.Healthy && s.Issue != "" {
					fmt.Fprintf(&b, "        → %s\n", s.Issue)
				}
			}
		}
	}

	if r.Speed != nil {
		s := r.Speed
		fmt.Fprintln(&b, "\nСкорость:")
		if s.Err != "" {
			fmt.Fprintf(&b, "  ⚠ ошибка: %s\n", s.Err)
		} else {
			fmt.Fprintf(&b, "  загрузка ↓: %.1f Mbps\n", s.DownloadMbps)
			fmt.Fprintf(&b, "  отдача ↑: %.1f Mbps\n", s.UploadMbps)
			fmt.Fprintf(&b, "  латентность: %.1f ms (джиттер %.1f ms)\n", s.LatencyMs, s.JitterMs)
			if s.Server != "" {
				fmt.Fprintf(&b, "  Cloudflare: %s\n", s.Server)
			}
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func writeHops(b *strings.Builder, hops []HopReport) {
	fmt.Fprintln(b, "  TTL  Хост / IP                                          Loss   Avg RTT  AS")
	for _, h := range hops {
		who := "*"
		if h.IP != "" {
			who = h.IP
			if h.Host != "" {
				who = fmt.Sprintf("%s (%s)", h.Host, h.IP)
			}
		}
		if len(who) > 48 {
			who = who[:47] + "…"
		}
		as := h.ASN
		if h.ASName != "" {
			as = h.ASN + " " + h.ASName
		}
		flag := " "
		if h.LossPersists || h.RTTPersists {
			flag = "!"
		}
		fmt.Fprintf(b, "  %2d %s %-48s  %4.0f%%  %6.1f ms  %s\n",
			h.TTL, flag, who, h.LossPct, h.AvgMs, as)
	}
}

func hopRangeText(s SegmentReport) string {
	if s.HopFrom == s.HopTo {
		return fmt.Sprintf("хоп %d", s.HopFrom)
	}
	return fmt.Sprintf("хопы %d-%d", s.HopFrom, s.HopTo)
}
