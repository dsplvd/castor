import type { CastStatus, Ended } from "@/gen/castor/v1/cast_pb";

type WatchState =
  | { phase: "watching"; status?: CastStatus }
  | { phase: "ended"; status?: CastStatus; ended: Ended }
  | { phase: "lost"; status?: CastStatus; error: string };

type WatchEvent =
  | { type: "restart" }
  | { type: "status"; status: CastStatus }
  | { type: "ended"; ended: Ended }
  | { type: "lost"; error: string };

// Completion is terminal until an explicit reconnect. Keep the last progress
// snapshot for rendering, but never combine completion with a transport failure.
export function watchState(state: WatchState, event: WatchEvent): WatchState {
  if (event.type === "restart") return { phase: "watching" };
  if (state.phase !== "watching") return state;
  switch (event.type) {
    case "status":
      return { phase: "watching", status: event.status };
    case "ended":
      return { phase: "ended", status: state.status, ended: event.ended };
    case "lost":
      return { phase: "lost", status: state.status, error: event.error };
  }
}
