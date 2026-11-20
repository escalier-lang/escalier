import * as monaco from 'monaco-editor-core';

import { useEditorStore } from './editor-store';
import type { BrowserFS } from './fs/browser-fs';
import { collectProjectFiles, encodeSnapshot, permalinkUrl } from './permalink';

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
        // A tab is recorded by its path, so the diagnostics tab records none of the
        // diagnostics it shows. The reopened page fills it from a fresh check.
        left: {
            paths: state.leftTabs.map((tab) => tab.path),
            active: state.activeLeftTabIndex,
        },
        right: {
            paths: state.rightTabs.map((tab) => tab.path),
            active: state.activeRightTabIndex,
        },
    });
    return permalinkUrl(href, encoded);
}
