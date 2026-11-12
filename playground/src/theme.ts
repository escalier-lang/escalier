import { create } from 'zustand';

/** The theme the user picked. `system` follows the operating system's setting. */
export type ThemePreference = 'system' | 'light' | 'dark';

/** The theme the playground draws in. */
export type Theme = 'light' | 'dark';

export const themePreferences: readonly ThemePreference[] = [
    'system',
    'light',
    'dark',
];

/** The localStorage key that holds the user's ThemePreference. */
export const themeStorageKey = 'escalier-playground-theme';

/** The media query that matches when the operating system uses a light theme. */
export const prefersLightQuery = '(prefers-color-scheme: light)';

/** Returns the theme to draw in for preference, given the operating system's theme. */
export function resolveTheme(
    preference: ThemePreference,
    systemPrefersLight: boolean,
): Theme {
    if (preference === 'system') {
        return systemPrefersLight ? 'light' : 'dark';
    }
    return preference;
}

/** Returns the name of the Monaco theme that matches theme. */
export function monacoThemeName(theme: Theme): string {
    return `escalier-${theme}`;
}

/**
 * Returns the preference saved in storage, or `system` when storage holds none,
 * holds an unknown value, or can't be read.
 */
export function readThemePreference(
    storage: Pick<Storage, 'getItem'> | undefined,
): ThemePreference {
    try {
        const saved = storage?.getItem(themeStorageKey);
        return themePreferences.find((p) => p === saved) ?? 'system';
    } catch {
        return 'system';
    }
}

/**
 * Returns the window's localStorage, or undefined when the browser blocks it.
 * Reading `window.localStorage` throws a SecurityError when site data is blocked.
 */
export function localStorageIfAvailable(
    win: Pick<Window, 'localStorage'>,
): Storage | undefined {
    try {
        return win.localStorage;
    } catch {
        return undefined;
    }
}

type ThemeStore = {
    preference: ThemePreference;
    systemPrefersLight: boolean;
    theme: Theme;
    setPreference: (preference: ThemePreference) => void;
    setSystemPrefersLight: (systemPrefersLight: boolean) => void;
};

export const useThemeStore = create<ThemeStore>((set) => ({
    preference: 'system',
    systemPrefersLight: false,
    theme: 'dark',
    setPreference: (preference) =>
        set((state) => ({
            preference,
            theme: resolveTheme(preference, state.systemPrefersLight),
        })),
    setSystemPrefersLight: (systemPrefersLight) =>
        set((state) => ({
            systemPrefersLight,
            theme: resolveTheme(state.preference, systemPrefersLight),
        })),
}));

/**
 * Loads the saved preference into useThemeStore and keeps the page in step with
 * the store from then on. It sets `data-theme` on the root element to the resolved
 * theme, which theme.css reads, and saves each new preference to storage. It also
 * follows changes to the operating system's theme. Returns a function that stops
 * following them.
 */
export function syncTheme(
    root: HTMLElement,
    storage: Pick<Storage, 'getItem' | 'setItem'> | undefined,
    systemQuery: MediaQueryList,
): () => void {
    const store = useThemeStore.getState();
    store.setSystemPrefersLight(systemQuery.matches);
    store.setPreference(readThemePreference(storage));
    root.dataset.theme = useThemeStore.getState().theme;

    const unsubscribe = useThemeStore.subscribe((state, prev) => {
        root.dataset.theme = state.theme;
        if (state.preference !== prev.preference) {
            try {
                storage?.setItem(themeStorageKey, state.preference);
            } catch {
                // A browser that blocks storage keeps the preference for this visit.
            }
        }
    });
    const onSystemChange = (event: MediaQueryListEvent) => {
        useThemeStore.getState().setSystemPrefersLight(event.matches);
    };
    systemQuery.addEventListener('change', onSystemChange);

    return () => {
        unsubscribe();
        systemQuery.removeEventListener('change', onSystemChange);
    };
}
