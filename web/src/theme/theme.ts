import { createTheme, type MantineColorsTuple } from "@mantine/core";

const cyan: MantineColorsTuple = ["#e0fbff", "#cbf2ff", "#9ae2ff", "#64d2ff", "#3cc4fe", "#23bcfe", "#09b8ff", "#00a1e4", "#008fcd", "#007cb6"];

export const theme = createTheme({
  primaryColor: "mf",
  colors: { mf: cyan },
  fontFamily: 'Inter, "Segoe UI", system-ui, -apple-system, sans-serif',
  fontFamilyMonospace: '"JetBrains Mono", "SFMono-Regular", Menlo, Consolas, monospace',
  defaultRadius: "md",
  headings: { fontFamily: 'Inter, "Segoe UI", system-ui, sans-serif', fontWeight: "650" },
});

export const COLOR_SCHEME_KEY = "mf-color-scheme";
