import { describe, expect, test, vi } from 'vitest';

vi.mock('monaco-editor-core');

import { oversizedLinkMessage, slugToLabel } from './playground';

describe('slugToLabel', () => {
    test.each([
        ['hello-world', 'Hello world'],
        ['calculator', 'Calculator'],
        ['overloads-and-generators', 'Overloads and generators'],
    ])('labels %s as %s', (slug, label) => {
        expect(slugToLabel(slug)).toBe(label);
    });
});

test('oversizedLinkMessage gives the link length and the limit', () => {
    expect(oversizedLinkMessage(45_678)).toBe(
        "A link to this project would be 45,678 characters, and a link can hold at most 32,000. Remove the files and code that the problem doesn't need, then share again.",
    );
});
