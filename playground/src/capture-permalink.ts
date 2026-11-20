import * as monaco from 'monaco-editor-core';

import type { Tab, TabLayout } from './editor-state';
import { useEditorStore } from './editor-store';
import type { BrowserFS } from './fs/browser-fs';
import { collectProjectFiles, encodeSnapshot, permalinkUrl } from './permalink';
import { DIAGNOSTICS_TAB_PATH } from './project-diagnostics';

/**
 * Returns a permalink to the project in fs and to the tabs the editor has open. The
 * link is href with its query and hash replaced, so it opens the same playground. It
 * can be longer than MAX_PERMALINK_CHARS.
 */
export async function capturePermalink(
    fs: BrowserFS,
    href: string,
): Promise<string> {
    const state = useEditorStore.getState();
    // The editor holds edits that haven't reached the filesystem, so a file's text
    // comes from its model when it has one.
    const files = collectProjectFiles(fs.rootDir, (path) =>
        monaco.editor.getModel(monaco.Uri.parse(`file://${path}`))?.getValue(),
    );
    const encoded = await encodeSnapshot({
        files,
        left: fileTabsLayout(state.leftTabs, state.activeLeftTabIndex),
        right: fileTabsLayout(state.rightTabs, state.activeRightTabIndex),
    });
    return permalinkUrl(href, encoded);
}

/**
 * Returns the layout of tabs without the diagnostics tab. The active tab keeps its
 * place. When the diagnostics tab is the active one, the tab before it becomes active,
 * or the tab after it when it is first. The layout has no active tab when it has no
 * tabs.
 */
export function fileTabsLayout(
    tabs: readonly Tab[],
    active: number | null,
): TabLayout {
    // A link opens the project somewhere else, where the language server reports the
    // diagnostics afresh. A restored diagnostics tab that opens active also leaves the
    // server unchecked, since the server checks the project once an Escalier file opens.
    const removed = tabs.findIndex((tab) => tab.path === DIAGNOSTICS_TAB_PATH);
    const paths = tabs.filter((_, i) => i !== removed).map((tab) => tab.path);
    if (removed === -1 || active === null) {
        return { paths, active };
    }
    if (paths.length === 0) {
        return { paths, active: null };
    }
    return {
        paths,
        active: active < removed ? active : Math.max(active - 1, 0),
    };
}
