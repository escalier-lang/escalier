import { afterEach, beforeEach, expect, test, vi } from 'vitest';

import type { CheckEvent } from '#lsp-client/client';

import {
    CheckMonitor,
    type CheckSource,
    type CheckStatus,
    SOFT_BUDGET_MS,
    checkStatusText,
    formatSeconds,
    monitorChecks,
    useCheckStore,
} from './check-monitor';

/** A CheckSource whose events the test fires by hand. */
class FakeSource implements CheckSource {
    started: (event: CheckEvent) => void = () => {};
    finished: (event: CheckEvent) => void = () => {};
    exited: () => void = () => {};

    onCheckStarted(callback: (event: CheckEvent) => void) {
        this.started = callback;
    }
    onCheckFinished(callback: (event: CheckEvent) => void) {
        this.finished = callback;
    }
    onServerExit(callback: () => void) {
        this.exited = callback;
    }
}

const v1 = { uri: 'file:///lib/index.esc', version: 1 };
const v2 = { uri: 'file:///lib/index.esc', version: 2 };

let source: FakeSource;
let status: CheckStatus;

beforeEach(() => {
    vi.useFakeTimers();
    vi.spyOn(console, 'info').mockImplementation(() => {});
    source = new FakeSource();
    new CheckMonitor(source, (s) => {
        status = s;
    });
});

afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
});

test('times a check from its start to its finish', () => {
    source.started(v1);
    vi.advanceTimersByTime(400);
    // Under SOFT_BUDGET_MS the status keeps what it showed when the check started.
    expect(status).toEqual({
        kind: 'checking',
        elapsedMs: 0,
        lastCheckMs: null,
    });
    vi.advanceTimersByTime(SOFT_BUDGET_MS);
    expect(status).toEqual({
        kind: 'checking',
        elapsedMs: 1250,
        lastCheckMs: null,
    });

    source.finished(v1);

    expect(status).toEqual({ kind: 'idle', lastCheckMs: 1400 });
    expect(console.info).toHaveBeenCalledWith(
        '[Escalier LSP] checked file:///lib/index.esc v1 in 1400 ms',
    );
});

test('a busy period lasts until every overlapping check finishes', () => {
    source.started(v1);
    vi.advanceTimersByTime(100);
    source.started(v2);
    vi.advanceTimersByTime(100);
    source.finished(v1);
    expect(status.kind).toBe('checking');

    vi.advanceTimersByTime(100);
    source.finished(v2);

    expect(status).toEqual({ kind: 'idle', lastCheckMs: 300 });
});

test('two checks of one version both have to finish', () => {
    source.started(v1);
    source.started(v1);
    source.finished(v1);
    expect(status.kind).toBe('checking');

    source.finished(v1);

    expect(status.kind).toBe('idle');
});

test('a finish without a matching start changes nothing', () => {
    source.started(v1);
    source.finished(v2);

    expect(status.kind).toBe('checking');
});

test('a server that exits mid-check ends the busy period without a time', () => {
    source.started(v1);
    vi.advanceTimersByTime(100);
    source.finished(v1);
    source.started(v2);
    vi.advanceTimersByTime(500);

    source.exited();

    // The time of the check of v1 describes older text.
    expect(status).toEqual({ kind: 'idle', lastCheckMs: null });
    // The server that replaces it checks the same version from scratch.
    source.started(v2);
    vi.advanceTimersByTime(250);
    source.finished(v2);
    expect(status).toEqual({ kind: 'idle', lastCheckMs: 250 });
});

test('a server that exits while idle keeps the last time', () => {
    source.started(v1);
    vi.advanceTimersByTime(100);
    source.finished(v1);

    source.exited();

    expect(status).toEqual({ kind: 'idle', lastCheckMs: 100 });
});

test('monitorChecks publishes the status to useCheckStore', () => {
    const storeSource = new FakeSource();
    monitorChecks(storeSource);

    storeSource.started(v1);
    vi.advanceTimersByTime(300);
    storeSource.finished(v1);

    expect(useCheckStore.getState().status).toEqual({
        kind: 'idle',
        lastCheckMs: 300,
    });
});

test.each<[string, number, string]>([
    ['tenths below ten seconds', 2_345, '2.3 s'],
    ['zero', 0, '0.0 s'],
    ['whole seconds from ten seconds', 12_600, '13 s'],
])('formatSeconds shows %s', (_name, ms, expected) => {
    expect(formatSeconds(ms)).toBe(expected);
});

test.each<[string, CheckStatus, string]>([
    ['nothing before the first check', { kind: 'idle', lastCheckMs: null }, ''],
    [
        'the last check time when idle',
        { kind: 'idle', lastCheckMs: 180 },
        'Checked in 0.2 s',
    ],
    [
        'the last check time during a check under the soft budget',
        { kind: 'checking', elapsedMs: SOFT_BUDGET_MS - 1, lastCheckMs: 180 },
        'Checked in 0.2 s',
    ],
    [
        'the elapsed time once a check passes the soft budget',
        { kind: 'checking', elapsedMs: SOFT_BUDGET_MS, lastCheckMs: 180 },
        'Checking… 1.0 s',
    ],
])('checkStatusText shows %s', (_name, checkStatus, expected) => {
    expect(checkStatusText(checkStatus)).toBe(expected);
});
