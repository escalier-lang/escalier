import { vi } from 'vitest';

const mockEditor = {
    setModel: vi.fn(),
    updateOptions: vi.fn(),
    setScrollTop: vi.fn(),
    dispose: vi.fn(),
    onDidFocusEditorWidget: vi.fn(() => ({ dispose: vi.fn() })),
};

export const editor = {
    create: vi.fn(() => mockEditor),
    createModel: vi.fn(() => ({ getValue: vi.fn(() => '') })),
    getModel: vi.fn(() => null),
};

export const Uri = {
    parse: vi.fn((s: string) => s),
};
