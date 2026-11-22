import { useCallback, useEffect, useState } from "react";

export type Theme = "dark" | "light";

/**
 * Every design token a custom theme can override - the same CSS custom
 * properties globals.css defines for the built-in dark/light themes (see
 * src/styles/globals.css; values are "R G B" triplets so Tailwind's
 * alpha modifiers keep working - see tailwind.config.ts).
 */
export const THEME_TOKENS = [
  "surface-0",
  "surface-1",
  "surface-2",
  "border",
  "border-strong",
  "text-primary",
  "text-secondary",
  "text-muted",
  "accent",
  "status-healthy",
  "status-degraded",
  "status-unhealthy",
  "status-unknown",
] as const;

export type ThemeToken = (typeof THEME_TOKENS)[number];
export type CustomTheme = Partial<Record<ThemeToken, string>>;

export type ThemeSelection = { mode: Theme } | { mode: "custom"; values: CustomTheme };

const STORAGE_KEY = "forge.theme";

export function parseThemeSelection(raw: string | null): ThemeSelection {
  if (!raw) return { mode: "dark" };
  try {
    const parsed = JSON.parse(raw) as ThemeSelection;
    if (parsed.mode === "custom" && parsed.values && typeof parsed.values === "object") {
      // Keep only well-formed token values ("R G B" triplets) so a
      // hand-edited or outdated localStorage blob can't break rendering.
      const clean: CustomTheme = {};
      for (const token of THEME_TOKENS) {
        const value = parsed.values[token];
        if (typeof value === "string" && isValidTriplet(value)) {
          clean[token] = value;
        }
      }
      return { mode: "custom", values: clean };
    }
    if (parsed.mode === "dark" || parsed.mode === "light") {
      return { mode: parsed.mode };
    }
  } catch {
    // Legacy value: the pre-custom-theme hook stored a bare "dark"/"light".
    if (raw === "dark" || raw === "light") return { mode: raw };
  }
  return { mode: "dark" };
}

/** "82 155 255" / "82,155,255" / "#529BFF" → normalized "R G B", else null. */
export function toTriplet(input: string): string | null {
  const value = input.trim();
  const triplet = value.match(/^(\d{1,3})[ ,]+(\d{1,3})[ ,]+(\d{1,3})$/);
  if (triplet) {
    const [r, g, b] = [triplet[1], triplet[2], triplet[3]].map((n) => Math.min(255, Number(n)));
    return `${r} ${g} ${b}`;
  }
  const hex = value.match(/^#?([0-9a-f]{6})$/i);
  if (hex) {
    const n = parseInt(hex[1], 16);
    return `${(n >> 16) & 255} ${(n >> 8) & 255} ${n & 255}`;
  }
  return null;
}

function isValidTriplet(value: string): boolean {
  return /^\d{1,3} \d{1,3} \d{1,3}$/.test(value.trim()) && value.split(" ").every((n) => Number(n) <= 255);
}

/**
 * Applies a theme selection to the document: built-in modes toggle the
 * `dark` class (globals.css's two token blocks), a custom theme writes
 * the CSS custom properties inline on <html> on top of whichever base
 * mode the user picked as its starting point (custom themes are stored
 * with their base so un-set tokens inherit sensible values).
 */
export function applyTheme(selection: ThemeSelection) {
  const root = document.documentElement;
  if (selection.mode === "custom") {
    root.classList.add("dark"); // custom themes are authored against dark defaults
    for (const token of THEME_TOKENS) {
      const value = selection.values[token];
      if (value) {
        root.style.setProperty(`--${token}`, value);
      } else {
        root.style.removeProperty(`--${token}`);
      }
    }
  } else {
    for (const token of THEME_TOKENS) {
      root.style.removeProperty(`--${token}`);
    }
    root.classList.toggle("dark", selection.mode === "dark");
  }
}

/**
 * Theme lives in the DOM (a class on <html>, plus inline token overrides
 * for custom themes) and in memory as the source of truth for rendering;
 * user changes are also mirrored to localStorage purely so the app
 * doesn't flash the wrong theme on next launch. The Settings page
 * persists the chosen theme name to Weloury core via settings.update for
 * cross-session, cross-device truth (see spec section 16) - this hook
 * stays the fast, local applier.
 */
export function useTheme(): [ThemeSelection, (s: ThemeSelection) => void] {
  const [selection, setSelectionState] = useState<ThemeSelection>(() =>
    parseThemeSelection(window.localStorage.getItem(STORAGE_KEY)),
  );

  useEffect(() => {
    applyTheme(selection);
  }, [selection]);

  const setSelection = useCallback((s: ThemeSelection) => {
    setSelectionState(s);
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(s));
  }, []);

  return [selection, setSelection];
}
