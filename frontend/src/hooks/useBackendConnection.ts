import { useEffect, useState } from "react";
import { FwpClient } from "@/lib/ipc/client";

interface BackendEndpoint {
  base_url: string;
  token: string;
}

export type ConnectionState =
  | { status: "connecting" }
  | { status: "connected"; client: FwpClient }
  | { status: "error"; message: string };

/**
 * Resolves how to reach Weloury core and returns a ready FwpClient.
 *
 * In the packaged desktop app, connection details come from the Tauri
 * `get_backend_endpoint` command (see apps/desktop/src/main.rs). When
 * running the frontend standalone in a browser during development
 * (`npm run dev` without Tauri), that command isn't available, so this
 * falls back to `VITE_Weloury_CORE_URL` / `VITE_Weloury_CORE_TOKEN` env vars
 * pointing at a manually-started `weloury-core` process - see
 * DEVELOPMENT.md.
 */
export function useBackendConnection(): ConnectionState {
  const [state, setState] = useState<ConnectionState>({ status: "connecting" });

  useEffect(() => {
    let cancelled = false;

    async function connect() {
      try {
        const endpoint = await resolveEndpoint();
        if (cancelled) return;
        setState({
          status: "connected",
          client: new FwpClient({ baseUrl: endpoint.base_url, token: endpoint.token }),
        });
      } catch (err) {
        if (cancelled) return;
        setState({ status: "error", message: err instanceof Error ? err.message : String(err) });
      }
    }

    void connect();
    return () => {
      cancelled = true;
    };
  }, []);

  return state;
}

async function resolveEndpoint(): Promise<BackendEndpoint> {
  try {
    const { invoke } = await import("@tauri-apps/api/core");
    return await invoke<BackendEndpoint>("get_backend_endpoint");
  } catch {
    // Not running inside Tauri (e.g. `npm run dev` in a plain browser for
    // frontend-only iteration) - fall back to explicit env vars below.
  }

  const url = import.meta.env.VITE_Weloury_CORE_URL as string | undefined;
  const token = import.meta.env.VITE_Weloury_CORE_TOKEN as string | undefined;
  if (!url || !token) {
    throw new Error(
      "Could not reach Weloury core via Tauri, and VITE_Weloury_CORE_URL / VITE_Weloury_CORE_TOKEN " +
        "are not set. See DEVELOPMENT.md for running the frontend standalone against a " +
        "manually-started weloury-core.",
    );
  }
  return { base_url: url, token };
}
