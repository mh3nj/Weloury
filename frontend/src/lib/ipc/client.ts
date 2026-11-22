// Weloury Wire Protocol (FWP) client. Talks to Weloury core over plain HTTP:
//   - POST /rpc      one Envelope in, one Envelope back (request/response)
//   - GET  /events   a long-lived NDJSON stream of event Envelopes
//   - POST /shutdown (used only by the desktop shell, not this client)
//
// See backend/internal/ipc's package doc comment and ARCHITECTURE.md for
// why FWP is carried over HTTP rather than a raw WebSocket in Phase 1.

import { PROTOCOL_VERSION, type Envelope, type ErrorObject } from "@/types/fwp";

export class FwpError extends Error {
  readonly code: string;
  readonly retryable: boolean;

  constructor(err: ErrorObject) {
    super(err.message);
    this.name = "FwpError";
    this.code = err.code;
    this.retryable = err.retryable ?? false;
  }
}

export interface FwpClientOptions {
  baseUrl: string;
  token: string;
}

let requestCounter = 0;
function nextRequestId(): string {
  requestCounter += 1;
  return `req-${Date.now().toString(36)}-${requestCounter}`;
}

/**
 * A thin, dependency-free client for FWP. One instance per connection to
 * Weloury core; the desktop shell obtains baseUrl/token once at startup via
 * the Tauri `get_backend_endpoint` command - see
 * hooks/useBackendConnection.ts.
 */
export class FwpClient {
  private readonly baseUrl: string;
  private readonly token: string;

  constructor(options: FwpClientOptions) {
    this.baseUrl = options.baseUrl.replace(/\/+$/, "");
    this.token = options.token;
  }

  /**
   * Invokes a FWP method and resolves with its result. Pass `signal` to
   * cancel the request; Weloury core observes the underlying HTTP request's
   * cancellation via the Go request context - see ipc.handleRPC's doc
   * comment on the backend.
   */
  async request<TResult = unknown, TParams = unknown>(
    method: string,
    params?: TParams,
    signal?: AbortSignal,
  ): Promise<TResult> {
    const envelope: Envelope<TParams> = {
      v: PROTOCOL_VERSION,
      type: "request",
      id: nextRequestId(),
      method,
      params,
    };

    const res = await fetch(`${this.baseUrl}/rpc`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${this.token}`,
      },
      body: JSON.stringify(envelope),
      signal,
    });

    const body = (await res.json()) as Envelope<never, TResult>;

    if (body.type === "error" || body.error) {
      throw new FwpError(body.error ?? { code: "INTERNAL", message: `unexpected HTTP ${res.status}` });
    }

    return body.result as TResult;
  }

  /**
   * Opens a persistent NDJSON stream of events, optionally filtered to
   * the given channels (FWP event `type`s - an empty array subscribes to
   * everything). Returns an unsubscribe function that aborts the
   * underlying request.
   *
   * This method makes exactly one connection attempt and reports failure
   * via onError; it does not retry. Callers that need a durable
   * subscription across transient hiccups should use useEventStream,
   * which wraps this with reconnect/backoff.
   */
  streamEvents<TPayload = unknown>(
    channels: string[],
    onEvent: (envelope: Envelope<never, never, TPayload>) => void,
    onError: (err: Error) => void,
  ): () => void {
    const controller = new AbortController();

    const url = new URL(`${this.baseUrl}/events`);
    if (channels.length > 0) {
      url.searchParams.set("channel", channels.join(","));
    }

    void (async () => {
      try {
        const res = await fetch(url.toString(), {
          headers: { Authorization: `Bearer ${this.token}` },
          signal: controller.signal,
        });

        if (!res.ok || !res.body) {
          throw new Error(`event stream request failed: HTTP ${res.status}`);
        }

        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buffer = "";

        for (;;) {
          const { value, done } = await reader.read();
          if (done) break;

          buffer += decoder.decode(value, { stream: true });
          let newlineIndex: number;
          while ((newlineIndex = buffer.indexOf("\n")) !== -1) {
            const line = buffer.slice(0, newlineIndex).trim();
            buffer = buffer.slice(newlineIndex + 1);
            if (line.length === 0) continue;
            onEvent(JSON.parse(line) as Envelope<never, never, TPayload>);
          }
        }
      } catch (err) {
        if (controller.signal.aborted) return; // intentional close, not a failure
        onError(err instanceof Error ? err : new Error(String(err)));
      }
    })();

    return () => controller.abort();
  }
}
