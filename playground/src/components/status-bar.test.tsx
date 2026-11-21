// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, test } from 'vitest';

import {
    DIAGNOSTICS_TAB_PATH,
    type DiagnosticSeverity,
    type ProjectDiagnostic,
} from '#diagnostics/project-diagnostics';
import { useProjectDiagnosticsStore } from '#diagnostics/project-diagnostics-store';
import { initialEditorState } from '#editor/editor-state';
import { useEditorStore } from '#editor/editor-store';
import { StatusBar } from './status-bar';

function diagnostic(severity: DiagnosticSeverity): ProjectDiagnostic {
    return { path: '/lib/a.esc', line: 1, column: 1, severity, message: 'bad' };
}

beforeEach(() => {
    useEditorStore.setState({ ...initialEditorState });
    useProjectDiagnosticsStore.setState({ diagnostics: {} });
});

afterEach(cleanup);

test("shows the project's error and warning counts and opens the diagnostics tab", () => {
    useProjectDiagnosticsStore.setState({
        diagnostics: {
            '/lib/a.esc': [
                diagnostic('error'),
                diagnostic('error'),
                diagnostic('warning'),
            ],
            '/lib/b.esc': [diagnostic('error'), diagnostic('info')],
        },
    });
    render(<StatusBar />);

    const button = screen.getByRole('button', {
        name: '3 errors, 1 warning',
    });
    expect(button).toHaveTextContent('31');
    fireEvent.click(button);

    const { leftTabs, activeLeftTabIndex } = useEditorStore.getState();
    expect(leftTabs.map((tab) => tab.path)).toContain(DIAGNOSTICS_TAB_PATH);
    expect(activeLeftTabIndex).toBe(leftTabs.length - 1);
});

test('names the counts when there are none', () => {
    render(<StatusBar />);

    expect(
        screen.getByRole('button', { name: '0 errors, 0 warnings' }),
    ).toBeInTheDocument();
});
