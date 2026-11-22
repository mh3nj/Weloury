import {
  THEME_TOKENS,
  type CustomTheme,
  type Theme,
  toTriplet,
} from "@/hooks/useTheme";

/**
 * User-managed theme storage and the built-in palettes the editor starts
 * from. Themes are plain JSON records - easy to export/import, trivially
 * inspectable - kept in localStorage (they are a frontend presentation
 * concern; the *active* mode is additionally mirrored to Weloury core via
 * settings.update so the desktop shell agrees on dark/light).
 */

export interface SavedTheme {
  /** Token → "R G B" triplet. Unset tokens inherit the base mode's value. */
  values: CustomTheme;
  /** The built-in palette the theme was authored against. */
  base: Theme;
  /** When the theme was last saved (ISO string), newest-first sorting. */
  updatedAt: string;
}

const THEMES_KEY = "forge.themes";
const ACTIVE_CUSTOM_KEY = "weloury.activeCustomTheme";

export const DEFAULT_DARK: CustomTheme = {
  "surface-0": "13 15 18",
  "surface-1": "19 22 26",
  "surface-2": "26 30 35",
  border: "40 45 51",
  "border-strong": "58 64 72",
  "text-primary": "230 233 236",
  "text-secondary": "163 171 180",
  "text-muted": "110 118 128",
  accent: "82 155 255",
  "status-healthy": "55 178 118",
  "status-degraded": "217 155 46",
  "status-unhealthy": "224 82 82",
  "status-unknown": "110 118 128",
};

export const DEFAULT_LIGHT: CustomTheme = {
  "surface-0": "247 248 249",
  "surface-1": "255 255 255",
  "surface-2": "255 255 255",
  border: "224 227 231",
  "border-strong": "199 204 210",
  "text-primary": "20 23 26",
  "text-secondary": "78 87 97",
  "text-muted": "130 138 148",
  accent: "26 108 219",
  "status-healthy": "27 138 87",
  "status-degraded": "173 111 8",
  "status-unhealthy": "191 45 45",
  "status-unknown": "130 138 148",
};

export function basePalette(base: Theme): CustomTheme {
  return base === "light" ? { ...DEFAULT_LIGHT } : { ...DEFAULT_DARK };
}

export function listThemes(): Record<string, SavedTheme> {
  try {
    const raw = window.localStorage.getItem(THEMES_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as Record<string, SavedTheme>;
    // Drop anything malformed rather than letting one bad row break the
    // whole editor.
    const clean: Record<string, SavedTheme> = {};
    for (const [name, theme] of Object.entries(parsed)) {
      if (theme && typeof theme === "object" && theme.values && typeof theme.values === "object") {
        clean[name] = theme;
      }
    }
    return clean;
  } catch {
    return {};
  }
}

function persistThemes(themes: Record<string, SavedTheme>) {
  window.localStorage.setItem(THEMES_KEY, JSON.stringify(themes));
}

export function saveTheme(name: string, values: CustomTheme, base: Theme): Record<string, SavedTheme> {
  const themes = listThemes();
  themes[name] = { values: { ...values }, base, updatedAt: new Date().toISOString() };
  persistThemes(themes);
  return themes;
}

export function deleteTheme(name: string): Record<string, SavedTheme> {
  const themes = listThemes();
  delete themes[name];
  persistThemes(themes);
  return themes;
}

export function renameTheme(oldName: string, newName: string): Record<string, SavedTheme> {
  const themes = listThemes();
  const theme = themes[oldName];
  if (!theme || oldName === newName) return themes;
  delete themes[oldName];
  themes[newName] = { ...theme, updatedAt: new Date().toISOString() };
  persistThemes(themes);
  return themes;
}

/**
 * The custom theme currently applied to the app, if any - remembered
 * separately from the theme library so "apply" survives a restart.
 */
export function getActiveCustomTheme(): string | null {
  return window.localStorage.getItem(ACTIVE_CUSTOM_KEY);
}

export function setActiveCustomTheme(name: string | null) {
  if (name === null) {
    window.localStorage.removeItem(ACTIVE_CUSTOM_KEY);
  } else {
    window.localStorage.setItem(ACTIVE_CUSTOM_KEY, name);
  }
}

/** Export shape: one self-describing JSON document per theme. */
export interface ThemeFile {
  format: "forge.theme";
  version: 1;
  name: string;
  base: Theme;
  values: CustomTheme;
}

export function exportTheme(name: string): string {
  const theme = listThemes()[name];
  const file: ThemeFile = {
    format: "forge.theme",
    version: 1,
    name,
    base: theme?.base ?? "dark",
    values: theme?.values ?? {},
  };
  return JSON.stringify(file, null, 2);
}

/** Import: validates every token value, returns the name to file. */
export function importTheme(json: string): { name: string; values: CustomTheme; base: Theme } {
  let parsed: unknown;
  try {
    parsed = JSON.parse(json);
  } catch {
    throw new Error("Not valid JSON.");
  }

  const file = parsed as Partial<ThemeFile>;
  if (!file || typeof file !== "object" || !file.values || typeof file.values !== "object") {
    throw new Error("Missing theme values - is this a Weloury theme file?");
  }

  const values: CustomTheme = {};
  for (const token of THEME_TOKENS) {
    const raw = (file.values as Record<string, unknown>)[token];
    if (raw === undefined || raw === null || raw === "") continue;
    if (typeof raw !== "string") throw new Error(`Token "${token}" must be a string.`);
    const triplet = toTriplet(raw);
    if (!triplet) {
      throw new Error(`Token "${token}": "${raw}" is not a color (use "R G B" or #RRGGBB).`);
    }
    values[token] = triplet;
  }

  const name = typeof file.name === "string" && file.name.trim() ? file.name.trim() : "Imported theme";
  const base: Theme = file.base === "light" ? "light" : "dark";
  return { name, values, base };
}
