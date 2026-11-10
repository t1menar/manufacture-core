// Package metrics exposes the core metrics in the Prometheus text format.
// Run and replica metrics are read from PostgreSQL on scrape, so they are the
// same whichever core process is scraped and survive restarts; event counters
// (stale results) live in the process.
package metrics

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Metrics collects in-process counters and serves /metrics.
type Metrics struct {
	Pool *pgxpool.Pool
	Log  *slog.Logger

	mu    sync.Mutex
	stale map[[2]string]float64
}

// RunFinished is counted from the database (mf_runs_finished_total).
func (m *Metrics) RunFinished(string, string, time.Duration) {}

// StaleResult counts a discarded module message.
func (m *Metrics) StaleResult(module, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stale == nil {
		m.stale = map[[2]string]float64{}
	}
	m.stale[[2]string{module, reason}]++
}

func label(v string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
}

type family struct {
	name, help, typ string
	samples         []string
}

func (f *family) add(value float64, labels ...string) {
	var b strings.Builder
	b.WriteString(f.name)
	if len(labels) > 0 {
		b.WriteByte('{')
		for i := 0; i+1 < len(labels); i += 2 {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `%s="%s"`, labels[i], label(labels[i+1]))
		}
		b.WriteByte('}')
	}
	fmt.Fprintf(&b, " %g", value)
	f.samples = append(f.samples, b.String())
}

func (f *family) write(w io.Writer) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", f.name, f.help, f.name, f.typ)
	for _, s := range f.samples {
		fmt.Fprintln(w, s)
	}
}

// ServeHTTP writes the metrics.
func (m *Metrics) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	fams, err := m.collect(ctx)
	if err != nil {
		if m.Log != nil {
			m.Log.WarnContext(ctx, "metrics", "err", err)
		}
		http.Error(w, "metrics unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	for _, f := range fams {
		f.write(w)
	}
}

func (m *Metrics) collect(ctx context.Context) ([]*family, error) {
	finished := &family{name: "mf_runs_finished_total", help: "Runs that ended, by pipeline and status.", typ: "counter"}
	active := &family{name: "mf_runs", help: "Unfinished runs by pipeline and status.", typ: "gauge"}
	rows, err := m.Pool.Query(ctx, `SELECT pipeline, status, count(*) FROM runs GROUP BY pipeline, status ORDER BY pipeline, status`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p, s string
		var n float64
		if err := rows.Scan(&p, &s, &n); err != nil {
			rows.Close()
			return nil, err
		}
		switch s {
		case "succeeded", "failed", "cancelled":
			finished.add(n, "pipeline", p, "status", s)
		default:
			active.add(n, "pipeline", p, "status", s)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	replicas := &family{name: "mf_module_replicas", help: "Registered module replicas by status.", typ: "gauge"}
	rows, err = m.Pool.Query(ctx, `SELECT module, status, count(*) FROM module_replicas GROUP BY module, status ORDER BY module, status`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var mod, s string
		var n float64
		if err := rows.Scan(&mod, &s, &n); err != nil {
			rows.Close()
			return nil, err
		}
		replicas.add(n, "module", mod, "status", s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	outbox := &family{name: "mf_outbox_messages", help: "Outbox rows not yet published (pending) or given up (dead).", typ: "gauge"}
	var pending, dead float64
	if err := m.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = 'pending'), count(*) FILTER (WHERE status = 'dead') FROM outbox`).
		Scan(&pending, &dead); err != nil {
		return nil, err
	}
	outbox.add(pending, "status", "pending")
	outbox.add(dead, "status", "dead")

	stale := &family{name: "mf_stale_results_total", help: "Module messages discarded as stale, by module and type.", typ: "counter"}
	m.mu.Lock()
	keys := make([][2]string, 0, len(m.stale))
	for k := range m.stale {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+keys[i][1] < keys[j][0]+keys[j][1] })
	for _, k := range keys {
		stale.add(m.stale[k], "module", k[0], "reason", k[1])
	}
	m.mu.Unlock()
	return []*family{finished, active, replicas, outbox, stale}, nil
}
