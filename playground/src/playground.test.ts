import { describe, expect, test, vi } from 'vitest';

vi.mock('monaco-editor-core');

import { slugToLabel } from './playground';

describe('slugToLabel', () => {
    test.each([
        ['hello-world', 'Hello world'],
        ['calculator', 'Calculator'],
        ['overloads-and-generators', 'Overloads and generators'],
    ])('labels %s as %s', (slug, label) => {
        expect(slugToLabel(slug)).toBe(label);
    });
});
