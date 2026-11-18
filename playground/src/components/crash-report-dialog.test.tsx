// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeAll, expect, test, vi } from 'vitest';

import { crashIssueUrl } from '../crash-notification';
import { CrashReportDialog } from './crash-report-dialog';

const report = {
    headline: 'panic: boom',
    text: 'The language server crashed and was restarted.\n\npanic: boom\n',
};

beforeAll(() => {
    // jsdom has no modal dialogs, so these open and close the element by its attribute.
    HTMLDialogElement.prototype.showModal = function () {
        this.open = true;
    };
    HTMLDialogElement.prototype.close = function () {
        this.open = false;
    };
});

afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
});

test('shows the report and links to a new issue for it', () => {
    render(<CrashReportDialog report={report} onClose={() => {}} />);

    expect(screen.getByLabelText('Crash report')).toHaveTextContent(
        'The language server crashed and was restarted. panic: boom',
    );
    expect(
        screen.getByRole('link', { name: 'Open GitHub issue' }),
    ).toHaveAttribute('href', crashIssueUrl(report));
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

test('the copy button reads as before a moment after a copy', async () => {
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
