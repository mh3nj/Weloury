// TypeScript mirror of backend/pkg/fwp - the Weloury Wire Protocol envelope.
//
// Kept in sync by hand (see ARCHITECTURE.md, "Protocol versioning"); the
// two languages don't share a schema compiler in Phase 1, so a mismatch is
// caught at runtime (a response that doesn't decode as expected) rather
// than at compile time. PROTOCOL_VERSION exists so client and server can
// at least assert they agree on the wire format's shape.

export const PROTOCOL_VERSION = 1;

export type EnvelopeType = "request" | "response" | "error" | "event" | "cancel";

export interface ErrorObject {
  code: string;
  message: string;
  retryable?: boolean;
}

export interface Envelope<TParams = unknown, TResult = unknown, TPayload = unknown> {
  v: number;
  type: EnvelopeType;
  id?: string;
  method?: string;
  params?: TParams;
  result?: TResult;
  error?: ErrorObject;
  channel?: string;
  seq?: number;
  payload?: TPayload;
}

/** Well-known FWP error codes emitted by Weloury core (see backend/pkg/fwp/errors.go). */
export const ErrorCodes = {
  UnsupportedMethod: "UNSUPPORTED_METHOD",
  InvalidParams: "INVALID_PARAMS",
  Internal: "INTERNAL",
  Unauthorized: "UNAUTHORIZED",
  Canceled: "CANCELED",
  NotFound: "NOT_FOUND",
  Unavailable: "UNAVAILABLE",
} as const;
