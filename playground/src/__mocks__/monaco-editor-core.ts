import { vi } from 'vitest';

function createMockEditor() {
    return {
        setModel: vi.fn(),
        updateOptions: vi.fn(),
        setScrollTop: vi.fn(),
        dispose: vi.fn(),
        onDidFocusEditorWidget: vi.fn(() => ({ dispose: vi.fn() })),
        onMouseUp: vi.fn(() => ({ dispose: vi.fn() })),
        getModel: vi.fn(() => null),
        getSelection: vi.fn(() => null),
        setPosition: vi.fn(),
        revealPositionInCenter: vi.fn(),
        focus: vi.fn(),
    };
}

export const editor = {
    create: vi.fn(() => createMockEditor()),
    createModel: vi.fn(() => ({ getValue: vi.fn(() => '') })),
    getModel: vi.fn(() => null),
    getModels: vi.fn(() => []),
    getModelMarkers: vi.fn(() => []),
};

export const MarkerSeverity = { Hint: 1, Info: 2, Warning: 4, Error: 8 };

export const Uri = {
    parse: vi.fn((s: string) => s),
};

export const languages = {
    // monaco-editor's basic-languages modules read IndentAction when they load.
    IndentAction: { None: 0, Indent: 1, IndentOutdent: 2, Outdent: 3 },
    CodeActionTriggerType: { Invoke: 1, Auto: 2 },
    register: vi.fn(),
    setMonarchTokensProvider: vi.fn(),
    setLanguageConfiguration: vi.fn(),
};
