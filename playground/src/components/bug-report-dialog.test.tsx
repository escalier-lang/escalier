// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeAll, expect, test, vi } from 'vitest';

import type { ShownError } from '#bug-report/bug-report';
import { MAX_PERMALINK_CHARS } from '#project/permalink';
import { BugReportDialog } from './bug-report-dialog';
import { stubModalDialogs } from './dialog-stub';

const link = 'https://example.com/#project=abc';
const errors: ShownError[] = [
    { path: '/lib/index.esc', line: 2, column: 7, message: 'bad' },
];

beforeAll(stubModalDialogs);

afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
});

function renderDialog(
    capturePermalink: () => Promise<string>,
    crashReport?: string,
) {
    const onClose = vi.fn();
    render(
        <BugReportDialog
            capturePermalink={capturePermalink}
            errors={errors}
            crashReport={crashReport}
            environment="Browser: TestBrowser/1.0"
            onClose={onClose}
        />,
    );
    return onClose;
}

/** Returns the query parameters of the dialog's issue link. */
async function issueParams(): Promise<Record<string, string>> {
    const issueLink = await screen.findByRole('link', {
        name: 'Open GitHub issue',
    });
    return Object.fromEntries(
        new URL(issueLink.getAttribute('href') ?? '').searchParams,
    );
}

test('fills the issue link in with the description and the project link', async () => {
    renderDialog(() => Promise.resolve(link), 'panic: boom');

    expect(screen.getByLabelText('What happened')).toHaveFocus();
    fireEvent.change(screen.getByLabelText('What happened'), {
        target: { value: 'Hover is blank.' },
    });
    fireEvent.change(screen.getByLabelText('What you expected'), {
        target: { value: 'A type.' },
    });

    expect(await issueParams()).toMatchObject({
        title: 'Playground: Hover is blank.',
        'what-happened': 'Hover is blank.',
        expected: 'A type.',
        'playground-link': link,
        errors: '/lib/index.esc:2:7: bad',
        'crash-report': 'panic: boom',
        environment: 'Browser: TestBrowser/1.0',
    });
    expect(
        screen.getByText(
            'The report also holds 1 error shown in the open files, the latest crash report and your browser and the playground build.',
        ),
    ).toBeInTheDocument();
});

test('waits for the project link before it offers the issue or the copy', () => {
    renderDialog(() => new Promise(() => {}));

    expect(
        screen.getByRole('button', { name: 'Open GitHub issue' }),
    ).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Copy report' })).toBeDisabled();
    expect(
        screen.getByText('Making a link to the project…'),
    ).toBeInTheDocument();
});

test('says when the project is too large to link to', async () => {
    renderDialog(() =>
        Promise.resolve(
            `https://x/#project=${'a'.repeat(MAX_PERMALINK_CHARS)}`,
        ),
    );

    expect((await issueParams())['playground-link']).toBe(
        'None. The project is too large to link to.',
    );
    expect(
        screen.getByText(
            'The project is too large to link to. A link can be at most 32,000 characters.',
        ),
    ).toBeInTheDocument();
});

test('says when the playground could not make a link', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    renderDialog(() => Promise.reject(new Error('no CompressionStream')));

    expect((await issueParams())['playground-link']).toBe(
        'None. The playground could not make a link.',
    );
    expect(
        screen.getByText(
            'The playground could not make a link to the project.',
        ),
    ).toBeInTheDocument();
});

test('leaves the project link out when the user unticks it', async () => {
    renderDialog(() => Promise.resolve(link));
    await screen.findByRole('link', { name: 'Open GitHub issue' });

    fireEvent.click(
        screen.getByRole('checkbox', {
            name: 'Include a link to this project',
        }),
    );

    expect(await issueParams()).not.toHaveProperty('playground-link');
});

test('copies the report as Markdown', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    renderDialog(() => Promise.resolve(link));
    await screen.findByRole('link', { name: 'Open GitHub issue' });
    fireEvent.change(screen.getByLabelText('What happened'), {
        target: { value: 'Hover is blank.' },
    });

    fireEvent.click(screen.getByRole('button', { name: 'Copy report' }));

    await screen.findByRole('button', { name: 'Copied' });
    expect(writeText).toHaveBeenCalledWith(
        expect.stringMatching(
            /^### What happened\n\nHover is blank\.\n\n### Playground link\n\nhttps:\/\/example\.com\/#project=abc\n/,
        ),
    );
});

test('says the issue form opened once the user opens it, and Done closes the dialog', async () => {
    const onClose = renderDialog(() => Promise.resolve(link));
    fireEvent.change(screen.getByLabelText('What happened'), {
        target: { value: 'Hover is blank.' },
    });
    const issueLink = await screen.findByRole('link', {
        name: 'Open GitHub issue',
    });
    const href = issueLink.getAttribute('href');

    fireEvent.click(issueLink);

    expect(screen.getByRole('dialog')).toHaveAccessibleName(
        'Issue form opened',
    );
    expect(screen.getByRole('dialog')).toHaveAccessibleDescription(
        'The GitHub issue form opened in a new tab with your report filled in. Check it there and submit it to file the bug.',
    );
    expect(
        screen.getByRole('link', { name: 'Open the form again' }),
    ).toHaveAttribute('href', href);
    const done = screen.getByRole('button', { name: 'Done' });
    expect(done).toHaveFocus();

    fireEvent.click(done);
    expect(onClose).toHaveBeenCalledOnce();
});

test('Cancel closes the dialog', () => {
    const onClose = renderDialog(() => Promise.resolve(link));

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(onClose).toHaveBeenCalledOnce();
});
