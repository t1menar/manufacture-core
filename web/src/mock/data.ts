/** Example specs the simulator boots with (hello-world is the acceptance pipeline from the design, §10). */

export const HELLO_YAML = `apiVersion: manufacture.dev/v1
kind: Module
metadata: {name: hello-init}
spec: {image: localhost:5001/mf-hello-init:0.1.0, replicas: 1}
---
apiVersion: manufacture.dev/v1
kind: Module
metadata: {name: hello-hello}
spec: {image: localhost:5001/mf-hello-hello:0.1.0, replicas: 2}
---
apiVersion: manufacture.dev/v1
kind: Module
metadata: {name: hello-world}
spec: {image: localhost:5001/mf-hello-world:0.1.0, replicas: 1}
---
apiVersion: manufacture.dev/v1
kind: Module
metadata: {name: hello-exclaim}
spec: {image: localhost:5001/mf-hello-exclaim:0.1.0, replicas: 1}
---
apiVersion: manufacture.dev/v1
kind: Pipeline
metadata: {name: hello-world}
spec:
  trigger: {source: hello-init}
  concurrency: {max_runs: 10}
  context:
    schema: {type: object, properties: {text: {type: string}}, required: [text]}
  stages:
    - name: hello
      step: {module: hello-hello, timeout: 30s, retry: {max_attempts: 3}}
    - name: world
      step: {module: hello-world, timeout: 30s}
    - name: exclaim
      step: {module: hello-exclaim, timeout: 30s}
`;

export const GEO_YAML = `apiVersion: manufacture.dev/v1
kind: Pipeline
metadata: {name: geo-analysis, labels: {project: geo}}
spec:
  trigger: {source: geo-ingest}
  concurrency: {max_runs: 6}
  stages:
    - name: ingest
      step: {module: geo-ingest}
    - name: tiles
      step: {module: geo-split, merge: none}
    - name: analyze-tiles
      foreach: steps.tiles.output.tiles
      max_parallel: 16
      join: {mode: all, merge: array_append}
      step: {module: geo-analyze, input: {tile: item, n: index}}
    - name: layers
      parallel:
        - {module: geo-ndvi}
        - {module: geo-roads, optional: true}
        - {id: buildings-v2, module: geo-buildings, config: {model: v2}}
      join: {mode: all, merge: error}
    - name: report
      step: {module: geo-report}
`;

export const VIDEO_YAML = `apiVersion: manufacture.dev/v1
kind: Pipeline
metadata: {name: video, labels: {project: reels}}
spec:
  trigger: {source: ideas}
  concurrency: {max_runs: 3}
  blobs: {ttl: 30d}
  stages:
    - name: video
      step: {module: video-render, timeout: 90m}
    - name: review
      step: {module: review-telegram, timeout: 7d, lease: 10m, retry: {max_attempts: 1}}
      routes:
        - when: "steps.review.output.decision == 'rejected'"
          goto: video
          max_visits: 3
    - name: publish
      step: {module: publish-instagram, timeout: 30m}
`;

/** 15 stages / 20 modules: exercises the snake layout and the "20 modules, 200 items" performance target. */
export function stressYaml(): string {
  const lines = [
    "apiVersion: manufacture.dev/v1",
    "kind: Pipeline",
    "metadata: {name: stress-20, labels: {project: bench}}",
    "spec:",
    "  concurrency: {max_runs: 400}",
    "  stages:",
  ];
  const parallelAt: Record<number, number> = { 3: 3, 7: 3, 11: 2 };
  let m = 1;
  for (let i = 0; i < 15; i++) {
    const lanes = parallelAt[i];
    lines.push(`    - name: stage-${String(i + 1).padStart(2, "0")}`);
    if (lanes) {
      lines.push("      parallel:");
      for (let j = 0; j < lanes; j++) lines.push(`        - {module: bench-${String(m++).padStart(2, "0")}}`);
    } else {
      lines.push(`      step: {module: bench-${String(m++).padStart(2, "0")}}`);
    }
  }
  return `${lines.join("\n")}\n`;
}

export type ModuleSeed = { name: string; image: string; replicas: number; concurrency: number; latencyMs: number; failRate: number; transientRate: number; attentionRate?: number; external?: boolean };

export const MODULE_SEEDS: ModuleSeed[] = [
  { name: "hello-init", image: "localhost:5001/mf-hello-init:0.1.0", replicas: 1, concurrency: 1, latencyMs: 50, failRate: 0, transientRate: 0 },
  { name: "hello-hello", image: "localhost:5001/mf-hello-hello:0.1.0", replicas: 2, concurrency: 2, latencyMs: 900, failRate: 0, transientRate: 0.04 },
  { name: "hello-world", image: "localhost:5001/mf-hello-world:0.1.0", replicas: 1, concurrency: 4, latencyMs: 700, failRate: 0.01, transientRate: 0.02 },
  { name: "hello-exclaim", image: "localhost:5001/mf-hello-exclaim:0.1.0", replicas: 1, concurrency: 4, latencyMs: 400, failRate: 0, transientRate: 0 },
  { name: "geo-ingest", image: "ghcr.io/t1menar/mf-geo-ingest:0.2.0", replicas: 1, concurrency: 2, latencyMs: 1500, failRate: 0.01, transientRate: 0.03 },
  { name: "geo-split", image: "ghcr.io/t1menar/mf-geo-split:0.2.0", replicas: 1, concurrency: 2, latencyMs: 600, failRate: 0, transientRate: 0 },
  { name: "geo-analyze", image: "ghcr.io/t1menar/mf-geo-analyze:0.5.1", replicas: 4, concurrency: 4, latencyMs: 1800, failRate: 0.005, transientRate: 0.05 },
  { name: "geo-ndvi", image: "ghcr.io/t1menar/mf-geo-ndvi:0.3.0", replicas: 2, concurrency: 2, latencyMs: 1200, failRate: 0, transientRate: 0.02 },
  { name: "geo-roads", image: "ghcr.io/t1menar/mf-geo-roads:0.1.4", replicas: 1, concurrency: 2, latencyMs: 1600, failRate: 0.06, transientRate: 0.05 },
  { name: "geo-buildings", image: "ghcr.io/t1menar/mf-geo-buildings:2.0.0", replicas: 2, concurrency: 1, latencyMs: 2000, failRate: 0.01, transientRate: 0.02 },
  { name: "geo-report", image: "ghcr.io/t1menar/mf-geo-report:0.1.0", replicas: 1, concurrency: 2, latencyMs: 800, failRate: 0, transientRate: 0 },
  { name: "ideas", image: "ghcr.io/t1menar/mf-ideas:1.2.0", replicas: 1, concurrency: 1, latencyMs: 100, failRate: 0, transientRate: 0 },
  { name: "video-render", image: "ghcr.io/t1menar/mf-video@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", replicas: 2, concurrency: 1, latencyMs: 4200, failRate: 0.02, transientRate: 0.05 },
  { name: "review-telegram", image: "ghcr.io/t1menar/mf-review:0.4.1", replicas: 1, concurrency: 8, latencyMs: 2500, failRate: 0, transientRate: 0, attentionRate: 0.05 },
  { name: "publish-instagram", image: "", replicas: 1, concurrency: 2, latencyMs: 1500, failRate: 0.03, transientRate: 0.08, external: true },
];

export function benchSeeds(): ModuleSeed[] {
  return Array.from({ length: 20 }, (_, i) => ({
    name: `bench-${String(i + 1).padStart(2, "0")}`,
    image: `localhost:5001/mf-bench:0.1.0`,
    replicas: 1 + (i % 3),
    concurrency: 16,
    latencyMs: 300 + ((i * 137) % 900),
    failRate: i === 13 ? 0.08 : 0.002,
    transientRate: 0.01,
  }));
}
