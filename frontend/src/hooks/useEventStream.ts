import { useEffect, useState } from "react";
import type { FwpClient } from "@/lib/ipc/client";
import type { Envelope } from "@/types/fwp";

export interface StreamedEvent<TPayload = unknown> {
  receivedAt: number;
  channel?: string;
  payload?: TPayload;
}

export type StreamStatus = "connecting" | "open" | "reconnecting";

const MAX_BUFFERED_EVENTS = 200;
const RECONNECT_BASE_DELAY_MS = 500;
const RECONNECT_MAX_DELAY_MS = 8000;

/**
 * Subscribes to Weloury core's event stream and keeps a bounded, most-recent
 * window of events in memory - never the full history, so a long-running
 * session cannot grow this component's memory usage without bound (see
 * spec section 26, "log streaming should not accumulate unlimited
 * memory," which applies equally to any live event ticker).
 */
export function useEventStream<TPayload = unknown>(
  client: FwpClient | undefined,
  channels: string[] = [],
): { events: StreamedEvent<TPayload>[]; status: StreamStatus } {
  const [events, setEvents] = useState<StreamedEvent<TPayload>[]>([]);
  const [status, setStatus] = useState<StreamStatus>("connecting");
  const channelsKey = channels.join(",");

  useEffect(() => {
    if (!client) return;

    let stopped = false;
    let unsubscribe: (() => void) | undefined;
    let attempt = 0;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;

    function connect() {
      setStatus(attempt === 0 ? "connecting" : "reconnecting");

      unsubscribe = client!.streamEvents<TPayload>(
        channels,
        (envelope: Envelope<never, never, TPayload>) => {
          attempt = 0;
          setStatus("open");
          setEvents((prev) => {
            const next = [
              ...prev,
              { receivedAt: Date.now(), channel: envelope.channel, payload: envelope.payload },
            ];
            return next.length > MAX_BUFFERED_EVENTS ? next.slice(next.length - MAX_BUFFERED_EVENTS) : next;
          });
        },
        () => {
          if (stopped) return;
          const delay = Math.min(RECONNECT_BASE_DELAY_MS * 2 ** attempt, RECONNECT_MAX_DELAY_MS);
          attempt += 1;
          retryTimer = setTimeout(connect, delay);
        },
      );
    }

    connect();

    return () => {
      stopped = true;
      unsubscribe?.();
      if (retryTimer) clearTimeout(retryTimer);
    };
    // channels is intentionally reduced to channelsKey below so the effect
    // doesn't re-run every render on a fresh array literal from the caller.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, channelsKey]);

  return { events, status };
}
