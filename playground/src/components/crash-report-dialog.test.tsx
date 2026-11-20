// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import {
    act,
    cleanup,
    fireEvent,
    render,
    screen,
} from '@testing-library/react';
import { afterEach, beforeAll, expect, test, vi } from 'vitest';

import { playgroundEnvironment } from '../bug-report/bug-report';
import { crashIssueUrl } from '../bug-report/crash-notification';
import { CrashReportDialog } from './crash-report-dialog';
import { stubModalDialogs } from './dialog-stub';

const report = {
    headline: 'panic: boom',
    text: 'The language server crashed and was restarted.\n\npanic: boom\n',
};

beforeAll(stubModalDialogs);

afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.useRealTimers();
});

test('shows the report and links to a new issue for it', () => {
    render(<CrashReportDialog report={report} onClose={() => {}} />);

    expect(screen.getByRole('dialog')).toHaveAccessibleDescription(
        'Copy this report into a GitHub issue, with the code that triggers the crash if you can share it.',
    );
    expect(screen.getByLabelText('Crash report')).toHaveTextContent(
        'The language server crashed and was restarted. panic: boom',
    );
    expect(
        screen.getByRole('link', { name: 'Open GitHub issue' }),
    ).toHaveAttribute('href', crashIssueUrl(report, playgroundEnvironment()));
});

test('copies the report to the clipboard', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    render(<CrashReportDialog report={report} onClose={() => {}} />);

    fireEvent.click(screen.getByRole('button', { name: 'Copy report' }));

    expect(
        await screen.findByRole('button', { name: 'Copied' }),
    ).toBeInTheDocument();
    expect(writeText).toHaveBeenCalledWith(report.text);
});

test('the copy button goes back to "Copy report" two seconds after it shows "Copied"', async () => {
    vi.stubGlobal('navigator', {
        clipboard: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
    render(<CrashReportDialog report={report} onClose={() => {}} />);
    fireEvent.click(screen.getByRole('button', { name: 'Copy report' }));
    await screen.findByRole('button', { name: 'Copied' });

    expect(
        await screen.findByRole(
            'button',
            { name: 'Copy report' },
            { timeout: 3000 },
        ),
    ).toBeInTheDocument();
});

test('a second copy shows "Copied" for two seconds from the second press', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    vi.stubGlobal('navigator', {
        clipboard: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
    render(<CrashReportDialog report={report} onClose={() => {}} />);
    const copyButton = screen.getByRole('button', { name: 'Copy report' });

    await act(async () => fireEvent.click(copyButton));
    await act(async () => vi.advanceTimersByTime(1500));
    await act(async () => fireEvent.click(copyButton));
    await act(async () => vi.advanceTimersByTime(1500));
    expect(copyButton).toHaveTextContent('Copied');

    await act(async () => vi.advanceTimersByTime(600));
    expect(copyButton).toHaveTextContent('Copy report');
});

test('says so when the clipboard refuses', async () => {
    const writeText = vi.fn().mockRejectedValue(new Error('denied'));
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    render(<CrashReportDialog report={report} onClose={() => {}} />);

    fireEvent.click(screen.getByRole('button', { name: 'Copy report' }));

    expect(
        await screen.findByRole('button', {
            name: 'Copy failed. Select the text instead.',
        }),
    ).toBeInTheDocument();
});

test('closes from its Close button', () => {
    const onClose = vi.fn();
    render(<CrashReportDialog report={report} onClose={onClose} />);

    fireEvent.click(screen.getByRole('button', { name: 'Close' }));

    expect(onClose).toHaveBeenCalledOnce();
});

test('shows and copies the playground link with the report', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    const linked = { ...report, permalink: 'https://example.com/#project=abc' };
    render(<CrashReportDialog report={linked} onClose={() => {}} />);

    expect(screen.getByRole('dialog')).toHaveAccessibleDescription(
        'Copy this report into a GitHub issue. Its playground link opens the project as it was when the server crashed.',
    );
    expect(screen.getByLabelText('Crash report')).toHaveTextContent(
        'Playground link: https://example.com/#project=abc',
    );
    fireEvent.click(screen.getByRole('button', { name: 'Copy report' }));

    await screen.findByRole('button', { name: 'Copied' });
    expect(writeText).toHaveBeenCalledWith(
        `${linked.text}\nPlayground link: https://example.com/#project=abc\n`,
    );
});
