import { yaml } from "@codemirror/lang-yaml";
import { linter, lintGutter, type Diagnostic as CmDiagnostic } from "@codemirror/lint";
import { useComputedColorScheme } from "@mantine/core";
import CodeMirror, { EditorView } from "@uiw/react-codemirror";
import { useMemo } from "react";

import type { Diagnostic } from "../lib/validate";

export type ExternalDiagnostic = { line: number; message: string; severity: "error" | "warning" };

/** YAML editor (CodeMirror 6) with inline diagnostics from the in-browser validator and the server. */
export function YamlEditor({
  value,
  onChange,
  diagnostics,
  external = [],
  readOnly = false,
  height = "520px",
}: {
  value: string;
  onChange?: (v: string) => void;
  diagnostics: Diagnostic[];
  external?: ExternalDiagnostic[];
  readOnly?: boolean;
  height?: string;
}) {
  const scheme = useComputedColorScheme("dark");
  const extensions = useMemo(() => {
    const lint = linter(
      (view) => {
        const len = view.state.doc.length;
        const out: CmDiagnostic[] = diagnostics.map((d) => ({
          from: Math.min(d.from, len),
          to: Math.min(Math.max(d.to, d.from + 1), len),
          severity: d.severity,
          message: d.path ? `${d.path}: ${d.message}` : d.message,
          source: "schema",
        }));
        for (const e of external) {
          if (e.line < 1 || e.line > view.state.doc.lines) continue;
          const line = view.state.doc.line(e.line);
          out.push({ from: line.from, to: Math.max(line.to, line.from + 1), severity: e.severity, message: e.message, source: "core" });
        }
        return out;
      },
      { delay: 150 },
    );
    return [yaml(), lintGutter(), lint, EditorView.lineWrapping];
  }, [diagnostics, external]);

  return (
    <CodeMirror
      value={value}
      height={height}
      theme={scheme === "dark" ? "dark" : "light"}
      extensions={extensions}
      onChange={onChange}
      readOnly={readOnly}
      basicSetup={{ foldGutter: true, highlightActiveLine: !readOnly }}
      aria-label="YAML"
    />
  );
}
