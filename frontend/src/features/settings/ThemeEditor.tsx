import { useMemo, useRef, useState } from "react";
import {
  THEME_TOKENS,
  type CustomTheme,
  type Theme,
  type ThemeSelection,
  type ThemeToken,
  toTriplet,
} from "@/hooks/useTheme";
import { Button } from "@/components/ui/Button";
import { Panel, PanelHeader } from "@/components/ui/Panel";
import {
  basePalette,
  deleteTheme,
  exportTheme,
  getActiveCustomTheme,
  importTheme,
  listThemes,
  renameTheme,
  saveTheme,
  setActiveCustomTheme,
  type SavedTheme,
} from "./themeLibrary";

interface ThemeEditorProps {
  selection: ThemeSelection;
  onApplySelection: (selection: ThemeSelection) => void;
}

type TokenValues = Record<ThemeToken, string>;

/** Fill unset tokens from the base palette so the preview and CSS output are complete. */
function completeTheme(values: CustomTheme, base: Theme): TokenValues {
  const palette = basePalette(base);
  const out = {} as TokenValues;
  for (const token of THEME_TOKENS) {
    out[token] = values[token] ?? palette[token] ?? "0 0 0";
  }
  return out;
}

function rgbCss(triplet: string): string {
  return `rgb(${triplet.split(/\s+/).join(",")})`;
}

const TOKEN_LABEL: Record<ThemeToken, string> = {
  "surface-0": "Background",
  "surface-1": "Panel",
  "surface-2": "Raised element",
  border: "Border",
  "border-strong": "Border (strong)",
  "text-primary": "Text (primary)",
  "text-secondary": "Text (secondary)",
  "text-muted": "Text (muted)",
  accent: "Accent",
  "status-healthy": "Healthy",
  "status-degraded": "Degraded",
  "status-unhealthy": "Unhealthy",
  "status-unknown": "Unknown",
};

/**
 * A miniature replica of the app shell - sidebar, top bar, a panel with
 * rows, buttons, status dots - styled entirely from the values currently
 * in the editor, so the preview is exactly what Apply will produce, live
 * before it's applied.
 */
function ThemePreview({ v }: { v: TokenValues }) {
  return (
    <div
      className="mt-3 flex overflow-hidden rounded-md border text-[10px]"
      style={{
        borderColor: rgbCss(v.border),
        background: rgbCss(v["surface-0"]),
        color: rgbCss(v["text-primary"]),
        height: 190,
      }}
    >
      <div
        className="flex w-28 shrink-0 flex-col gap-1 p-2"
        style={{ background: rgbCss(v["surface-1"]), borderRight: `1px solid ${rgbCss(v.border)}` }}
      >
        <div className="mb-1 flex items-center gap-1.5">
          <img src="/logo-small.png" alt="" className="h-3.5 w-3.5" />
          <span className="font-semibold tracking-wide">Weloury</span>
        </div>
        {["Overview", "Processes", "Services", "Health", "Settings"].map((item, i) => (
          <div
            key={item}
            className="rounded px-1.5 py-0.5"
            style={{
              background: i === 0 ? rgbCss(v["surface-2"]) : "transparent",
              color: i === 0 ? rgbCss(v["text-primary"]) : rgbCss(v["text-secondary"]),
            }}
          >
            {item}
          </div>
        ))}
      </div>
      <div className="flex min-w-0 flex-1 flex-col">
        <div
          className="flex items-center justify-between px-2 py-1.5"
          style={{ background: rgbCss(v["surface-1"]), borderBottom: `1px solid ${rgbCss(v.border)}` }}
        >
          <span className="rounded border px-1.5 py-0.5" style={{ borderColor: rgbCss(v.border), color: rgbCss(v["text-muted"]) }}>
            Search or jump to...
          </span>
          <span className="flex items-center gap-1" style={{ color: rgbCss(v["text-secondary"]) }}>
            <span className="h-1.5 w-1.5 rounded-full" style={{ background: rgbCss(v["status-healthy"]) }} />
            Connected
          </span>
        </div>
        <div className="min-h-0 flex-1 p-2">
          <div className="rounded border" style={{ borderColor: rgbCss(v.border), background: rgbCss(v["surface-1"]) }}>
            <div
              className="flex items-center justify-between border-b px-2 py-1"
              style={{ borderColor: rgbCss(v.border) }}
            >
              <span className="font-semibold">Overview</span>
              <span
                className="rounded px-1.5 py-0.5 font-medium"
                style={{ background: rgbCss(v.accent), color: "#ffffff" }}
              >
                New check
              </span>
            </div>
            <div className="grid grid-cols-3 gap-2 p-2">
              {[
                { label: "CPU", value: "12%", color: v["status-healthy"] },
                { label: "Memory", value: "64%", color: v["status-degraded"] },
                { label: "check: api", value: "failing", color: v["status-unhealthy"] },
              ].map((cell) => (
                <div
                  key={cell.label}
                  className="rounded border p-1.5"
                  style={{ borderColor: rgbCss(v.border), background: rgbCss(v["surface-2"]) }}
                >
                  <div style={{ color: rgbCss(v["text-muted"]) }}>{cell.label}</div>
                  <div className="flex items-center gap-1 font-semibold">
                    <span className="h-1.5 w-1.5 rounded-full" style={{ background: rgbCss(cell.color) }} />
                    {cell.value}
                  </div>
                </div>
              ))}
            </div>
            <div className="px-2 pb-2">
              {[0, 1].map((i) => (
                <div
                  key={i}
                  className="flex items-center justify-between rounded px-1.5 py-1"
                  style={{
                    background: i === 0 ? rgbCss(v["surface-2"]) : "transparent",
                    color: rgbCss(v["text-secondary"]),
                  }}
                >
                  <span className="tabular">weloury-core</span>
                  <span className="tabular" style={{ color: rgbCss(v["text-muted"]) }}>
                    pid 48213
                  </span>
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

export function ThemeEditor({ selection, onApplySelection }: ThemeEditorProps) {
  const [base, setBase] = useState<Theme>(() => (selection.mode === "light" ? "light" : "dark"));
  const [values, setValues] = useState<CustomTheme>(() => (selection.mode === "custom" ? { ...selection.values } : {}));
  const [themes, setThemes] = useState<Record<string, SavedTheme>>(() => listThemes());
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [exportText, setExportText] = useState<string | null>(null);
  const [renameTarget, setRenameTarget] = useState<string | null>(null);
  const [renameValue, setRenameValue] = useState("");
  const fileInputRef = useRef<HTMLInputElement>(null);

  const completed = useMemo(() => completeTheme(values, base), [values, base]);
  const activeCustom = getActiveCustomTheme();

  const isCustomActive = selection.mode === "custom";
  const dirty = isCustomActive && JSON.stringify(values) !== JSON.stringify(selection.values);

  function setToken(token: ThemeToken, raw: string) {
    setError(null);
    setNotice(null);
    setValues((prev) => {
      const next = { ...prev };
      const triplet = toTriplet(raw);
      if (raw.trim() === "") {
        delete next[token];
      } else if (triplet) {
        next[token] = triplet;
      } else {
        next[token] = raw; // keep half-typed input in the field; validation happens on save/apply
      }
      return next;
    });
  }

  function tokenInputValue(token: ThemeToken): string {
    const value = values[token];
    if (value === undefined) return "";
    const triplet = toTriplet(value);
    return triplet ? `#${Number(triplet.split(" ")[0]).toString(16).padStart(2, "0")}${Number(triplet.split(" ")[1]).toString(16).padStart(2, "0")}${Number(triplet.split(" ")[2]).toString(16).padStart(2, "0")}` : value;
  }

  function invalidTokens(): ThemeToken[] {
    return THEME_TOKENS.filter((token) => {
      const value = values[token];
      return value !== undefined && toTriplet(value) === null;
    });
  }

  function startFrom(palette: Theme) {
    setBase(palette);
    setValues({});
    setError(null);
    setNotice(`Editing a new ${palette} theme from the built-in palette. Save or apply to keep it.`);
  }

  function editSavedTheme(themeName: string) {
    const theme = themes[themeName];
    if (!theme) return;
    setBase(theme.base);
    setValues({ ...theme.values });
    setName(themeName);
    setError(null);
    setNotice(`Loaded "${themeName}" into the editor.`);
  }

  function handleSave() {
    const invalid = invalidTokens();
    if (invalid.length > 0) {
      setError(`Invalid color for: ${invalid.map((t) => TOKEN_LABEL[t]).join(", ")} - use "R G B" or #RRGGBB.`);
      return;
    }
    const finalName = name.trim() || `My ${base} theme`;
    setThemes(saveTheme(finalName, values, base));
    setName(finalName);
    setNotice(`Saved "${finalName}".`);
  }

  function handleApply() {
    const invalid = invalidTokens();
    if (invalid.length > 0) {
      setError(`Invalid color for: ${invalid.map((t) => TOKEN_LABEL[t]).join(", ")} - use "R G B" or #RRGGBB.`);
      return;
    }
    const finalName = name.trim();
    if (finalName) {
      setThemes(saveTheme(finalName, values, base));
      setActiveCustomTheme(finalName);
    }
    onApplySelection({ mode: "custom", values });
    setNotice("Theme applied.");
  }

  function handleUseBuiltIn(mode: Theme) {
    setActiveCustomTheme(null);
    onApplySelection({ mode });
    setNotice(null);
  }

  function handleApplySaved(themeName: string) {
    const theme = themes[themeName];
    if (!theme) return;
    setActiveCustomTheme(themeName);
    onApplySelection({ mode: "custom", values: { ...theme.values } });
    setNotice(`Applied "${themeName}".`);
  }

  function handleExport(themeName: string) {
    const json = exportTheme(themeName);
    setExportText(json);
    try {
      const blob = new Blob([json], { type: "application/json" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `${themeName.replace(/[^\w-]+/g, "_")}.weloury-theme.json`;
      a.click();
      URL.revokeObjectURL(url);
    } catch {
      // Download unavailable (some webview configs); the JSON is still
      // shown in a copyable field below.
    }
  }

  function handleImportFile(file: File) {
    const reader = new FileReader();
    reader.onload = () => {
      try {
        const { name: importedName, values: importedValues, base: importedBase } = importTheme(String(reader.result));
        setThemes(saveTheme(importedName, importedValues, importedBase));
        setBase(importedBase);
        setValues({ ...importedValues });
        setName(importedName);
        setNotice(`Imported "${importedName}" - review it and click Apply.`);
        setError(null);
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
      }
    };
    reader.readAsText(file);
  }

  return (
    <Panel>
      <PanelHeader>
        <h2 className="text-sm font-semibold">Theme editor</h2>
        <div className="flex items-center gap-2">
          <Button variant={selection.mode === "dark" && !isCustomActive ? "primary" : "secondary"} onClick={() => handleUseBuiltIn("dark")}>
            Dark
          </Button>
          <Button variant={selection.mode === "light" && !isCustomActive ? "primary" : "secondary"} onClick={() => handleUseBuiltIn("light")}>
            Light
          </Button>
        </div>
      </PanelHeader>

      <div className="grid grid-cols-1 gap-4 p-4 lg:grid-cols-2">
        {/* Editor column */}
        <div>
          <div className="flex items-center gap-2">
            <span className="text-xs text-text-muted">Base:</span>
            <Button variant={base === "dark" ? "primary" : "secondary"} onClick={() => startFrom("dark")}>
              Dark palette
            </Button>
            <Button variant={base === "light" ? "primary" : "secondary"} onClick={() => startFrom("light")}>
              Light palette
            </Button>
          </div>

          <div className="mt-3 grid grid-cols-1 gap-2">
            {THEME_TOKENS.map((token) => (
              <label key={token} className="flex items-center justify-between gap-2">
                <span className="text-xs text-text-secondary">{TOKEN_LABEL[token]}</span>
                <span className="flex items-center gap-2">
                  <input
                    type="color"
                    aria-label={`${TOKEN_LABEL[token]} color picker`}
                    value={tokenInputValue(token).startsWith("#") ? tokenInputValue(token) : "#000000"}
                    onChange={(e) => setToken(token, e.target.value)}
                    className="h-6 w-8 cursor-pointer rounded border border-border bg-surface-2"
                  />
                  <input
                    value={tokenInputValue(token)}
                    placeholder={completed[token]}
                    onChange={(e) => setToken(token, e.target.value)}
                    className="w-28 rounded border border-border bg-surface-2 px-1.5 py-1 font-mono text-xs text-text-primary placeholder:text-text-muted focus:border-border-strong focus:outline-none"
                  />
                </span>
              </label>
            ))}
          </div>

          <div className="mt-3 flex flex-wrap items-center gap-2">
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Theme name"
              className="w-40 rounded border border-border bg-surface-2 px-2 py-1.5 text-sm text-text-primary placeholder:text-text-muted focus:border-border-strong focus:outline-none"
            />
            <Button variant="primary" onClick={handleSave}>
              Save
            </Button>
            <Button variant="secondary" onClick={handleApply}>
              Apply{dirty ? " (edited)" : ""}
            </Button>
            <label className="inline-flex cursor-pointer items-center gap-2 rounded-md border border-border bg-surface-2 px-3 py-1.5 text-sm font-medium text-text-primary hover:border-border-strong">
              Import
              <input
                ref={fileInputRef}
                type="file"
                accept=".json,application/json"
                className="hidden"
                onChange={(e) => {
                  const file = e.target.files?.[0];
                  if (file) handleImportFile(file);
                  e.target.value = "";
                }}
              />
            </label>
          </div>

          {error && <p className="mt-2 text-xs text-status-unhealthy">{error}</p>}
          {notice && <p className="mt-2 text-xs text-status-healthy">{notice}</p>}

          {/* Preview */}
          <p className="mt-4 text-xs font-medium text-text-secondary">Live preview</p>
          <ThemePreview v={completed} />
        </div>

        {/* Library column */}
        <div>
          <p className="text-xs font-medium text-text-secondary">Saved themes</p>
          <div className="mt-2 flex flex-col gap-2">
            {Object.keys(themes).length === 0 && (
              <p className="text-xs text-text-muted">
                Nothing saved yet. Configure colors, name the theme, and click Save.
              </p>
            )}
            {Object.entries(themes)
              .sort(([, a], [, b]) => (a.updatedAt < b.updatedAt ? 1 : -1))
              .map(([themeName, theme]) => (
                <div
                  key={themeName}
                  className="rounded-md border border-border bg-surface-2 p-2"
                  style={
                    isCustomActive && activeCustom === themeName
                      ? { borderColor: rgbCss(completed.accent) }
                      : undefined
                  }
                >
                  <div className="flex items-center justify-between gap-2">
                    <div className="flex min-w-0 items-center gap-2">
                      <span className="flex shrink-0 gap-0.5">
                        {(["accent", "surface-1", "status-healthy"] as ThemeToken[]).map((t) => (
                          <span
                            key={t}
                            className="h-3.5 w-3.5 rounded-sm border"
                            style={{
                              background: rgbCss(theme.values[t] ?? basePalette(theme.base)[t] ?? "0 0 0"),
                              borderColor: rgbCss(completed.border),
                            }}
                          />
                        ))}
                      </span>
                      {renameTarget === themeName ? (
                        <input
                          autoFocus
                          value={renameValue}
                          onChange={(e) => setRenameValue(e.target.value)}
                          onKeyDown={(e) => {
                            if (e.key === "Enter" && renameValue.trim()) {
                              setThemes(renameTheme(themeName, renameValue.trim()));
                              if (activeCustom === themeName) setActiveCustomTheme(renameValue.trim());
                              setRenameTarget(null);
                            }
                            if (e.key === "Escape") setRenameTarget(null);
                          }}
                          className="w-32 rounded border border-border-strong bg-surface-0 px-1.5 py-0.5 text-xs text-text-primary focus:outline-none"
                        />
                      ) : (
                        <span className="truncate text-xs font-medium text-text-primary">
                          {themeName}
                          {isCustomActive && activeCustom === themeName && (
                            <span className="ml-1.5 rounded bg-accent px-1 py-0.5 text-[10px] font-semibold text-white">active</span>
                          )}
                        </span>
                      )}
                    </div>
                    <div className="flex shrink-0 items-center gap-1">
                      <Button variant="ghost" onClick={() => handleApplySaved(themeName)}>
                        Apply
                      </Button>
                      <Button
                        variant="ghost"
                        onClick={() => {
                          setRenameTarget(themeName);
                          setRenameValue(themeName);
                        }}
                      >
                        Rename
                      </Button>
                      <Button variant="ghost" onClick={() => editSavedTheme(themeName)}>
                        Edit
                      </Button>
                      <Button variant="ghost" onClick={() => handleExport(themeName)}>
                        Export
                      </Button>
                      <Button variant="ghost" onClick={() => setThemes(deleteTheme(themeName))}>
                        Delete
                      </Button>
                    </div>
                  </div>
                  <p className="mt-1 text-[10px] text-text-muted">
                    Base: {theme.base} · saved {new Date(theme.updatedAt).toLocaleString()}
                  </p>
                </div>
              ))}
          </div>

          {exportText && (
            <div className="mt-3">
              <p className="text-xs font-medium text-text-secondary">Exported JSON (copy or download)</p>
              <textarea
                readOnly
                value={exportText}
                onFocus={(e) => e.currentTarget.select()}
                className="mt-1 h-32 w-full rounded border border-border bg-surface-2 p-2 font-mono text-[10px] text-text-primary"
              />
            </div>
          )}
        </div>
      </div>
    </Panel>
  );
}
