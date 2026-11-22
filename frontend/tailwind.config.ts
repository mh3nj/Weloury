import type { Config } from "tailwindcss";

// FORGE design tokens. Kept intentionally small and semantic rather than
// a generic palette: every color name describes what it means (status,
// surface elevation, text emphasis), not what it looks like, so feature
// code never hardcodes a raw hex value. See src/styles/globals.css for
// the actual RGB values per theme.
export default {
  darkMode: "class",
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        surface: {
          0: "rgb(var(--surface-0) / <alpha-value>)",
          1: "rgb(var(--surface-1) / <alpha-value>)",
          2: "rgb(var(--surface-2) / <alpha-value>)",
        },
        border: {
          DEFAULT: "rgb(var(--border) / <alpha-value>)",
          strong: "rgb(var(--border-strong) / <alpha-value>)",
        },
        text: {
          primary: "rgb(var(--text-primary) / <alpha-value>)",
          secondary: "rgb(var(--text-secondary) / <alpha-value>)",
          muted: "rgb(var(--text-muted) / <alpha-value>)",
        },
        accent: "rgb(var(--accent) / <alpha-value>)",
        status: {
          healthy: "rgb(var(--status-healthy) / <alpha-value>)",
          degraded: "rgb(var(--status-degraded) / <alpha-value>)",
          unhealthy: "rgb(var(--status-unhealthy) / <alpha-value>)",
          unknown: "rgb(var(--status-unknown) / <alpha-value>)",
        },
      },
      fontFamily: {
        sans: ["Inter", "system-ui", "sans-serif"],
        mono: ['"JetBrains Mono"', '"SF Mono"', "Consolas", "monospace"],
      },
      fontSize: {
        xs: ["0.75rem", { lineHeight: "1rem" }],
        sm: ["0.8125rem", { lineHeight: "1.125rem" }],
        base: ["0.875rem", { lineHeight: "1.375rem" }],
      },
    },
  },
  plugins: [],
} satisfies Config;
