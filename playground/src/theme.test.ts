// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

import {
    type ThemePreference,
    monacoThemeName,
    readThemePreference,
    resolveTheme,
    syncTheme,
    themeStorageKey,
    useThemeStore,
} from './theme';

/** Returns a Storage stand-in that holds its items in a Map. */
function memoryStorage(items: Record<string, string> = {}) {
    const map = new Map(Object.entries(items));
    return {
        getItem: (key: string) => map.get(key) ?? null,
        setItem: (key: string, value: string) => {
            map.set(key, value);
        },
    };
}

/** Returns a MediaQueryList stand-in whose change event change() fires. */
function fakeQuery(matches: boolean) {
    const listeners = new Set<(event: MediaQueryListEvent) => void>();
    const query = {
        matches,
        addEventListener: (
            _type: string,
            listener: (event: MediaQueryListEvent) => void,
        ) => listeners.add(listener),
        removeEventListener: (
            _type: string,
            listener: (event: MediaQueryListEvent) => void,
        ) => listeners.delete(listener),
    } as unknown as MediaQueryList;
    const change = (nextMatches: boolean) => {
        for (const listener of listeners) {
            listener({ matches: nextMatches } as MediaQueryListEvent);
        }
    };
    return { query, change, listeners };
}

describe('resolveTheme', () => {
    test.each<[ThemePreference, boolean, string]>([
        ['system', true, 'light'],
        ['system', false, 'dark'],
        ['light', false, 'light'],
        ['dark', true, 'dark'],
    ])(
        'resolves %s with a light system theme %s to %s',
        (preference, prefersLight, expected) => {
            expect(resolveTheme(preference, prefersLight)).toBe(expected);
        },
    );
});

describe('monacoThemeName', () => {
    test('names the Monaco theme for each theme', () => {
        expect(monacoThemeName('light')).toBe('escalier-light');
        expect(monacoThemeName('dark')).toBe('escalier-dark');
    });
});

describe('readThemePreference', () => {
    test.each<[Record<string, string>, ThemePreference]>([
        [{ [themeStorageKey]: 'light' }, 'light'],
        [{ [themeStorageKey]: 'dark' }, 'dark'],
        [{ [themeStorageKey]: 'sepia' }, 'system'],
        [{}, 'system'],
    ])('reads %o as %s', (items, expected) => {
        expect(readThemePreference(memoryStorage(items))).toBe(expected);
    });

    test('returns system without storage or when storage throws', () => {
        expect(readThemePreference(undefined)).toBe('system');
        expect(
            readThemePreference({
                getItem: () => {
                    throw new Error('SecurityError');
                },
            }),
        ).toBe('system');
    });
});

describe('syncTheme', () => {
    let stop: () => void = () => {};

    beforeEach(() => {
        useThemeStore.setState({
            preference: 'system',
            systemPrefersLight: false,
            theme: 'dark',
        });
    });

    afterEach(() => {
        stop();
        delete document.documentElement.dataset.theme;
    });

    test('applies the saved preference to the root element', () => {
        const { query } = fakeQuery(false);

        stop = syncTheme(
            document.documentElement,
            memoryStorage({ [themeStorageKey]: 'light' }),
            query,
        );

        expect(useThemeStore.getState().preference).toBe('light');
        expect(document.documentElement.dataset.theme).toBe('light');
    });

    test('follows the system theme while the preference is system', () => {
        const { query, change } = fakeQuery(false);
        stop = syncTheme(document.documentElement, memoryStorage(), query);
        expect(document.documentElement.dataset.theme).toBe('dark');

        change(true);
        expect(document.documentElement.dataset.theme).toBe('light');

        useThemeStore.getState().setPreference('dark');
        change(false);
        change(true);
        expect(document.documentElement.dataset.theme).toBe('dark');
    });

    test('saves each new preference', () => {
        const storage = memoryStorage();
        stop = syncTheme(
            document.documentElement,
            storage,
            fakeQuery(false).query,
        );

        useThemeStore.getState().setPreference('light');

        expect(storage.getItem(themeStorageKey)).toBe('light');
    });

    test('keeps working when storage rejects a write', () => {
        const storage = {
            getItem: () => null,
            setItem: vi.fn(() => {
                throw new Error('QuotaExceededError');
            }),
        };
        stop = syncTheme(
            document.documentElement,
            storage,
            fakeQuery(false).query,
        );

        useThemeStore.getState().setPreference('light');

        expect(storage.setItem).toHaveBeenCalled();
        expect(document.documentElement.dataset.theme).toBe('light');
    });

    test('stops following the system theme once stopped', () => {
        const { query, listeners } = fakeQuery(false);
        stop = syncTheme(document.documentElement, memoryStorage(), query);

        stop();

        expect(listeners.size).toBe(0);
    });
});
