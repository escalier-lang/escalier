import { create } from 'zustand';

import type { CheckEvent } from '#lsp-client/client';

/**
 * How long the server has to be checking before the status bar says so. A shorter check
 * leaves the status bar showing how long the last one took, so a quick edit doesn't flash
 * the indicator. Checking an edit to one of the playground's examples takes 100 to
 * 220 ms in a production build, so this is about five times the slowest.
 */
export const SOFT_BUDGET_MS = 1_000;

/** How often the status bar's elapsed time updates once a check passes SOFT_BUDGET_MS. */
const TICK_MS = 250;

/** What the status bar shows about checks. */
export type CheckStatus =
    | {
          kind: 'idle';
          /** How long the last check took, or null before the first one finishes. */
          lastCheckMs: number | null;
      }
    | {
          kind: 'checking';
          /** How long the server has been checking. */
          elapsedMs: number;
          lastCheckMs: number | null;
      };

/** The parts of a Client that a CheckMonitor uses. */
export interface CheckSource {
    onCheckStarted(callback: (event: CheckEvent) => void): void;
    onCheckFinished(callback: (event: CheckEvent) => void): void;
    onServerExit(callback: () => void): void;
}

export const useCheckStore = create<{ status: CheckStatus }>(() => ({
    status: { kind: 'idle', lastCheckMs: null },
}));

/** Returns ms in seconds, with a tenth of a second below ten seconds. */
export function formatSeconds(ms: number): string {
    return ms < 10_000
        ? `${(ms / 1000).toFixed(1)} s`
        : `${Math.round(ms / 1000)} s`;
}

/** Returns what the status bar shows for status, or an empty string for nothing. */
export function checkStatusText(status: CheckStatus): string {
    if (status.kind === 'checking' && status.elapsedMs >= SOFT_BUDGET_MS) {
        return `Checking… ${formatSeconds(status.elapsedMs)}`;
    }
    return status.lastCheckMs === null
        ? ''
        : `Checked in ${formatSeconds(status.lastCheckMs)}`;
}

/**
 * Times the server's checks from its `escalier/checkStarted` and `escalier/checkFinished`
 * notifications, and publishes a CheckStatus through setStatus.
 *
 * The server is busy from the start of a check until no check it started is still
 * running. Checks can overlap, such as when an edit lands during a check, so a busy
 * period can span several of them.
 */
export class CheckMonitor {
    /**
     * How many checks of each document version are running, keyed by keyOf(). A
     * document closed and opened again can have two checks of one version.
     */
    private running = new Map<string, number>();
    /** When the current busy period began, or undefined when the server is idle. */
    private busySince: number | undefined;
    private lastCheckMs: number | null = null;
    private timer: ReturnType<typeof setInterval> | undefined;

    constructor(
        source: CheckSource,
        private setStatus: (status: CheckStatus) => void,
    ) {
        source.onCheckStarted((event) => this.started(event));
        source.onCheckFinished((event) => this.finished(event));
        source.onServerExit(() => this.serverExited());
    }

    private started(event: CheckEvent) {
        const key = keyOf(event);
        this.running.set(key, (this.running.get(key) ?? 0) + 1);
        if (this.busySince !== undefined) {
            return;
        }
        this.busySince = Date.now();
        // A check under SOFT_BUDGET_MS changes nothing the status bar shows, so the ticks
        // publish only past it.
        this.timer = setInterval(() => {
            if (
                this.busySince !== undefined &&
                Date.now() - this.busySince >= SOFT_BUDGET_MS
            ) {
                this.publish();
            }
        }, TICK_MS);
        this.publish();
    }

    private finished(event: CheckEvent) {
        const key = keyOf(event);
        const count = this.running.get(key);
        if (count === undefined) {
            return;
        }
        if (count > 1) {
            this.running.set(key, count - 1);
            return;
        }
        this.running.delete(key);
        if (this.running.size > 0) {
            return;
        }
        const tookMs = Date.now() - (this.busySince ?? Date.now());
        this.lastCheckMs = tookMs;
        console.info(
            `[Escalier LSP] checked ${event.uri} v${event.version} in ${tookMs} ms`,
        );
        this.endBusyPeriod();
        this.publish();
    }

    /**
     * Forgets every running check, since the server that ran them ended without finishing
     * them. A check cut short this way leaves no time to show, because the last time
     * describes older text.
     */
    private serverExited() {
        if (this.busySince !== undefined) {
            this.lastCheckMs = null;
        }
        this.endBusyPeriod();
        this.publish();
    }

    private endBusyPeriod() {
        this.running.clear();
        this.busySince = undefined;
        clearInterval(this.timer);
        this.timer = undefined;
    }

    private publish() {
        if (this.busySince !== undefined) {
            this.setStatus({
                kind: 'checking',
                elapsedMs: Date.now() - this.busySince,
                lastCheckMs: this.lastCheckMs,
            });
        } else {
            this.setStatus({ kind: 'idle', lastCheckMs: this.lastCheckMs });
        }
    }
}

function keyOf(event: CheckEvent): string {
    return `${event.version} ${event.uri}`;
}

/** Starts a CheckMonitor on source that publishes to useCheckStore. */
export function monitorChecks(source: CheckSource): CheckMonitor {
    return new CheckMonitor(source, (status) =>
        useCheckStore.setState({ status }),
    );
}
